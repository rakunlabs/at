package nativeauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/httpx"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
)

func nativeFixture(t *testing.T) (*Auth, *nativeauthtest.FakeStore, *ada.Server) {
	t.Helper()
	f := &nativeauthtest.FakeStore{Users: map[string]service.AuthUser{
		"admin":  {ID: "admin", Username: "admin", PasswordHash: nativeauthtest.PasswordHash, Admin: true},
		"reader": {ID: "reader", Username: "reader", PasswordHash: nativeauthtest.PasswordHash},
	}, Sessions: make(map[string]service.AuthSession)}
	a, err := New(nativeauthtest.Config(), f)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.Register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(a.Require(true))
	api.GET("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, identity.FromContext(r.Context()), 200)
	})
	api.POST("/v1/test", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	return a, f, mux
}

func TestNativeAuthConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*config.Server)
		wantErr bool
	}{
		{"https", func(*config.Server) {}, false},
		{"24 hour session", func(c *config.Server) { c.NativeAuth.SessionTTL = 24 * time.Hour }, false},
		{"negative session", func(c *config.Server) { c.NativeAuth.SessionTTL = -time.Second }, true},
		{"short session", func(c *config.Server) { c.NativeAuth.SessionTTL = 9 * time.Minute }, true},
		{"long session", func(c *config.Server) { c.NativeAuth.SessionTTL = 25 * time.Hour }, true},
		{"short remember", func(c *config.Server) { c.NativeAuth.RememberTTL = time.Hour }, true},
		{"long remember", func(c *config.Server) { c.NativeAuth.RememberTTL = 31 * 24 * time.Hour }, true},
		{"custom remember", func(c *config.Server) { c.NativeAuth.RememberTTL = 24 * time.Hour }, false},
		{"disabled", func(c *config.Server) { c.NativeAuth.Enabled = false }, false},
		{"missing origin", func(c *config.Server) { c.NativeAuth.Origin = "" }, true},
		{"origin path", func(c *config.Server) { c.NativeAuth.Origin += "/at" }, true},
		{"origin credentials", func(c *config.Server) { c.NativeAuth.Origin = "https://user:pass@at.example" }, true},
		{"weak bootstrap", func(c *config.Server) { c.NativeAuth.BootstrapToken = "weak" }, true},
		{"bootstrap removed", func(c *config.Server) { c.NativeAuth.BootstrapToken = "" }, false},
		{"http denied", func(c *config.Server) { c.NativeAuth.Origin = "http://localhost:8080" }, true},
		{"http loopback", func(c *config.Server) {
			c.NativeAuth.Origin = "http://localhost:8080"
			c.NativeAuth.InsecureHTTP = true
		}, false},
		{"http remote denied", func(c *config.Server) { c.NativeAuth.Origin = "http://at.example"; c.NativeAuth.InsecureHTTP = true }, true},
		{"bad base", func(c *config.Server) { c.BasePath = "/at/../other" }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := nativeauthtest.Config()
			tt.mutate(&cfg)
			_, err := New(cfg, &nativeauthtest.FakeStore{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := New(nativeauthtest.Config(), nil); err == nil {
		t.Fatal("accepted missing store")
	}
	if a, err := New(config.Server{}, nil); a != nil || err != nil {
		t.Fatal("legacy changed")
	}
}

func TestNativeAuthAdminOnlyAndCSRF(t *testing.T) {
	_, _, mux := nativeFixture(t)
	w := nativeauthtest.Request(mux, "GET", "/at/api/v1/test", "", "", nil)
	if w.Code != 401 || w.Header().Get("Location") != "" || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("not JSON 401: %d %s", w.Code, w.Body)
	}
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	if w := nativeauthtest.Request(mux, "GET", "/at/api/v1/test", "", "", reader); w.Code != 403 {
		t.Fatalf("reader: %d", w.Code)
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 200 {
		t.Fatalf("reader me: %d", w.Code)
	}
	admin := nativeauthtest.LoginCookie(t, mux, " ADMIN ")
	for _, tt := range []struct {
		method, origin string
		code           int
	}{
		{"GET", "", 200}, {"POST", "https://at.example", 204}, {"POST", "", 403}, {"POST", "https://evil.example", 403}, {"POST", "null", 403}, {"GET", "https://evil.example", 403},
	} {
		w := nativeauthtest.Request(mux, tt.method, "/at/api/v1/test", "{}", tt.origin, admin)
		if w.Code != tt.code {
			t.Fatalf("%s %q: %d %s", tt.method, tt.origin, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("GET", "/at/api/v1/test", nil)
	r.Header.Set("Authorization", "Bearer gateway-token")
	r.Header.Set("X-User", "admin")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("accepted bearer or identity header")
	}
	r.AddCookie(admin)
	r.AddCookie(admin)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("accepted ambiguous cookie")
	}
}

func TestNativeAuthSessionLifecycle(t *testing.T) {
	a, f, mux := nativeFixture(t)
	c := nativeauthtest.LoginCookie(t, mux, "admin")
	if _, ok := f.Sessions[c.Value]; ok {
		t.Fatal("persisted raw cookie")
	}
	_, live, err := f.ResolveAuthAccess(t.Context(), SessionHash(c.Value))
	if err != nil || live == nil {
		t.Fatal("missing access hash")
	}
	// A fresh issuer instance, as after a restart, can resolve the same session.
	restarted, err := New(nativeauthtest.Config(), f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Resolve(t.Context(), c.Value); err != nil {
		t.Fatal(err)
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/logout", "", "https://evil.example", c); w.Code != 403 {
		t.Fatal("logout CSRF")
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/logout", "", "https://at.example", c); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if _, err := a.Resolve(t.Context(), c.Value); err == nil {
		t.Fatal("revoked session accepted")
	}
	c = nativeauthtest.LoginCookie(t, mux, "admin")
	_, _ = f.InvalidateAuthUser(t.Context(), "admin", false)
	if w := nativeauthtest.Request(mux, "GET", "/at/api/v1/test", "", "", c); w.Code != 401 {
		t.Fatal("user revocation ignored")
	}
	stale := Identity(&service.AuthUser{ID: "admin", Admin: true})
	stale.Claims = map[string]any{"session_version": int64(0)}
	if _, err := a.Issue(t.Context(), stale); err == nil {
		t.Fatal("stale login issued session after revocation")
	}
	c = nativeauthtest.LoginCookie(t, mux, "reader")
	_, _ = f.InvalidateAuthUser(t.Context(), "reader", true)
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
		t.Fatal("disabled user accepted")
	}
	w := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", nil)
	if w.Code != 401 {
		t.Fatalf("disabled login: %d", w.Code)
	}
	c = nativeauthtest.LoginCookie(t, mux, "admin")
	_, live, _ = f.ResolveAuthAccess(t.Context(), SessionHash(c.Value))
	s := *live
	s.ExpiresAt = time.Now().Add(-time.Second)
	f.Sessions[s.Hash] = s
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestNativeAuthInputAndBootstrap(t *testing.T) {
	a, f, mux := nativeFixture(t)
	for _, tt := range []struct {
		body string
		code int
	}{
		{`{"username":"admin","password":"bad"}`, 401},
		{`{"username":"missing","password":"bad"}`, 401},
		{`{"password":"` + strings.Repeat("x", 5000) + `"}`, 413},
		{`{} {}`, 400}, {`{"extra":true}`, 400},
	} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/login", tt.body, "https://at.example", nil)
		if w.Code != tt.code {
			t.Fatalf("input: %d want %d %s", w.Code, tt.code, w.Body)
		}
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/bootstrap", `{}`, "https://at.example", nil); w.Code != 401 {
		t.Fatal("bootstrap without operator token")
	}
	bootstrap := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/at/auth/bootstrap", strings.NewReader(`{"username":"first","password":"a sufficiently long password"}`))
		r.Header.Set("Origin", a.cfg.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+a.cfg.BootstrapToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := bootstrap(); w.Code != 201 {
		t.Fatalf("bootstrap %d %s", w.Code, w.Body)
	}
	if !f.Users["first"].Admin || f.Users["first"].PasswordHash == "a sufficiently long password" {
		t.Fatal("bad bootstrap record")
	}
	if w := bootstrap(); w.Code != 409 {
		t.Fatal("bootstrap repeated")
	}
	a.LoginLimit = rate.NewLimiter(0, 1)
	_ = nativeauthtest.Request(mux, "POST", "/at/auth/login", `{}`, a.cfg.Origin, nil)
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{}`, a.cfg.Origin, nil); w.Code != 429 {
		t.Fatal("login rate limit ignored")
	}
	f.Fail = true
	if w := nativeauthtest.Request(mux, "GET", "/at/api/v1/test", "", "", &http.Cookie{Name: a.session.CookieName, Value: strings.Repeat("A", 43)}); w.Code == 200 {
		t.Fatal("failed open")
	}
}
