package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/ada"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestAuthSettingsSetupOrigin(t *testing.T) {
	for _, tt := range []struct {
		name, host, origin, pinned, body, forwarded string
		tls, denied                                 bool
	}{
		{name: "loopback default", host: "localhost:8080", origin: "http://localhost:8080"},
		{name: "TLS", host: "at.example", origin: "https://at.example", tls: true},
		{name: "TLS proxy preserved Host", host: "at.example", origin: "https://at.example", pinned: "https://at.example"},
		{name: "TLS proxy first claim", host: "at.example", origin: "https://at.example"},
		{name: "missing origin", host: "localhost:8080", denied: true},
		{name: "cross origin", host: "at.example", origin: "https://evil.example", denied: true},
		{name: "malicious configured host", host: "evil.example", origin: "https://evil.example", pinned: "https://at.example", denied: true},
		{name: "forwarded host ignored", host: "evil.example", origin: "https://at.example", forwarded: "at.example", denied: true},
		{name: "body origin mismatch", host: "at.example", origin: "https://at.example", body: "https://evil.example", denied: true},
		{name: "remote insecure HTTP", host: "at.example", origin: "http://at.example", denied: true},
		{name: "TLS scheme mismatch", host: "localhost", origin: "http://localhost", tls: true, denied: true},
		{name: "trailing slash", host: "at.example", origin: "https://at.example/", denied: true},
		{name: "different port", host: "at.example:8443", origin: "https://at.example", denied: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tt.host+"/auth/setup", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			r.Header.Set("X-Forwarded-Host", tt.forwarded)
			r.Header.Set("X-Forwarded-Proto", "https")
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			_, err := nativeSetupOrigin(r, tt.pinned, tt.body)
			if (err != nil) != tt.denied {
				t.Fatalf("origin validation: %v", err)
			}
		})
	}
}

type unavailableAuthSettings struct{ service.AuthSettingsStorer }

func (unavailableAuthSettings) GetAuthSettings(context.Context) (*service.AuthSettingsState, error) {
	return nil, errors.New("database unavailable")
}

func TestAuthSettingsFailClosed(t *testing.T) {
	m := &nativeAuthSettings{store: unavailableAuthSettings{}}
	for _, route := range []string{"/auth/status", "/auth/settings", "/auth/login", "/auth/setup"} {
		r := httptest.NewRequest("GET", route, nil)
		if route == "/auth/setup" {
			r.Method = "POST"
		}
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != 503 || strings.Contains(w.Body.String(), `"enabled":false`) {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
	}
}

func TestAuthSettingsImportConfig(t *testing.T) {
	v, err := initialAuthSettings(config.Server{BasePath: "/at", ExternalURL: "https://at.example/at"})
	if err != nil || v.Origin != "https://at.example" || !v.LocalLoginEnabled {
		t.Fatalf("defaults: %+v %v", v, err)
	}
	if _, err := initialAuthSettings(config.Server{BasePath: "/at", ExternalURL: "https://at.example/wrong"}); err == nil {
		t.Fatal("accepted mismatched base path")
	}
	v, err = initialAuthSettings(config.Server{})
	if err != nil || v.Origin != "" {
		t.Fatalf("fresh installation needs YAML: %+v %v", v, err)
	}
	for _, mutate := range []func(*service.AuthSettings){
		func(v *service.AuthSettings) { v.MFAPolicy = "off" },
		func(v *service.AuthSettings) { v.MaxSessions = 0 },
		func(v *service.AuthSettings) { v.SignupAdmission = "open" },
		func(v *service.AuthSettings) { v.RememberTTLSeconds = 1 },
	} {
		bad := v
		mutate(&bad)
		if bad.Validate(true) == nil {
			t.Fatalf("accepted bypass: %+v", bad)
		}
	}
}

func settingsHTTPRequest(handler http.Handler, method, route string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "https://at.example/at"+route, strings.NewReader(string(data)))
	r.Header.Set("Origin", "https://at.example")
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAuthSettingsRuntimePostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	cfg := config.Server{BasePath: "/at"}
	first, err := newNativeAuthSettings(t.Context(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newNativeAuthSettings(t.Context(), cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	first.limit = rate.NewLimiter(rate.Inf, 100)
	second.limit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	first.register(mux, cfg.BasePath)
	w := settingsHTTPRequest(mux, "GET", "/auth/status", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"setup_required":true`) {
		t.Fatalf("fresh status: %d %s", w.Code, w.Body.String())
	}
	management := first.require(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if w := settingsHTTPRequest(management, "GET", "/api/v1/test", nil, nil); w.Code != 403 {
		t.Fatalf("unclaimed management bypass: %d", w.Code)
	}
	w = settingsHTTPRequest(mux, "POST", "/auth/setup", map[string]string{"username": "admin", "password": "a strong initial password", "origin": "https://at.example"}, nil)
	if w.Code != 201 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), `"subject"`) {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if w := settingsHTTPRequest(second, "GET", "/auth/status", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"setup_required":false`) {
		t.Fatalf("replica status: %d %s", w.Code, w.Body.String())
	}
	if w := settingsHTTPRequest(second, "POST", "/auth/setup", nil, nil); w.Code != 409 {
		t.Fatalf("double setup: %d", w.Code)
	}
	w = settingsHTTPRequest(first, "POST", "/auth/login", map[string]string{"username": "admin", "password": "a strong initial password"}, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	w = settingsHTTPRequest(first, "GET", "/auth/settings", nil, cookies)
	var v service.AuthSettings
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
		t.Fatalf("settings: %d %s %v", w.Code, w.Body.String(), err)
	}
	v.SessionTTLSeconds = 600
	v.DisplayTitle = "My AT"
	w = settingsHTTPRequest(first, "PUT", "/auth/settings", v, cookies)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w := settingsHTTPRequest(second, "PUT", "/auth/settings", v, cookies); w.Code != 409 {
		t.Fatalf("stale version: %d %s", w.Code, w.Body.String())
	}
	state, _ := p.GetAuthSettings(t.Context())
	a, _, err := second.snapshot(state.Settings)
	if err != nil || a.cfg.SessionTTL.Seconds() != 600 {
		t.Fatalf("runtime snapshot: %v %v", a, err)
	}
	if w := settingsHTTPRequest(second, "GET", "/auth/status", nil, nil); !strings.Contains(w.Body.String(), `"display_title":"My AT"`) {
		t.Fatalf("replica stale policy: %s", w.Body.String())
	}
	// Requests racing version rebuilds use immutable configs and shared limits.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 8 {
				w := settingsHTTPRequest(second, "GET", "/auth/status", nil, nil)
				if w.Code != 200 {
					t.Error(fmt.Sprintf("concurrent status: %d", w.Code))
				}
			}
		})
	}
	for range 4 {
		state, _ := p.GetAuthSettings(t.Context())
		state.Settings.DisplayTitle += "."
		if _, err := p.SaveAuthSettings(t.Context(), state.Settings); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	if w := settingsHTTPRequest(second, "GET", "/auth/settings", nil, nil); w.Code != 401 {
		t.Fatalf("public settings: %d", w.Code)
	}
}
