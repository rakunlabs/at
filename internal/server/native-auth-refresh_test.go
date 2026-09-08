package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
	"golang.org/x/time/rate"
)

func (f *fakeAuthStore) ResolveAuthAccess(ctx context.Context, hash string) (*service.AuthUser, *service.AuthSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, nil, errors.New("database unavailable")
	}
	for _, s := range f.sessions {
		if s.AccessHash != hash || !s.AccessExpiresAt.After(time.Now()) || !s.ExpiresAt.After(time.Now()) {
			continue
		}
		for _, u := range f.users {
			if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
				return &u, &s, nil
			}
		}
	}
	return nil, nil, nil
}

func TestNativeRefreshHTTPAndStableCeremonies(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 1000)
	mux := ada.New()
	a.register(mux, "/at")
	login := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`","remember_me":true}`, a.cfg.Origin, nil)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatal("missing pair")
	}
	old := cookies[0]
	refresh := cookies[1]
	pair, err := a.Resolve(t.Context(), old.Value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/at/api/v1/oauth/start", nil)
	request.AddCookie(old)
	state, err := a.newOAuthState(request, "payload", "callback")
	if err != nil {
		t.Fatal(err)
	}
	pkceBefore, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(request, "google", "connection")
	if err != nil {
		t.Fatal(err)
	}
	challenge, ceremony := passkeyBegin(t, mux, true, old, false)
	for _, tt := range []struct {
		body, origin string
		code         int
	}{{"{}", "", 403}, {"{}", "https://evil.example", 403}, {`{"ttl":100}`, a.cfg.Origin, 400}, {"{}", a.cfg.Origin, 200}} {
		w := nativeRequest(mux, "POST", "/at/auth/refresh", tt.body, tt.origin, refresh)
		if w.Code != tt.code {
			t.Fatal("refresh", w.Code, w.Body)
		}
		if w.Code != 200 {
			continue
		}
		cookies = w.Result().Cookies()
		if len(cookies) != 2 || cookies[0].Value == old.Value || cookies[1].Value == refresh.Value {
			t.Fatal("pair did not rotate")
		}
		if strings.Contains(w.Body.String(), cookies[0].Value) || strings.Contains(w.Body.String(), cookies[1].Value) || strings.Contains(w.Body.String(), nativeSessionHash(cookies[1].Value)) {
			t.Fatal("secret leaked")
		}
		var body struct {
			Claims struct {
				SessionID string    `json:"session_id"`
				Deadline  time.Time `json:"session_expires_at"`
			} `json:"claims"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Claims.SessionID != pair.SessionID || !body.Claims.Deadline.Equal(pair.Refresh.ExpiresAt) {
			t.Fatal("family moved")
		}
		if cookies[1].MaxAge > refresh.MaxAge || cookies[0].MaxAge > 600 || !cookies[1].Expires.Equal(refresh.Expires) {
			t.Fatal("sliding cookie expiry")
		}
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", old); w.Code != 401 {
		t.Fatal("retired access accepted")
	}
	if w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, cookies[1]); w.Code != 429 || w.Header().Get("Retry-After") == "" || len(w.Result().Cookies()) != 0 {
		t.Fatal("early refresh contract", w.Code, w.Body)
	}
	request = httptest.NewRequest("GET", "/at/api/v1/oauth/callback?state="+state, nil)
	request.AddCookie(cookies[0])
	if got, ok := a.takeOAuthState(httptest.NewRecorder(), request, "callback"); !ok || got != "payload" {
		t.Fatal("refresh broke OAuth")
	}
	if got, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(request, "google", "connection"); err != nil || got != pkceBefore {
		t.Fatal("refresh broke PKCE")
	}
	key := newSoftwarePasskey(t)
	response := key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	if w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, cookies[0]); w.Code != 204 {
		t.Fatal("refresh broke enrollment", w.Code, w.Body)
	}
	// Fresh authentication must not inherit an earlier enrollment binding.
	challenge, ceremony = passkeyBegin(t, mux, true, cookies[0], false)
	freshLogin := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, nil)
	if freshLogin.Code != 200 {
		t.Fatal(freshLogin.Code, freshLogin.Body)
	}
	freshCookies := freshLogin.Result().Cookies()
	fresh := freshCookies[0]
	key = newSoftwarePasskey(t)
	response = key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	if w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, fresh); w.Code != 401 {
		t.Fatal("fresh login inherited ceremony", w.Code)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, refresh); w.Code != 401 {
		t.Fatal("replay accepted", w.Code)
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", cookies[0]); w.Code != 401 {
		t.Fatal("replay did not revoke access")
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", fresh); w.Code != 200 {
		t.Fatal("replay revoked another device")
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal("missing enrolled key", err)
	}
	w := passkeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, fresh)
	if w.Code != 204 || len(w.Result().Cookies()) != 2 {
		t.Fatal("passkey deletion", w.Code, w.Body)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, freshCookies[1]); w.Code != 401 {
		t.Fatal("passkey deletion left refresh", w.Code)
	}
}

func TestNativeRefreshExpiredAccessLogout(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	a.register(mux, "/at")
	for _, logout := range []bool{false, true} {
		access, refresh, err := nativeCredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		s := service.AuthSession{Hash: nativeSessionHash(access + refresh), AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Minute), AccessExpiresAt: time.Now().Add(-time.Minute)}
		if err := p.CreateAuthSession(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		c := &http.Cookie{Name: a.refreshCookieName(), Value: refresh}
		if logout {
			if w := nativeRequest(mux, "POST", "/at/auth/logout", "", a.cfg.Origin, c); w.Code != 204 || len(w.Result().Cookies()) != 2 {
				t.Fatal("expired-access logout", w.Code)
			}
			if w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, c); w.Code != 401 {
				t.Fatal("logout left refresh", w.Code)
			}
		} else {
			w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, c)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			next := w.Result().Cookies()
			for _, cookie := range next {
				if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
					t.Fatal("nonremember persisted")
				}
			}
			_, live, err := p.ResolveAuthAccess(t.Context(), nativeSessionHash(next[0].Value))
			if err != nil || live == nil || !live.AccessExpiresAt.Equal(live.ExpiresAt) {
				t.Fatal("access not capped to absolute", err)
			}
		}
	}
}

type blockingAuthCleanup struct {
	service.AuthCredentialStorer
	started chan struct{}
}

func (s *blockingAuthCleanup) CleanupAuthCredentials(ctx context.Context, limit uint) (int64, error) {
	close(s.started)
	<-ctx.Done()
	return 0, ctx.Err()
}
func TestNativeRefreshJanitorStops(t *testing.T) {
	store := &blockingAuthCleanup{started: make(chan struct{})}
	a := &nativeAuth{credentials: store}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { a.runAuthJanitor(ctx); close(done) }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("no initial sweep")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("janitor leaked")
	}
}

func TestNativeRefreshConnectorRoutes(t *testing.T) {
	p := postgrestest.New(t, nil)
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy, Admin: true}, false); err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	vars := newFakeVariableStore()
	vars.vars["id"] = &service.Variable{Key: "test_client_id", Value: "client"}
	vars.vars["secret"] = &service.Variable{Key: "test_client_secret", Value: "secret"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpResponseJSON(w, map[string]string{"access_token": "upstream-token"}, 200)
	}))
	defer upstream.Close()
	s := &Server{nativeAuth: a, config: nativeTestConfig(), variableStore: vars, builtinConnectors: []service.Connector{{Slug: "test", AuthKind: service.ConnectorAuthOAuth2, OAuth: &service.ConnectorOAuth{AuthURL: "https://provider.example/authorize", TokenURL: upstream.URL, UsePKCE: true}}}}
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api/v1/oauth")
	api.Use(a.require(true))
	api.GET("/start", s.OAuthStartAPI)
	api.GET("/callback", s.OAuthCallbackAPI)
	login := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"admin","password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, nil)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	start := nativeRequest(mux, "GET", "/at/api/v1/oauth/start?provider=test", "", "", cookies[0])
	if start.Code != 200 {
		t.Fatal(start.Code, start.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(start.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	authorize, err := url.Parse(body["url"])
	if err != nil {
		t.Fatal(err)
	}
	if authorize.Query().Get("code_challenge") == "" {
		t.Fatal("PKCE missing")
	}
	rotated := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, cookies[1])
	if rotated.Code != 200 {
		t.Fatal(rotated.Code, rotated.Body)
	}
	r := httptest.NewRequest("GET", "/at/api/v1/oauth/callback?code=code&state="+url.QueryEscape(authorize.Query().Get("state")), nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.AddCookie(rotated.Result().Cookies()[0])
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || len(vars.created) != 1 {
		t.Fatal("rotated production callback failed", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("OAuth state replay accepted", w.Code)
	}
}

func TestNativeRefreshFailureContract(t *testing.T) {
	a, f, mux := nativeFixture(t)
	c := &http.Cookie{Name: a.refreshCookieName(), Value: strings.Repeat("a", 43)}
	for _, tt := range []struct {
		body   string
		cookie *http.Cookie
		code   int
	}{{"null", c, 400}, {"[]", c, 400}, {"{} {}", c, 400}, {`{"remember_me":true}`, c, 400}, {"{}", nil, 401}, {"{}", c, 401}} {
		w := nativeRequest(mux, "POST", "/at/auth/refresh", tt.body, a.cfg.Origin, tt.cookie)
		if w.Code != tt.code || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body)
		}
	}
	f.fail = true
	if key, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(httptest.NewRequest("GET", "/", nil), "test", "connection"); err == nil || key != "" {
		t.Fatal("native PKCE fell back to unbound key")
	}
	w := nativeRequest(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, c)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "authentication unavailable") {
		t.Fatal(w.Code, w.Body)
	}
}

func (f *fakeAuthStore) RotateAuthRefresh(ctx context.Context, hash, access, refresh string) (*service.AuthUser, *service.AuthSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, nil, errors.New("database unavailable")
	}
	for id, s := range f.sessions {
		if s.RefreshHash != hash || !s.ExpiresAt.After(time.Now()) {
			continue
		}
		for _, u := range f.users {
			if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
				s.AccessHash = access
				s.RefreshHash = refresh
				s.AccessExpiresAt = time.Now().Add(10 * time.Minute)
				if s.AccessExpiresAt.After(s.ExpiresAt) {
					s.AccessExpiresAt = s.ExpiresAt
				}
				f.sessions[id] = s
				return &u, &s, nil
			}
		}
	}
	return nil, nil, nil
}

func (f *fakeAuthStore) RevokeAuthCredential(ctx context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("database unavailable")
	}
	for id, s := range f.sessions {
		if s.AccessHash == hash || s.RefreshHash == hash {
			delete(f.sessions, id)
		}
	}
	return nil
}

func (f *fakeAuthStore) CleanupAuthCredentials(ctx context.Context, limit uint) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var n int64
	for id, s := range f.sessions {
		if n >= int64(limit) {
			break
		}
		if !s.ExpiresAt.After(time.Now()) {
			delete(f.sessions, id)
			n++
		}
	}
	return n, nil
}
