package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/oauth2"

	"github.com/rakunlabs/at/internal/service"
)

// All hooks are server-owned. The external adapter cannot mint opaque sessions.
type nativeExternalHooks struct {
	Recent         func(http.ResponseWriter, *http.Request, string, string) (service.AuthExternalAccount, error)
	Account        func(*http.Request) (service.AuthExternalAccount, error)
	Complete       func(http.ResponseWriter, *http.Request, *service.AuthUser, service.AuthExternalCompletion)
	Reauthenticate func(http.ResponseWriter, *http.Request, *service.AuthUser, service.AuthExternalCompletion)
}

type nativeExternalAuth struct {
	a     *nativeAuth
	store service.AuthExternalStorer
	hooks nativeExternalHooks
}

func newNativeExternalAuth(a *nativeAuth, store any, hooks nativeExternalHooks) (*nativeExternalAuth, error) {
	s, ok := store.(service.AuthExternalStorer)
	if a == nil || !ok || hooks.Recent == nil || hooks.Account == nil || hooks.Complete == nil || hooks.Reauthenticate == nil {
		return nil, fmt.Errorf("external authentication requires persistent storage and common security coordinator")
	}
	return &nativeExternalAuth{a: a, store: s, hooks: hooks}, nil
}

func (e *nativeExternalAuth) register(mux *ada.Server, base string) {
	public := mux.Group(base + "/auth")
	public.GET("/login-providers", e.loginProviders)
	public.POST("/external/{provider}/begin", e.begin)
	// Intentionally outside require/sameOrigin: Ada validates the exact callback,
	// single-use state and browser binding before any user or session mutation.
	public.GET("/external/{provider}/callback", e.callback)
	self := mux.Group(base + "/auth")
	self.Use(e.a.require(false))
	self.GET("/identities", e.identities)
	self.DELETE("/identities/{link}", e.unlink)
	self.POST("/external/reauth/finish", e.finishReauth)
	admin := mux.Group(base + "/auth/identity-providers")
	admin.Use(e.a.require(true))
	admin.GET("", e.providers)
	admin.POST("", e.saveProvider)
	admin.PUT("/{provider}", e.saveProvider)
	admin.DELETE("/{provider}", e.disableProvider)
}

func externalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAuthConflict):
		nativeError(w, 409, "external authentication expired or conflicted; restart")
	case errors.Is(err, service.ErrAuthSessionLimit):
		nativeError(w, 429, "external authentication capacity reached; retry later")
	default:
		nativeError(w, 503, "external authentication unavailable")
	}
}

func (e *nativeExternalAuth) loginProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := e.store.ListAuthIdentityProviders(r.Context())
	if err != nil {
		externalError(w, err)
		return
	}
	type publicProvider struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	result := make([]publicProvider, 0)
	for _, v := range rows {
		if v.Enabled {
			result = append(result, publicProvider{v.ID, v.Label})
		}
	}
	httpResponseJSON(w, result, 200)
}

func (e *nativeExternalAuth) providers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := e.store.ListAuthIdentityProviders(r.Context())
	if err != nil {
		externalError(w, err)
		return
	}
	httpResponseJSON(w, rows, 200)
}

func (e *nativeExternalAuth) saveProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		service.AuthIdentityProvider
		ClientSecret      *string `json:"client_secret"`
		ClearClientSecret bool    `json:"clear_client_secret"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if req.ClearClientSecret && req.ClientSecret != nil {
		nativeError(w, 400, "secret and clear_client_secret are mutually exclusive")
		return
	}
	if req.ClientSecret != nil && len(*req.ClientSecret) > 8192 {
		nativeError(w, 400, "client secret too long")
		return
	}
	if req.ClearClientSecret {
		empty := ""
		req.ClientSecret = &empty
	}
	req.ID = r.PathValue("provider")
	if req.ID != "" && req.Version < 1 {
		nativeError(w, 400, "expected provider version required")
		return
	}
	// Store the list canonicalized, so what the administrator reads back is
	// what is enforced and a difference in case or spacing is never the reason
	// a sign-in is refused.
	req.AllowedEmails = service.NormalizeAuthEmailAllowlist(req.AllowedEmails)
	if err := validateExternalProvider(req.AuthIdentityProvider, e.a.cfg.InsecureHTTP); err != nil {
		nativeError(w, 400, err.Error())
		return
	}
	if err := e.refuseSelfLockout(r, req.AuthIdentityProvider); err != nil {
		nativeError(w, 409, err.Error())
		return
	}
	v, err := e.store.SaveAuthIdentityProvider(r.Context(), req.AuthIdentityProvider, req.ClientSecret)
	if err != nil {
		externalError(w, err)
		return
	}
	code := 200
	if req.ID == "" {
		code = 201
	}
	httpResponseJSON(w, v, code)
}

// refuseSelfLockout stops the one allowed-email mistake that is not reversible
// from the UI: an administrator who signs in through this provider writing a
// list that excludes their own linked address. The list is enforced on every
// sign-in, so the next one would be theirs, and with local sign-in disabled the
// installation has no other way back in.
//
// It is a convenience guard, not a boundary, and therefore fails open: an
// administrator reaching this endpoint without a resolvable browser account, or
// a store that cannot answer, leaves the save alone rather than refusing it for
// a reason that has nothing to do with the configuration. It also cannot cover
// an administrator other than the one saving, whose links this endpoint has no
// business enumerating.
func (e *nativeExternalAuth) refuseSelfLockout(r *http.Request, p service.AuthIdentityProvider) error {
	if len(p.AllowedEmails) == 0 || p.ID == "" {
		return nil
	}
	account, err := e.hooks.Account(r)
	if err != nil || account.UserID == "" {
		return nil
	}
	links, err := e.store.ListAuthIdentityLinks(r.Context(), account.UserID)
	if err != nil {
		return nil
	}
	linked := false
	for _, l := range links {
		if l.ProviderID != p.ID {
			continue
		}
		if service.AuthEmailAdmits(p.AllowedEmails, l.Email, l.EmailVerified) == nil {
			return nil
		}
		linked = true
	}
	if !linked {
		return nil
	}
	return fmt.Errorf("this list excludes your own account on this provider and would lock you out; add your address, or unlink this provider from your account first")
}

func (e *nativeExternalAuth) disableProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version int64 `json:"version"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	v, err := e.store.GetAuthIdentityProvider(r.Context(), r.PathValue("provider"))
	if err != nil {
		externalError(w, err)
		return
	}
	if v == nil || v.Version != req.Version {
		externalError(w, service.ErrAuthConflict)
		return
	}
	v.Enabled = false
	if _, err = e.store.SaveAuthIdentityProvider(r.Context(), *v, nil); err != nil {
		externalError(w, err)
		return
	}
	w.WriteHeader(204)
}

func validateExternalURL(raw string, loopback bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\r\n\\") {
		return fmt.Errorf("invalid identity provider endpoint")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if loopback && u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("identity provider endpoints require HTTPS")
}

func validateExternalProvider(p service.AuthIdentityProvider, loopback bool) error {
	if p.AuthHeaderStyle != "" && p.AuthHeaderStyle != "client_secret_basic" && p.AuthHeaderStyle != "client_secret_post" {
		return fmt.Errorf("invalid auth_header_style")
	}
	if strings.TrimSpace(p.Label) == "" || len(p.Label) > 128 || p.ClientID == "" || len(p.ClientID) > 1024 || len(p.Scopes) > 32 {
		return fmt.Errorf("label, client_id and bounded scopes required")
	}
	for _, scope := range p.Scopes {
		if scope == "" || len(scope) > 256 || strings.ContainsAny(scope, " \r\n\t") {
			return fmt.Errorf("invalid scope")
		}
	}
	if err := service.ValidateClaimPaths(p.RolesClaims); err != nil {
		return err
	}
	if err := service.ValidateAuthEmailAllowlist(p.AllowedEmails); err != nil {
		return err
	}
	if p.AuthURL == "" || p.TokenURL == "" {
		return fmt.Errorf("authorization and token endpoints are required")
	}
	// A login needs an authenticated claim source. Userinfo is read with the
	// access token; JWKS verifies the id_token instead. With neither there is
	// nothing to derive an identity from that the browser could not have
	// forged, and the strategy refuses the login at callback time — so refuse
	// the configuration here, where the administrator can see why.
	if p.UserInfoURL == "" && p.JWKSURL == "" {
		return fmt.Errorf("configure a userinfo endpoint, a JWKS endpoint, or both")
	}
	// The subject is the account's permanent name in this provider's identity
	// namespace: a link is keyed on it, so a claim that can be reassigned
	// (email, username) eventually hands one person another's account.
	if p.SubjectClaim == "" || len(p.SubjectClaim) > 128 {
		return fmt.Errorf("a stable subject claim is required")
	}
	for _, raw := range []string{p.AuthURL, p.TokenURL, p.UserInfoURL, p.JWKSURL} {
		if raw != "" {
			if err := validateExternalURL(raw, loopback); err != nil {
				return err
			}
		}
	}
	return nil
}

type externalTransport struct{ loopback bool }

func (t externalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := validateExternalURL(r.URL.String(), t.loopback); err != nil {
		return nil, err
	}
	return http.DefaultTransport.RoundTrip(r)
}

type nativeExternalIntent struct {
	Flow            oauth2.FlowData
	Purpose         string
	Remember        bool
	Account         service.AuthExternalAccount
	ReauthPurpose   string
	MobileRequestID string
	MobileExpiresAt time.Time
}

// A request-local adapter adds AT intent to Ada's persisted encrypted FlowData.
// Ada stores only the random cookie handle in the browser; SQL stores its hash.
type nativeExternalFlowStore struct {
	store    service.AuthExternalStorer
	provider service.AuthIdentityProvider
	intent   nativeExternalIntent
	consumed bool
}

func (s *nativeExternalFlowStore) Save(ctx context.Context, key string, f oauth2.FlowData) error {
	if !s.intent.MobileExpiresAt.IsZero() && s.intent.MobileExpiresAt.Before(f.ExpiresAt) {
		f.ExpiresAt = s.intent.MobileExpiresAt
	}
	s.intent.Flow = f
	data, err := json.Marshal(s.intent)
	if err != nil {
		return fmt.Errorf("encode external intent: %w", err)
	}
	err = s.store.SaveAuthExternalFlow(ctx, service.AuthExternalFlow{Hash: nativeSessionHash(key), UserID: s.intent.Account.UserID, ProviderID: s.provider.ID, ProviderVersion: s.provider.Version, Source: f.Source, ExpiresAt: f.ExpiresAt, Payload: data})
	if errors.Is(err, service.ErrAuthSessionLimit) {
		return oauth2.ErrFlowSourceFull
	}
	return err
}
func (s *nativeExternalFlowStore) Consume(ctx context.Context, key string) (oauth2.FlowData, error) {
	f, err := s.store.ConsumeAuthExternalFlow(ctx, nativeSessionHash(key), s.provider.ID, s.provider.Version)
	if errors.Is(err, service.ErrAuthConflict) {
		return oauth2.FlowData{}, oauth2.ErrFlowInvalid
	}
	if err != nil {
		return oauth2.FlowData{}, err
	}
	if f == nil {
		return oauth2.FlowData{}, oauth2.ErrFlowInvalid
	}
	if err = json.Unmarshal(f.Payload, &s.intent); err != nil {
		return oauth2.FlowData{}, fmt.Errorf("decode external intent: %w", err)
	}
	if !s.intent.Flow.ExpiresAt.Equal(f.ExpiresAt) && s.intent.Flow.ExpiresAt.Sub(f.ExpiresAt).Abs() > time.Microsecond {
		return oauth2.FlowData{}, oauth2.ErrFlowInvalid
	}
	s.consumed = true
	return s.intent.Flow, nil
}

func (e *nativeExternalAuth) adapter(ctx context.Context, p service.AuthIdentityProvider, f *nativeExternalFlowStore) (*oauth2.Strategy, error) {
	if err := validateExternalProvider(p, e.a.cfg.InsecureHTTP); err != nil {
		return nil, err
	}
	client := &http.Client{Transport: externalTransport{e.a.cfg.InsecureHTTP}, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// No IssuerURL: in ada, a non-empty issuer *is* the instruction to fetch
	// the discovery document, and there is no way to supply one for claim
	// checking alone. Leaving it empty keeps every endpoint exactly what the
	// administrator entered and makes strategy construction a local operation.
	//
	// RequireIDToken therefore stays off — it demands a discovered issuer — so
	// ada resolves the identity through its ordinary claim path: an id_token is
	// still signature-verified against JWKSURL when one is configured, with
	// audience (defaulted to ClientID), expiry and nonce enforced, and userinfo
	// is used when it is not. Only the `iss` claim goes unchecked, since
	// nothing here declares what it should be.
	cfg := oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, Scopes: p.Scopes, AuthURL: p.AuthURL, TokenURL: p.TokenURL, UserInfoURL: p.UserInfoURL, JWKSURL: p.JWKSURL}
	if err := cfg.AuthHeaderStyle.UnmarshalText([]byte(p.AuthHeaderStyle)); err != nil {
		return nil, fmt.Errorf("configure token authentication: %w", err)
	}
	secure := cookie.SecureAlways
	if strings.HasPrefix(e.a.cfg.Origin, "http://") {
		secure = cookie.SecureNever
	}
	base := strings.TrimSuffix(e.a.session.Cookie.Path, "/") + "/auth/external/" + p.ID
	// Keep reported email as metadata even without verification. Admission
	// checks EmailVerified separately before trusting an allowlisted address.
	opts := oauth2.Options{HTTPClient: client, CallbackBaseURL: e.a.cfg.Origin, CallbackBasePath: base, FlowStore: f, FlowTTL: 5 * time.Minute, FlowCookie: cookie.Options{Path: base + "/", Secure: secure, SameSite: http.SameSiteLaxMode}, EmailVerifyCheck: false}
	opts.XUserClaims.Subject = []string{p.SubjectClaim}
	return oauth2.NewWithContext(ctx, "callback", cfg, opts)
}

func (e *nativeExternalAuth) loadProvider(w http.ResponseWriter, r *http.Request) *service.AuthIdentityProvider {
	p, err := e.store.GetAuthIdentityProvider(r.Context(), r.PathValue("provider"))
	if err != nil {
		externalError(w, err)
		return nil
	}
	if p == nil || !p.Enabled {
		nativeError(w, 404, "login provider unavailable")
		return nil
	}
	return p
}

func (e *nativeExternalAuth) begin(w http.ResponseWriter, r *http.Request) {
	if !e.a.sameOrigin(w, r) {
		return
	}
	var req struct {
		Purpose         string `json:"purpose"`
		Remember        bool   `json:"remember_me"`
		RecentProof     string `json:"recent_proof"`
		ReauthPurpose   string `json:"reauth_purpose"`
		MobileRequestID string `json:"mobile_request_id"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if req.Purpose == "" {
		req.Purpose = "login"
	}
	if req.Purpose != "login" && req.Purpose != "link" && req.Purpose != "reauth" {
		nativeError(w, 400, "invalid external authentication purpose")
		return
	}
	intent := nativeExternalIntent{Purpose: req.Purpose, Remember: req.Remember, ReauthPurpose: req.ReauthPurpose}
	if req.MobileRequestID != "" {
		if req.Purpose != "login" || !validMobileSecret(req.MobileRequestID) || e.a.mobileStore == nil {
			nativeError(w, 400, "invalid mobile continuation")
			return
		}
		mobile, err := e.a.mobileStore.GetAuthMobileRequest(r.Context(), req.MobileRequestID)
		if err != nil {
			externalError(w, err)
			return
		}
		if mobile == nil || !mobile.ExpiresAt.After(time.Now()) || mobile.UserID != "" {
			nativeError(w, 400, "mobile authorization unavailable")
			return
		}
		intent.MobileRequestID, intent.MobileExpiresAt = mobile.ID, mobile.ExpiresAt
	}
	if req.Purpose != "login" {
		var err error
		if req.Purpose == "link" {
			intent.Account, err = e.hooks.Recent(w, r, "identity_link", req.RecentProof)
		} else {
			// Reauth proves the primary method, so requiring a recent proof here
			// would make external-only accounts unable to obtain their first proof.
			intent.Account, err = e.hooks.Account(r)
			if req.ReauthPurpose == "" || len(req.ReauthPurpose) > 64 {
				nativeError(w, 400, "recent authentication purpose required")
				return
			}
		}
		if err != nil || intent.Account.UserID == "" || intent.Account.SessionID == "" {
			nativeError(w, 401, "recent browser authentication required")
			return
		}
	}
	p := e.loadProvider(w, r)
	if p == nil {
		return
	}
	f := &nativeExternalFlowStore{store: e.store, provider: *p, intent: intent}
	a, err := e.adapter(r.Context(), *p, f)
	if err != nil {
		externalError(w, err)
		return
	}
	// Ada initiates on GET. Capture its redirect and return a JSON URL for the
	// same-origin POST UI; no user-controlled query or callback path is retained.
	rr := r.Clone(r.Context())
	rr.Method = "GET"
	u := *r.URL
	u.RawQuery = ""
	rr.URL = &u
	rec := httptest.NewRecorder()
	_, outcome, err := a.Login(rec, rr)
	if err != nil || outcome != strategy.OutcomePending || rec.Header().Get("Location") == "" {
		status := http.StatusServiceUnavailable
		if rec.Code >= 400 && rec.Code <= 599 {
			status = rec.Code
		}
		nativeError(w, status, "could not begin external authentication")
		return
	}
	for _, c := range rec.Header().Values("Set-Cookie") {
		w.Header().Add("Set-Cookie", c)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpResponseJSON(w, map[string]string{"authorization_url": rec.Header().Get("Location")}, 200)
}

// The callback is a top-level navigation in the popup the UI opened, not an
// XHR. Writing the result as JSON left the browser parked on a page of JSON
// while the opener waited for a message that never came, so the whole external
// sign-in hung at the last step. Capture the response and hand it to the opener
// as the postMessage `_ui/src/lib/helper/auth-popup.ts` is waiting on.
//
// A recorder is used rather than a streaming wrapper because the decision needs
// the finished status, headers and body: only a 2xx JSON body is a result, a
// redirect the strategy staged must be replayed untouched, and anything else is
// reported as an error the opener can show instead of hanging until its timeout.
func (e *nativeExternalAuth) callback(w http.ResponseWriter, r *http.Request) {
	rec := httptest.NewRecorder()
	e.callbackResult(rec, r)
	e.deliverCallback(w, rec)
}

func (e *nativeExternalAuth) deliverCallback(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	header := w.Header()
	for key, values := range rec.Header() {
		// Content-Type/Length describe the captured body, not what is sent, and
		// the continuation header is consumed here rather than forwarded. Keys
		// arrive canonicalized, so compare in that form.
		if key == "Content-Type" || key == "Content-Length" || key == http.CanonicalHeaderKey("X-AT-Auth-Continue") {
			continue
		}
		for _, value := range values {
			header.Add(key, value)
		}
	}
	// A staged redirect is part of the ceremony (ada restarts an interrupted
	// flow this way); wrapping it in a document would strand the popup.
	if location := rec.Header().Get("Location"); location != "" && rec.Code >= 300 && rec.Code < 400 {
		header.Set("Location", location)
		w.WriteHeader(rec.Code)
		return
	}
	result := rec.Body.Bytes()
	if rec.Code < 200 || rec.Code > 299 || !json.Valid(result) {
		message := "The identity provider could not complete this request."
		var reported struct {
			Message string `json:"message"`
		}
		if json.Valid(result) && json.Unmarshal(result, &reported) == nil && reported.Message != "" {
			message = reported.Message
		}
		result, _ = json.Marshal(map[string]any{"error": true, "message": message})
	}
	payload, err := json.Marshal(map[string]any{"type": "at-auth-result", "result": json.RawMessage(result), "continue": rec.Header().Get("X-AT-Auth-Continue")})
	if err != nil {
		payload = []byte(`{"type":"at-auth-result","result":{"error":true,"message":"The sign-in result could not be delivered."}}`)
	}
	e.renderAuthBridge(w, rec.Code, payload)
}

// renderAuthBridge writes the popup document. The payload is posted to the exact
// configured origin: it can carry an MFA challenge, and "*" would hand that to
// whatever site happens to be the opener. That is also why the document is
// unframeable and runs one nonce-pinned script and nothing else.
func (e *nativeExternalAuth) renderAuthBridge(w http.ResponseWriter, code int, payload []byte) {
	var escaped bytes.Buffer
	json.HTMLEscape(&escaped, payload)
	origin, _ := json.Marshal(e.a.cfg.Origin)
	home, _ := json.Marshal(e.a.session.Cookie.Path)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		nativeError(w, 503, "external authentication unavailable")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(nonce[:])
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+token+"'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if code < 200 || code > 599 {
		code = 200
	}
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Signing in</title></head>
<body><p>You can close this window and return to the application.</p>
<script type="application/json" id="at-auth-payload" nonce="%[1]s">%[2]s</script>
<script nonce="%[1]s">
(function () {
  var payload = JSON.parse(document.getElementById('at-auth-payload').textContent);
  if (window.opener) { window.opener.postMessage(payload, %[3]s); window.close(); return; }
  // No opener: the popup was reused as a normal tab, or the browser severed the
  // relationship. Cookies for a completed sign-in are already set, so returning
  // to the application is the useful outcome; a pending step is reported here.
  var result = payload.result || {};
  if (result.error || result.mfa_required) {
    document.body.textContent = result.message || 'Sign-in needs another step. Return to the application and start again.';
    return;
  }
  location.replace(payload.continue || %[4]s);
})();
</script>
</body></html>`, token, escaped.String(), string(origin), string(home))
}

func (e *nativeExternalAuth) callbackResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p := e.loadProvider(w, r)
	if p == nil {
		return
	}
	f := &nativeExternalFlowStore{store: e.store, provider: *p}
	a, err := e.adapter(r.Context(), *p, f)
	if err != nil {
		externalError(w, err)
		return
	}
	id, outcome, err := a.Login(w, r)
	if err != nil {
		externalError(w, err)
		return
	}
	if outcome != strategy.OutcomeContinue {
		return
	}
	if id == nil || id.Subject == "" || !f.consumed {
		externalError(w, service.ErrAuthConflict)
		return
	}
	i := f.intent
	var account *service.AuthExternalAccount
	if i.Purpose == "link" || i.Purpose == "reauth" {
		current, err := e.hooks.Account(r)
		if err != nil || current != i.Account {
			nativeError(w, 401, "linking account changed; restart")
			return
		}
		account = &i.Account
	} else if i.Purpose != "login" {
		externalError(w, service.ErrAuthConflict)
		return
	}
	namespace := service.AuthIdentityNamespace(p.ID)
	// Do not carry upstream Identity.Roles or Claims into the local identity.
	rawAssertions := make(map[string]any)
	for _, key := range []string{"roles", "groups", "permissions", "scope"} {
		if value, ok := id.Claims[key]; ok {
			rawAssertions[key] = value
		}
	}
	// Configured claim paths reach nested role sets (Keycloak realm_access.roles,
	// resource_access.*.roles) that a flat claim read cannot see. They join the
	// roles assertion under the same bounds, never the local identity's roles.
	roles := service.MergeClaimValues(id.Roles, service.HarvestClaimValues(id.Claims, p.RolesClaims))
	asserted, _ := json.Marshal(map[string]any{"provider_id": p.ID, "issuer": namespace, "roles": roles, "scopes": id.Scopes, "claims": rawAssertions})
	verifiedEmail, _ := id.Claims["email_verified"].(bool)
	// The provider username seeds new accounts and remains refreshable display
	// metadata on the link. Identity is always provider ID plus subject.
	link := service.AuthIdentityLink{ProviderID: p.ID, Issuer: namespace, Subject: id.Subject, Username: service.ClaimUsername(id.Claims, id.Name), Email: id.Email, EmailVerified: verifiedEmail && id.Email != "", AssertedPermissions: asserted}
	// Admission runs here, before any account is created or any link is
	// refreshed, and for link and reauth as well as login. Gating provisioning
	// alone would leave every identity that already signed in — precisely the
	// ones an administrator removes an entry to cut off — able to keep doing
	// so. The address checked is the one about to be stored on the link, so the
	// decision cannot disagree with the record it admits.
	if err := service.AuthEmailAdmits(p.AllowedEmails, link.Email, link.EmailVerified); err != nil {
		// Refusals are logged: from the browser this is one error message, and
		// an administrator debugging "why can this person not sign in" has no
		// other view of the address the provider actually reported.
		slog.WarnContext(r.Context(), "external sign-in refused by allowed-email list",
			slog.String("provider", p.ID), slog.String("subject", id.Subject),
			slog.String("email", link.Email), slog.Bool("email_verified", link.EmailVerified),
			slog.String("purpose", i.Purpose), slog.String("error", err.Error()))
		nativeError(w, 403, err.Error())
		return
	}
	if i.Purpose == "reauth" {
		links, err := e.store.ListAuthIdentityLinks(r.Context(), i.Account.UserID)
		if err != nil {
			externalError(w, err)
			return
		}
		if !slices.ContainsFunc(links, func(l service.AuthIdentityLink) bool {
			return l.ProviderID == link.ProviderID && l.Issuer == link.Issuer && l.Subject == link.Subject
		}) {
			externalError(w, service.ErrAuthConflict)
			return
		}
		for _, existing := range links {
			if existing.ProviderID == link.ProviderID && existing.Issuer == link.Issuer && existing.Subject == link.Subject {
				link.ID = existing.ID
			}
		}
	}
	u, l, err := e.store.CompleteAuthExternalIdentity(r.Context(), link, p.Version, account, i.Flow.ExpiresAt)
	if err != nil {
		externalError(w, err)
		return
	}
	c := service.AuthExternalCompletion{ProviderID: p.ID, ProviderVersion: p.Version, LinkID: l.ID, Remember: i.Remember, Deadline: i.Flow.ExpiresAt, Account: i.Account, ReauthPurpose: i.ReauthPurpose, MobileRequestID: i.MobileRequestID}
	switch i.Purpose {
	case "link":
		httpResponseJSON(w, l, 200)
	case "reauth":
		e.hooks.Reauthenticate(w, r, u, c)
	default:
		e.hooks.Complete(w, r, u, c)
	}
}

func (e *nativeExternalAuth) identities(w http.ResponseWriter, r *http.Request) {
	a, err := e.hooks.Account(r)
	if err != nil {
		nativeError(w, 401, "browser authentication required")
		return
	}
	links, err := e.store.ListAuthIdentityLinks(r.Context(), a.UserID)
	if err != nil {
		externalError(w, err)
		return
	}
	httpResponseJSON(w, links, 200)
}
func (e *nativeExternalAuth) unlink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proof string `json:"recent_proof"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	a, err := e.hooks.Recent(w, r, "identity_unlink", req.Proof)
	if err != nil {
		nativeError(w, 401, "recent browser authentication required")
		return
	}
	if err = e.store.UnlinkAuthIdentity(r.Context(), r.PathValue("link"), a); err != nil {
		externalError(w, err)
		return
	}
	w.WriteHeader(204)
}
