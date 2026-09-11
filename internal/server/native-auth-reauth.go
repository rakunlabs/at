package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"

	"github.com/rakunlabs/at/internal/service"
)

func validRecentPurpose(p string) bool {
	switch p {
	case "totp.enroll", "totp.remove", "totp.backup-codes", "password.change", "passkey.enroll", "passkey.delete", "identity.link", "identity.unlink", "recovery.issue":
		return true
	}
	return false
}

func (a *nativeAuth) reauthBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Purpose string `json:"purpose"`
		Method  string `json:"method"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if !validRecentPurpose(req.Purpose) || (req.Method != "password" && req.Method != "passkey") {
		nativeError(w, 400, "invalid reauthentication method or purpose")
		return
	}
	u, sid, err := a.securitySelf(r)
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
	var options any
	var data []byte
	var passkeyDeadline time.Time
	if req.Method == "passkey" {
		if a.passkey == nil {
			nativeError(w, 400, "passkeys unavailable")
			return
		}
		keys, err := a.keyStore.ListAuthPasskeys(r.Context(), u.ID)
		if err != nil {
			a.securityError(w, err)
			return
		}
		if len(keys) == 0 {
			a.securityError(w, service.ErrAuthConflict)
			return
		}
		ids := make([][]byte, 0, len(keys))
		for _, k := range keys {
			ids = append(ids, k.Credential.ID)
		}
		opts, ceremony, err := a.passkey.BeginLogin(ids)
		if err != nil {
			a.securityError(w, err)
			return
		}
		options = opts
		passkeyDeadline = ceremony.Expires
		data, err = json.Marshal(ceremony)
		if err != nil {
			a.securityError(w, err)
			return
		}
	}
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		if !securityAttempt(update) || len(update.State.Transactions) >= 20 {
			return service.ErrAuthConflict
		}
		expires := update.Now.Add(5 * time.Minute)
		if !passkeyDeadline.IsZero() && passkeyDeadline.Before(expires) {
			expires = passkeyDeadline
		}
		update.Deadline = expires
		update.State.Transactions = append(update.State.Transactions, service.AuthTransaction{Hash: hash, Binding: binding, Purpose: "reauth:" + req.Purpose, Method: req.Method, SessionID: sid, Version: u.SessionVersion, Expires: expires, Data: data})
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	result := map[string]any{"challenge": raw, "expires_in": 300}
	if options != nil {
		result["publicKey"] = options
	}
	httpResponseJSON(w, result, 200)
}

func (a *nativeAuth) reauthFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge  string          `json:"challenge"`
		Password   string          `json:"password"`
		Credential json.RawMessage `json:"credential"`
	}
	// Assertions can exceed the ordinary small credential body limit.
	if !decodeNativeBodyLimit(w, r, &req, 64*1024) {
		return
	}
	u, sid, err := a.securitySelf(r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	if !a.passwordSlot(w) {
		return
	}
	defer func() { <-a.passwordSlots }()
	var purpose string
	var verified bool
	var keyID string
	var oldCount, newCount uint32
	var expires time.Time
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		if !securityAttempt(update) {
			return nil
		}
		for i := range update.State.Transactions {
			c := &update.State.Transactions[i]
			if c.Hash != nativeSessionHash(req.Challenge) || c.Binding != a.securityBinding(r) || c.SessionID != sid || len(c.Purpose) < 7 || c.Purpose[:7] != "reauth:" || c.Attempts >= 5 {
				continue
			}
			purpose = c.Purpose[7:]
			c.Attempts++
			expires = c.Expires
			update.Deadline = c.Expires
			if c.Method == "password" {
				verified = a.password.Verify(update.User.PasswordHash, req.Password) == nil
			} else if c.Method == "passkey" && a.passkey != nil {
				var ceremony passkey.SessionData
				if json.Unmarshal(c.Data, &ceremony) != nil {
					return service.ErrAuthConflict
				}
				var assertion passkey.AssertionResponseJSON
				if json.Unmarshal(req.Credential, &assertion) != nil || assertion.ID != assertion.RawID {
					return nil
				}
				rawID, err := base64.RawURLEncoding.DecodeString(assertion.RawID)
				if err != nil {
					return nil
				}
				keys, err := a.keyStore.ListAuthPasskeys(r.Context(), u.ID)
				if err != nil {
					return err
				}
				for _, key := range keys {
					if bytes.Equal(rawID, key.Credential.ID) {
						result, err := a.passkey.FinishLogin(&ceremony, &key.Credential, req.Credential)
						if err == nil {
							verified = true
							keyID = key.ID
							oldCount = key.Credential.SignCount
							newCount = result.NewSignCount
						}
						break
					}
				}
			}
			if verified {
				c.Expires = update.Now
			}
			return nil
		}
		return nil
	})
	if err == nil && !verified {
		err = service.ErrAuthConflict
	}
	if err == nil && keyID != "" {
		err = a.keyStore.AdvanceAuthPasskey(r.Context(), u.ID, keyID, u.SessionVersion, oldCount, newCount, expires)
	}
	if err != nil {
		securityAudit("reauth", u.ID, "rejected")
		a.securityError(w, err)
		return
	}
	a.issueRecentAuthProof(w, r.WithContext(context.WithValue(r.Context(), nativeCeremonyDeadlineContextKey{}, expires)), u, sid, purpose)
}

// The caller must have freshly verified a primary method for this exact account
// and session; this helper rechecks both under the DB lock before publishing.
func (a *nativeAuth) issueRecentAuthProof(w http.ResponseWriter, r *http.Request, u *service.AuthUser, sid, purpose string) {
	w.Header().Set("Cache-Control", "no-store")
	if !validRecentPurpose(purpose) || a.security == nil {
		a.securityError(w, service.ErrAuthConflict)
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
		update.Deadline, _ = r.Context().Value(nativeCeremonyDeadlineContextKey{}).(time.Time)
		if len(update.State.Transactions) >= 20 {
			return service.ErrAuthConflict
		}
		update.State.Transactions = append(update.State.Transactions, service.AuthTransaction{Hash: hash, Binding: binding, Purpose: "proof:" + purpose, SessionID: sid, Version: u.SessionVersion, Expires: update.Now.Add(5 * time.Minute)})
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	securityAudit("reauth", u.ID, "success")
	httpResponseJSON(w, map[string]any{"proof": raw, "expires_in": 300}, 200)
}

func consumeSecurityProof(u *service.AuthSecurityUpdate, sid, purpose, proof string) bool {
	for i := range u.State.Transactions {
		c := &u.State.Transactions[i]
		if c.Hash == nativeSessionHash(proof) && c.Purpose == "proof:"+purpose && c.SessionID == sid && c.Version == u.User.SessionVersion && c.Expires.After(u.Now) {
			if u.Deadline.IsZero() || c.Expires.Before(u.Deadline) {
				u.Deadline = c.Expires
			}
			c.Expires = u.Now
			return true
		}
	}
	return false
}

func (a *nativeAuth) consumeRecentAuthProof(ctx context.Context, userID, sid string, version int64, purpose, proof string) error {
	if a.security == nil || !validRecentPurpose(purpose) {
		return service.ErrAuthConflict
	}
	return a.security.UpdateAuthSecurity(ctx, userID, sid, version, func(u *service.AuthSecurityUpdate) error {
		if !consumeSecurityProof(u, sid, purpose, proof) {
			return service.ErrAuthConflict
		}
		return nil
	})
}
