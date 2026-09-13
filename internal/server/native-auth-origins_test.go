package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestNativeMixedOriginCookieLifecycle(t *testing.T) {
	a, _, mux := nativeFixture(t)
	a.allowedOrigins = []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"}
	for _, origin := range append([]string{a.cfg.Origin}, a.allowedOrigins...) {
		t.Run(origin, func(t *testing.T) {
			u, _ := url.Parse(origin)
			secure := u.Scheme == "https"
			configure := func(r *http.Request) { r.Host = u.Host; r.Header.Set("Sec-Fetch-Site", "same-origin") }
			w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, origin, nil, configure)
			if w.Code != 200 {
				t.Fatalf("login: %d %s", w.Code, w.Body)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 2 {
				t.Fatalf("cookies: %+v", cookies)
			}
			for _, c := range cookies {
				if c.Secure != secure || strings.HasPrefix(c.Name, "__Secure-") != secure || !c.HttpOnly || c.Domain != "" || c.Path != "/at/" {
					t.Fatalf("cookie: %+v", c)
				}
			}
			// Browser GETs normally omit Origin. Host must still select the same cookie.
			w = nativeRequest(mux, "GET", "/at/auth/session", "", "", cookies[0], configure)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"subject":"reader"`) {
				t.Fatalf("session: %d %s", w.Code, w.Body)
			}
			w = nativeRequest(mux, "POST", "/at/auth/refresh", `{}`, origin, cookies[1], configure)
			if w.Code != 200 {
				t.Fatalf("refresh: %d %s", w.Code, w.Body)
			}
			rotated := w.Result().Cookies()
			if len(rotated) != 2 || rotated[0].Name != cookies[0].Name || rotated[0].Secure != secure || rotated[1].Name != cookies[1].Name || rotated[1].Secure != secure {
				t.Fatalf("rotated: %+v", rotated)
			}
			w = nativeRequest(mux, "GET", "/at/auth/me", "", "", rotated[0], configure)
			if w.Code != 200 {
				t.Fatalf("me: %d %s", w.Code, w.Body)
			}
			w = nativeRequest(mux, "POST", "/at/auth/logout", `{}`, origin, rotated[0], configure)
			if w.Code != 204 {
				t.Fatalf("logout: %d %s", w.Code, w.Body)
			}
			for _, c := range w.Result().Cookies() {
				if c.MaxAge != -1 || c.Secure != secure {
					t.Fatalf("clear: %+v", c)
				}
			}
			w = nativeRequest(mux, "GET", "/at/auth/me", "", "", rotated[0], configure)
			if w.Code != 401 {
				t.Fatalf("session survived logout: %d", w.Code)
			}

			r := httptest.NewRequest("POST", "/at/auth/mfa/verify", nil)
			configure(r)
			r.Header.Set("Origin", origin)
			bindingResponse := httptest.NewRecorder()
			binding, err := a.ensureSecurityBinding(bindingResponse, r)
			if err != nil {
				t.Fatal(err)
			}
			bindingCookie := bindingResponse.Result().Cookies()[0]
			if bindingCookie.Secure != secure || strings.HasPrefix(bindingCookie.Name, "__Secure-") != secure {
				t.Fatalf("MFA cookie: %+v", bindingCookie)
			}
			r.AddCookie(bindingCookie)
			if a.securityBinding(r) != binding {
				t.Fatal("MFA binding was lost")
			}
			if c := a.passkeyCookie(r, "ceremony", 300); c.Secure != secure {
				t.Fatal("ceremony transport mismatch")
			}
		})
	}
}

func TestNativeCookieTransportCannotBeDowngraded(t *testing.T) {
	a, _, _ := nativeFixture(t)
	a.allowedOrigins = []string{"http://localhost:8080"}
	for _, tt := range []struct {
		name, host, origin string
		tls                bool
	}{
		{"forged origin on public host", "at.example", "http://localhost:8080", false},
		{"forwarded localhost", "at.example", "", false},
		{"unlisted loopback", "127.0.0.1:8080", "", false},
		{"loopback TLS", "localhost:8080", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/at/auth/session", nil)
			r.Host = tt.host
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("X-Forwarded-Host", "localhost:8080")
			r.Header.Set("X-Forwarded-Proto", "http")
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if !a.cookieSecure(r) || a.sessionCookieName(r) != "__Secure-at_session" {
				t.Fatal("public/TLS request downgraded")
			}
		})
	}
}

func TestNativeAdditionalOrigins(t *testing.T) {
	a, _, mux := nativeFixture(t)
	a.allowedOrigins = []string{"https://alternate.example"}
	for _, tt := range []struct {
		name, origin, site string
		status             int
	}{
		{"primary", a.cfg.Origin, "same-origin", 200},
		{"additional", "https://alternate.example", "same-origin", 200},
		{"unknown", "https://evil.example", "same-origin", 403},
		{"suffix", "https://alternate.example.evil.example", "same-origin", 403},
		{"cross-site", "https://alternate.example", "cross-site", 403},
		{"missing", "", "same-origin", 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, tt.origin, nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", tt.site) })
			if w.Code != tt.status {
				t.Fatalf("login = %d: %s", w.Code, w.Body)
			}
			if tt.status == 200 {
				cookies := w.Result().Cookies()
				for _, c := range cookies {
					if !c.Secure || !c.HttpOnly || c.Domain != "" {
						t.Fatalf("cookie scope: %+v", c)
					}
				}
				probe := nativeRequest(mux, "GET", "/at/auth/session", "", tt.origin, cookies[0])
				if probe.Code != 200 || !strings.Contains(probe.Body.String(), `"subject":"reader"`) {
					t.Fatalf("probe: %d %s", probe.Code, probe.Body)
				}
			}
		})
	}
	a.allowedOrigins = nil
	w := nativeRequest(mux, "POST", "/at/auth/login", `{}`, "https://alternate.example", nil)
	if w.Code != 403 {
		t.Fatal("removed origin still admitted")
	}
}

func TestNativeOptionalSessionProbe(t *testing.T) {
	a, store, mux := nativeFixture(t)
	for _, tt := range []struct {
		name    string
		cookie  *http.Cookie
		refresh bool
	}{
		{"signed out", nil, false},
		{"refresh present", &http.Cookie{Name: a.refreshCookieName(nil), Value: strings.Repeat("A", 43)}, true},
		{"malformed refresh", &http.Cookie{Name: a.refreshCookieName(nil), Value: "invalid"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := nativeRequest(mux, "GET", "/at/auth/session", "", "", tt.cookie)
			var probe struct {
				Identity any  `json:"identity"`
				Refresh  bool `json:"refresh_available"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &probe); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || probe.Identity != nil || probe.Refresh != tt.refresh || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
				t.Fatalf("probe: %d %s", w.Code, w.Body)
			}
		})
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", nil); w.Code != 401 {
		t.Fatal("protected me must remain 401")
	}
	c := nativeLoginCookie(t, mux, "reader")
	store.fail = true
	if w := nativeRequest(mux, "GET", "/at/auth/session", "", "", c); w.Code != 503 {
		t.Fatalf("database error hidden: %d %s", w.Code, w.Body)
	}
}

func TestAuthSettingsDurationInput(t *testing.T) {
	for _, tt := range []struct {
		name, session, remember string
		want                    int64
		valid                   bool
	}{
		{"defaults", "8h", "30d", 2592000, true},
		{"weeks and days", "12h", "4w1d2h", 2512800, true},
		{"fractional hours", "1.5h", "1w", 604800, true},
		{"minimum", "10m", "10m", 600, true},
		{"normal too long", "2d", "4w", 0, false},
		{"remember too long", "8h", "5w", 0, false},
		{"remember shorter", "1d", "8h", 0, false},
		{"negative", "-8h", "4w", 0, false},
		{"empty", "", "4w", 0, false},
		{"missing unit", "28800", "4w", 0, false},
		{"subsecond", "8h1ms", "4w", 0, false},
		{"overflow", "999999999999999w", "4w", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := service.DefaultAuthSettings()
			v.Origin = "https://at.example"
			v.SessionTTLSeconds = 0
			v.RememberTTLSeconds = 0
			result, err := (authSettingsRequest{AuthSettings: v, SessionTTL: &tt.session, RememberTTL: &tt.remember}).settings()
			if (err == nil) != tt.valid {
				t.Fatalf("result: %+v error: %v", result, err)
			}
			if tt.valid && result.RememberTTLSeconds != tt.want {
				t.Fatalf("seconds: %d", result.RememberTTLSeconds)
			}
		})
	}
}
