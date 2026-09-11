package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// nativeExternalCoordinatorHooks uses the shared primary/recent-auth machinery.
// The parent wires these hooks during native auth route registration. Keeping
// registration separate lets native mode fail closed when storage is incomplete.
func nativeExternalCoordinatorHooks(a *nativeAuth) nativeExternalHooks {
	account := func(r *http.Request) (service.AuthExternalAccount, error) {
		u, sid, err := a.securitySelf(r)
		if err != nil {
			return service.AuthExternalAccount{}, err
		}
		return service.AuthExternalAccount{UserID: u.ID, SessionID: sid, Version: u.SessionVersion}, nil
	}
	return nativeExternalHooks{
		Account: account,
		Recent: func(_ http.ResponseWriter, r *http.Request, purpose, proof string) (service.AuthExternalAccount, error) {
			current, err := account(r)
			if err != nil {
				return current, err
			}
			err = a.consumeRecentAuthProof(r.Context(), current.UserID, current.SessionID, current.Version, purpose, proof)
			return current, err
		},
		Complete: func(w http.ResponseWriter, r *http.Request, u *service.AuthUser, c service.AuthExternalCompletion) {
			if a.security == nil {
				nativeError(w, 503, "external authentication requires common MFA completion")
				return
			}
			ctx := service.ContextWithAuthExternalCompletion(r.Context(), c)
			a.externalContinuationHeader(w, c)
			ctx = context.WithValue(ctx, nativeCeremonyDeadlineContextKey{}, c.Deadline)
			a.finishPrimaryLogin(w, r.WithContext(ctx), u, c.Remember, nativeExternalMethod(c))
		},
		Reauthenticate: func(w http.ResponseWriter, r *http.Request, u *service.AuthUser, c service.AuthExternalCompletion) {
			a.finishExternalRecentPrimary(w, r, u, c)
		},
	}
}

// The only continuation is an existing server-side mobile authorization request;
// arbitrary redirect URLs are never accepted. Restore after common MFA too.
func (a *nativeAuth) externalContinuationHeader(w http.ResponseWriter, c service.AuthExternalCompletion) {
	if validMobileSecret(c.MobileRequestID) {
		w.Header().Set("X-AT-Auth-Continue", a.session.Cookie.Path+"#/mobile-authorize?request_id="+url.QueryEscape(c.MobileRequestID))
	}
}

// Method is durable across common MFA without copying upstream credentials or
// claims. Restore this context before calling createCompletedSession on MFA finish.
func nativeExternalMethod(c service.AuthExternalCompletion) string {
	b, _ := json.Marshal(c)
	return "external:" + base64.RawURLEncoding.EncodeToString(b)
}

func nativeExternalCompletionFromMethod(method string) (service.AuthExternalCompletion, error) {
	var c service.AuthExternalCompletion
	encoded, ok := strings.CutPrefix(method, "external:")
	if !ok || len(encoded) > 4096 {
		return c, service.ErrAuthConflict
	}
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return c, fmt.Errorf("decode external provenance: %w", err)
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("decode external completion: %w", err)
	}
	if c.ProviderID == "" || c.ProviderVersion < 1 || c.LinkID == "" || c.Deadline.IsZero() {
		return c, service.ErrAuthConflict
	}
	return c, nil
}

func (a *nativeAuth) finishExternalRecentPrimary(w http.ResponseWriter, r *http.Request, u *service.AuthUser, c service.AuthExternalCompletion) {
	if a.security == nil || !validRecentPurpose(c.ReauthPurpose) || c.Account.UserID != u.ID || c.Account.Version != u.SessionVersion {
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
	pending := false
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, c.Account.SessionID, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		update.Deadline = c.Deadline
		if !securityAttempt(update) || len(update.State.Transactions) >= 20 {
			return service.ErrAuthConflict
		}
		if update.State.Secret == "" {
			return nil
		}
		expires := update.Now.Add(5 * time.Minute)
		if c.Deadline.Before(expires) {
			expires = c.Deadline
		}
		update.State.Transactions = append(update.State.Transactions, service.AuthTransaction{Hash: hash, Binding: binding, Purpose: "external-reauth:" + c.ReauthPurpose, Method: nativeExternalMethod(c), SessionID: c.Account.SessionID, Version: u.SessionVersion, Generation: update.State.Generation, Expires: expires})
		pending = true
		return nil
	})
	if err != nil {
		a.securityError(w, err)
		return
	}
	if pending {
		httpResponseJSON(w, map[string]any{"mfa_required": true, "challenge": raw, "next": "external_reauth", "expires_in": 300}, 200)
		return
	}
	ctx := context.WithValue(r.Context(), nativeCeremonyDeadlineContextKey{}, c.Deadline)
	a.issueRecentAuthProof(w, r.WithContext(ctx), u, c.Account.SessionID, c.ReauthPurpose)
}

// Registered as a self route by the external adapter. External recent auth never
// issues a new normal session and cannot switch its account during local MFA.
func (e *nativeExternalAuth) finishReauth(w http.ResponseWriter, r *http.Request) {
	a := e.a
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if a.security == nil {
		nativeError(w, 503, "account security unavailable")
		return
	}
	u, sid, err := a.securitySelf(r)
	if err != nil {
		a.securityError(w, err)
		return
	}
	var completion service.AuthExternalCompletion
	verified := false
	err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, u.SessionVersion, func(update *service.AuthSecurityUpdate) error {
		if !securityAttempt(update) {
			return nil
		}
		for i := range update.State.Transactions {
			c := &update.State.Transactions[i]
			if c.Hash != nativeSessionHash(req.Challenge) || c.Binding != a.securityBinding(r) || c.SessionID != sid || c.Version != u.SessionVersion || c.Generation != update.State.Generation || !strings.HasPrefix(c.Purpose, "external-reauth:") || c.Attempts >= 5 || !c.Expires.After(update.Now) {
				continue
			}
			var err error
			completion, err = nativeExternalCompletionFromMethod(c.Method)
			if err != nil {
				return err
			}
			if completion.Account.UserID != u.ID || completion.Account.SessionID != sid || completion.Account.Version != u.SessionVersion {
				return service.ErrAuthConflict
			}
			update.Deadline = c.Expires
			c.Attempts++
			if verifySecurityCode(&update.State, req.Code, update.Now) {
				c.Expires = update.Now
				verified = true
			}
			return nil
		}
		return nil
	})
	if err == nil && !verified {
		err = service.ErrAuthConflict
	}
	if err != nil {
		a.securityError(w, err)
		return
	}
	ctx := context.WithValue(r.Context(), nativeCeremonyDeadlineContextKey{}, completion.Deadline)
	a.issueRecentAuthProof(w, r.WithContext(ctx), u, sid, completion.ReauthPurpose)
}
