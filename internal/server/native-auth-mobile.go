package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/service"
)

const nativeMobileCallback = "atmobile://auth/callback"

type nativeMobileContextKey struct{}

type nativeMobileSource struct {
	limit    *rate.Limiter
	lastSeen time.Time
}

// Only socket peer IPs are trusted. A proxy's clients share its bucket; forwarded
// headers must not let callers manufacture fresh quotas or unbounded map keys.
func (a *nativeAuth) allowMobileBegin(r *http.Request, now time.Time) bool {
	addr, err := netip.ParseAddrPort(r.RemoteAddr)
	ip := addr.Addr()
	if err != nil {
		ip, _ = netip.ParseAddr(r.RemoteAddr)
	}
	key := ip.Unmap().String() // Invalid addresses share one bounded fallback bucket.
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

func (a *nativeAuth) mobileIssuer() string {
	return a.cfg.Origin + strings.TrimSuffix(a.session.Cookie.Path, "/")
}

func (a *nativeAuth) mobileDescriptor() map[string]any {
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

func (a *nativeAuth) mobileBearer(r *http.Request) (*issuer.Pair, error) {
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Cookie")) != 0 {
		return nil, issuer.ErrNotFound
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || !validMobileSecret(strings.TrimPrefix(h, "Bearer ")) {
		return nil, issuer.ErrNotFound
	}
	u, s, err := a.credentials.ResolveAuthAccess(r.Context(), nativeSessionHash(strings.TrimPrefix(h, "Bearer ")))
	if err != nil {
		return nil, err
	}
	if u == nil || u.Disabled || s == nil || s.Transport != "mobile" || !s.AccessExpiresAt.After(time.Now()) {
		return nil, issuer.ErrNotFound
	}
	return nativeAuthPair(u, s, "", ""), nil
}

func (a *nativeAuth) mobilePublic(next http.HandlerFunc, bearer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if len(r.Header.Values("Cookie")) != 0 || (!bearer && len(r.Header.Values("Authorization")) != 0) {
			nativeError(w, 400, "native endpoint does not accept browser cookies or unrelated credentials")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

func (a *nativeAuth) mobileFailure(w http.ResponseWriter, err error) {
	var early *service.AuthRefreshEarlyError
	switch {
	case errors.As(err, &early):
		w.Header().Set("Retry-After", strconv.FormatInt(int64((early.RetryAfter+time.Second-1)/time.Second), 10))
		nativeError(w, 429, "refresh too early; retry after the indicated delay")
	case errors.Is(err, service.ErrAuthSessionLimit):
		nativeError(w, 429, "authentication capacity reached; sign out a device or retry later")
	case errors.Is(err, service.ErrAuthConflict):
		nativeError(w, 400, "authorization request expired, consumed, or rejected")
	default:
		nativeError(w, 503, "authentication unavailable")
	}
}

func (a *nativeAuth) mobileBegin(w http.ResponseWriter, r *http.Request) {
	if !a.allowMobileBegin(r, time.Now()) {
		w.Header().Set("Retry-After", "1")
		nativeError(w, 429, "mobile authentication rate limit exceeded")
		return
	}
	var req struct {
		Challenge  string `json:"code_challenge"`
		Method     string `json:"code_challenge_method"`
		State      string `json:"state"`
		Remember   bool   `json:"remember_me"`
		DeviceName string `json:"device_name"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if req.Method != "S256" || !validMobileSecret(req.Challenge) || !validMobileSecret(req.State) || len(req.DeviceName) > 128 || !utf8.ValidString(req.DeviceName) || strings.IndexFunc(req.DeviceName, unicode.IsControl) >= 0 {
		nativeError(w, 400, "invalid mobile authorization parameters")
		return
	}
	id, _, err := nativeCredentialPair()
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
	httpResponseJSON(w, map[string]any{"authorization_url": a.mobileIssuer() + "/#/mobile-authorize?request_id=" + url.QueryEscape(id), "expires_in": 300}, 200)
}

func (a *nativeAuth) mobileRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validMobileSecret(id) {
		nativeError(w, 400, "invalid request_id")
		return
	}
	req, err := a.mobileStore.GetAuthMobileRequest(r.Context(), id)
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	if req == nil {
		nativeError(w, 404, "authorization request unavailable")
		return
	}
	httpResponseJSON(w, map[string]any{"request_id": req.ID, "device_name": req.DeviceName, "remember_me": req.Remember, "created_at": req.CreatedAt, "expires_at": req.ExpiresAt, "issuer": a.mobileIssuer(), "callback_uri": nativeMobileCallback}, 200)
}

func (a *nativeAuth) mobileDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"request_id"`
		}
		if !decodeNativeBody(w, r, &req) {
			return
		}
		if !validMobileSecret(req.ID) {
			nativeError(w, 400, "invalid request_id")
			return
		}
		pair, err := a.currentSession(r)
		if err != nil {
			nativeError(w, 401, "browser authentication required")
			return
		}
		code, hash := "", ""
		if approve {
			code, _, err = nativeCredentialPair()
			if err != nil {
				a.mobileFailure(w, err)
				return
			}
			hash = nativeSessionHash(code)
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
		httpResponseJSON(w, map[string]string{"redirect_url": nativeMobileCallback + "?" + q.Encode()}, 200)
	}
}

func (a *nativeAuth) mobileTokens(w http.ResponseWriter, u *service.AuthUser, s *service.AuthSession, access, refresh string) {
	httpResponseJSON(w, map[string]any{"token_type": "Bearer", "access_token": access, "refresh_token": refresh, "access_expires_at": s.AccessExpiresAt, "session_expires_at": s.ExpiresAt, "session_id": s.Hash, "remember_me": s.Remember, "issuer": a.mobileIssuer(), "identity": authIdentity(u)}, 200)
}

func (a *nativeAuth) mobileToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if !validMobileSecret(req.Code) {
		nativeError(w, 400, "invalid authorization code")
		return
	}
	// Invalid verifier syntax still consumes an identified code, fail-closed.
	challenge := ""
	if validMobileSecret(req.Verifier) {
		challenge = mobileChallenge(req.Verifier)
	}
	access, refresh, err := nativeCredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	sid, _, err := nativeCredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	u, s, err := a.mobileStore.RedeemAuthMobileCode(r.Context(), nativeSessionHash(req.Code), challenge, service.AuthSession{Hash: nativeSessionHash(sid), AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh)}, a.cfg.SessionTTL, a.cfg.RememberTTL)
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	a.mobileTokens(w, u, s, access, refresh)
}

func (a *nativeAuth) mobileRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Refresh string `json:"refresh_token"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if !validMobileSecret(req.Refresh) {
		nativeError(w, 401, "refresh rejected; sign in again")
		return
	}
	access, refresh, err := nativeCredentialPair()
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	u, s, err := a.mobileStore.RotateAuthRefreshTransport(r.Context(), nativeSessionHash(req.Refresh), nativeSessionHash(access), nativeSessionHash(refresh), "mobile")
	if err != nil {
		a.mobileFailure(w, err)
		return
	}
	if u == nil || s == nil {
		nativeError(w, 401, "refresh rejected; sign in again")
		return
	}
	a.mobileTokens(w, u, s, access, refresh)
}

func (a *nativeAuth) mobileLogout(w http.ResponseWriter, r *http.Request) {
	var req *struct {
		Refresh string `json:"refresh_token"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if req == nil {
		nativeError(w, 400, "invalid authentication request")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		if req.Refresh != "" {
			nativeError(w, 400, "provide bearer or refresh_token, not both")
			return
		}
		pair, err := a.mobileBearer(r)
		if err != nil {
			if errors.Is(err, issuer.ErrNotFound) {
				nativeError(w, 401, "authentication required")
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
			nativeError(w, 400, "refresh_token required")
			return
		}
		if err := a.mobileStore.RevokeAuthCredentialTransport(r.Context(), nativeSessionHash(req.Refresh), "mobile", "refresh"); err != nil {
			a.mobileFailure(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *nativeAuth) registerMobile(mux *ada.Server, base string) {
	if a.mobileStore == nil {
		return
	}
	g := mux.Group(base + "/auth/mobile")
	g.POST("/begin", a.mobilePublic(a.mobileBegin, false))
	g.POST("/token", a.mobilePublic(a.mobileToken, false))
	g.POST("/refresh", a.mobilePublic(a.mobileRefresh, false))
	g.POST("/logout", a.mobilePublic(a.mobileLogout, true))
	web := mux.Group(base + "/auth/mobile")
	web.Use(a.require(false))
	web.GET("/requests/{id}", a.mobileRequest)
	web.POST("/approve", a.mobileDecide(true))
	web.POST("/deny", a.mobileDecide(false))
}
