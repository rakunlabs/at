package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
)

type nativeUser struct {
	ID                  string                 `json:"id"`
	Username            string                 `json:"username"`
	Admin               bool                   `json:"admin"`
	Disabled            bool                   `json:"disabled"`
	PasswordLockedUntil *time.Time             `json:"password_locked_until,omitempty"`
	LastLogin           *service.AuthLastLogin `json:"last_login,omitempty"`
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
	if err != nil || limit < 1 || limit > 100 || len(q.Get("after")) > 128 {
		nativeError(w, 400, "limit must be 1-100; after must be at most 128 bytes")
		return
	}
	for key, values := range q {
		if (key != "limit" && key != "after") || len(values) != 1 {
			nativeError(w, 400, "invalid user list query")
			return
		}
	}
	users, err := a.store.ListAuthUsers(r.Context(), q.Get("after"), uint(limit+1))
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return
	}
	result := struct {
		Data       []nativeUser `json:"data"`
		NextCursor string       `json:"next_cursor"`
	}{Data: make([]nativeUser, 0, len(users))}
	if len(users) > limit {
		users = users[:limit]
		result.NextCursor = users[len(users)-1].ID
	}
	var locks map[string]time.Time
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}
	if reader, ok := a.security.(service.AuthLoginLockoutReader); ok {
		locks, err = reader.ListAuthLoginLocks(r.Context(), ids)
		if err != nil {
			nativeError(w, 503, "account security unavailable")
			return
		}
	}
	var lastLogins map[string]service.AuthLastLogin
	if store, ok := a.store.(service.AuthLoginEventStorer); ok {
		lastLogins, err = store.ListAuthLastLogins(r.Context(), ids)
		if err != nil {
			nativeError(w, 503, "login history unavailable")
			return
		}
	}
	for _, u := range users {
		item := nativeUser{ID: u.ID, Username: u.Username, Admin: u.Admin, Disabled: u.Disabled}
		if until, ok := locks[u.ID]; ok {
			item.PasswordLockedUntil = &until
		}
		if last, ok := lastLogins[u.ID]; ok {
			item.LastLogin = &last
		}
		result.Data = append(result.Data, item)
	}
	httpResponseJSON(w, result, 200)
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
