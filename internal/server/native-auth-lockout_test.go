package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestPasswordLoginLockoutPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	users := make(map[string]*service.AuthUser)
	for _, name := range []string{"admin", "reader", "other"} {
		u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: name, PasswordHash: password.Dummy, Admin: name == "admin"}, false)
		if err != nil {
			t.Fatal(err)
		}
		users[name] = u
	}
	newServer := func() (*nativeAuth, *ada.Server) {
		t.Helper()
		a, err := newNativeAuth(nativeTestConfig(), p)
		if err != nil {
			t.Fatal(err)
		}
		a.loginLimit = rate.NewLimiter(rate.Inf, 100)
		mux := ada.New()
		a.register(mux, "/at")
		return a, mux
	}
	a, mux := newServer()
	adminCookie := nativeLoginCookie(t, mux, "admin")
	readerCookie := nativeLoginCookie(t, mux, "reader")
	correct := strings.Repeat("x", 32)
	login := func(t *testing.T, handler http.Handler, name, pw string, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": name, "password": pw})
		w := nativeRequest(handler, "POST", "/at/auth/login", string(body), a.cfg.Origin, nil)
		if w.Code != want {
			t.Fatalf("login %s: got %d, want %d: %s", name, w.Code, want, w.Body)
		}
		if want != 200 && len(w.Result().Cookies()) != 0 {
			t.Fatal("rejected login issued cookies")
		}
		return w
	}
	state := func() service.AuthSecurityState {
		t.Helper()
		var s service.AuthSecurityState
		if err := p.UpdateAuthSecurity(t.Context(), users["reader"].ID, "", -1, func(u *service.AuthSecurityUpdate) error { s = u.State; return nil }); err != nil {
			t.Fatal(err)
		}
		return s
	}
	mutateState := func(fn func(*service.AuthSecurityUpdate)) {
		t.Helper()
		if err := p.UpdateAuthSecurity(t.Context(), users["reader"].ID, "", -1, func(u *service.AuthSecurityUpdate) error { fn(u); return nil }); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("fifth failure locks normalized account and survives new instance", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			login(t, mux, " READER ", "wrong", 401)
		}
		w := login(t, mux, "reader", "wrong", 429)
		if w.Header().Get("Retry-After") != "900" {
			t.Fatalf("fifth failure retry: %s", w.Header().Get("Retry-After"))
		}
		before := state()
		if before.PasswordFailures != 5 || time.Until(before.PasswordLockedUntil) < 14*time.Minute {
			t.Fatalf("lock state: %+v", before)
		}
		_, restarted := newServer()
		login(t, restarted, "reader", correct, 429)
		login(t, restarted, "reader", "wrong", 429)
		if after := state(); after.PasswordFailures != 5 || !after.PasswordLockedUntil.Equal(before.PasswordLockedUntil) {
			t.Fatal("blocked requests extended lock or changed counter")
		}
		login(t, restarted, "other", correct, 200)
		login(t, restarted, "unknown", "wrong", 401)
	})

	t.Run("administrator sees lock and only administrator can clear it", func(t *testing.T) {
		w := nativeRequest(mux, "GET", "/at/auth/users", "", a.cfg.Origin, adminCookie)
		var page struct {
			Data []nativeUser `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("list: %d %s", w.Code, w.Body)
		}
		seen := false
		for _, u := range page.Data {
			if u.ID == users["reader"].ID {
				seen = u.PasswordLockedUntil != nil
			} else if u.PasswordLockedUntil != nil {
				t.Fatal("unlocked account reported locked")
			}
		}
		if !seen || strings.Contains(w.Body.String(), "PasswordHash") || strings.Contains(w.Body.String(), "BackupHashes") {
			t.Fatalf("lock metadata missing or secret exposure: %s", w.Body)
		}
		path := "/at/auth/users/" + users["reader"].ID + "/unlock-login"
		for _, tt := range []struct {
			name   string
			cookie *http.Cookie
			origin string
			want   int
		}{
			{"anonymous", nil, a.cfg.Origin, 401},
			{"nonadmin", readerCookie, a.cfg.Origin, 403},
			{"cross origin", adminCookie, "https://evil.example", 403},
			{"admin", adminCookie, a.cfg.Origin, 204},
		} {
			t.Run(tt.name, func(t *testing.T) {
				w := nativeRequest(mux, "POST", path, "", tt.origin, tt.cookie)
				if w.Code != tt.want {
					t.Fatalf("unlock: %d %s", w.Code, w.Body)
				}
			})
		}
		if s := state(); s.PasswordFailures != 0 || !s.PasswordLockedUntil.IsZero() {
			t.Fatal("unlock did not clear state")
		}
		locks, err := p.ListAuthLoginLocks(t.Context(), []string{users["reader"].ID})
		if err != nil || len(locks) != 0 {
			t.Fatalf("unlocked account still listed: %v %v", locks, err)
		}
		login(t, mux, "reader", correct, 200)
		if w := nativeRequest(mux, "GET", "/at/auth/me", "", a.cfg.Origin, readerCookie); w.Code != 200 {
			t.Fatal("unlock revoked existing session")
		}
	})

	t.Run("successful password resets consecutive failures", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			login(t, mux, "reader", "wrong", 401)
		}
		login(t, mux, "reader", correct, 200)
		if state().PasswordFailures != 0 {
			t.Fatal("success did not reset failures")
		}
		login(t, mux, "reader", "wrong", 401)
		if state().PasswordFailures != 1 {
			t.Fatal("counter not consecutive")
		}
	})

	t.Run("expiry automatically starts fresh budget", func(t *testing.T) {
		mutateState(func(u *service.AuthSecurityUpdate) {
			u.State.PasswordFailures = 5
			u.State.PasswordLockedUntil = u.Now.Add(-time.Second)
		})
		locks, err := p.ListAuthLoginLocks(t.Context(), []string{users["reader"].ID})
		if err != nil || len(locks) != 0 {
			t.Fatalf("expired lock listed: %v %v", locks, err)
		}
		login(t, mux, "reader", "wrong", 401)
		if s := state(); s.PasswordFailures != 1 || !s.PasswordLockedUntil.IsZero() {
			t.Fatal("expiry did not reset")
		}
		login(t, mux, "reader", correct, 200)
	})

	t.Run("concurrent instances cannot overrun threshold", func(t *testing.T) {
		other, _ := newServer()
		var rejected, locked atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				instance := a
				if i%2 == 0 {
					instance = other
				}
				u := *users["reader"]
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/at/auth/login", nil).WithContext(t.Context())
				instance.verifyLoginPassword(w, r, &u, "wrong")
				switch w.Code {
				case 401:
					rejected.Add(1)
				case 429:
					locked.Add(1)
				default:
					t.Errorf("concurrent login: %d %s", w.Code, w.Body)
				}
			}(i)
		}
		wg.Wait()
		if rejected.Load() != 4 || locked.Load() != 8 || state().PasswordFailures != 5 {
			t.Fatalf("concurrent results: rejected=%d locked=%d", rejected.Load(), locked.Load())
		}
	})

	t.Run("persistent activity and last login are administrator only", func(t *testing.T) {
		path := "/at/auth/users/" + users["reader"].ID + "/login-events"
		for _, tt := range []struct {
			cookie *http.Cookie
			want   int
		}{{nil, 401}, {readerCookie, 403}, {adminCookie, 200}} {
			w := nativeRequest(mux, "GET", path, "", a.cfg.Origin, tt.cookie)
			if w.Code != tt.want {
				t.Fatalf("history authorization: %d %s", w.Code, w.Body)
			}
			if tt.want != 200 {
				continue
			}
			var result struct {
				Data []service.AuthLoginEvent `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]bool)
			for _, event := range result.Data {
				seen[event.Action] = true
				if event.UserID != users["reader"].ID {
					t.Fatal("other user's event leaked")
				}
				if event.Action == "login_unlocked" && event.ActorID != users["admin"].ID {
					t.Fatal("unlock actor not recorded")
				}
			}
			for _, action := range []string{"login_success", "login_failed", "login_locked", "login_blocked", "login_unlocked"} {
				if !seen[action] {
					t.Fatalf("missing %s event", action)
				}
			}
		}
		last, err := p.ListAuthLastLogins(t.Context(), []string{users["reader"].ID})
		if err != nil || last[users["reader"].ID].At.IsZero() {
			t.Fatalf("last login: %v %v", last, err)
		}
		// Caller-supplied forwarding headers must not forge the recorded address.
		r := httptest.NewRequest("POST", "/at/auth/login", nil)
		r.RemoteAddr = "198.51.100.7:1234"
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		r.Header.Set("User-Agent", "AuditBrowser/1.0")
		a.recordLoginEvent(r, users["reader"].ID, "login_blocked")
		events, err := p.ListAuthLoginEvents(t.Context(), users["reader"].ID, 1)
		if err != nil || len(events) != 1 || events[0].SourceIP != "198.51.100.7" || events[0].UserAgent != "AuditBrowser/1.0" {
			t.Fatalf("source audit: %+v %v", events, err)
		}
		path = "/at/auth/users/" + users["reader"].ID + "/revoke-sessions"
		if w := nativeRequest(mux, "POST", path, "", a.cfg.Origin, adminCookie); w.Code != 204 {
			t.Fatalf("kick: %d %s", w.Code, w.Body)
		}
		if w := nativeRequest(mux, "GET", "/at/auth/me", "", a.cfg.Origin, readerCookie); w.Code != 401 {
			t.Fatal("kicked session still admitted")
		}
		events, err = p.ListAuthLoginEvents(t.Context(), users["reader"].ID, 1)
		if err != nil || len(events) != 1 || events[0].Action != "sessions_revoked" || events[0].ActorID != users["admin"].ID {
			t.Fatalf("kick audit: %+v %v", events, err)
		}
	})

	t.Run("unlock does not enable a disabled account", func(t *testing.T) {
		if found, err := p.InvalidateAuthUser(t.Context(), users["reader"].ID, true); err != nil || !found {
			t.Fatalf("disable: %v %v", found, err)
		}
		path := "/at/auth/users/" + users["reader"].ID + "/unlock-login"
		w := nativeRequest(mux, "POST", path, "", a.cfg.Origin, adminCookie)
		if w.Code != 204 {
			t.Fatalf("unlock disabled user: %d %s", w.Code, w.Body)
		}
		login(t, mux, "reader", correct, 401)
		u, err := p.GetAuthUser(t.Context(), "reader")
		if err != nil || u == nil || !u.Disabled {
			t.Fatal("unlock enabled disabled user")
		}
	})
}
