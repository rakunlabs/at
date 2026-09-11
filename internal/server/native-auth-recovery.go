package server

import (
	"context"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// IssueAuthRecoveryTicket is shared by the authenticated administrator handler
// and the explicit operator CLI. Its caller supplies the verified authority.
func IssueAuthRecoveryTicket(ctx context.Context, store service.AuthSecurityStorer, userID, source string) (string, error) {
	raw, hash, err := securityToken(userID)
	if err != nil {
		return "", err
	}
	err = store.UpdateAuthSecurity(ctx, userID, "", -1, func(u *service.AuthSecurityUpdate) error {
		if !securityAttempt(u) {
			return service.ErrAuthConflict
		}
		live := u.State.Transactions[:0]
		for _, c := range u.State.Transactions {
			if c.Purpose != "recovery" {
				live = append(live, c)
			}
		}
		u.State.Transactions = live
		u.State.Transactions = append(u.State.Transactions, service.AuthTransaction{Hash: hash, Purpose: "recovery", Method: source, Version: u.User.SessionVersion, Expires: u.Now.Add(15 * time.Minute)})
		u.RecoveryAction = "issue"
		u.RecoverySource = source
		return nil
	})
	if err != nil {
		securityAudit("recovery.issue", userID, "rejected")
		return "", err
	}
	securityAudit("recovery.issue", userID, source)
	return raw, nil
}

func (a *nativeAuth) recoveryIssue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proof string `json:"proof"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	u, sid, err := a.securitySelf(r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	if !u.Admin {
		nativeError(w, 403, "installation administrator required")
		return
	}
	issuer, ok := a.security.(service.AuthRecoveryIssuer)
	if !ok {
		a.securityError(w, service.ErrAuthConflict)
		return
	}
	target := r.PathValue("id")
	ticket, hash, err := securityToken(target)
	if err == nil {
		err = issuer.IssueAuthRecovery(r.Context(), u.ID, sid, u.SessionVersion, nativeSessionHash(req.Proof), target, hash)
	}
	if err != nil {
		securityAudit("recovery.issue", target, "rejected")
		a.securityError(w, err)
		return
	}
	securityAudit("recovery.issue", target, "admin:"+u.ID)
	httpResponseJSON(w, map[string]any{"ticket": ticket, "expires_in": 900}, 201)
}

func (a *nativeAuth) recoveryInspect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !a.sameOrigin(w, r) {
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	user := securityTokenUser(req.Ticket)
	if user == "" {
		a.securityError(w, service.ErrAuthConflict)
		return
	}
	err := a.security.UpdateAuthSecurity(r.Context(), user, "", -1, func(u *service.AuthSecurityUpdate) error {
		if securityTransaction(u, req.Ticket, "", "recovery") == nil {
			return service.ErrAuthConflict
		}
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	httpResponseJSON(w, map[string]bool{"valid": true}, 200)
}

func (a *nativeAuth) recoveryRedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !a.sameOrigin(w, r) || !a.passkeyRate(w) {
		return
	}
	var req struct {
		Ticket   string `json:"ticket"`
		Password string `json:"password"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	user := securityTokenUser(req.Ticket)
	if user == "" {
		a.securityError(w, service.ErrAuthConflict)
		return
	}
	if !a.passwordSlot(w) {
		return
	}
	defer func() { <-a.passwordSlots }()
	// Validate authority before doing expensive hashing, then validate again in
	// the replacement transaction after obtaining its account lock.
	err := a.security.UpdateAuthSecurity(r.Context(), user, "", -1, func(u *service.AuthSecurityUpdate) error {
		if securityTransaction(u, req.Ticket, "", "recovery") == nil {
			return service.ErrAuthConflict
		}
		return nil
	})
	if err != nil {
		securityAudit("recovery.redeem", user, "rejected")
		a.securityError(w, err)
		return
	}
	hash, err := a.password.Hash(req.Password)
	if err != nil {
		nativeError(w, 400, nativePasswordMessage(req.Password))
		return
	}
	err = a.security.UpdateAuthSecurity(r.Context(), user, "", -1, func(u *service.AuthSecurityUpdate) error {
		c := securityTransaction(u, req.Ticket, "", "recovery")
		if c == nil {
			return service.ErrAuthConflict
		}
		u.RecoveryAction = "redeem"
		u.RecoverySource = c.Method
		u.ResetPassword = hash
		u.Revoke = true
		return nil
	})
	if err != nil {
		securityAudit("recovery.redeem", user, "rejected")
		a.securityError(w, err)
		return
	}
	securityAudit("recovery.redeem", user, "success")
	a.clearCredentialCookies(w)
	w.WriteHeader(204)
}
