package postgres

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestNativeAuthBootstrapAtomic(t *testing.T) {
	p := newTestStore(t, nil)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			_, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: fmt.Sprintf("admin%d", i), PasswordHash: "test-hash"}, true)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, service.ErrAuthConflict) {
				t.Errorf("bootstrap: %v", err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("bootstrap winners = %d", winners.Load())
	}
	count, err := p.goqu.From(p.tableAuthUsers).CountContext(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("users=%d err=%v", count, err)
	}
	// Deleting users cannot reopen first-admin registration.
	if _, err := p.goqu.Delete(p.tableAuthUsers).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "later", PasswordHash: "hash"}, true); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("bootstrap reopened: %v", err)
	}
}

func TestNativeAuthBootstrapRollback(t *testing.T) {
	p := newTestStore(t, nil)
	u := service.AuthUser{Username: "existing", PasswordHash: "hash"}
	if _, err := p.CreateAuthUser(t.Context(), u, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateAuthUser(t.Context(), u, true); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal(err)
	}
	u.Username = "first"
	if _, err := p.CreateAuthUser(t.Context(), u, true); err != nil {
		t.Fatalf("failed insert consumed latch: %v", err)
	}
}

func TestNativeAuthPersistentSessionRevocation(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := service.AuthSession{Hash: "hashed-token", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	// A different store facade resolves persisted state, without issuer memory.
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, tableAuthSessions: p.tableAuthSessions}
	resolved, _, err := other.ResolveAuthSession(t.Context(), s.Hash)
	if err != nil || resolved == nil || !resolved.Admin || resolved.PasswordHash != "" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
		t.Fatal(err)
	}
	if resolved, _, err := p.ResolveAuthSession(t.Context(), s.Hash); err != nil || resolved != nil {
		t.Fatalf("revoked: %+v %v", resolved, err)
	}
	if err := p.CreateAuthSession(t.Context(), s); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("stale issuance: %v", err)
	}
	u, err = p.GetAuthUser(t.Context(), u.Username)
	if err != nil {
		t.Fatal(err)
	}
	s.Version = u.SessionVersion
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteAuthSession(t.Context(), s.Hash); err != nil {
		t.Fatal(err)
	}
	if resolved, _, _ := p.ResolveAuthSession(t.Context(), s.Hash); resolved != nil {
		t.Fatal("logout failed")
	}
	s.ExpiresAt = time.Now().Add(-time.Minute)
	if err := p.CreateAuthSession(t.Context(), s); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal(err)
	}
	if resolved, _, _ := p.ResolveAuthSession(t.Context(), s.Hash); resolved != nil {
		t.Fatal("expired accepted")
	}
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, true); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("last admin disabled: %v", err)
	}
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin2", PasswordHash: "hash", Admin: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, true); err != nil {
		t.Fatal(err)
	}
	u, err = p.GetAuthUser(t.Context(), u.Username)
	if err != nil || !u.Disabled {
		t.Fatalf("disable: %+v %v", u, err)
	}
	s.Version = u.SessionVersion
	s.ExpiresAt = time.Now().Add(time.Hour)
	if err := p.CreateAuthSession(t.Context(), s); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("issued for disabled user")
	}
}

func TestNativeAuthConcurrentDisable(t *testing.T) {
	p := newTestStore(t, nil)
	var users []*service.AuthUser
	for i := range 2 {
		u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: fmt.Sprintf("admin%d", i), PasswordHash: "hash", Admin: true}, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Go(func() {
			_, err := p.InvalidateAuthUser(t.Context(), u.ID, true)
			if err != nil && !errors.Is(err, service.ErrAuthConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	count, err := p.goqu.From(p.tableAuthUsers).Where(goqu.Ex{"admin": true, "disabled": false}).CountContext(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("active admins=%d %v", count, err)
	}
}

func TestNativeAuthConcurrentIssueAndRevoke(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: "hash"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		start := make(chan struct{})
		var wg sync.WaitGroup
		s := service.AuthSession{Hash: fmt.Sprintf("hash-%d", i), UserID: u.ID, Version: int64(i), ExpiresAt: time.Now().Add(time.Hour)}
		wg.Go(func() {
			<-start
			if err := p.CreateAuthSession(t.Context(), s); err != nil && !errors.Is(err, service.ErrAuthConflict) {
				t.Error(err)
			}
		})
		wg.Go(func() {
			<-start
			if _, err := p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
				t.Error(err)
			}
		})
		close(start)
		wg.Wait()
		resolved, _, err := p.ResolveAuthSession(t.Context(), s.Hash)
		if err != nil || resolved != nil {
			t.Fatalf("revocation lost to concurrent issuance: %+v %v", resolved, err)
		}
	}
}
