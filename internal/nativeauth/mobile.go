package nativeauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/httpx"
	"github.com/rakunlabs/at/internal/service"
)

const nativeMobileCallback = "atmobile://auth/callback"

type MobileContextKey struct{}

type nativeMobileSource struct {
	limit    *rate.Limiter
	lastSeen time.Time
}

// Only the socket peer is trusted, or — behind a configured trusted proxy — the
// client address that proxy reports. An untrusted caller's forwarded header must
// not let it manufacture fresh quotas or unbounded map keys, so unconfigured
// deployments still bucket a proxy's clients together.
func (a *Auth) allowMobileBegin(r *http.Request, now time.Time) bool {
	key := a.clientIP.SourceKey(r) // Invalid addresses share one bounded fallback bucket.
	a.mobileBeginMu.Lock()
	defer a.mobileBeginMu.Unlock()
	source, ok := a.mobileBeginSources[key]
	if !ok {
		for key, old := range a.mobileBeginSources {
			if now.Sub(old.lastSeen) >= 5*time.Minute {
				delete(a.mobileBeginSources, key)
			}
		}
		// Do not evict live buckets: that would reset quotas under source churn.
		if len(a.mobileBeginSources) >= 1024 {
			return false
		}
		source.limit = rate.NewLimiter(rate.Every(time.Second), 30)
	}
	source.lastSeen = now
	a.mobileBeginSources[key] = source
	return source.limit.AllowN(now, 1)
}

func (a *Auth) mobileIssuer() string {
	return a.cfg.Origin + strings.TrimSuffix(a.session.Cookie.Path, "/")
}

func (a *Auth) mobileDescriptor() map[string]any {
	i := a.mobileIssuer()
	return map[string]any{"version": 1, "issuer": i, "callback_uri": nativeMobileCallback, "code_challenge_methods_supported": []string{"S256"}, "request_expires_in": 300, "code_expires_in": 60, "begin_endpoint": i + "/auth/mobile/begin", "token_endpoint": i + "/auth/mobile/token", "refresh_endpoint": i + "/auth/mobile/refresh", "logout_endpoint": i + "/auth/mobile/logout", "me_endpoint": i + "/auth/me"}
}

// This v1 contract uses exactly 32 random bytes for state, verifier, and code.
func validMobileSecret(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

func mobileChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (a *Auth) MobileBearer(r *http.Request) (*issuer.Pair, error) {
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Cookie")) != 0 {
		return nil, issuer.ErrNotFound
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || !validMobileSecret(strings.TrimPrefix(h, "Bearer ")) {
		return nil, issuer.ErrNotFound
	}
	u, s, err := a.credentials.ResolveAuthAccess(r.Context(), SessionHash(strings.TrimPrefix(h, "Bearer ")))
	if err != nil {
		return nil, err
	}
	if u == nil || u.Disabled || s == nil || s.Transport != "mobile" || !s.AccessExpiresAt.After(time.Now()) {
		return nil, issuer.ErrNotFound
	}
	return nativeAuthPair(u, s, "", ""), nil
}

func (a *Auth) mobilePublic(next http.HandlerFunc, bearer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if len(r.Header.Values("Cookie")) != 0 || (!bearer && len(r.Header.Values("Authorization")) != 0) {
			WriteError(w, 400, "native endpoint does not accept browser cookies or unrelated credentials")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

func (a *Auth) mobileFailure(w http.ResponseWriter, err error) {
	var early *service.AuthRefreshEarlyError
	switch {
	case errors.As(err, &early):
		w.Header().Set("Retry-After", strconv.FormatInt(int64((early.RetryAfter+time.Second-1)/time.Second), 10))
		WriteError(w, 429, "refresh too early; retry after the indicated delay")
	case errors.Is(err, service.ErrAuthSessionLimit):
		WriteError(w, 429, "authentication capacity reached; sign out a device or retry later")
	case errors.Is(err, service.ErrAuthConflict):
		WriteError(w, 400, "authorization request expired, consumed, or rejected")
	default:
		WriteError(w, 503, "authentication unavailable")
	}
}

func (a *Auth) mobileBegin(w http.ResponseWriter, r *http.Request) {
	if !a.allowMobileBegin(r, time.Now()) {
		w.Header().Set("Retry-After", "1")
		WriteError(w, 429, "mobile authentication rate limit exceeded")
		return
	}
	var req struct {
		Challenge  string `json:"code_challenge"`
		Method     string `json:"code_challenge_method"`
		State      string `json:"state"`
		Remember   bool   `json:"remember_me"`
		DeviceName string `json:"device_name"`
	}
	if !DecodeBody(w, r, &req) {
		return
	}
	if req.Method != "S256" || !validMobileSecret(req.Challenge) || !validMobileSecret(req.State) || len(req.DeviceName) > 128 || !utf8.ValidString(req.DeviceName) || strings.IndexFunc(req.DeviceName, unicode.IsControl) >= 0 {
		WriteError(w, 400, "invalid mobile authorization parameters")
		return
	}
	id, _, err := CredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	err = a.mobileStore.CreateAuthMobileRequest(r.Context(), service.AuthMobileRequest{ID: id, Challenge: req.Challenge, State: req.State, Remember: req.Remember, DeviceName: req.DeviceName, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	httpx.JSON(w, map[string]any{"authorization_url": a.mobileIssuer() + "/#/mobile-authorize?request_id=" + url.QueryEscape(id), "expires_in": 300}, 200)
}

func (a *Auth) mobileRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validMobileSecret(id) {
		WriteError(w, 400, "invalid request_id")
		return
	}
	req, err := a.mobileStore.GetAuthMobileRequest(r.Context(), id)
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	if req == nil {
		WriteError(w, 404, "authorization request unavailable")
		return
	}
	httpx.JSON(w, map[string]any{"request_id": req.ID, "device_name": req.DeviceName, "remember_me": req.Remember, "created_at": req.CreatedAt, "expires_at": req.ExpiresAt, "issuer": a.mobileIssuer(), "callback_uri": nativeMobileCallback}, 200)
}

func (a *Auth) mobileDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"request_id"`
		}
		if !DecodeBody(w, r, &req) {
			return
		}
		if !validMobileSecret(req.ID) {
			WriteError(w, 400, "invalid request_id")
			return
		}
		pair, err := a.CurrentSession(r)
		if err != nil {
			WriteError(w, 401, "browser authentication required")
			return
		}
		code, hash := "", ""
		if approve {
			code, _, err = CredentialPair()
			if err != nil {
				a.mobileFailure(w, err)
				return
			}
			hash = SessionHash(code)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := a.mobileStore.DecideAuthMobileRequest(ctx, req.ID, pair.Identity.Subject, pair.SessionID, hash)
		if err != nil {
			a.mobileFailure(w, err)
			return
		}
		q := url.Values{"state": {result.State}}
		if approve {
			q.Set("code", code)
		} else {
			q.Set("error", "access_denied")
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		httpx.JSON(w, map[string]string{"redirect_url": nativeMobileCallback + "?" + q.Encode()}, 200)
	}
}

func (a *Auth) mobileTokens(w http.ResponseWriter, u *service.AuthUser, s *service.AuthSession, access, refresh string) {
	httpx.JSON(w, map[string]any{"token_type": "Bearer", "access_token": access, "refresh_token": refresh, "access_expires_at": s.AccessExpiresAt, "session_expires_at": s.ExpiresAt, "session_id": s.Hash, "remember_me": s.Remember, "issuer": a.mobileIssuer(), "identity": Identity(u)}, 200)
}

func (a *Auth) mobileToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
	}
	if !DecodeBody(w, r, &req) {
		return
	}
	if !validMobileSecret(req.Code) {
		WriteError(w, 400, "invalid authorization code")
		return
	}
	// Invalid verifier syntax still consumes an identified code, fail-closed.
	challenge := ""
	if validMobileSecret(req.Verifier) {
		challenge = mobileChallenge(req.Verifier)
	}
	access, refresh, err := CredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	sid, _, err := CredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	u, s, err := a.mobileStore.RedeemAuthMobileCode(r.Context(), SessionHash(req.Code), challenge, service.AuthSession{Hash: SessionHash(sid), AccessHash: SessionHash(access), RefreshHash: SessionHash(refresh)}, a.cfg.SessionTTL, a.cfg.RememberTTL)
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	a.mobileTokens(w, u, s, access, refresh)
}

func (a *Auth) mobileRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Refresh string `json:"refresh_token"`
	}
	if !DecodeBody(w, r, &req) {
		return
	}
	if !validMobileSecret(req.Refresh) {
		WriteError(w, 401, "refresh rejected; sign in again")
		return
	}
	access, refresh, err := CredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	u, s, err := a.mobileStore.RotateAuthRefreshTransport(r.Context(), SessionHash(req.Refresh), SessionHash(access), SessionHash(refresh), "mobile")
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	if u == nil || s == nil {
		WriteError(w, 401, "refresh rejected; sign in again")
		return
	}
	a.mobileTokens(w, u, s, access, refresh)
}

func (a *Auth) mobileLogout(w http.ResponseWriter, r *http.Request) {
	var req *struct {
		Refresh string `json:"refresh_token"`
	}
	if !DecodeBody(w, r, &req) {
		return
	}
	if req == nil {
		WriteError(w, 400, "invalid authentication request")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		if req.Refresh != "" {
			WriteError(w, 400, "provide bearer or refresh_token, not both")
			return
		}
		pair, err := a.MobileBearer(r)
		if err != nil {
			if errors.Is(err, issuer.ErrNotFound) {
				WriteError(w, 401, "authentication required")
			} else {
				a.mobileFailure(w, err)
			}
			return
		}
		if err := a.store.DeleteAuthSession(r.Context(), pair.SessionID); err != nil {
			a.mobileFailure(w, err)
			return
		}
	} else {
		if !validMobileSecret(req.Refresh) {
			WriteError(w, 400, "refresh_token required")
			return
		}
		if err := a.mobileStore.RevokeAuthCredentialTransport(r.Context(), SessionHash(req.Refresh), "mobile", "refresh"); err != nil {
			a.mobileFailure(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) registerMobile(mux *ada.Server, base string) {
	if a.mobileStore == nil {
		return
	}
	g := mux.Group(base + "/auth/mobile")
	g.POST("/begin", a.mobilePublic(a.mobileBegin, false))
	g.POST("/token", a.mobilePublic(a.mobileToken, false))
	g.POST("/refresh", a.mobilePublic(a.mobileRefresh, false))
	g.POST("/logout", a.mobilePublic(a.mobileLogout, true))
	web := mux.Group(base + "/auth/mobile")
	web.Use(a.Require(false))
	web.GET("/requests/{id}", a.mobileRequest)
	web.POST("/approve", a.mobileDecide(true))
	web.POST("/deny", a.mobileDecide(false))
}
