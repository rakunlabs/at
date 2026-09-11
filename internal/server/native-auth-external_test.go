package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/session"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type externalTestStore struct {
	service.AuthExternalStorer
	mu          sync.Mutex
	provider    service.AuthIdentityProvider
	flows       map[string]service.AuthExternalFlow
	completions int
	last        service.AuthIdentityLink
}

func (s *externalTestStore) GetAuthIdentityProvider(context.Context, string) (*service.AuthIdentityProvider, error) {
	p := s.provider
	return &p, nil
}
func (s *externalTestStore) ListAuthIdentityProviders(context.Context) ([]service.AuthIdentityProvider, error) {
	return []service.AuthIdentityProvider{s.provider}, nil
}
func (s *externalTestStore) SaveAuthExternalFlow(_ context.Context, f service.AuthExternalFlow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[f.Hash] = f
	return nil
}
func (s *externalTestStore) ConsumeAuthExternalFlow(_ context.Context, key, p string, v int64) (*service.AuthExternalFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[key]
	if !ok || f.ProviderID != p || f.ProviderVersion != v {
		return nil, service.ErrAuthConflict
	}
	delete(s.flows, key)
	return &f, nil
}
func (s *externalTestStore) CompleteAuthExternalIdentity(_ context.Context, l service.AuthIdentityLink, _ int64, _ *service.AuthExternalAccount, _ time.Time) (*service.AuthUser, *service.AuthIdentityLink, error) {
	s.completions++
	s.last = l
	l.ID = "link"
	return &service.AuthUser{ID: "local-user", SessionVersion: 1}, &l, nil
}

func externalJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test","typ":"JWT"}`))
	body := h + "." + base64.RawURLEncoding.EncodeToString(b)
	digest := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestExternalStrictOIDCAndReplicaFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "oauth2", "missing_id_token", "issuer", "audience", "nonce", "signature", "expiry", "userinfo_subject", "discovery_issuer", "cross_browser", "state", "provider_version", "callback_path", "account_switch"} {
		t.Run(name, func(t *testing.T) {
			var issuer, nonce, challenge string
			idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					discovered := issuer
					if name == "discovery_issuer" {
						discovered += "/other"
					}
					json.NewEncoder(w).Encode(map[string]any{"issuer": discovered, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo", "jwks_uri": issuer + "/jwks"})
				case "/jwks":
					json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
				case "/token":
					r.ParseForm()
					verifier := r.Form.Get("code_verifier")
					sum := sha256.Sum256([]byte(verifier))
					if verifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
						t.Error("PKCE verifier mismatch")
					}
					if r.Form.Get("redirect_uri") != "http://localhost/auth/external/provider/callback" {
						t.Errorf("unexpected redirect %s", r.Form.Get("redirect_uri"))
					}
					claims := map[string]any{"iss": issuer, "aud": "client", "sub": "stable-subject", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "roles": []string{"platform_admin"}}
					signer := key
					switch name {
					case "issuer":
						claims["iss"] = issuer + "/wrong"
					case "audience":
						claims["aud"] = "other"
					case "nonce":
						claims["nonce"] = "wrong"
					case "expiry":
						claims["exp"] = time.Now().Add(-5 * time.Minute).Unix()
					case "signature":
						signer = wrongKey
					}
					response := map[string]any{"access_token": "upstream-token", "token_type": "Bearer", "id_token": externalJWT(t, signer, claims)}
					if name == "missing_id_token" || name == "oauth2" {
						delete(response, "id_token")
					}
					json.NewEncoder(w).Encode(response)
				case "/userinfo":
					sub := "stable-subject"
					if name == "userinfo_subject" {
						sub = "other"
					}
					json.NewEncoder(w).Encode(map[string]any{"sub": sub, "email": "same@example.test", "email_verified": true, "roles": []string{"platform_admin"}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer idp.Close()
			issuer = idp.URL
			s := &externalTestStore{provider: service.AuthIdentityProvider{ID: "provider", Label: "Test", Mode: "oidc", Enabled: true, Version: 1, Issuer: issuer, ClientID: "client", Scopes: []string{"openid"}}, flows: map[string]service.AuthExternalFlow{}}
			if name == "oauth2" {
				s.provider.Mode = "oauth2"
				s.provider.Issuer = ""
				s.provider.AuthURL = issuer + "/authorize"
				s.provider.TokenURL = issuer + "/token"
				s.provider.UserInfoURL = issuer + "/userinfo"
				s.provider.SubjectClaim = "sub"
			}
			a := &nativeAuth{cfg: config.NativeAuth{Origin: "http://localhost", InsecureHTTP: true}, session: session.Session{Cookie: session.CookieOptions{Path: "/"}}}
			completed := 0
			hooks := nativeExternalHooks{
				Account: func(*http.Request) (service.AuthExternalAccount, error) {
					return service.AuthExternalAccount{}, service.ErrAuthConflict
				},
				Recent: func(http.ResponseWriter, *http.Request, string, string) (service.AuthExternalAccount, error) {
					if name == "account_switch" {
						return service.AuthExternalAccount{UserID: "link-user", SessionID: "link-session", Version: 1}, nil
					}
					return service.AuthExternalAccount{}, service.ErrAuthConflict
				},
				Complete: func(w http.ResponseWriter, r *http.Request, u *service.AuthUser, c service.AuthExternalCompletion) {
					completed++
					if u.Admin || u.ID != "local-user" || c.ProviderVersion != 1 || c.LinkID != "link" {
						t.Error("lost local identity/provenance")
					}
					w.WriteHeader(204)
				},
				Reauthenticate: func(http.ResponseWriter, *http.Request, *service.AuthUser, service.AuthExternalCompletion) {
					t.Error("unexpected reauth")
				},
			}
			e, _ := newNativeExternalAuth(a, s, hooks)
			requestBody := `{"remember_me":true}`
			if name == "account_switch" {
				requestBody = `{"purpose":"link","recent_proof":"proof"}`
			}
			begin := httptest.NewRequest("POST", "http://localhost/auth/external/provider/begin", strings.NewReader(requestBody))
			begin.Header.Set("Origin", "http://localhost")
			begin.Header.Set("Content-Type", "application/json")
			begin.SetPathValue("provider", "provider")
			w := httptest.NewRecorder()
			e.begin(w, begin)
			if name == "discovery_issuer" {
				if w.Code == 200 {
					t.Fatal("accepted wrong discovery issuer")
				}
				return
			}
			if w.Code != 200 {
				t.Fatalf("begin %d: %s", w.Code, w.Body.String())
			}
			var response map[string]string
			json.Unmarshal(w.Body.Bytes(), &response)
			authorize, _ := url.Parse(response["authorization_url"])
			nonce = authorize.Query().Get("nonce")
			challenge = authorize.Query().Get("code_challenge")
			if nonce == "" || challenge == "" || authorize.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("missing nonce/PKCE")
			}
			state := authorize.Query().Get("state")
			if name == "state" {
				state = "wrong"
			}
			if name == "provider_version" {
				s.provider.Version++
			}
			path := "/auth/external/provider/callback"
			if name == "callback_path" {
				path = "/auth/external/wrong/callback"
			}
			callback := httptest.NewRequest("GET", "http://localhost"+path+"?code=code&state="+url.QueryEscape(state), nil)
			callback.SetPathValue("provider", "provider")
			if name != "cross_browser" {
				for _, c := range w.Result().Cookies() {
					callback.AddCookie(c)
				}
			}
			// A newly constructed adapter represents another replica with no local flow state.
			other, _ := newNativeExternalAuth(a, s, hooks)
			out := httptest.NewRecorder()
			other.callback(out, callback)
			if name == "valid" || name == "oauth2" {
				if out.Code != 204 || completed != 1 || s.completions != 1 {
					t.Fatalf("callback %d: %s (complete=%d)", out.Code, out.Body.String(), completed)
				}
				replay := httptest.NewRecorder()
				e.callback(replay, callback)
				if completed != 1 || s.completions != 1 {
					t.Fatal("callback replay completed")
				}
				if !strings.Contains(string(s.last.AssertedPermissions), "provider") {
					t.Fatal("missing qualified assertions")
				}
				if name == "oauth2" && s.last.Issuer != "oauth2:provider" {
					t.Fatal("generic subject lost provider namespace")
				}
			} else if completed != 0 || s.completions != 0 {
				t.Fatalf("invalid %s authenticated", name)
			}
		})
	}
}

func TestExternalProviderValidation(t *testing.T) {
	p := service.AuthIdentityProvider{Label: "OAuth", Mode: "oauth2", ClientID: "client", AuthURL: "https://idp.test/auth", TokenURL: "https://idp.test/token", UserInfoURL: "https://idp.test/me", SubjectClaim: "id"}
	if err := validateExternalProvider(p, false); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://public.test/auth", "https://user:pass@idp.test/auth", "https://idp.test/auth#fragment", "javascript:alert(1)"} {
		q := p
		q.AuthURL = raw
		if validateExternalProvider(q, false) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	p.Mode = "oidc"
	if validateExternalProvider(p, false) == nil {
		t.Fatal("accepted OIDC without issuer/openid")
	}
}

func TestExternalLoginProviderRedaction(t *testing.T) {
	s := &externalTestStore{provider: service.AuthIdentityProvider{ID: "id", Label: "Visible", Mode: "oidc", Enabled: true, ClientID: "private-client", ClientSecret: "secret", Issuer: "https://private.test"}}
	e := &nativeExternalAuth{store: s}
	w := httptest.NewRecorder()
	e.loginProviders(w, httptest.NewRequest("GET", "/auth/login-providers", nil))
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("provider details leaked: %s", w.Body.String())
	}
}

func TestExternalCommonMFACoordinatorPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.SaveAuthIdentityProvider(t.Context(), service.AuthIdentityProvider{Label: "IdP", Mode: "oidc", Issuer: "https://idp.test", ClientID: "client", Enabled: true, Scopes: []string{"openid"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, l, err := p.CompleteAuthExternalIdentity(t.Context(), service.AuthIdentityLink{ProviderID: v.ID, Issuer: v.Issuer, Subject: "subject", AssertedPermissions: json.RawMessage(`{}`)}, v.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	backup := "0123456789ABCDEF0123456789ABCDEF"
	if err = p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		update.State.Secret = "JBSWY3DPEHPK3PXP"
		update.State.Generation = 1
		update.State.BackupHashes = []string{nativeSessionHash(backup)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hooks := nativeExternalCoordinatorHooks(a)
	c := service.AuthExternalCompletion{ProviderID: v.ID, ProviderVersion: v.Version, LinkID: l.ID, Remember: true, Deadline: time.Now().Add(time.Minute)}
	r := httptest.NewRequest("GET", "/at/auth/external/"+v.ID+"/callback", nil)
	w := httptest.NewRecorder()
	hooks.Complete(w, r, u, c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mfa_required":true`) {
		t.Fatalf("external MFA not pending: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == a.session.CookieName && cookie.MaxAge >= 0 {
			t.Fatal("normal external session before MFA")
		}
	}
	approve := httptest.NewRequest("POST", "/at/auth/mobile/approve", strings.NewReader(`{"request_id":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
	approve.Header.Set("Content-Type", "application/json")
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			approve.AddCookie(cookie)
		}
	}
	approval := httptest.NewRecorder()
	a.mobileDecide(true)(approval, approve)
	if approval.Code != 401 {
		t.Fatalf("pending external primary approved mobile: %d", approval.Code)
	}
	var pending struct {
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(w.Body.Bytes(), &pending)
	// Provider disable between primary and second-factor completion invalidates
	// the original user version, so even a valid backup code cannot finish it.
	v.Enabled = false
	if _, err = p.SaveAuthIdentityProvider(t.Context(), *v, nil); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"challenge": pending.Challenge, "code": backup})
	verify := httptest.NewRequest("POST", "/at/auth/mfa/verify", strings.NewReader(string(body)))
	verify.Header.Set("Origin", a.cfg.Origin)
	verify.Header.Set("Content-Type", "application/json")
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			verify.AddCookie(cookie)
		}
	}
	out := httptest.NewRecorder()
	a.mfaVerify(out, verify)
	if out.Code == 200 {
		t.Fatalf("disabled provider MFA completed: %s", out.Body.String())
	}
	parsed, err := nativeExternalCompletionFromMethod(nativeExternalMethod(c))
	if err != nil || parsed.ProviderID != c.ProviderID || parsed.LinkID != c.LinkID || !parsed.Deadline.Equal(c.Deadline) {
		t.Fatal("MFA provenance round trip failed", err)
	}
}

func TestExternalProviderAdminRESTPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newNativeExternalAuth(a, p, nativeExternalCoordinatorHooks(a))
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	e.register(mux, "/at")
	call := func(admin bool) *httptest.ResponseRecorder {
		t.Helper()
		u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: fmt.Sprintf("rest-%v", admin), PasswordHash: "hash", Admin: admin}, false)
		if err != nil {
			t.Fatal(err)
		}
		login := httptest.NewRecorder()
		a.finishCompletedLogin(login, httptest.NewRequest("POST", "/at/auth/login", nil), u, false)
		if login.Code != 200 {
			t.Fatalf("login: %d %s", login.Code, login.Body.String())
		}
		r := httptest.NewRequest("POST", "/at/auth/identity-providers", strings.NewReader(`{"label":"Login provider","mode":"oidc","issuer":"https://idp.test","client_id":"client","client_secret":"top-secret","enabled":true,"scopes":["openid"]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", a.cfg.Origin)
		for _, c := range login.Result().Cookies() {
			if c.MaxAge >= 0 {
				r.AddCookie(c)
			}
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call(false); w.Code != 403 {
		t.Fatalf("non-platform user configured IdP: %d %s", w.Code, w.Body.String())
	}
	w := call(true)
	if w.Code != 201 || strings.Contains(w.Body.String(), "top-secret") || !strings.Contains(w.Body.String(), `"has_client_secret":true`) {
		t.Fatalf("admin config/redaction: %d %s", w.Code, w.Body.String())
	}
}

func TestExternalRecentAuthLocalMFAPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newNativeExternalAuth(a, p, nativeExternalCoordinatorHooks(a))
	if err != nil {
		t.Fatal(err)
	}
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "external-recent", PasswordHash: ""}, false)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	a.finishCompletedLogin(login, httptest.NewRequest("POST", "/at/auth/login", nil), u, false)
	if login.Code != 200 {
		t.Fatalf("fixture login: %d %s", login.Code, login.Body.String())
	}
	cookies := map[string]*http.Cookie{}
	for _, c := range login.Result().Cookies() {
		if c.MaxAge >= 0 {
			cookies[c.Name] = c
		}
	}
	request := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/at/auth/external/reauth/finish", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", a.cfg.Origin)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	account, err := e.hooks.Account(request(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	backup := "ABCDEF0123456789ABCDEF0123456789"
	if err = p.UpdateAuthSecurity(t.Context(), u.ID, account.SessionID, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		update.State.Secret = "JBSWY3DPEHPK3PXP"
		update.State.Generation = 1
		update.State.BackupHashes = []string{nativeSessionHash(backup)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := service.AuthExternalCompletion{ProviderID: "provider", ProviderVersion: 1, LinkID: "link", Account: account, ReauthPurpose: "identity.link", Deadline: time.Now().Add(time.Minute)}
	w := httptest.NewRecorder()
	e.hooks.Reauthenticate(w, request(`{}`), u, c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mfa_required":true`) || strings.Contains(w.Body.String(), `"proof":`) {
		t.Fatalf("external reauth skipped local MFA: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 {
			cookies[c.Name] = c
		}
	}
	var pending struct {
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(w.Body.Bytes(), &pending)
	body, _ := json.Marshal(map[string]string{"challenge": pending.Challenge, "code": backup})
	finish := httptest.NewRecorder()
	e.finishReauth(finish, request(string(body)))
	if finish.Code != 200 {
		t.Fatalf("external reauth finish: %d %s", finish.Code, finish.Body.String())
	}
	var proof struct {
		Proof string `json:"proof"`
	}
	json.Unmarshal(finish.Body.Bytes(), &proof)
	if proof.Proof == "" {
		t.Fatal("missing recent proof")
	}
	if _, err = e.hooks.Recent(httptest.NewRecorder(), request(`{}`), "identity.unlink", proof.Proof); err == nil {
		t.Fatal("wrong-purpose proof accepted")
	}
	current, err := e.hooks.Recent(httptest.NewRecorder(), request(`{}`), "identity.link", proof.Proof)
	if err != nil || current != account {
		t.Fatalf("recent link proof: %v", err)
	}
	if _, err = e.hooks.Recent(httptest.NewRecorder(), request(`{}`), "identity.link", proof.Proof); err == nil {
		t.Fatal("recent proof replay accepted")
	}
	replay := httptest.NewRecorder()
	e.finishReauth(replay, request(string(body)))
	if replay.Code == 200 {
		t.Fatal("external reauth challenge replay accepted")
	}
}
