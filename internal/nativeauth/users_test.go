package nativeauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/issuer"

	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeAuthUserManagementBoundary(t *testing.T) {
	_, _, mux := nativeFixture(t)
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
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
			w := nativeauthtest.Request(mux, route.method, route.path, `{}`, "https://at.example", c)
			if w.Code != want {
				t.Fatalf("%s: got %d want %d", route.path, w.Code, want)
			}
		}
		if route.method == "POST" {
			for _, origin := range []string{"", "https://evil.example", "null"} {
				if w := nativeauthtest.Request(mux, route.method, route.path, `{}`, origin, admin); w.Code != 403 {
					t.Fatalf("CSRF %s %q: %d", route.path, origin, w.Code)
				}
			}
		}
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/password", `{}`, "", reader); w.Code != 403 {
		t.Fatalf("self change CSRF: %d", w.Code)
	}
	for _, suffix := range []string{"enable", "password"} {
		if w := nativeauthtest.Request(mux, "POST", "/at/auth/users/missing/"+suffix, `{"password":"a sufficiently long password"}`, "https://at.example", admin); w.Code != 404 {
			t.Fatalf("missing %s: %d %s", suffix, w.Code, w.Body)
		}
	}
}

func TestNativeAuthUserListing(t *testing.T) {
	_, _, mux := nativeFixture(t)
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=x", "limit=1&limit=2", "unknown=x", "after=" + strings.Repeat("x", 129), "q=" + strings.Repeat("x", 129), "after=%zz"} {
		if w := nativeauthtest.Request(mux, "GET", "/at/auth/users?"+query, "", "", admin); w.Code != 400 {
			t.Fatalf("query %s: %d", query, w.Code)
		}
	}
	for _, tt := range []struct{ query, id, cursor string }{
		{"limit=1", "admin", "admin"},
		{"limit=1&after=admin", "reader", ""},
		{"after=reader", "", ""},
	} {
		w := nativeauthtest.Request(mux, "GET", "/at/auth/users?"+tt.query, "", "", admin)
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
	// Search is a store predicate, not a filter over the current page, so the
	// cursor and the page bound still apply to the matching set.
	for _, tt := range []struct {
		query string
		ids   []string
	}{{"q=read", []string{"reader"}}, {"q=READ", []string{"reader"}}, {"q=", []string{"admin", "reader"}}, {"q=nobody", nil}} {
		w := nativeauthtest.Request(mux, "GET", "/at/auth/users?"+tt.query, "", "", admin)
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Data) != len(tt.ids) {
			t.Fatalf("search %q: %d %s", tt.query, w.Code, w.Body)
		}
		for i, id := range tt.ids {
			if result.Data[i].ID != id {
				t.Fatalf("search %q: %s", tt.query, w.Body)
			}
		}
	}
}

// Deletion is irreversible, so its refusals matter more than its success path.
func TestNativeAuthUserDetailAndDeletion(t *testing.T) {
	_, _, mux := nativeFixture(t)
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	w := nativeauthtest.Request(mux, "GET", "/at/auth/users/reader", "", "", admin)
	var detail struct {
		ID         string           `json:"id"`
		Username   string           `json:"username"`
		Workspaces []map[string]any `json:"workspaces"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.ID != "reader" || detail.Workspaces == nil {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/users/missing", "", "", admin); w.Code != 404 {
		t.Fatalf("missing detail: %d", w.Code)
	}
	for _, origin := range []string{"", "https://evil.example"} {
		if w := nativeauthtest.Request(mux, "DELETE", "/at/auth/users/reader", "", origin, admin); w.Code != 403 {
			t.Fatalf("CSRF delete %q: %d", origin, w.Code)
		}
	}
	if w := nativeauthtest.Request(mux, "DELETE", "/at/auth/users/reader", "", "https://at.example", nativeauthtest.LoginCookie(t, mux, "reader")); w.Code != 403 {
		t.Fatalf("non-administrator delete: %d", w.Code)
	}
	// Removing yourself, or the only account that can still administer the
	// installation, is refused rather than performed.
	if w := nativeauthtest.Request(mux, "DELETE", "/at/auth/users/admin", "", "https://at.example", admin); w.Code != 409 {
		t.Fatalf("self delete: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "DELETE", "/at/auth/users/missing", "", "https://at.example", admin); w.Code != 404 {
		t.Fatalf("missing delete: %d", w.Code)
	}
	if w := nativeauthtest.Request(mux, "DELETE", "/at/auth/users/reader", "", "https://at.example", admin); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/users/reader", "", "", admin); w.Code != 404 {
		t.Fatalf("deleted account still resolves: %d", w.Code)
	}
}

func TestNativeAuthPasswordChangeRevocation(t *testing.T) {
	a, f, mux := nativeFixture(t)
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	second := nativeauthtest.LoginCookie(t, mux, "reader")
	newPassword := "a new sufficiently long password"
	request := func(current, next string) int {
		body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
		w := nativeauthtest.Request(mux, "POST", "/at/auth/password", string(body), a.cfg.Origin, reader)
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
	if len(f.Sessions) != 2 || f.Users["reader"].SessionVersion != 0 {
		t.Fatal("failed attempt mutated account")
	}
	if code := request(strings.Repeat("x", 32), newPassword); code != 204 {
		t.Fatalf("self change: %d", code)
	}
	if len(f.Sessions) != 0 || f.Users["reader"].SessionVersion != 1 || f.Users["reader"].Admin || f.Users["reader"].Disabled {
		t.Fatal("incorrect account/session mutation")
	}
	for _, c := range []*http.Cookie{reader, second} {
		if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
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
		w := nativeauthtest.Request(mux, "POST", "/at/auth/login", string(body), a.cfg.Origin, nil)
		if w.Code != tt.code {
			t.Fatalf("login after change: %d want %d", w.Code, tt.code)
		}
	}
}

func TestNativeAuthAdminPasswordResetAndEnable(t *testing.T) {
	a, f, mux := nativeFixture(t)
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/users/reader/password", `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin); w.Code != 204 || len(w.Result().Cookies()) != 0 || w.Body.Len() != 0 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("reset did not revoke")
	}
	for _, action := range []string{"disable", "password", "enable"} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/users/reader/"+action, `{"password":"another sufficiently long password"}`, a.cfg.Origin, admin)
		if w.Code != 204 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
		if f.Users["reader"].Disabled != (action != "enable") {
			t.Fatalf("%s changed disabled incorrectly", action)
		}
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", reader); w.Code != 401 {
		t.Fatal("enable resurrected old session")
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"another sufficiently long password"}`, a.cfg.Origin, nil); w.Code != 200 {
		t.Fatalf("enabled login: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/users/admin/password", `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin); w.Code != 204 || len(w.Result().Cookies()) != 2 {
		t.Fatalf("admin self reset: %d %s", w.Code, w.Body)
	}
	if !f.Users["admin"].Admin || f.Users["admin"].Disabled {
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
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	oldHash := f.Users["reader"].PasswordHash
	a.store = conflictingPasswordStore{a.store}
	w := nativeauthtest.Request(mux, "POST", "/at/auth/password", `{"current_password":"`+strings.Repeat("x", 32)+`","new_password":"a sufficiently long password"}`, a.cfg.Origin, reader)
	if w.Code != 409 || len(w.Result().Cookies()) != 0 || f.Users["reader"].PasswordHash != oldHash {
		t.Fatalf("concurrent revoke lost: %d %s", w.Code, w.Body)
	}
}

func TestNativeAuthUserLifecyclePostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	adminUser, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: nativeauthtest.PasswordHash}, true)
	if err != nil {
		t.Fatal(err)
	}
	readerUser, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	a.Register(mux, "/at")
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	second := nativeauthtest.LoginCookie(t, mux, "reader")
	w := nativeauthtest.Request(mux, "GET", "/at/auth/users", "", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "session_version") || !strings.Contains(w.Body.String(), readerUser.ID) {
		t.Fatalf("postgres listing: %d %s", w.Code, w.Body)
	}
	for _, suffix := range []string{"disable", "password", "enable"} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/users/"+readerUser.ID+"/"+suffix, `{"password":"a new sufficiently long password"}`, a.cfg.Origin, admin)
		if w.Code != 204 {
			t.Fatalf("postgres %s: %d %s", suffix, w.Code, w.Body)
		}
		for _, c := range []*http.Cookie{reader, second} {
			if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", c); w.Code != 401 {
				t.Fatalf("postgres %s retained session: %d", suffix, w.Code)
			}
		}
	}
	w = nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"a new sufficiently long password"}`, a.cfg.Origin, nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 2 {
		t.Fatalf("postgres reset login: %d %s", w.Code, w.Body)
	}
	reader = w.Result().Cookies()[0]
	w = nativeauthtest.Request(mux, "POST", "/at/auth/password", `{"current_password":"a new sufficiently long password","new_password":"another sufficiently long password"}`, a.cfg.Origin, reader)
	if w.Code != 204 || len(w.Result().Cookies()) != 2 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("postgres self change: %d %s", w.Code, w.Body)
	}
	if _, err := a.Resolve(t.Context(), reader.Value); !errors.Is(err, issuer.ErrNotFound) {
		t.Fatalf("postgres changed session resolved: %v", err)
	}
	if _, err := a.Refresh(t.Context(), reader.Value, ""); !errors.Is(err, issuer.ErrRefreshExpired) {
		t.Fatalf("postgres changed session refreshed: %v", err)
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/users/"+adminUser.ID+"/disable", "", a.cfg.Origin, admin); w.Code != 409 {
		t.Fatalf("last admin self-disable: %d", w.Code)
	}
}
