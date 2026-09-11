package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"github.com/rakunlabs/ada/middleware/auth/session"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

const nativeSessionTTL = 8 * time.Hour

const nativeRememberTTL = 30 * 24 * time.Hour

// nativePasswordMinLength is the floor for every password entry point: setup,
// user creation, self change, administrator reset and recovery redemption.
// 8 matches NIST SP 800-63B's minimum for user-chosen secrets. Length alone is
// weak protection here, so enrol a second factor on administrator accounts.
const nativePasswordMinLength = 8

// nativePasswordMaxBytes bounds the hashing work a single request can request.
const nativePasswordMaxBytes = 1024

// nativePasswordMessage reports why a password was refused. The byte ceiling is
// an implementation bound almost nobody reaches, so it is mentioned only when
// it is the actual reason rather than advertised in every prompt.
func nativePasswordMessage(pw string) string {
	if len(pw) > nativePasswordMaxBytes {
		return "password must be at most 1024 bytes"
	}
	return "password must be at least 8 characters"
}

type nativeRememberContextKey struct{}

type nativeCeremonyDeadlineContextKey struct{}

// nativeAuth is intentionally not a general authorization framework. Every
// management route requires a live administrator; organizations are NOT scopes.
type nativeAuth struct {
	security           service.AuthSecurityStorer
	store              service.AuthStorer
	credentials        service.AuthCredentialStorer
	cfg                config.NativeAuth
	session            session.Session
	password           password.PBKDF2
	loginLimit         *rate.Limiter
	passwordSlots      chan struct{}
	oauthMu            sync.Mutex
	oauthStates        map[string]nativeOAuthState
	passkey            *passkey.WebAuthn
	keyStore           service.AuthPasskeyStorer
	mobileStore        service.AuthMobileStorer
	mobileBeginMu      sync.Mutex
	mobileBeginSources map[string]nativeMobileSource
}

func newNativeAuth(cfg config.Server, store any) (*nativeAuth, error) {
	if cfg.NativeAuth == nil || !cfg.NativeAuth.Enabled {
		return nil, nil
	}
	if cfg.ForwardAuth != nil {
		return nil, fmt.Errorf("native_auth and forward_auth cannot both be enabled")
	}
	if cfg.BasePath != "" && (cfg.BasePath == "/" || !strings.HasPrefix(cfg.BasePath, "/") || path.Clean(cfg.BasePath) != cfg.BasePath || strings.ContainsAny(cfg.BasePath, "?#%\\")) {
		return nil, fmt.Errorf("native_auth requires a canonical base_path without a trailing slash")
	}
	c := *cfg.NativeAuth
	if c.SessionTTL == 0 {
		c.SessionTTL = nativeSessionTTL
	}
	if c.RememberTTL == 0 {
		c.RememberTTL = nativeRememberTTL
	}
	if c.SessionTTL < 10*time.Minute || c.SessionTTL > 24*time.Hour || c.RememberTTL < c.SessionTTL || c.RememberTTL > nativeRememberTTL {
		return nil, fmt.Errorf("native_auth requires session_ttl in [10m,24h] and remember_ttl in [session_ttl,720h]")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || c.Origin != u.Scheme+"://"+u.Host {
		return nil, fmt.Errorf("native_auth.origin must be an exact HTTP(S) origin without a path")
	}
	secure := cookie.SecureAlways
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.InsecureHTTP || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, fmt.Errorf("native_auth requires HTTPS (insecure_http permits only loopback HTTP)")
		}
		secure = cookie.SecureNever
	}
	if c.BootstrapToken != "" && len(c.BootstrapToken) < 32 {
		return nil, fmt.Errorf("native_auth.bootstrap_token must be at least 32 bytes")
	}
	authStore, ok := store.(service.AuthStorer)
	if !ok || authStore == nil {
		return nil, fmt.Errorf("native_auth requires a persistent AuthStorer")
	}
	a := &nativeAuth{store: authStore, cfg: c, password: password.PBKDF2{MinLength: nativePasswordMinLength}, loginLimit: rate.NewLimiter(rate.Every(6*time.Second), 5), passwordSlots: make(chan struct{}, 2)}
	a.credentials, ok = store.(service.AuthCredentialStorer)
	if !ok {
		return nil, fmt.Errorf("native_auth requires a persistent AuthCredentialStorer")
	}
	a.mobileStore, _ = store.(service.AuthMobileStorer)
	a.security, _ = store.(service.AuthSecurityStorer)
	a.mobileBeginSources = make(map[string]nativeMobileSource)
	name := "__Secure-at_session"
	if secure == cookie.SecureNever {
		name = "at_session"
	}
	a.session = session.Session{Issuer: a, CookieName: name, Cookie: session.CookieOptions{Path: cfg.BasePath + "/", Secure: secure, SameSite: http.SameSiteLaxMode}}
	if err := a.session.Init(); err != nil {
		return nil, fmt.Errorf("initialize native session: %w", err)
	}
	if err := a.initPasskeys(u.Hostname()); err != nil {
		return nil, err
	}
	return a, nil
}

func authIdentity(u *service.AuthUser) *identity.Identity {
	id := &identity.Identity{Subject: u.ID, Name: u.Username, Provider: "local"}
	if u.Admin {
		id.Roles = []string{"admin"}
	}
	return id
}

func nativeSessionHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// Issue uses the version observed at password verification, never a newly read
// version, so concurrent disable/revoke wins over an in-flight login.
func (a *nativeAuth) Issue(ctx context.Context, id *identity.Identity) (*issuer.Pair, error) {
	version, ok := id.Claims["session_version"].(int64)
	if !ok {
		return nil, fmt.Errorf("issue native session: missing verified version")
	}
	raw, refresh, err := nativeCredentialPair()
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}
	sid := hex.EncodeToString(nonce[:])
	remember, _ := ctx.Value(nativeRememberContextKey{}).(bool)
	ttl := a.cfg.SessionTTL
	if remember {
		ttl = a.cfg.RememberTTL
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(ttl)
	accessExpires := now.Add(10 * time.Minute)
	if accessExpires.After(expires) {
		accessExpires = expires
	}
	deadline, _ := ctx.Value(nativeCeremonyDeadlineContextKey{}).(time.Time)
	s := service.AuthSession{Hash: sid, AccessHash: nativeSessionHash(raw), RefreshHash: nativeSessionHash(refresh), AccessExpiresAt: accessExpires, Remember: remember, UserID: id.Subject, Version: version, ExpiresAt: expires, AdmissionDeadline: deadline}
	if err := a.createCompletedSession(ctx, s); err != nil {
		return nil, fmt.Errorf("persist native session: %w", err)
	}
	id.Claims = map[string]any{"session_id": sid, "session_expires_at": expires, "remember_me": remember}
	id.ExpiresAt = accessExpires
	return &issuer.Pair{SessionID: sid, Identity: id, Access: issuer.Token{Value: raw, ExpiresAt: accessExpires}, Refresh: issuer.Token{Value: refresh, ExpiresAt: expires}}, nil
}

func (a *nativeAuth) Resolve(ctx context.Context, token string) (*issuer.Pair, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(token) != 43 {
		return nil, issuer.ErrNotFound
	}
	u, s, err := a.credentials.ResolveAuthAccess(ctx, nativeSessionHash(token))
	if err != nil {
		return nil, fmt.Errorf("resolve native session: %w", err)
	}
	if u == nil || u.Disabled || s == nil || s.Transport == "mobile" || !s.AccessExpiresAt.After(time.Now()) {
		return nil, issuer.ErrNotFound
	}
	return nativeAuthPair(u, s, "", ""), nil
}

// Disable Ada's automatic refresh protocol. Only the explicit refresh endpoint
// consumes the browser's refresh cookie; a stable session ID is never a bearer.
func (a *nativeAuth) Refresh(context.Context, string, string) (*issuer.Pair, error) {
	return nil, issuer.ErrRefreshExpired
}

func (a *nativeAuth) Revoke(ctx context.Context, token string) error {
	return a.credentials.RevokeAuthCredential(ctx, nativeSessionHash(token))
}

func nativeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	httpResponseJSON(w, map[string]string{"message": message}, code)
}

// Unsafe cookie requests require an exact configured Origin, including login
// and bootstrap. This also rejects cross-origin WebSocket handshakes; neither
// Host nor forwarded headers are trusted to define the allowed origin.
func (a *nativeAuth) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	// Only require's cookie-free mobile bearer branch sets this marker.
	if mobile, _ := r.Context().Value(nativeMobileContextKey{}).(bool); mobile {
		return true
	}
	origin := r.Header.Get("Origin")
	unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	// Only connector returns may navigate cross-site. Their handlers must consume
	// session-bound state before displaying a code or exchanging credentials.
	oauthPath := a.session.Cookie.Path + "api/v1/oauth/"
	callbackNavigation := r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document" &&
		(r.URL.Path == oauthPath+"callback" || r.URL.Path == oauthPath+"code-display")
	if (unsafe && origin != a.cfg.Origin) || (origin != "" && origin != a.cfg.Origin) || (r.Header.Get("Sec-Fetch-Site") == "cross-site" && !callbackNavigation) {
		nativeError(w, http.StatusForbidden, "same-origin request required")
		return false
	}
	if unsafe && strings.HasPrefix(r.URL.Path, a.session.Cookie.Path+"auth/") {
		if limiter, ok := a.security.(service.AuthSecurityAdmissionStorer); ok {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			allowed, err := limiter.AdmitAuthSecuritySource(r.Context(), nativeSessionHash(host))
			if err != nil {
				nativeError(w, 503, "authentication unavailable")
				return false
			}
			if !allowed {
				nativeError(w, 429, "authentication rate limit exceeded")
				return false
			}
		}
	}
	return true
}

func (a *nativeAuth) require(admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if len(r.Header.Values("Authorization")) != 0 {
				if !admin && r.URL.Path != a.session.Cookie.Path+"auth/me" {
					nativeError(w, 401, "browser authentication required")
					return
				}
				pair, err := a.mobileBearer(r)
				if err != nil {
					if errors.Is(err, issuer.ErrNotFound) {
						nativeError(w, 401, "authentication required")
					} else {
						nativeError(w, 503, "authentication unavailable")
					}
					return
				}
				if admin && !pair.Identity.HasRole("admin") {
					nativeError(w, 403, "management access is administrator-only")
					return
				}
				ctx := context.WithValue(r.Context(), nativeSessionContextKey{}, pair)
				ctx = context.WithValue(ctx, nativeMobileContextKey{}, true)
				next.ServeHTTP(w, r.WithContext(identity.WithContext(ctx, pair.Identity)))
				return
			}
			pair, err := a.currentSession(r)
			if err != nil {
				if errors.Is(err, issuer.ErrNotFound) {
					nativeError(w, 401, "authentication required")
				} else {
					nativeError(w, 503, "authentication unavailable")
				}
				return
			}
			if !a.sameOrigin(w, r) {
				return
			}
			if admin && !pair.Identity.HasRole("admin") {
				nativeError(w, http.StatusForbidden, "management access is administrator-only")
				return
			}
			ctx := context.WithValue(r.Context(), nativeSessionContextKey{}, pair)
			next.ServeHTTP(w, r.WithContext(identity.WithContext(ctx, pair.Identity)))
		})
	}
}

func decodeNativeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeNativeBodyLimit(w, r, dst, 4096)
}

func decodeNativeBodyLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		nativeError(w, 415, "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err = d.Decode(dst)
	if err == nil {
		if trailing := d.Decode(new(any)); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("trailing data")
			}
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		code := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		nativeError(w, code, "invalid authentication request")
		return false
	}
	return true
}

func normalizeNativeUsername(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func validNativeUsername(name string) bool {
	if len(name) < 3 || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("._@+-", c)) {
			return false
		}
	}
	return true
}

func (a *nativeAuth) passwordSlot(w http.ResponseWriter) bool {
	select {
	case a.passwordSlots <- struct{}{}:
		return true
	default:
		nativeError(w, 429, "authentication busy; retry later")
		return false
	}
}

func (a *nativeAuth) login(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	if !a.loginLimit.Allow() {
		w.Header().Set("Retry-After", "6")
		nativeError(w, 429, "login rate limit exceeded")
		return
	}
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		RememberMe bool   `json:"remember_me"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if !a.passwordSlot(w) {
		return
	}
	defer func() { <-a.passwordSlots }()
	u, err := a.store.GetAuthUser(r.Context(), normalizeNativeUsername(req.Username))
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	hash := password.Dummy
	if u != nil {
		if !a.admitSecurityAccount(w, r, u.ID) {
			return
		}
		hash = u.PasswordHash
	}
	err = a.password.Verify(hash, req.Password)
	if err != nil || u == nil || u.Disabled {
		userID := ""
		if u != nil {
			userID = u.ID
		}
		securityAudit("login", userID, "rejected")
		nativeError(w, 401, "invalid credentials")
		return
	}
	a.finishPrimaryLogin(w, r, u, req.RememberMe, "password")
}

// The lifetime belongs to this issuance only, never to mutable shared config.
func (a *nativeAuth) finishLogin(w http.ResponseWriter, r *http.Request, u *service.AuthUser, remember bool) {
	a.finishPrimaryLogin(w, r, u, remember, "password")
}

func (a *nativeAuth) finishCompletedLogin(w http.ResponseWriter, r *http.Request, u *service.AuthUser, remember bool) {
	id := authIdentity(u)
	id.Claims = map[string]any{"session_version": u.SessionVersion}
	pair, err := a.Issue(context.WithValue(r.Context(), nativeRememberContextKey{}, remember), id)
	if errors.Is(err, service.ErrAuthConflict) {
		nativeError(w, 401, "invalid or expired authentication")
		return
	}
	if errors.Is(err, service.ErrAuthSessionLimit) {
		nativeError(w, 429, "maximum 20 active sessions; sign out a device or revoke sessions")
		return
	}
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	// Only nonsecret family metadata enters JSON, never bearer values or hashes.
	// A successful login ignores any client-supplied session identity.
	if err := a.revokeCookies(r); err != nil {
		_ = a.Revoke(r.Context(), pair.Access.Value)
		nativeError(w, 503, "authentication unavailable")
		return
	}
	a.setCredentialCookies(w, pair, remember)
	securityAudit("login", u.ID, "success")
	w.Header().Set("Cache-Control", "no-store")
	httpResponseJSON(w, pair.Identity, 200)
}

func (a *nativeAuth) createUser(bootstrap bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(w, r) {
			return
		}
		if bootstrap {
			if !a.loginLimit.Allow() {
				nativeError(w, 429, "login rate limit exceeded")
				return
			}
			want := sha256.Sum256([]byte(a.cfg.BootstrapToken))
			got := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
			if a.cfg.BootstrapToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
				nativeError(w, 401, "invalid bootstrap credentials")
				return
			}
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Admin    bool   `json:"admin"`
		}
		if !decodeNativeBody(w, r, &req) {
			return
		}
		req.Username = normalizeNativeUsername(req.Username)
		if !validNativeUsername(req.Username) {
			nativeError(w, 400, "username must be 3-128 ASCII letters, digits, or ._@+-")
			return
		}
		if !a.passwordSlot(w) {
			return
		}
		defer func() { <-a.passwordSlots }()
		hash, err := a.password.Hash(req.Password)
		if err != nil {
			nativeError(w, 400, nativePasswordMessage(req.Password))
			return
		}
		u, err := a.store.CreateAuthUser(r.Context(), service.AuthUser{Username: req.Username, PasswordHash: hash, Admin: req.Admin}, bootstrap)
		if errors.Is(err, service.ErrAuthConflict) {
			nativeError(w, 409, "username unavailable or bootstrap already completed")
			return
		}
		if err != nil {
			nativeError(w, 503, "authentication unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpResponseJSON(w, authIdentity(u), 201)
	}
}

func (a *nativeAuth) logout(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	if err := a.revokeCookies(r); err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	a.clearCredentialCookies(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *nativeAuth) invalidateUser(disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if disable && id == identity.FromContext(r.Context()).Subject {
			nativeError(w, 409, "cannot disable your own account")
			return
		}
		found, err := a.store.InvalidateAuthUser(r.Context(), id, disable)
		if errors.Is(err, service.ErrAuthConflict) {
			nativeError(w, 409, "cannot disable the last active administrator")
			return
		}
		if err != nil {
			nativeError(w, 503, "authentication unavailable")
			return
		}
		if !found {
			nativeError(w, 404, "user not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *nativeAuth) register(mux *ada.Server, base string) {
	a.registerSecurity(mux, base)
	a.registerMobile(mux, base)
	a.registerPasskeys(mux, base)
	public := mux.Group(base + "/auth")
	public.POST("/login", a.login)
	public.POST("/refresh", a.refresh)
	public.POST("/logout", a.logout)
	public.POST("/bootstrap", a.createUser(true))
	self := mux.Group(base + "/auth")
	self.Use(a.require(false))
	self.GET("/me", func(w http.ResponseWriter, r *http.Request) {
		httpResponseJSON(w, identity.FromContext(r.Context()), 200)
	})
	self.POST("/password", a.changePassword(false))
	admin := mux.Group(base + "/auth/users")
	admin.Use(a.require(true))
	admin.POST("", a.createUser(false))
	admin.GET("", a.listUsers)
	admin.POST("/{id}/enable", a.enableUser)
	admin.POST("/{id}/password", a.changePassword(true))
	admin.POST("/{id}/disable", a.invalidateUser(true))
	admin.POST("/{id}/revoke-sessions", a.invalidateUser(false))
}
