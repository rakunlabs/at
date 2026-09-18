package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
)

type nativeUser struct {
	ID                  string                     `json:"id"`
	Username            string                     `json:"username"`
	Admin               bool                       `json:"admin"`
	Disabled            bool                       `json:"disabled"`
	PasswordLockedUntil *time.Time                 `json:"password_locked_until,omitempty"`
	LastLogin           *service.AuthLastLogin     `json:"last_login,omitempty"`
	Identities          []service.AuthUserIdentity `json:"identities,omitempty"`
}

// decorate fills the parts of the list row that live outside auth_users: the
// active password lock, the last sign-in, and the external identities that give
// a generated `external-<ulid>` account a name a person recognises.
func (a *nativeAuth) decorateUsers(r *http.Request, users []service.AuthUser) ([]nativeUser, int, string) {
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	var locks map[string]time.Time
	if reader, ok := a.security.(service.AuthLoginLockoutReader); ok {
		var err error
		if locks, err = reader.ListAuthLoginLocks(r.Context(), ids); err != nil {
			return nil, 503, "account security unavailable"
		}
	}
	var lastLogins map[string]service.AuthLastLogin
	if store, ok := a.store.(service.AuthLoginEventStorer); ok {
		var err error
		if lastLogins, err = store.ListAuthLastLogins(r.Context(), ids); err != nil {
			return nil, 503, "login history unavailable"
		}
	}
	var identities map[string][]service.AuthUserIdentity
	if store, ok := a.store.(service.AuthUserDirectory); ok {
		var err error
		if identities, err = store.ListAuthUserIdentities(r.Context(), ids); err != nil {
			return nil, 503, "identity directory unavailable"
		}
	}
	out := make([]nativeUser, 0, len(users))
	for _, u := range users {
		item := nativeUser{ID: u.ID, Username: u.Username, Admin: u.Admin, Disabled: u.Disabled, Identities: identities[u.ID]}
		if until, ok := locks[u.ID]; ok {
			item.PasswordLockedUntil = &until
		}
		if last, ok := lastLogins[u.ID]; ok {
			item.LastLogin = &last
		}
		out = append(out, item)
	}
	return out, 0, ""
}

func (a *nativeAuth) listUsers(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		nativeError(w, 400, "invalid user list query")
		return
	}
	limit := 50
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 100 || len(q.Get("after")) > 128 || len(q.Get("q")) > 128 {
		nativeError(w, 400, "limit must be 1-100; after and q must be at most 128 bytes")
		return
	}
	for key, values := range q {
		if (key != "limit" && key != "after" && key != "q") || len(values) != 1 {
			nativeError(w, 400, "invalid user list query")
			return
		}
	}
	// One row over the page decides whether a cursor is reported, so an exact
	// page boundary does not advertise a next page that is empty.
	users, err := a.store.ListAuthUsers(r.Context(), service.AuthUserQuery{After: q.Get("after"), Search: strings.TrimSpace(q.Get("q")), Limit: uint(limit + 1)})
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	result := struct {
		Data       []nativeUser `json:"data"`
		NextCursor string       `json:"next_cursor"`
	}{Data: []nativeUser{}}
	if len(users) > limit {
		users = users[:limit]
		result.NextCursor = users[len(users)-1].ID
	}
	data, status, message := a.decorateUsers(r, users)
	if status != 0 {
		nativeError(w, status, message)
		return
	}
	result.Data = data
	httpResponseJSON(w, result, 200)
}

// getUser answers the account detail an administrator needs to act on a row:
// who the account is upstream, and which workspaces it actually reaches. The
// list endpoint cannot carry the memberships — that is one query per account.
func (a *nativeAuth) getUser(w http.ResponseWriter, r *http.Request) {
	u, err := a.store.GetAuthUserByID(r.Context(), r.PathValue("id"))
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	if u == nil {
		nativeError(w, 404, "user not found")
		return
	}
	data, status, message := a.decorateUsers(r, []service.AuthUser{*u})
	if status != 0 {
		nativeError(w, status, message)
		return
	}
	result := struct {
		nativeUser
		Workspaces []service.AuthUserWorkspace `json:"workspaces"`
	}{nativeUser: data[0], Workspaces: []service.AuthUserWorkspace{}}
	if store, ok := a.store.(service.AuthUserDirectory); ok {
		workspaces, err := store.ListAuthUserWorkspaces(r.Context(), u.ID)
		if err != nil {
			nativeError(w, 503, "workspace directory unavailable")
			return
		}
		result.Workspaces = workspaces
	}
	httpResponseJSON(w, result, 200)
}

// deleteUser is irreversible and deliberately narrower than disable: an account
// that is still referenced as the only active administrator, or is the caller's
// own, is refused rather than silently locking the installation out of itself.
func (a *nativeAuth) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == identity.FromContext(r.Context()).Subject {
		nativeError(w, 409, "cannot delete your own account")
		return
	}
	found, err := a.store.DeleteAuthUser(r.Context(), id)
	if errors.Is(err, service.ErrAuthConflict) {
		nativeError(w, 409, "cannot delete the last active administrator, or the last administrator external sign-in depends on")
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
	slog.InfoContext(r.Context(), "account deleted", "user_id", id, "actor_id", identity.FromContext(r.Context()).Subject)
	w.WriteHeader(http.StatusNoContent)
}

func (a *nativeAuth) enableUser(w http.ResponseWriter, r *http.Request) {
	found, err := a.store.EnableAuthUser(r.Context(), r.PathValue("id"))
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

func (a *nativeAuth) changePassword(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var current, next, proof string
		if admin {
			var req struct {
				Password string `json:"password"`
			}
			if !decodeNativeBody(w, r, &req) {
				return
			}
			next = req.Password
		} else {
			if !a.loginLimit.Allow() {
				w.Header().Set("Retry-After", "6")
				nativeError(w, 429, "authentication rate limit exceeded")
				return
			}
			var req struct {
				CurrentPassword string `json:"current_password"`
				NewPassword     string `json:"new_password"`
				Proof           string `json:"proof"`
			}
			if !decodeNativeBody(w, r, &req) {
				return
			}
			current, next = req.CurrentPassword, req.NewPassword
			proof = req.Proof
		}
		if !a.passwordSlot(w) {
			return
		}
		defer func() { <-a.passwordSlots }()
		self := identity.FromContext(r.Context()).Subject
		id := r.PathValue("id")
		var version *int64
		if !admin {
			id = self
			u, err := a.store.GetAuthUserByID(r.Context(), id)
			if err != nil {
				nativeError(w, 503, "authentication unavailable")
				return
			}
			if u == nil || u.Disabled || (proof == "" && a.password.Verify(u.PasswordHash, current) != nil) {
				nativeError(w, 401, "invalid credentials")
				return
			}
			version = &u.SessionVersion
		}
		hash, err := a.password.Hash(next)
		if err != nil {
			nativeError(w, 400, nativePasswordMessage(next))
			return
		}
		if !admin && proof != "" {
			u, sid, err := a.securitySelf(r)
			if err == nil && a.security != nil {
				err = a.security.UpdateAuthSecurity(r.Context(), u.ID, sid, *version, func(update *service.AuthSecurityUpdate) error {
					if !consumeSecurityProof(update, sid, "password.change", proof) {
						return service.ErrAuthConflict
					}
					update.PasswordHash = hash
					update.Revoke = true
					return nil
				})
			} else if err == nil {
				err = service.ErrAuthConflict
			}
			if err != nil {
				a.securityError(w, err)
				return
			}
			a.clearCredentialCookies(w, r)
			w.WriteHeader(204)
			return
		}
		found, err := a.store.SetAuthUserPassword(r.Context(), id, hash, version)
		if errors.Is(err, service.ErrAuthConflict) {
			nativeError(w, 409, "account changed; sign in again before retrying")
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
		mobile, _ := r.Context().Value(nativeMobileContextKey{}).(bool)
		if id == self && !mobile {
			a.clearCredentialCookies(w, r)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
