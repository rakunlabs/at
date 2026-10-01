package nativeauth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativeRefreshExpiredAccessLogout(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	a.Register(mux, "/at")
	for _, logout := range []bool{false, true} {
		access, refresh, err := CredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		s := service.AuthSession{Hash: SessionHash(access + refresh), AccessHash: SessionHash(access), RefreshHash: SessionHash(refresh), UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Minute), AccessExpiresAt: time.Now().Add(-time.Minute)}
		if err := p.CreateAuthSession(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		c := &http.Cookie{Name: a.RefreshCookieName(nil), Value: refresh}
		if logout {
			if w := nativeauthtest.Request(mux, "POST", "/at/auth/logout", "", a.cfg.Origin, c); w.Code != 204 || len(w.Result().Cookies()) != 2 {
				t.Fatal("expired-access logout", w.Code)
			}
			if w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, c); w.Code != 401 {
				t.Fatal("logout left refresh", w.Code)
			}
		} else {
			w := nativeauthtest.Request(mux, "POST", "/at/auth/refresh", "{}", a.cfg.Origin, c)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			next := w.Result().Cookies()
			for _, cookie := range next {
				if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
					t.Fatal("nonremember persisted")
				}
			}
			_, live, err := p.ResolveAuthAccess(t.Context(), SessionHash(next[0].Value))
			if err != nil || live == nil || !live.AccessExpiresAt.Equal(live.ExpiresAt) {
				t.Fatal("access not capped to absolute", err)
			}
		}
	}
}

type blockingAuthCleanup struct {
	service.AuthCredentialStorer
	started chan struct{}
}

func (s *blockingAuthCleanup) CleanupAuthCredentials(ctx context.Context, limit uint) (int64, error) {
	close(s.started)
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestNativeRefreshJanitorStops(t *testing.T) {
	store := &blockingAuthCleanup{started: make(chan struct{})}
	a := &Auth{credentials: store}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { a.RunJanitor(ctx); close(done) }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("no initial sweep")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("janitor leaked")
	}
}
