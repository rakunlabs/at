package server

import (
	"net/http"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"

	"github.com/rakunlabs/at/internal/service"
)

func (a *nativeAuth) totpStatus(w http.ResponseWriter, r *http.Request) {
	u, sid, err := a.securitySelf(r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	var enabled bool
	var remaining int
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		enabled = update.State.Secret != ""
		remaining = len(update.State.BackupHashes)
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"enabled": enabled, "backup_codes_remaining": remaining}, 200)
}

func (a *nativeAuth) totpEnroll(w http.ResponseWriter, r *http.Request) {
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
	secret, err := totp.NewSecret(nil, 20)
	if err != nil {
		a.securityError(w, err)
		return
	}
	raw, hash, err := securityToken(u.ID)
	if err != nil {
		a.securityError(w, err)
		return
	}
	binding, err := a.ensureSecurityBinding(w, r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		if update.State.Secret != "" || !consumeSecurityProof(update, sid, "totp.enroll", req.Proof) {
			return service.ErrAuthConflict
		}
		// A new begin replaces pending enrollment, never an active factor.
		live := update.State.Transactions[:0]
		for _, c := range update.State.Transactions {
			if c.Purpose != "totp.enroll" {
				live = append(live, c)
			}
		}
		update.State.Transactions = live
		update.State.Transactions = append(update.State.Transactions, service.AuthTransaction{Hash: hash, Binding: binding, Purpose: "totp.enroll", SessionID: sid, Version: u.SessionVersion, Expires: update.Now.Add(5 * time.Minute), Secret: secret.Base32()})
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"enrollment": raw, "secret": secret.Base32(), "otpauth_uri": secret.URL(totp.KeyURIParams{Issuer: "AT", Account: u.Username, Config: totp.Default()}), "expires_in": 300}, 200)
}

func (a *nativeAuth) totpConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enrollment string `json:"enrollment"`
		Code       string `json:"code"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	u, sid, err := a.securitySelf(r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	codes, hashes, err := newBackupCodes()
	if err != nil {
		a.securityError(w, err)
		return
	}
	confirmed := false
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		if !securityAttempt(update) {
			return nil
		}
		c := securityTransaction(update, req.Enrollment, a.securityBinding(r), "totp.enroll")
		if c == nil || c.SessionID != sid || update.State.Secret != "" {
			return nil
		}
		c.Attempts++
		factor := service.AuthSecurityState{Secret: c.Secret, LastStep: -1}
		if !verifySecurityCode(&factor, req.Code, update.Now) {
			return nil
		}
		update.State.Secret = factor.Secret
		update.State.LastStep = factor.LastStep
		update.State.Generation++
		update.State.BackupHashes = hashes
		update.Revoke = true
		confirmed = true
		return nil
	})
	if err == nil && !confirmed {
		err = service.ErrAuthConflict
	}
	if err != nil {
		a.securityError(w, err)
		return
	}
	securityAudit("totp.activate", u.ID, "success")
	a.clearCredentialCookies(w)
	httpResponseJSON(w, map[string]any{"backup_codes": codes, "login_required": true}, 200)
}

func (a *nativeAuth) totpMutate(regenerate bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Proof string `json:"proof"`
			Code  string `json:"code"`
		}
		if !decodeNativeBody(w, r, &req) {
			return
		}
		u, sid, err := a.securitySelf(r)
		if err != nil {
			a.securityError(w, err)
			return
		}
		purpose := "totp.remove"
		if regenerate {
			purpose = "totp.backup-codes"
		}
		codes, hashes, err := newBackupCodes()
		if err != nil {
			a.securityError(w, err)
			return
		}
		changed := false
		err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
			if !securityAttempt(update) {
				return nil
			}
			if !consumeSecurityProof(update, sid, purpose, req.Proof) {
				return nil
			}
			if !verifySecurityCode(&update.State, req.Code, update.Now) {
				return nil
			}
			update.State.Generation++
			update.Revoke = true
			changed = true
			if regenerate {
				update.State.BackupHashes = hashes
			} else {
				update.State.Secret = ""
				update.State.BackupHashes = nil
				update.State.LastStep = 0
			}
			return nil
		})
		if err == nil && !changed {
			err = service.ErrAuthConflict
		}
		if err != nil {
			securityAudit(purpose, u.ID, "rejected")
			a.securityError(w, err)
			return
		}
		securityAudit(purpose, u.ID, "success")
		a.clearCredentialCookies(w)
		if regenerate {
			httpResponseJSON(w, map[string]any{"backup_codes": codes, "login_required": true}, 200)
		} else {
			w.WriteHeader(204)
		}
	}
}
