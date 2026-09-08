package postgres

import (
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

func mobileFixture(t *testing.T) (*Postgres, *service.AuthUser, service.AuthSession, service.AuthMobileRequest) {
	t.Helper()
	p, u, web := refreshFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := service.AuthMobileRequest{ID: "request", Challenge: "challenge", State: "state", DeviceName: "phone", Remember: true, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err := p.CreateAuthMobileRequest(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	return p, u, web, r
}

func TestAuthMobileMigrationPreservesWebFamilies(t *testing.T) {
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
		if version >= 28 {
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
	if _, err := p.db.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO %sauth_users(id,username,password_hash) VALUES ('u','user','hash'); INSERT INTO %sauth_sessions(hash,user_id,version,expires_at) VALUES ('family','u',0,now()+interval '8 hours'); INSERT INTO %sauth_credentials(hash,session_id,kind,expires_at) VALUES ('access','family','access',now()+interval '10 minutes'), ('refresh','family','refresh',now()+interval '8 hours')", prefix, prefix, prefix)); err != nil {
		t.Fatal(err)
	}
	m.FS = migrationFS
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	old := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: goqu.T(prefix + "auth_users"), tableAuthSessions: goqu.T(prefix + "auth_sessions"), tableAuthCredentials: goqu.T(prefix + "auth_credentials")}
	if u, s, err := old.ResolveAuthAccess(t.Context(), "access"); err != nil || u == nil || s == nil || s.Transport != "web" {
		t.Fatal("migration invalidated web access", err)
	}
	if u, s, err := old.RotateAuthRefresh(t.Context(), "refresh", "next-access", "next-refresh"); err != nil || u == nil || s == nil || s.Hash != "family" {
		t.Fatal("migration invalidated web refresh", err)
	}
}

func TestAuthMobileAtomicRedemption(t *testing.T) {
	p, u, web, r := mobileFixture(t)
	if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for i := range 8 {
		wg.Go(func() {
			s := service.AuthSession{Hash: fmt.Sprint("mobile", i), AccessHash: fmt.Sprint("ma", i), RefreshHash: fmt.Sprint("mr", i)}
			got, family, err := p.RedeemAuthMobileCode(t.Context(), "code", "challenge", s, 8*time.Hour, 30*24*time.Hour)
			if errors.Is(err, service.ErrAuthConflict) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			winners.Add(1)
			if got.ID != u.ID || family.Transport != "mobile" || !family.Remember || time.Until(family.ExpiresAt) < 29*24*time.Hour {
				t.Error("wrong mobile identity/lifetime")
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("redemption winners", winners.Load())
	}
	if n, err := p.goqu.From(p.tableAuthSessions).CountContext(t.Context()); err != nil || n != 2 {
		t.Fatal("non-atomic session insertion", n, err)
	}
}

func TestAuthMobileBindingsAndConsumption(t *testing.T) {
	for _, mode := range []string{"wrong-pkce", "disabled", "password", "revoked", "web-expired", "code-expired", "request-expired", "wrong-owner", "mobile-approver"} {
		t.Run(mode, func(t *testing.T) {
			p, u, web, r := mobileFixture(t)
			if mode == "wrong-owner" {
				other, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "other", PasswordHash: "hash"}, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, other.ID, web.Hash, "code"); !errors.Is(err, service.ErrAuthConflict) {
					t.Fatal("wrong owner approved", err)
				}
				return
			}
			if mode == "mobile-approver" {
				if _, err := p.goqu.Update(p.tableAuthSessions).Set(goqu.Record{"transport": "mobile"}).Executor().ExecContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); !errors.Is(err, service.ErrAuthConflict) {
					t.Fatal("mobile family approved", err)
				}
				return
			}
			if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); err != nil {
				t.Fatal(err)
			}
			challenge := "challenge"
			switch mode {
			case "wrong-pkce":
				challenge = "wrong"
			case "disabled":
				if _, err := p.InvalidateAuthUser(t.Context(), u.ID, true); err != nil {
					t.Fatal(err)
				}
			case "password":
				if _, err := p.SetAuthUserPassword(t.Context(), u.ID, "new-hash", nil); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := p.DeleteAuthSession(t.Context(), web.Hash); err != nil {
					t.Fatal(err)
				}
			case "web-expired":
				if _, err := p.goqu.Update(p.tableAuthSessions).Set(goqu.Record{"expires_at": time.Now().Add(-time.Second)}).Executor().ExecContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "code-expired", "request-expired":
				column := "code_expires_at"
				if mode == "request-expired" {
					column = "expires_at"
				}
				if _, err := p.goqu.Update(p.tableAuthMobileRequests).Set(goqu.Record{column: time.Now().Add(-time.Second)}).Executor().ExecContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if _, _, err := p.RedeemAuthMobileCode(t.Context(), "code", challenge, service.AuthSession{Hash: "mobile", AccessHash: "ma", RefreshHash: "mr"}, time.Hour, time.Hour); !errors.Is(err, service.ErrAuthConflict) {
					t.Fatal("invalid redemption", err)
				}
				challenge = "challenge"
			}
			if n, err := p.goqu.From(p.tableAuthMobileRequests).CountContext(t.Context()); err != nil || n != 0 {
				t.Fatal("failed code not consumed", n, err)
			}
		})
	}
}

func TestAuthMobileDeadlineAfterLockWait(t *testing.T) {
	for _, mode := range []string{"approve-request", "approve-web", "redeem-code", "redeem-web"} {
		t.Run(mode, func(t *testing.T) {
			p, u, web, r := mobileFixture(t)
			if mode == "redeem-code" || mode == "redeem-web" {
				if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); err != nil {
					t.Fatal(err)
				}
			}
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
				if mode == "approve-request" || mode == "approve-web" {
					_, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code")
					done <- err
				} else {
					_, _, err := p.RedeemAuthMobileCode(t.Context(), "code", "challenge", service.AuthSession{Hash: "late", AccessHash: "la", RefreshHash: "lr"}, time.Hour, time.Hour)
					done <- err
				}
			}()
			waitAuthTestLock(t, p, fmt.Sprint(p.tableAuthUsers.GetTable()))
			table, column := p.tableAuthMobileRequests, "expires_at"
			if mode == "redeem-code" {
				column = "code_expires_at"
			}
			if mode == "approve-web" || mode == "redeem-web" {
				table = p.tableAuthSessions
			}
			if _, err := tx.Update(table).Set(goqu.Record{column: time.Now().Add(-time.Second)}).Executor().ExecContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, service.ErrAuthConflict) {
				t.Fatal("lock wait admitted expired authorization", err)
			}
		})
	}
}

func TestAuthMobileBoundsAndTransport(t *testing.T) {
	p, u, web, r := mobileFixture(t)
	if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); err != nil {
		t.Fatal(err)
	}
	_, s, err := p.RedeemAuthMobileCode(t.Context(), "code", "challenge", service.AuthSession{Hash: "mobile", AccessHash: "ma", RefreshHash: "mr"}, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, err := p.RotateAuthRefresh(t.Context(), "mr", "a2", "r2"); err != nil || got != nil {
		t.Fatal("web rotated mobile", err)
	}
	if err := p.RevokeAuthCredential(t.Context(), "mr"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := p.ResolveAuthAccess(t.Context(), "ma"); err != nil || got == nil {
		t.Fatal("web revoked mobile", err)
	}
	_, rotated, err := p.RotateAuthRefreshTransport(t.Context(), "mr", "a2", "r2", "mobile")
	if err != nil || rotated == nil || !rotated.ExpiresAt.Equal(s.ExpiresAt) || rotated.Transport != "mobile" {
		t.Fatal("mobile rotation", err)
	}
	if _, _, err := p.RotateAuthRefreshTransport(t.Context(), "mr", "a3", "r3", "mobile"); err != nil {
		t.Fatal(err)
	}
	if got, _, err := p.ResolveAuthAccess(t.Context(), "a2"); err != nil || got != nil {
		t.Fatal("replay did not revoke", err)
	}
	for i := range 1000 {
		r.ID = fmt.Sprint(i)
		if err := p.CreateAuthMobileRequest(t.Context(), r); err != nil {
			t.Fatal(i, err)
		}
	}
	r.ID = "overflow"
	if err := p.CreateAuthMobileRequest(t.Context(), r); !errors.Is(err, service.ErrAuthSessionLimit) {
		t.Fatal("unbounded request table", err)
	}
	if _, err := p.goqu.Update(p.tableAuthMobileRequests).Set(goqu.Record{"expires_at": time.Now().Add(-time.Second)}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupAuthMobileRequests(t.Context(), 17); err != nil {
		t.Fatal(err)
	}
	if n, err := p.goqu.From(p.tableAuthMobileRequests).CountContext(t.Context()); err != nil || n != 983 {
		t.Fatal("unbounded cleanup", n, err)
	}
}

func TestAuthMobileSessionCap(t *testing.T) {
	p, u, web, r := mobileFixture(t)
	for i := range 19 {
		if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: fmt.Sprint("existing", i), UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.DecideAuthMobileRequest(t.Context(), r.ID, u.ID, web.Hash, "code"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.RedeemAuthMobileCode(t.Context(), "code", "challenge", service.AuthSession{Hash: "excess", AccessHash: "ea", RefreshHash: "er"}, time.Hour, time.Hour); !errors.Is(err, service.ErrAuthSessionLimit) {
		t.Fatal("mobile bypassed session cap", err)
	}
	if n, err := p.goqu.From(p.tableAuthSessions).CountContext(t.Context()); err != nil || n != 20 {
		t.Fatal("wrong family count", n, err)
	}
}
