package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestMobileCanonicalSecrets(t *testing.T) {
	s := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tt := range []struct {
		s     string
		valid bool
	}{{s, true}, {s + "=", false}, {s[:42] + "B", false}, {strings.Repeat("a", 42), false}, {strings.Repeat("a", 44), false}, {"", false}} {
		if validMobileSecret(tt.s) != tt.valid {
			t.Errorf("canonical validation %q", tt.s)
		}
	}
	if mobileChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("RFC 7636 vector")
	}
}

func TestMobileProductionRoutes(t *testing.T) {
	p := postgrestest.New(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// An unclaimed installation reports setup, not a usable login descriptor.
	w := nativeRequest(s.server, "GET", "/at/auth/status", "", "", nil)
	var status struct {
		SetupRequired bool           `json:"setup_required"`
		Mobile        map[string]any `json:"mobile_auth"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.SetupRequired || status.Mobile != nil {
		t.Fatal("unclaimed installation exposed mobile auth", status)
	}
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy}, true)
	if err != nil {
		t.Fatal(err)
	}
	w = nativeRequest(s.server, "GET", "/at/auth/status", "", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.SetupRequired || status.Mobile["version"] != float64(1) || status.Mobile["issuer"] != "https://at.example/at" || status.Mobile["callback_uri"] != nativeMobileCallback {
		t.Fatal(status)
	}
	access, refresh, err := nativeCredentialPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "mobile", UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/at/auth/me", "", 200},
		{"GET", "/at/api/v1/info", "", 200},
		{"GET", "/at/gateway/v1/models", "", 401},
		{"POST", "/at/auth/users", `{"username":"created-mobile","password":"a sufficiently long password"}`, 201},
		{"POST", "/at/auth/password", `{}`, 401},
	} {
		r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%s %d: %s", tt.path, w.Code, w.Body)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("bearer request set cookies", tt.path)
		}
	}
}

func TestMobileHTTPContract(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.mobileBeginSources["192.0.2.1"] = nativeMobileSource{limit: rate.NewLimiter(rate.Inf, 1000), lastSeen: time.Now()}
	mux := ada.New()
	a.register(mux, "/at")
	admin := mux.Group("/at/api/v1")
	admin.Use(a.require(true))
	admin.GET("/probe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	call := func(method, path, body, bearer, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "evil.example"
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store", path)
		}
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
		}
	}
	login := call("POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"}`, "", a.cfg.Origin, nil)
	check(login, 200)
	web := login.Result().Cookies()[0]
	webRefresh := login.Result().Cookies()[1]
	begin := func() (string, string, string) {
		t.Helper()
		verifier, state, err := nativeCredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"code_challenge": mobileChallenge(verifier), "code_challenge_method": "S256", "state": state, "remember_me": true, "device_name": "Test phone"})
		w := call("POST", "/at/auth/mobile/begin", string(body), "", "", nil)
		check(w, 200)
		var response struct {
			URL     string `json:"authorization_url"`
			Expires int    `json:"expires_in"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(response.URL, a.cfg.Origin+"/at/#/mobile-authorize?request_id=") || response.Expires != 300 {
			t.Fatal(response)
		}
		id := strings.Split(response.URL, "request_id=")[1]
		return id, verifier, state
	}
	decide := func(id, state string, approve bool) string {
		t.Helper()
		path := "/at/auth/mobile/approve"
		if !approve {
			path = "/at/auth/mobile/deny"
		}
		w := call("POST", path, `{"request_id":"`+id+`"}`, "", a.cfg.Origin, web)
		check(w, 200)
		var response map[string]string
		json.Unmarshal(w.Body.Bytes(), &response)
		uri, err := url.Parse(response["redirect_url"])
		if err != nil {
			t.Fatal(err)
		}
		if uri.Scheme != "atmobile" || uri.Host != "auth" || uri.Path != "/callback" || uri.Query().Get("state") != state {
			t.Fatal(uri)
		}
		if !approve && uri.Query().Get("error") != "access_denied" {
			t.Fatal(uri)
		}
		return uri.Query().Get("code")
	}
	var tokens struct {
		Access         string    `json:"access_token"`
		Refresh        string    `json:"refresh_token"`
		Session        string    `json:"session_id"`
		Issuer         string    `json:"issuer"`
		Deadline       time.Time `json:"session_expires_at"`
		AccessDeadline time.Time `json:"access_expires_at"`
	}
	id, verifier, state := begin()
	validBegin := `{"code_challenge":"` + mobileChallenge(verifier) + `","code_challenge_method":"S256","state":"` + state + `"}`
	for _, body := range []string{
		strings.TrimSuffix(validBegin, "}") + `,"redirect_uri":"https://evil.example"}`,
		strings.TrimSuffix(validBegin, "}") + `,"user_id":"` + u.ID + `"}`,
		strings.Replace(validBegin, "S256", "plain", 1),
		strings.Replace(validBegin, state, state+"=", 1),
		"null", "{} {}",
	} {
		check(call("POST", "/at/auth/mobile/begin", body, "", "", nil), 400)
	}
	check(call("POST", "/at/auth/mobile/begin", validBegin, web.Value, "", nil), 400)
	check(call("POST", "/at/auth/mobile/begin", validBegin, "", "", web), 400)
	check(call("POST", "/at/auth/mobile/begin", `{"device_name":"`+strings.Repeat("x", 4096)+`"}`, "", "", nil), 413)
	check(call("GET", "/at/auth/mobile/requests/"+id, "", "", "", nil), 401)
	details := call("GET", "/at/auth/mobile/requests/"+id, "", "", "", web)
	check(details, 200)
	if strings.Contains(details.Body.String(), state) || strings.Contains(details.Body.String(), mobileChallenge(verifier)) {
		t.Fatal("details leaked PKCE/state")
	}
	check(call("POST", "/at/auth/mobile/approve", `{"request_id":"`+id+`"}`, "", "https://evil.example", web), 403)
	code := decide(id, state, true)
	// Exhausted begin admission must not prevent exchange or refresh.
	a.mobileBeginSources["192.0.2.1"] = nativeMobileSource{limit: rate.NewLimiter(0, 0), lastSeen: time.Now()}
	check(call("POST", "/at/auth/mobile/approve", `{"request_id":"`+id+`"}`, "", a.cfg.Origin, web), 400)
	exchange := `{"code":"` + code + `","code_verifier":"` + verifier + `"}`
	w := call("POST", "/at/auth/mobile/token", exchange, "", "", nil)
	check(w, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if !validMobileSecret(tokens.Access) || !validMobileSecret(tokens.Refresh) || tokens.Issuer != a.mobileIssuer() || len(w.Result().Cookies()) != 0 || !tokens.Deadline.After(tokens.AccessDeadline) {
		t.Fatal("invalid tokens", w.Code)
	}
	check(call("POST", "/at/auth/mobile/token", exchange, "", "", nil), 400)
	check(call("GET", "/at/auth/me", "", tokens.Access, "", nil), 200)
	check(call("GET", "/at/api/v1/probe", "", tokens.Access, "", nil), 403)
	check(call("GET", "/at/auth/me", "", web.Value, "", nil), 401)
	check(call("GET", "/at/auth/me", "", "", "", &http.Cookie{Name: web.Name, Value: tokens.Access}), 401)
	check(call("GET", "/at/auth/me", "", tokens.Access, "", web), 401)
	check(call("GET", "/at/auth/me", "", "not-a-native-key", "", nil), 401)
	check(call("GET", "/at/auth/mobile/requests/"+id, "", tokens.Access, "", nil), 401)
	check(call("POST", "/at/auth/mobile/refresh", `{"refresh_token":"`+webRefresh.Value+`"}`, "", "", nil), 401)
	check(call("POST", "/at/auth/refresh", `{}`, "", a.cfg.Origin, &http.Cookie{Name: webRefresh.Name, Value: tokens.Refresh}), 401)
	check(call("POST", "/at/auth/mobile/token", exchange, "", "", web), 400)
	oldRefresh, oldAccess, oldSession, oldDeadline := tokens.Refresh, tokens.Access, tokens.Session, tokens.Deadline
	w = call("POST", "/at/auth/mobile/refresh", `{"refresh_token":"`+tokens.Refresh+`"}`, "", "", nil)
	check(w, 200)
	json.Unmarshal(w.Body.Bytes(), &tokens)
	if tokens.Session != oldSession || !tokens.Deadline.Equal(oldDeadline) || tokens.Refresh == oldRefresh || len(w.Result().Cookies()) != 0 {
		t.Fatal("rotation mutated family")
	}
	check(call("GET", "/at/auth/me", "", oldAccess, "", nil), 401)
	check(call("POST", "/at/auth/mobile/refresh", `{"refresh_token":"`+tokens.Refresh+`"}`, "", "", nil), 429)
	check(call("POST", "/at/auth/mobile/refresh", `{"refresh_token":"`+oldRefresh+`"}`, "", "", nil), 401)
	check(call("GET", "/at/auth/me", "", tokens.Access, "", nil), 401)
	check(call("GET", "/at/auth/me", "", "", "", web), 200)
	a.mobileBeginSources["192.0.2.1"] = nativeMobileSource{limit: rate.NewLimiter(rate.Inf, 1000), lastSeen: time.Now()}
	for _, mode := range []string{"deny", "wrong-verifier", "logout-refresh", "logout-bearer", "web-logout"} {
		t.Run(mode, func(t *testing.T) {
			id, v, state := begin()
			if mode == "deny" {
				decide(id, state, false)
				check(call("GET", "/at/auth/mobile/requests/"+id, "", "", "", web), 404)
				return
			}
			c := decide(id, state, true)
			if mode == "wrong-verifier" {
				check(call("POST", "/at/auth/mobile/token", `{"code":"`+c+`","code_verifier":"bad"}`, "", "", nil), 400)
				check(call("POST", "/at/auth/mobile/token", `{"code":"`+c+`","code_verifier":"`+v+`"}`, "", "", nil), 400)
				return
			}
			if mode == "web-logout" {
				check(call("POST", "/at/auth/logout", `{}`, "", a.cfg.Origin, web), 204)
			}
			w := call("POST", "/at/auth/mobile/token", `{"code":"`+c+`","code_verifier":"`+v+`"}`, "", "", nil)
			if mode == "web-logout" {
				check(w, 400)
				return
			}
			check(w, 200)
			json.Unmarshal(w.Body.Bytes(), &tokens)
			if mode == "logout-refresh" {
				check(call("POST", "/at/auth/mobile/logout", `{"refresh_token":"`+tokens.Refresh+`"}`, "", "", nil), 204)
			} else {
				check(call("POST", "/at/auth/mobile/logout", `{}`, tokens.Access, "", nil), 204)
			}
			check(call("GET", "/at/auth/me", "", tokens.Access, "", nil), 401)
		})
	}
	if u.ID == "" {
		t.Fatal("missing user")
	}
	a.mobileBeginSources["192.0.2.1"] = nativeMobileSource{limit: rate.NewLimiter(0, 1), lastSeen: time.Now()}
	check(call("POST", "/at/auth/mobile/begin", validBegin, "", "", nil), 200)
	check(call("POST", "/at/auth/mobile/begin", validBegin, "", "", nil), 429)
}
