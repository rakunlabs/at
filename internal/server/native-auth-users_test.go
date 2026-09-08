package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func (f *fakeAuthStore) GetAuthUserByID(_ context.Context, id string) (*service.AuthUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("database unavailable")
	}
	for _, u := range f.users {
		if u.ID == id {
			return &u, nil
		}
	}
	return nil, nil
}

func (f *fakeAuthStore) ListAuthUsers(_ context.Context, after string, limit uint) ([]service.AuthUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("database unavailable")
	}
	var users []service.AuthUser
	for _, u := range f.users {
		if u.ID > after {
			users = append(users, u)
		}
	}
	slices.SortFunc(users, func(a, b service.AuthUser) int { return strings.Compare(a.ID, b.ID) })
	if len(users) > int(limit) {
		users = users[:limit]
	}
	return users, nil
}

func (f *fakeAuthStore) EnableAuthUser(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, u := range f.users {
		if u.ID == id {
			u.Disabled = false
			u.SessionVersion++
			f.users[name] = u
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeAuthStore) SetAuthUserPassword(_ context.Context, id, hash string, version *int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, u := range f.users {
		if u.ID == id {
			if version != nil && (u.SessionVersion != *version || u.Disabled) {
				return false, service.ErrAuthConflict
			}
			u.PasswordHash = hash
			u.SessionVersion++
			f.users[name] = u
			for hash, s := range f.sessions {
				if s.UserID == id {
					delete(f.sessions, hash)
				}
			}
			return true, nil
		}
	}
	if version != nil {
		return false, service.ErrAuthConflict
	}
	return false, nil
}

func TestNativeAuthUserManagementBoundary(t *testing.T) {
	_, _, mux := nativeFixture(t)
	reader := nativeLoginCookie(t, mux, "reader")
	admin := nativeLoginCookie(t, mux, "admin")
	for _, route := range []struct{ method, path string }{
		{"GET", "/at/auth/users"},
		{"POST", "/at/auth/users/reader/enable"},
		{"POST", "/at/auth/users/reader/password"},
		{"POST", "/at/auth/users/reader/disable"},
	} {
		for _, c := range []*http.Cookie{nil, reader} {
			want := 401
			if c != nil {
				want = 403
			}
			w := nativeRequest(mux, route.method, route.path, `{}`, "https://at.example", c)
			if w.Code != want {
				t.Fatalf("%s: got %d want %d", route.path, w.Code, want)
			}
		}
		if route.method == "POST" {
			for _, origin := range []string{"", "https://evil.example", "null"} {
				if w := nativeRequest(mux, route.method, route.path, `{}`, origin, admin); w.Code != 403 {
					t.Fatalf("CSRF %s %q: %d", route.path, origin, w.Code)
				}
			}
		}
	}
	if w := nativeRequest(mux, "POST", "/at/auth/password", `{}`, "", reader); w.Code != 403 {
		t.Fatalf("self change CSRF: %d", w.Code)
	}
	for _, suffix := range []string{"enable", "password"} {
		if w := nativeRequest(mux, "POST", "/at/auth/users/missing/"+suffix, `{"password":"a sufficiently long password"}`, "https://at.example", admin); w.Code != 404 {
			t.Fatalf("missing %s: %d %s", suffix, w.Code, w.Body)
		}
	}
}

func TestNativeAuthUserListing(t *testing.T) {
	_, _, mux := nativeFixture(t)
	admin := nativeLoginCookie(t, mux, "admin")
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=x", "limit=1&limit=2", "unknown=x", "after=" + strings.Repeat("x", 129), "after=%zz"} {
		if w := nativeRequest(mux, "GET", "/at/auth/users?"+query, "", "", admin); w.Code != 400 {
			t.Fatalf("query %s: %d", query, w.Code)
		}
	}
	for _, tt := range []struct{ query, id, cursor string }{
		{"limit=1", "admin", "admin"},
		{"limit=1&after=admin", "reader", ""},
		{"after=reader", "", ""},
	} {
		w := nativeRequest(mux, "GET", "/at/auth/users?"+tt.query, "", "", admin)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("listing: %d %s", w.Code, w.Body)
		}
		var result struct {
			Data       []map[string]any `json:"data"`
			NextCursor string           `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.NextCursor != tt.cursor || result.Data == nil {
			t.Fatalf("result: %s", w.Body)
		}
		if tt.id == "" {
			if len(result.Data) != 0 {
				t.Fatalf("expected empty page: %s", w.Body)
			}
		} else if len(result.Data) != 1 || result.Data[0]["id"] != tt.id || len(result.Data[0]) != 4 {
			t.Fatalf("incorrect or leaking DTO: %s", w.Body)
		}
	}
}

func TestNativeAuthPasswordChangeRevocation(t *testing.T) {
	a, f, mux := nativeFixture(t)
	reader := nativeLoginCookie(t, mux, "reader")
	second := nativeLoginCookie(t, mux, "reader")
	newPassword := "a new sufficiently long password"
	request := func(current, next string) int {
		body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		w := nativeRequest(mux, "POST", "/at/auth/password", string(body), a.cfg.Origin, reader)
		if strings.Contains(w.Body.String(), next) && next != "" {
			t.Fatal("password response leak")
		}
		if w.Code == 204 {
			cookies := w.Result().Cookies()
			if w.Body.Len() != 0 || len(cookies) != 2 || cookies[0].MaxAge != -1 || cookies[0].Value != "" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/at/" {
				t.Fatalf("invalid signout response: %+v %s", cookies, w.Body)
			}
		}
		return w.Code
	}
	if code := request("wrong", newPassword); code != 401 {
		t.Fatalf("wrong current password: %d", code)
	}
	if code := request(strings.Repeat("x", 32), "short"); code != 400 {
		t.Fatalf("weak new password: %d", code)
	}
	if len(f.sessions) != 2 || f.users["reader"].SessionVersion != 0 {
		t.Fatal("failed attempt mutated account")
	}
	if code := request(strings.Repeat("x", 32), newPassword); code != 204 {
		t.Fatalf("self change: %d", code)
	}
	if len(f.sessions) != 0 || f.users["reader"].SessionVersion != 1 || f.users["reader"].Admin || f.users["reader"].Disabled {
		t.Fatal("incorrect account/session mutation")
	}
	for _, c := range []*http.Cookie{reader, second} {
		if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
			t.Fatalf("revoked session admitted: %d", w.Code)
		}
		if pair, err := a.Refresh(t.Context(), c.Value, ""); pair != nil || !errors.Is(err, issuer.ErrRefreshExpired) {
			t.Fatalf("revoked session refreshed: %+v %v", pair, err)
		}
	}
	for _, tt := range []struct {
		password string
		code     int
	}{{strings.Repeat("x", 32), 401}, {newPassword, 200}} {
		body, _ := json.Marshal(map[string]string{"username": "reader", "password": tt.password})
		w := nativeRequest(mux, "POST", "/at/auth/login", string(body), a.cfg.Origin, nil)
		if w.Code != tt.code {
			t.Fatalf("login after change: %d want %d", w.Code, tt.code)
		}
	}
}

func TestNativeAuthAdminPasswordResetAndEnable(t *testing.T) {
	a, f, mux := nativeFixture(t)
	admin := nativeLoginCookie(t, mux, "admin")
	reader := nativeLoginCookie(t, mux, "reader")
	if w := nativeRequest(mux, "POST", "/at/auth/users/reader/password", `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin); w.Code != 204 || len(w.Result().Cookies()) != 0 || w.Body.Len() != 0 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("reset did not revoke")
	}
	for _, action := range []string{"disable", "password", "enable"} {
		w := nativeRequest(mux, "POST", "/at/auth/users/reader/"+action, `{"password":"another sufficiently long password"}`, a.cfg.Origin, admin)
		if w.Code != 204 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
		if f.users["reader"].Disabled != (action != "enable") {
			t.Fatalf("%s changed disabled incorrectly", action)
		}
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("enable resurrected old session")
	}
	if w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"another sufficiently long password"}`, a.cfg.Origin, nil); w.Code != 200 {
		t.Fatalf("enabled login: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/users/admin/password", `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin); w.Code != 204 || len(w.Result().Cookies()) != 2 {
		t.Fatalf("admin self reset: %d %s", w.Code, w.Body)
	}
	if !f.users["admin"].Admin || f.users["admin"].Disabled {
		t.Fatal("last administrator lost during password reset")
	}
}

type conflictingPasswordStore struct{ service.AuthStorer }

func (f conflictingPasswordStore) SetAuthUserPassword(ctx context.Context, id, hash string, version *int64) (bool, error) {
	if _, err := f.AuthStorer.InvalidateAuthUser(ctx, id, false); err != nil {
		return false, err
	}
	return f.AuthStorer.SetAuthUserPassword(ctx, id, hash, version)
}

func TestNativeAuthSelfPasswordChangeConflict(t *testing.T) {
	a, f, mux := nativeFixture(t)
	reader := nativeLoginCookie(t, mux, "reader")
	oldHash := f.users["reader"].PasswordHash
	a.store = conflictingPasswordStore{a.store}
	w := nativeRequest(mux, "POST", "/at/auth/password", `{"current_password":"`+strings.Repeat("x", 32)+`","new_password":"a sufficiently long password"}`, a.cfg.Origin, reader)
	if w.Code != 409 || len(w.Result().Cookies()) != 0 || f.users["reader"].PasswordHash != oldHash {
		t.Fatalf("concurrent revoke lost: %d %s", w.Code, w.Body)
	}
}

func TestNativeAuthUserLifecyclePostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	adminUser, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy}, true)
	if err != nil {
		t.Fatal(err)
	}
	readerUser, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	a.register(mux, "/at")
	admin := nativeLoginCookie(t, mux, "admin")
	reader := nativeLoginCookie(t, mux, "reader")
	second := nativeLoginCookie(t, mux, "reader")
	w := nativeRequest(mux, "GET", "/at/auth/users", "", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "session_version") || !strings.Contains(w.Body.String(), readerUser.ID) {
		t.Fatalf("postgres listing: %d %s", w.Code, w.Body)
	}
	for _, suffix := range []string{"disable", "password", "enable"} {
		w := nativeRequest(mux, "POST", "/at/auth/users/"+readerUser.ID+"/"+suffix, `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin)
		if w.Code != 204 {
			t.Fatalf("postgres %s: %d %s", suffix, w.Code, w.Body)
		}
		for _, c := range []*http.Cookie{reader, second} {
			if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
				t.Fatalf("postgres %s retained session: %d", suffix, w.Code)
			}
		}
	}
	w = nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"a new sufficiently long password"}`, a.cfg.Origin, nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 2 {
		t.Fatalf("postgres reset login: %d %s", w.Code, w.Body)
	}
	reader = w.Result().Cookies()[0]
	w = nativeRequest(mux, "POST", "/at/auth/password", `{"current_password":"a new sufficiently long password","new_password":"another sufficiently long password"}`, a.cfg.Origin, reader)
	if w.Code != 204 || len(w.Result().Cookies()) != 2 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("postgres self change: %d %s", w.Code, w.Body)
	}
	if _, err := a.Resolve(t.Context(), reader.Value); !errors.Is(err, issuer.ErrNotFound) {
		t.Fatalf("postgres changed session resolved: %v", err)
	}
	if _, err := a.Refresh(t.Context(), reader.Value, ""); !errors.Is(err, issuer.ErrRefreshExpired) {
		t.Fatalf("postgres changed session refreshed: %v", err)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/users/"+adminUser.ID+"/disable", "", a.cfg.Origin, admin); w.Code != 409 {
		t.Fatalf("last admin self-disable: %d", w.Code)
	}
}
