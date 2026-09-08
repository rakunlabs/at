package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/password"
	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type fakeAuthStore struct {
	mu       sync.Mutex
	users    map[string]service.AuthUser
	sessions map[string]service.AuthSession
	claimed  bool
	fail     bool
}

func (f *fakeAuthStore) GetAuthUser(_ context.Context, name string) (*service.AuthUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("database unavailable")
	}
	u, ok := f.users[name]
	if !ok {
		return nil, nil
	}
	return &u, nil
}
func (f *fakeAuthStore) CreateAuthUser(_ context.Context, u service.AuthUser, bootstrap bool) (*service.AuthUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.Username]; ok || bootstrap && f.claimed {
		return nil, service.ErrAuthConflict
	}
	if bootstrap {
		f.claimed = true
		u.Admin = true
	}
	u.ID = u.Username
	f.users[u.Username] = u
	return &u, nil
}
func (f *fakeAuthStore) CreateAuthSession(_ context.Context, s service.AuthSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
			f.sessions[s.Hash] = s
			return nil
		}
	}
	return service.ErrAuthConflict
}
func (f *fakeAuthStore) ResolveAuthSession(_ context.Context, hash string) (*service.AuthUser, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, time.Time{}, errors.New("database unavailable")
	}
	s, ok := f.sessions[hash]
	if !ok {
		return nil, time.Time{}, nil
	}
	for _, u := range f.users {
		if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version && s.ExpiresAt.After(time.Now()) {
			return &u, s.ExpiresAt, nil
		}
	}
	return nil, time.Time{}, nil
}
func (f *fakeAuthStore) DeleteAuthSession(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, hash)
	return nil
}
func (f *fakeAuthStore) InvalidateAuthUser(_ context.Context, id string, disable bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, u := range f.users {
		if u.ID == id {
			u.Disabled = u.Disabled || disable
			u.SessionVersion++
			f.users[name] = u
			return true, nil
		}
	}
	return false, nil
}

func nativeTestConfig() config.Server {
	return config.Server{BasePath: "/at", NativeAuth: &config.NativeAuth{Enabled: true, Origin: "https://at.example", BootstrapToken: strings.Repeat("t", 32)}}
}

func nativeFixture(t *testing.T) (*nativeAuth, *fakeAuthStore, *ada.Server) {
	t.Helper()
	f := &fakeAuthStore{users: map[string]service.AuthUser{
		"admin":  {ID: "admin", Username: "admin", PasswordHash: password.Dummy, Admin: true},
		"reader": {ID: "reader", Username: "reader", PasswordHash: password.Dummy},
	}, sessions: make(map[string]service.AuthSession)}
	a, err := newNativeAuth(nativeTestConfig(), f)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(a.require(true))
	api.GET("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		httpResponseJSON(w, identity.FromContext(r.Context()), 200)
	})
	api.POST("/v1/test", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	return a, f, mux
}

func nativeRequest(handler http.Handler, method, target, body, origin string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func nativeLoginCookie(t *testing.T, mux http.Handler, user string) *http.Cookie {
	t.Helper()
	w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"`+user+`","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies: %+v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.Path != "/at/" || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 || !c.Expires.IsZero() || c.Domain != "" {
		t.Fatalf("unsafe cookie: %+v", c)
	}
	if strings.Contains(w.Body.String(), c.Value) || strings.Contains(w.Body.String(), "session_version") || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("credential leak: %s", w.Body)
	}
	return c
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
		{"disabled", func(c *config.Server) { c.NativeAuth.Enabled = false; c.ForwardAuth = &mforwardauth.ForwardAuth{} }, false},
		{"forward conflict", func(c *config.Server) { c.ForwardAuth = &mforwardauth.ForwardAuth{} }, true},
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
			cfg := nativeTestConfig()
			tt.mutate(&cfg)
			_, err := newNativeAuth(cfg, &fakeAuthStore{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := newNativeAuth(nativeTestConfig(), nil); err == nil {
		t.Fatal("accepted missing store")
	}
	if a, err := newNativeAuth(config.Server{}, nil); a != nil || err != nil {
		t.Fatal("legacy changed")
	}
}

func TestNativeAuthAdminOnlyAndCSRF(t *testing.T) {
	_, _, mux := nativeFixture(t)
	w := nativeRequest(mux, "GET", "/at/api/v1/test", "", "", nil)
	if w.Code != 401 || w.Header().Get("Location") != "" || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("not JSON 401: %d %s", w.Code, w.Body)
	}
	reader := nativeLoginCookie(t, mux, "reader")
	if w := nativeRequest(mux, "GET", "/at/api/v1/test", "", "", reader); w.Code != 403 {
		t.Fatalf("reader: %d", w.Code)
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 200 {
		t.Fatalf("reader me: %d", w.Code)
	}
	admin := nativeLoginCookie(t, mux, " ADMIN ")
	for _, tt := range []struct {
		method, origin string
		code           int
	}{
		{"GET", "", 200}, {"POST", "https://at.example", 204}, {"POST", "", 403}, {"POST", "https://evil.example", 403}, {"POST", "null", 403}, {"GET", "https://evil.example", 403},
	} {
		w := nativeRequest(mux, tt.method, "/at/api/v1/test", "{}", tt.origin, admin)
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
	c := nativeLoginCookie(t, mux, "admin")
	if _, ok := f.sessions[c.Value]; ok {
		t.Fatal("persisted raw cookie")
	}
	_, live, err := f.ResolveAuthAccess(t.Context(), nativeSessionHash(c.Value))
	if err != nil || live == nil {
		t.Fatal("missing access hash")
	}
	// A fresh issuer instance, as after a restart, can resolve the same session.
	restarted, err := newNativeAuth(nativeTestConfig(), f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Resolve(t.Context(), c.Value); err != nil {
		t.Fatal(err)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/logout", "", "https://evil.example", c); w.Code != 403 {
		t.Fatal("logout CSRF")
	}
	if w := nativeRequest(mux, "POST", "/at/auth/logout", "", "https://at.example", c); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if _, err := a.Resolve(t.Context(), c.Value); err == nil {
		t.Fatal("revoked session accepted")
	}
	c = nativeLoginCookie(t, mux, "admin")
	_, _ = f.InvalidateAuthUser(t.Context(), "admin", false)
	if w := nativeRequest(mux, "GET", "/at/api/v1/test", "", "", c); w.Code != 401 {
		t.Fatal("user revocation ignored")
	}
	stale := authIdentity(&service.AuthUser{ID: "admin", Admin: true})
	stale.Claims = map[string]any{"session_version": int64(0)}
	if _, err := a.Issue(t.Context(), stale); err == nil {
		t.Fatal("stale login issued session after revocation")
	}
	c = nativeLoginCookie(t, mux, "reader")
	_, _ = f.InvalidateAuthUser(t.Context(), "reader", true)
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
		t.Fatal("disabled user accepted")
	}
	w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", nil)
	if w.Code != 401 {
		t.Fatalf("disabled login: %d", w.Code)
	}
	c = nativeLoginCookie(t, mux, "admin")
	_, live, _ = f.ResolveAuthAccess(t.Context(), nativeSessionHash(c.Value))
	s := *live
	s.ExpiresAt = time.Now().Add(-time.Second)
	f.sessions[s.Hash] = s
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
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
		w := nativeRequest(mux, "POST", "/at/auth/login", tt.body, "https://at.example", nil)
		if w.Code != tt.code {
			t.Fatalf("input: %d want %d %s", w.Code, tt.code, w.Body)
		}
	}
	if w := nativeRequest(mux, "POST", "/at/auth/bootstrap", `{}`, "https://at.example", nil); w.Code != 401 {
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
	if !f.users["first"].Admin || f.users["first"].PasswordHash == "a sufficiently long password" {
		t.Fatal("bad bootstrap record")
	}
	if w := bootstrap(); w.Code != 409 {
		t.Fatal("bootstrap repeated")
	}
	a.loginLimit = rate.NewLimiter(0, 1)
	_ = nativeRequest(mux, "POST", "/at/auth/login", `{}`, a.cfg.Origin, nil)
	if w := nativeRequest(mux, "POST", "/at/auth/login", `{}`, a.cfg.Origin, nil); w.Code != 429 {
		t.Fatal("login rate limit ignored")
	}
	f.fail = true
	if w := nativeRequest(mux, "GET", "/at/api/v1/test", "", "", &http.Cookie{Name: a.session.CookieName, Value: strings.Repeat("A", 43)}); w.Code == 200 {
		t.Fatal("failed open")
	}
}

func TestNativeAuthProductionRoutesPostgres(t *testing.T) {
	store := postgrestest.New(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	s, err := New(ctx, cfg, nil, store, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/at/", 200}, {"GET", "/at/auth/status", 200},
		{"GET", "/at/api/v1/info", 401}, {"GET", "/at/api/v1/providers", 401},
		{"POST", "/at/api/v1/settings/rotate-key", 401}, {"POST", "/at/internal/v1/mcp/test", 401},
		{"GET", "/at/gateway/v1/health", 200},
	} {
		w := nativeRequest(s.server, tt.method, tt.path, "{}", "", nil)
		if w.Code != tt.code {
			t.Errorf("%s: %d want %d %s", tt.path, w.Code, tt.code, w.Body)
		}
	}
	if _, err := store.CreateAuthUser(ctx, service.AuthUser{Username: "admin", PasswordHash: password.Dummy}, true); err != nil {
		t.Fatal(err)
	}
	admin := nativeLoginCookie(t, s.server, "admin")
	if w := nativeRequest(s.server, "GET", "/at/api/v1/info", "", "", admin); w.Code != 200 {
		t.Fatalf("admin info: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(s.server, "GET", "/at/gateway/v1/models", "", "", admin); w.Code != 401 {
		t.Fatal("management cookie authenticated gateway")
	}
	// Even a raw upstream provider sees no browser cookies in native mode.
	// The production route's request object is stripped before dispatch/auth.
	r := httptest.NewRequest("GET", "/at/gateway/v1/models", nil)
	r.AddCookie(admin)
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	if r.Header.Get("Cookie") != "" {
		t.Fatal("gateway retained management cookie")
	}
	w = nativeRequest(s.server, "POST", "/at/auth/users", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, "https://at.example", admin)
	if w.Code != 201 {
		t.Fatalf("create reader: %d %s", w.Code, w.Body)
	}
	var readerID identity.Identity
	if err := json.Unmarshal(w.Body.Bytes(), &readerID); err != nil {
		t.Fatal(err)
	}
	reader := nativeLoginCookie(t, s.server, "reader")
	if w := nativeRequest(s.server, "GET", "/at/api/v1/files/browse", "", "", reader); w.Code != 403 {
		t.Fatalf("reader file access: %d", w.Code)
	}
	if w := nativeRequest(s.server, "POST", "/at/auth/users/"+readerID.Subject+"/disable", "", "https://at.example", admin); w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(s.server, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("disabled reader session remained active")
	}
	if w := nativeRequest(s.server, "POST", "/at/auth/users/"+readerID.Subject+"/revoke-sessions", "", "https://at.example", admin); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	// Disabling the opt-in preserves the existing unguarded management routes
	// and separately guarded settings; it does not enable a gateway cookie login.
	cfg.NativeAuth = nil
	legacy, err := New(ctx, cfg, nil, store, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/at/api/v1/info", 200},
		{"POST", "/at/api/v1/settings/rotate-key", 403},
		{"GET", "/at/gateway/v1/models", 401},
	} {
		w := nativeRequest(legacy.server, tt.method, tt.path, "{}", "", nil)
		if w.Code != tt.code {
			t.Errorf("legacy %s: %d want %d", tt.path, w.Code, tt.code)
		}
	}
}
