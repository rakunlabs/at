package nativeauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/issuer"

	"github.com/rakunlabs/at/internal/httpx"
	"github.com/rakunlabs/at/internal/service"
)

func CredentialPair() (string, string, error) {
	var b [64]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate auth credentials: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:32]), base64.RawURLEncoding.EncodeToString(b[32:]), nil
}

func nativeAuthPair(u *service.AuthUser, s *service.AuthSession, access, refresh string) *issuer.Pair {
	id := Identity(u)
	id.Claims = map[string]any{"session_id": s.Hash, "session_expires_at": s.ExpiresAt, "remember_me": s.Remember}
	id.ExpiresAt = s.AccessExpiresAt
	return &issuer.Pair{SessionID: s.Hash, Identity: id, Access: issuer.Token{Value: access, ExpiresAt: s.AccessExpiresAt}, Refresh: issuer.Token{Value: refresh, ExpiresAt: s.ExpiresAt}}
}

type SessionContextKey struct{}

func (a *Auth) CurrentSession(r *http.Request) (*issuer.Pair, error) {
	// Authentication at request entry fixes the family for this request. A
	// concurrent rotation must not break an already admitted OAuth/passkey action.
	if pair, ok := r.Context().Value(SessionContextKey{}).(*issuer.Pair); ok {
		u, expires, err := a.store.ResolveAuthSession(r.Context(), pair.SessionID)
		if err != nil {
			return nil, fmt.Errorf("resolve admitted auth family: %w", err)
		}
		if u == nil || !expires.After(time.Now()) {
			return nil, issuer.ErrNotFound
		}
		return pair, nil
	}
	cookies := r.CookiesNamed(a.sessionCookieName(r))
	if len(cookies) != 1 {
		return nil, issuer.ErrNotFound
	}
	return a.Resolve(r.Context(), cookies[0].Value)
}

// Select cookie transport from an explicitly configured request address, never
// X-Forwarded-* or the primary origin alone. Only an admitted HTTP loopback host
// can use non-Secure cookies; unknown hosts and TLS requests always stay Secure.
func (a *Auth) cookieSecure(r *http.Request) bool {
	if r == nil {
		return a.session.Cookie.Secure == cookie.SecureAlways
	}
	if r.TLS != nil {
		return true
	}
	origin := r.Header.Get("Origin")
	loopbackHTTP := false
	for _, candidate := range append([]string{a.cfg.Origin}, a.allowedOrigins...) {
		u, err := url.Parse(candidate)
		if err != nil || u.Host != r.Host || (origin != "" && origin != candidate) {
			continue
		}
		if u.Scheme == "https" {
			return true
		}
		if strings.HasPrefix(candidate, "http://") && service.ValidateAuthOrigin(candidate) == nil {
			loopbackHTTP = true
		}
	}
	return !loopbackHTTP
}

func (a *Auth) sessionCookieName(r *http.Request) string {
	if a.cookieSecure(r) {
		return "__Secure-at_session"
	}
	return "at_session"
}

func (a *Auth) RefreshCookieName(r *http.Request) string {
	if a.cookieSecure(r) {
		return "__Secure-at_refresh"
	}
	return "at_refresh"
}

func (a *Auth) setCredentialCookies(w http.ResponseWriter, r *http.Request, p *issuer.Pair, remember bool) {
	for _, v := range []struct {
		name, value string
		expires     time.Time
	}{{a.sessionCookieName(r), p.Access.Value, p.Access.ExpiresAt}, {a.RefreshCookieName(r), p.Refresh.Value, p.Refresh.ExpiresAt}} {
		c := &http.Cookie{Name: v.name, Value: v.value, Path: a.session.Cookie.Path, Secure: a.cookieSecure(r), HttpOnly: true, SameSite: http.SameSiteLaxMode}
		if remember {
			c.Expires = v.expires
			c.MaxAge = int(time.Until(v.expires) / time.Second)
			if c.MaxAge < 1 {
				c.MaxAge = -1
			}
		}
		http.SetCookie(w, c)
	}
}

func (a *Auth) clearCredentialCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{a.sessionCookieName(r), a.RefreshCookieName(r)} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: a.session.Cookie.Path, Secure: a.cookieSecure(r), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	}
}

func (a *Auth) revokeCookies(r *http.Request) error {
	for _, name := range []string{a.sessionCookieName(r), a.RefreshCookieName(r)} {
		for _, c := range r.CookiesNamed(name) {
			if err := a.Revoke(r.Context(), c.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// Refresh is deliberately explicit, never run by management middleware. Ada's
// session middleware resolves a server-held refresh secret, unlike our cookies.
func (a *Auth) refresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.SameOrigin(w, r) {
		return
	}
	var req *struct{}
	if !DecodeBody(w, r, &req) {
		return
	}
	if req == nil {
		WriteError(w, 400, "invalid authentication request")
		return
	}
	cookies := r.CookiesNamed(a.RefreshCookieName(r))
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		WriteError(w, 401, "refresh rejected; sign in again")
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookies[0].Value)
	if err != nil || len(decoded) != 32 {
		WriteError(w, 401, "refresh rejected; sign in again")
		return
	}
	access, refresh, err := CredentialPair()
	if err != nil {
		slog.Warn("auth refresh failed", "error", err.Error())
		WriteError(w, 503, "authentication unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, s, err := a.credentials.RotateAuthRefresh(ctx, SessionHash(cookies[0].Value), SessionHash(access), SessionHash(refresh))
	var early *service.AuthRefreshEarlyError
	if errors.As(err, &early) {
		w.Header().Set("Retry-After", strconv.FormatInt(int64((early.RetryAfter+time.Second-1)/time.Second), 10))
		WriteError(w, 429, "refresh too early; retry after the indicated delay")
		return
	}
	if err != nil {
		slog.Warn("auth refresh failed", "error", err.Error())
		WriteError(w, 503, "authentication unavailable")
		return
	}
	if u == nil || s == nil {
		WriteError(w, 401, "refresh rejected; sign in again")
		return
	}
	pair := nativeAuthPair(u, s, access, refresh)
	a.setCredentialCookies(w, r, pair, s.Remember)
	httpx.JSON(w, pair.Identity, 200)
}

// NewJanitor returns an Auth that can only run the credential janitor, for
// installations without native sign-in. It is nil when store has no
// credential storage.
func NewJanitor(store any) *Auth {
	credentials, ok := store.(service.AuthCredentialStorer)
	if !ok {
		return nil
	}
	a := &Auth{credentials: credentials}
	a.mobileStore, _ = store.(service.AuthMobileStorer)
	return a
}

func (a *Auth) RunJanitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		sweep, cancel := context.WithTimeout(ctx, 5*time.Second)
		// One bounded batch per tick, including startup. Never drain an unbounded backlog.
		_, err := a.credentials.CleanupAuthCredentials(sweep, 500)
		if a.mobileStore != nil {
			if mobileErr := a.mobileStore.CleanupAuthMobileRequests(sweep, 500); mobileErr != nil && err == nil {
				err = mobileErr
			}
		}
		if store, ok := a.credentials.(service.AuthLoginEventStorer); ok {
			if eventErr := store.CleanupAuthLoginEvents(sweep, 500); eventErr != nil && err == nil {
				err = eventErr
			}
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("auth credential cleanup failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
