package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/muz"

	"github.com/rakunlabs/at/internal/service"
)

func refreshFixture(t *testing.T) (*Postgres, *service.AuthUser, service.AuthSession) {
	t.Helper()
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "refresh", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := service.AuthSession{Hash: "family", AccessHash: "access", RefreshHash: "refresh", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond), AccessExpiresAt: time.Now().UTC().Add(10 * time.Minute), Remember: true}
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return p, u, s
}

func TestAuthRefreshRaceAndPersistence(t *testing.T) {
	p, _, s := refreshFixture(t)
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, tableAuthSessions: p.tableAuthSessions, tableAuthCredentials: p.tableAuthCredentials}
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Go(func() {
			<-start
			u, next, err := other.RotateAuthRefresh(t.Context(), s.RefreshHash, fmt.Sprintf("a%d", i), fmt.Sprintf("r%d", i))
			if err != nil {
				t.Error(err)
			}
			if u != nil {
				winners.Add(1)
				if next.Hash != s.Hash || !next.ExpiresAt.Equal(s.ExpiresAt) || !next.Remember {
					t.Error("rotation moved family")
				}
			}
		})
	}
	close(start)
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners=%d", winners.Load())
	}
	for _, hash := range []string{"access", "a0", "a1"} {
		u, _, err := p.ResolveAuthAccess(t.Context(), hash)
		if err != nil || u != nil {
			t.Fatalf("replay left live access %s: %v", hash, err)
		}
	}
	if n, err := p.goqu.From(p.tableAuthCredentials).CountContext(t.Context()); err != nil || n != 0 {
		t.Fatalf("revoked family retained credentials: %d %v", n, err)
	}
}

func TestAuthRefreshRateGuardAndSessionCap(t *testing.T) {
	p, _, s := refreshFixture(t)
	_, next, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, "a2", "r2")
	if err != nil || next == nil {
		t.Fatal(err)
	}
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, tableAuthSessions: p.tableAuthSessions, tableAuthCredentials: p.tableAuthCredentials}
	for range 5 {
		_, _, err := other.RotateAuthRefresh(t.Context(), "r2", "early-a", "early-r")
		var early *service.AuthRefreshEarlyError
		if !errors.As(err, &early) || early.RetryAfter <= 0 || early.RetryAfter > 5*time.Minute {
			t.Fatal("missing persistent guard", err)
		}
	}
	if n, err := p.goqu.From(p.tableAuthCredentials).CountContext(t.Context()); err != nil || n != 3 {
		t.Fatal("early attempts grew storage", n, err)
	}
	// Advancing the stored guard models passage of time, without a five-minute sleep.
	if _, err := p.goqu.Update(p.tableAuthSessions).Set(goqu.Record{"refresh_after": time.Unix(0, 0)}).Where(goqu.Ex{"hash": s.Hash}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if u, _, err := other.RotateAuthRefresh(t.Context(), "r2", "a3", "r3"); err != nil || u == nil {
		t.Fatal("early refusal consumed credential", err)
	}
	// User-first locking makes the live-family cap hold for simultaneous logins.
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := range 25 {
		wg.Go(func() {
			copy := s
			copy.Hash = fmt.Sprintf("family-%d", i)
			copy.AccessHash = copy.Hash + "-a"
			copy.RefreshHash = copy.Hash + "-r"
			err := p.CreateAuthSession(t.Context(), copy)
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, service.ErrAuthSessionLimit) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 19 {
		t.Fatal("unbounded live sessions", admitted.Load())
	}
}

func TestAuthRefreshCleanupAndDeviceRevocation(t *testing.T) {
	p, u, s := refreshFixture(t)
	device := s
	device.Hash = "device2"
	device.AccessHash = "device2-access"
	device.RefreshHash = "device2-refresh"
	if err := p.CreateAuthSession(t.Context(), device); err != nil {
		t.Fatal(err)
	}
	// Access expiration does not end the family or prevent refresh/logout.
	if _, err := p.goqu.Update(p.tableAuthCredentials).Set(goqu.Record{"expires_at": time.Now().Add(-time.Hour)}).Where(goqu.Ex{"hash": s.AccessHash}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := p.CleanupAuthCredentials(t.Context(), 1); err != nil || n != 1 {
		t.Fatalf("early cleanup %d %v", n, err)
	}
	live, next, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, "a2", "r2")
	if err != nil || live == nil {
		t.Fatalf("rotate expired access: %v", err)
	}
	if !next.ExpiresAt.Equal(s.ExpiresAt) || time.Until(next.AccessExpiresAt) > 10*time.Minute {
		t.Fatal("sliding deadline")
	}
	if n, err := p.CleanupAuthCredentials(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("deleted live tombstone %d %v", n, err)
	}
	var consumed bool
	if found, err := p.goqu.From(p.tableAuthCredentials).Select("consumed").Where(goqu.Ex{"hash": s.RefreshHash}).ScanValContext(t.Context(), &consumed); err != nil || !found || !consumed {
		t.Fatal("missing replay tombstone", err)
	}
	// A restart (new facade) still sees the consumed credential and revokes.
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, tableAuthSessions: p.tableAuthSessions, tableAuthCredentials: p.tableAuthCredentials}
	if live, _, err := other.RotateAuthRefresh(t.Context(), s.RefreshHash, "bad-a", "bad-r"); err != nil || live != nil {
		t.Fatal("replay accepted", err)
	}
	if live, _, err := p.ResolveAuthAccess(t.Context(), device.AccessHash); err != nil || live == nil {
		t.Fatal("other device revoked", err)
	}
	if err := p.RevokeAuthCredential(t.Context(), device.RefreshHash); err != nil {
		t.Fatal(err)
	}
	if live, _, err := p.RotateAuthRefresh(t.Context(), device.RefreshHash, "bad-a", "bad-r"); err != nil || live != nil {
		t.Fatal("logout refresh accepted", err)
	}
	// Existing user version guards also invalidate both new credential types.
	s.Hash = "new-family"
	s.AccessHash = "new-access"
	s.RefreshHash = "new-refresh"
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetAuthUserPassword(t.Context(), u.ID, "new-password", &u.SessionVersion); err != nil {
		t.Fatal(err)
	}
	if live, _, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, "bad-a", "bad-r"); err != nil || live != nil {
		t.Fatal("password revocation ignored", err)
	}
}

func TestAuthRefreshPostLockExpiry(t *testing.T) {
	p, u, s := refreshFixture(t)
	tx, err := p.goqu.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id string
	if _, err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.Ex{"id": u.ID}).ForUpdate(goqu.Wait).ScanValContext(t.Context(), &id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		u, _, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, "late-a", "late-r")
		if u != nil {
			err = fmt.Errorf("expired family rotated")
		}
		done <- err
	}()
	waitAuthTestLock(t, p, fmt.Sprint(p.tableAuthUsers.GetTable()))
	if _, err := tx.Update(p.tableAuthSessions).Set(goqu.Record{"expires_at": time.Now().Add(-time.Second)}).Where(goqu.Ex{"hash": s.Hash}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var consumed bool
	if _, err := p.goqu.From(p.tableAuthCredentials).Select("consumed").Where(goqu.Ex{"hash": s.RefreshHash}).ScanValContext(t.Context(), &consumed); err != nil || consumed {
		t.Fatal("expiry partially committed", err)
	}
}

func TestAuthRefreshConcurrentUserInvalidation(t *testing.T) {
	for _, action := range []string{"disable", "revoke", "password", "enable"} {
		t.Run(action, func(t *testing.T) {
			p, u, s := refreshFixture(t)
			var wg sync.WaitGroup
			wg.Go(func() {
				if _, _, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, "raced-access", "raced-refresh"); err != nil {
					t.Error(err)
				}
			})
			wg.Go(func() {
				var err error
				switch action {
				case "disable":
					_, err = p.InvalidateAuthUser(t.Context(), u.ID, true)
				case "revoke":
					_, err = p.InvalidateAuthUser(t.Context(), u.ID, false)
				case "password":
					_, err = p.SetAuthUserPassword(t.Context(), u.ID, "new-hash", &u.SessionVersion)
				case "enable":
					_, err = p.EnableAuthUser(t.Context(), u.ID)
				}
				if err != nil {
					t.Error(err)
				}
			})
			wg.Wait()
			for _, hash := range []string{s.AccessHash, "raced-access"} {
				if live, _, err := p.ResolveAuthAccess(t.Context(), hash); err != nil || live != nil {
					t.Fatal("invalidation left access", err)
				}
			}
			for _, hash := range []string{s.RefreshHash, "raced-refresh"} {
				if live, _, err := p.RotateAuthRefresh(t.Context(), hash, "bad-access", "bad-refresh"); err != nil || live != nil {
					t.Fatal("invalidation left refresh", err)
				}
			}
			if err := p.CreateAuthSession(t.Context(), s); !errors.Is(err, service.ErrAuthConflict) {
				t.Fatal("stale issuance", err)
			}
		})
	}
}

func TestAuthRefreshCleanupBatchesAndCancellation(t *testing.T) {
	p, _, s := refreshFixture(t)
	for i := range 6 {
		if _, err := p.goqu.Update(p.tableAuthSessions).Set(goqu.Record{"refresh_after": time.Unix(0, 0)}).Where(goqu.Ex{"hash": s.Hash}).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, next, err := p.RotateAuthRefresh(t.Context(), s.RefreshHash, fmt.Sprintf("access%d", i), fmt.Sprintf("refresh%d", i))
		if err != nil || next == nil {
			t.Fatal(err)
		}
		s = *next
	}
	if _, err := p.goqu.Update(p.tableAuthCredentials).Set(goqu.Record{"expires_at": time.Now().Add(-time.Hour)}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.tableAuthSessions).Set(goqu.Record{"expires_at": time.Now().Add(-time.Hour)}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.CleanupAuthCredentials(ctx, 2); err == nil {
		t.Fatal("ignored cancellation")
	}
	before, err := p.goqu.From(p.tableAuthCredentials).CountContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.CleanupAuthCredentials(t.Context(), 2); err != nil || n != 2 {
		t.Fatal("batch not bounded", n, err)
	}
	after, err := p.goqu.From(p.tableAuthCredentials).CountContext(t.Context())
	if err != nil || before-after != 2 {
		t.Fatal("cascade exceeded batch", before, after, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if n, err := p.CleanupAuthCredentials(t.Context(), 2); err != nil || n > 2 {
				t.Error(n, err)
			}
		})
	}
	wg.Wait()
	if _, err := p.CleanupAuthCredentials(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if n, err := p.goqu.From(p.tableAuthSessions).CountContext(t.Context()); err != nil || n != 0 {
		t.Fatal("expired family remains", n, err)
	}
}

func TestAuthRefreshMigrationFrom26(t *testing.T) {
	p := newTestStore(t, nil)
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users") + "legacy_"
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(file.Name(), "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version >= 27 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		previous["migrations/"+file.Name()] = &fstest.MapFile{Data: body}
	}
	m := muz.Migrate{Path: "migrations", FS: previous, Extension: ".sql", Values: map[string]string{"TABLE_PREFIX": prefix}}
	driver := muz.NewPostgresDriver(p.db, prefix+"migrations", slog.Default())
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO %sauth_users(id,username,password_hash) VALUES ('u','user','hash'); INSERT INTO %sauth_sessions(hash,user_id,version,expires_at) VALUES ('old-token-hash','u',0,now()+interval '8 hours')", prefix, prefix)); err != nil {
		t.Fatal(err)
	}
	m.FS = migrationFS
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"auth_sessions", "auth_credentials"} {
		n, err := p.goqu.From(prefix + table).CountContext(t.Context())
		if err != nil || n != 0 {
			t.Fatal("legacy sessions survived", table, n, err)
		}
	}
	if n, err := p.goqu.From(prefix + "auth_users").CountContext(t.Context()); err != nil || n != 1 {
		t.Fatal("migration changed users", n, err)
	}
}
