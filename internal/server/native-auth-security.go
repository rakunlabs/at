package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"

	"github.com/rakunlabs/at/internal/service"
)

type nativeMFAContextKey struct{}
type nativeMFAEvidence struct{ Challenge, Binding, Code string }

func securityToken(userID string) (string, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate security token: %w", err)
	}
	raw := userID + "." + base64.RawURLEncoding.EncodeToString(b[:])
	return raw, nativeSessionHash(raw), nil
}

func securityTokenUser(raw string) string {
	user, token, ok := strings.Cut(raw, ".")
	if !ok || len(user) != 26 || len(token) != 43 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 {
		return ""
	}
	return user
}

func (a *nativeAuth) securityCookie(raw string, age int) *http.Cookie {
	return &http.Cookie{Name: a.session.CookieName + "_security", Value: raw, Path: a.session.Cookie.Path + "auth/", HttpOnly: true, Secure: a.session.Cookie.Secure == cookie.SecureAlways, SameSite: http.SameSiteStrictMode, MaxAge: age}
}

func (a *nativeAuth) securityBinding(r *http.Request) string {
	cs := r.CookiesNamed(a.securityCookie("", 0).Name)
	if len(cs) != 1 || len(cs[0].Value) != 43 {
		return ""
	}
	return nativeSessionHash(cs[0].Value)
}

func (a *nativeAuth) ensureSecurityBinding(w http.ResponseWriter, r *http.Request) (string, error) {
	if binding := a.securityBinding(r); binding != "" {
		http.SetCookie(w, a.securityCookie(r.CookiesNamed(a.securityCookie("", 0).Name)[0].Value, 900))
		return binding, nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b[:])
	http.SetCookie(w, a.securityCookie(raw, 900))
	return nativeSessionHash(raw), nil
}

func securityAttempt(u *service.AuthSecurityUpdate) bool {
	if !u.State.Window.Add(5 * time.Minute).After(u.Now) {
		u.State.Window = u.Now
		u.State.Attempts = 0
	}
	u.State.Attempts++
	return u.State.Attempts <= 60
}

func (a *nativeAuth) admitSecurityAccount(w http.ResponseWriter, r *http.Request, userID string) bool {
	if a.security == nil {
		return true
	}
	allowed := false
	err := a.security.UpdateAuthSecurity(r.Context(), userID, "", -1, func(u *service.AuthSecurityUpdate) error { allowed = securityAttempt(u); return nil })
	if err != nil {
		a.securityError(w, err)
		return false
	}
	if !allowed {
		nativeError(w, 429, "authentication rate limit exceeded")
		return false
	}
	return true
}

func securityTransaction(u *service.AuthSecurityUpdate, raw, binding, purpose string) *service.AuthTransaction {
	hash := nativeSessionHash(raw)
	for i := range u.State.Transactions {
		c := &u.State.Transactions[i]
		if c.Hash == hash && c.Binding == binding && c.Purpose == purpose && c.Version == u.User.SessionVersion && c.Expires.After(u.Now) && c.Attempts < 5 {
			if u.Deadline.IsZero() || c.Expires.Before(u.Deadline) {
				u.Deadline = c.Expires
			}
			return c
		}
	}
	return nil
}

func verifySecurityCode(s *service.AuthSecurityState, code string, now time.Time) bool {
	if s.Secret == "" {
		return false
	}
	if len(code) == 6 {
		secret, err := totp.SecretFromBase32(s.Secret)
		if err != nil {
			return false
		}
		cfg := totp.Default()
		cfg.Skew = 0
		step := now.Unix() / 30
		for delta := int64(-1); delta <= 1; delta++ {
			candidate := step + delta
			if candidate <= s.LastStep {
				continue
			}
			want, err := cfg.Generate(secret, time.Unix(candidate*30, 0))
			if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
				s.LastStep = candidate
				return true
			}
		}
		return false
	}
	hash := nativeSessionHash(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", "")))
	for i, want := range s.BackupHashes {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) == 1 {
			s.BackupHashes = append(s.BackupHashes[:i], s.BackupHashes[i+1:]...)
			return true
		}
	}
	return false
}

func newBackupCodes() ([]string, []string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, nil, err
		}
		codes[i] = fmt.Sprintf("%X", b[:])
		hashes[i] = nativeSessionHash(codes[i])
	}
	return codes, hashes, nil
}

func securityAudit(action, user, outcome string) {
	slog.Info("account security", "action", action, "user_id", user, "outcome", outcome)
}

func (a *nativeAuth) securityError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrAuthConflict) {
		nativeError(w, 401, "invalid or expired security request")
	} else {
		nativeError(w, 503, "account security unavailable")
	}
}

func (a *nativeAuth) createCompletedSession(ctx context.Context, s service.AuthSession) error {
	if a.security == nil {
		return a.store.CreateAuthSession(ctx, s)
	}
	var rejected bool
	err := a.security.UpdateAuthSecurity(ctx, s.UserID, "", s.Version, func(u *service.AuthSecurityUpdate) error {
		if u.State.Secret != "" {
			e, ok := ctx.Value(nativeMFAContextKey{}).(nativeMFAEvidence)
			if !ok {
				return service.ErrAuthConflict
			}
			c := securityTransaction(u, e.Challenge, e.Binding, "mfa")
			if !securityAttempt(u) || c == nil || c.Generation != u.State.Generation {
				rejected = true
				return nil
			}
			c.Attempts++
			if !verifySecurityCode(&u.State, e.Code, u.Now) {
				rejected = true
				return nil
			}
			deadline := c.Expires
			c.Expires = u.Now
			s.Remember = c.Remember
			s.AdmissionDeadline = deadline
		}
		u.Session = &s
		return nil
	})
	if err == nil && rejected {
		securityAudit("mfa", s.UserID, "rejected")
		return service.ErrAuthConflict
	}
	if _, ok := ctx.Value(nativeMFAContextKey{}).(nativeMFAEvidence); ok {
		outcome := "success"
		if err != nil {
			outcome = "rejected"
		}
		securityAudit("mfa", s.UserID, outcome)
	}
	return err
}

func (a *nativeAuth) finishPrimaryLogin(w http.ResponseWriter, r *http.Request, user *service.AuthUser, remember bool, method string) {
	w.Header().Set("Cache-Control", "no-store")
	if a.security == nil {
		a.finishCompletedLogin(w, r, user, remember)
		return
	}
	raw, hash, err := securityToken(user.ID)
	if err != nil {
		a.securityError(w, err)
		return
	}
	pending := false
	err = a.security.UpdateAuthSecurity(r.Context(), user.ID, "", user.SessionVersion, func(u *service.AuthSecurityUpdate) error {
		if deadline, _ := r.Context().Value(nativeCeremonyDeadlineContextKey{}).(time.Time); !deadline.IsZero() {
			if !deadline.After(u.Now) {
				return service.ErrAuthConflict
			}
			if u.Deadline.IsZero() || deadline.Before(u.Deadline) {
				u.Deadline = deadline
			}
		}
		if u.State.Secret == "" {
			return nil
		}
		binding, err := a.ensureSecurityBinding(w, r)
		if err != nil {
			return err
		}
		if !securityAttempt(u) || len(u.State.Transactions) >= 20 {
			return service.ErrAuthConflict
		}
		u.State.Transactions = append(u.State.Transactions, service.AuthTransaction{Hash: hash, Binding: binding, Purpose: "mfa", Method: method, Version: user.SessionVersion, Generation: u.State.Generation, Remember: remember, Expires: u.Now.Add(5 * time.Minute)})
		pending = true
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	if pending {
		if err := a.revokeCookies(r); err != nil {
			a.securityError(w, err)
			return
		}
		a.clearCredentialCookies(w)
		httpResponseJSON(w, map[string]any{"mfa_required": true, "challenge": raw, "expires_in": 300, "methods": []string{"totp", "backup_code"}}, 200)
		return
	}
	a.finishCompletedLogin(w, r, user, remember)
}

func (a *nativeAuth) mfaVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.sameOrigin(w, r) {
		return
	}
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	userID := securityTokenUser(req.Challenge)
	binding := a.securityBinding(r)
	if userID == "" || binding == "" {
		a.securityError(w, service.ErrAuthConflict)
		return
	}
	var user *service.AuthUser
	var remember bool
	var method string
	err := a.security.UpdateAuthSecurity(r.Context(), userID, "", -1, func(u *service.AuthSecurityUpdate) error {
		c := securityTransaction(u, req.Challenge, binding, "mfa")
		if c == nil || u.User.Disabled {
			return service.ErrAuthConflict
		}
		copy := u.User
		user = &copy
		remember = c.Remember
		method = c.Method
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	ctx := context.WithValue(r.Context(), nativeMFAContextKey{}, nativeMFAEvidence{req.Challenge, binding, req.Code})
	if strings.HasPrefix(method, "external:") {
		completion, err := nativeExternalCompletionFromMethod(method)
		if err != nil {
			a.securityError(w, err)
			return
		}
		ctx = service.ContextWithAuthExternalCompletion(ctx, completion)
		ctx = context.WithValue(ctx, nativeCeremonyDeadlineContextKey{}, completion.Deadline)
	}
	a.finishCompletedLogin(w, r.WithContext(ctx), user, remember)
}

func (a *nativeAuth) securitySelf(r *http.Request) (*service.AuthUser, string, error) {
	pair, err := a.currentSession(r)
	if err != nil {
		return nil, "", service.ErrAuthConflict
	}
	u, _, err := a.store.ResolveAuthSession(r.Context(), pair.SessionID)
	if err != nil {
		return nil, "", err
	}
	if u == nil {
		return nil, "", service.ErrAuthConflict
	}
	return u, pair.SessionID, nil
}

func (a *nativeAuth) registerSecurity(mux *ada.Server, base string) {
	if a.security == nil {
		return
	}
	public := mux.Group(base + "/auth")
	public.POST("/mfa/verify", a.mfaVerify)
	public.POST("/recovery/inspect", a.recoveryInspect)
	public.POST("/recovery/redeem", a.recoveryRedeem)
	self := mux.Group(base + "/auth")
	self.Use(a.require(false))
	self.POST("/reauth/begin", a.reauthBegin)
	self.POST("/reauth/finish", a.reauthFinish)
	self.GET("/totp/status", a.totpStatus)
	self.POST("/totp/enroll", a.totpEnroll)
	self.POST("/totp/confirm", a.totpConfirm)
	self.POST("/totp/remove", a.totpMutate(false))
	self.POST("/totp/backup-codes/regenerate", a.totpMutate(true))
	admin := mux.Group(base + "/auth/users")
	admin.Use(a.require(true))
	admin.POST("/{id}/recovery", a.recoveryIssue)
}
