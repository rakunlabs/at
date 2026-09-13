package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

const passwordFailureLimit = 5
const passwordLockDuration = 15 * time.Minute

// Verification and failure accounting share the account row lock. Concurrent
// requests on different replicas cannot pass the fifth failure or lose increments.
func (a *nativeAuth) verifyLoginPassword(w http.ResponseWriter, r *http.Request, user *service.AuthUser, password string) bool {
	if a.security == nil {
		if !user.Disabled && a.password.Verify(user.PasswordHash, password) == nil {
			return true
		}
		securityAudit("login", user.ID, "rejected")
		a.recordLoginEvent(r, user.ID, "login_failed")
		nativeError(w, 401, "invalid credentials")
		return false
	}
	var verified, newlyLocked bool
	var retry time.Duration
	err := a.security.UpdateAuthSecurity(r.Context(), user.ID, "", -1, func(u *service.AuthSecurityUpdate) error {
		if u.User.Disabled {
			return nil
		}
		if u.State.PasswordLockedUntil.After(u.Now) {
			retry = u.State.PasswordLockedUntil.Sub(u.Now)
			return nil
		}
		if !u.State.PasswordLockedUntil.IsZero() {
			u.State.PasswordFailures = 0
			u.State.PasswordLockedUntil = time.Time{}
		}
		if a.password.Verify(u.User.PasswordHash, password) == nil {
			u.State.PasswordFailures = 0
			*user = u.User
			verified = true
			return nil
		}
		u.State.PasswordFailures++
		if u.State.PasswordFailures >= passwordFailureLimit {
			u.State.PasswordLockedUntil = u.Now.Add(passwordLockDuration)
			retry = passwordLockDuration
			newlyLocked = true
		}
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return false
	}
	if retry > 0 {
		if newlyLocked {
			securityAudit("password_login_lock", user.ID, "locked")
			a.recordLoginEvent(r, user.ID, "login_locked")
		} else {
			a.recordLoginEvent(r, user.ID, "login_blocked")
		}
		w.Header().Set("Retry-After", strconv.FormatInt(int64((retry+time.Second-1)/time.Second), 10))
		nativeError(w, 429, "password sign-in temporarily locked after 5 incorrect attempts; wait for the lock to expire or ask an administrator to unlock it")
		return false
	}
	if !verified {
		securityAudit("login", user.ID, "rejected")
		a.recordLoginEvent(r, user.ID, "login_failed")
		nativeError(w, 401, "invalid credentials")
	}
	return verified
}

func (a *nativeAuth) unlockLogin(w http.ResponseWriter, r *http.Request) {
	if a.security == nil {
		nativeError(w, 503, "account security unavailable")
		return
	}
	id := r.PathValue("id")
	err := a.security.UpdateAuthSecurity(r.Context(), id, "", -1, func(u *service.AuthSecurityUpdate) error {
		u.State.PasswordFailures = 0
		u.State.PasswordLockedUntil = time.Time{}
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	a.recordLoginEvent(r, id, "login_unlocked")
	w.WriteHeader(http.StatusNoContent)
}
