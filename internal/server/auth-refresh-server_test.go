package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeRefreshHTTPAndStableCeremonies(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := nativeauth.New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 1000)
	mux := ada.New()
	a.Register(mux, "/at")
	login := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`","remember_me":true}`, a.Origin(), nil)
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
	state, err := a.NewOAuthState(request, "payload", "callback")
	if err != nil {
		t.Fatal(err)
	}
	pkceBefore, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(request, "google", "connection")
	if err != nil {
		t.Fatal(err)
	}
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, old, false)
	for _, tt := range []struct {
		body, origin string
		code         int
	}{{"{}", "", 403}, {"{}", "https://evil.example", 403}, {`{"ttl":100}`, a.Origin(), 400}, {"{}", a.Origin(), 200}} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", tt.body, tt.origin, refresh)
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
		if strings.Contains(w.Body.String(), cookies[0].Value) || strings.Contains(w.Body.String(), cookies[1].Value) || strings.Contains(w.Body.String(), nativeauth.SessionHash(cookies[1].Value)) {
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
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", old); w.Code != 401 {
		t.Fatal("retired access accepted")
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.Origin(), cookies[1]); w.Code != 429 || w.Header().Get("Retry-After") == "" || len(w.Result().Cookies()) != 0 {
		t.Fatal("early refresh contract", w.Code, w.Body)
	}
	request = httptest.NewRequest("GET", "/at/api/v1/oauth/callback?state="+state, nil)
	request.AddCookie(cookies[0])
	if got, ok := a.TakeOAuthState(httptest.NewRecorder(), request, "callback"); !ok || got != "payload" {
		t.Fatal("refresh broke OAuth")
	}
	if got, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(request, "google", "connection"); err != nil || got != pkceBefore {
		t.Fatal("refresh broke PKCE")
	}
	key := nativeauthtest.NewSoftwarePasskey(t)
	response := key.Response(t, true, challenge, a.Origin(), "at.example", u.ID, 0x45, 0, false)
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.Origin(), ceremony, cookies[0]); w.Code != 204 {
		t.Fatal("refresh broke enrollment", w.Code, w.Body)
	}
	// Fresh authentication must not inherit an earlier enrollment binding.
	challenge, ceremony = nativeauthtest.PasskeyBegin(t, mux, true, cookies[0], false)
	freshLogin := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, a.Origin(), nil)
	if freshLogin.Code != 200 {
		t.Fatal(freshLogin.Code, freshLogin.Body)
	}
	freshCookies := freshLogin.Result().Cookies()
	fresh := freshCookies[0]
	key = nativeauthtest.NewSoftwarePasskey(t)
	response = key.Response(t, true, challenge, a.Origin(), "at.example", u.ID, 0x45, 0, false)
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.Origin(), ceremony, fresh); w.Code != 401 {
		t.Fatal("fresh login inherited ceremony", w.Code)
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.Origin(), refresh); w.Code != 401 {
		t.Fatal("replay accepted", w.Code)
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", cookies[0]); w.Code != 401 {
		t.Fatal("replay did not revoke access")
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", fresh); w.Code != 200 {
		t.Fatal("replay revoked another device")
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal("missing enrolled key", err)
	}
	w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.Origin(), fresh)
	if w.Code != 204 || len(w.Result().Cookies()) != 2 {
		t.Fatal("passkey deletion", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.Origin(), freshCookies[1]); w.Code != 401 {
		t.Fatal("passkey deletion left refresh", w.Code)
	}
}

func TestNativeRefreshConnectorRoutes(t *testing.T) {
	p := postgrestest.New(t, nil)
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: nativeauthtest.PasswordHash, Admin: true}, false); err != nil {
		t.Fatal(err)
	}
	a, err := nativeauth.New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 100)
	vars := newFakeVariableStore()
	vars.vars["id"] = &service.Variable{Key: "test_client_id", Value: "client"}
	vars.vars["secret"] = &service.Variable{Key: "test_client_secret", Value: "secret"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpResponseJSON(w, map[string]string{"access_token": "upstream-token"}, 200)
	}))
	defer upstream.Close()
	s := &Server{nativeAuth: a, config: nativeauthtest.Config(), variableStore: vars, builtinConnectors: []service.Connector{{Slug: "test", AuthKind: service.ConnectorAuthOAuth2, OAuth: &service.ConnectorOAuth{AuthURL: "https://provider.example/authorize", TokenURL: upstream.URL, UsePKCE: true}}}}
	mux := ada.New()
	a.Register(mux, "/at")
	api := mux.Group("/at/api/v1/oauth")
	api.Use(a.Require(true))
	api.GET("/start", s.OAuthStartAPI)
	api.GET("/callback", s.OAuthCallbackAPI)
	login := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"admin","password":"`+strings.Repeat("x", 32)+`"}`, a.Origin(), nil)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	start := nativeauthtest.Request(mux, "GET", "/at/api/v1/oauth/start?provider=test", "", "", cookies[0])
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
	rotated := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.Origin(), cookies[1])
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
	c := &http.Cookie{Name: a.RefreshCookieName(nil), Value: strings.Repeat("a", 43)}
	for _, tt := range []struct {
		body   string
		cookie *http.Cookie
		code   int
	}{{"null", c, 400}, {"[]", c, 400}, {"{} {}", c, 400}, {`{"remember_me":true}`, c, 400}, {"{}", nil, 401}, {"{}", c, 401}} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", tt.body, a.Origin(), tt.cookie)
		if w.Code != tt.code || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body)
		}
	}
	f.Fail = true
	if key, err := (&Server{nativeAuth: a}).manualOAuthPKCEKey(httptest.NewRequest("GET", "/", nil), "test", "connection"); err == nil || key != "" {
		t.Fatal("native PKCE fell back to unbound key")
	}
	w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.Origin(), c)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "authentication unavailable") {
		t.Fatal(w.Code, w.Body)
	}
}
