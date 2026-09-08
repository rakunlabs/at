package postgres

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"

	"github.com/rakunlabs/at/internal/service"
)

func passkeyStoreFixture(t *testing.T) (*Postgres, *service.AuthUser, service.AuthChallenge, passkey.Credential) {
	t.Helper()
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: "test-only"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "session", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c := service.AuthChallenge{Hash: "challenge", Purpose: "enroll", UserID: u.ID, SessionHash: "session", Version: u.SessionVersion, Name: "key", Data: passkey.SessionData{Expires: time.Now().Add(5 * time.Minute), Challenge: []byte("test"), UserHandle: []byte(u.ID), UserVerification: passkey.UVRequired}}
	k := passkey.Credential{ID: []byte("credential"), UserHandle: []byte(u.ID), PublicKey: []byte("test-only"), SignCount: 0}
	return p, u, c, k
}

func TestNativePasskeyChallengeAtomicPostgres(t *testing.T) {
	p, u, c, _ := passkeyStoreFixture(t)
	if err := p.SaveAuthChallenge(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthChallenges: p.tableAuthChallenges}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got, err := other.ConsumeAuthChallenge(t.Context(), c.Hash)
			if err != nil {
				t.Error(err)
				return
			}
			if got != nil {
				winners.Add(1)
				if got.SessionHash != "session" || got.UserID != u.ID || got.Data.UserVerification != passkey.UVRequired {
					t.Error("lost binding")
				}
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("consume winners: %d", winners.Load())
	}
	if err := p.SaveAuthChallenge(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	// Expire both the indexed expiry and serialized Ada data, as time would.
	if _, err := p.goqu.Update(p.tableAuthChallenges).Set(goqu.Record{"expires_at": time.Now().Add(-time.Minute), "data": goqu.L("jsonb_set(data, '{Data,expires}', to_jsonb(?::text))", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))}).Where(goqu.Ex{"hash": c.Hash}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := other.ConsumeAuthChallenge(t.Context(), c.Hash); err != nil || got != nil {
		t.Fatalf("expired consume: %+v %v", got, err)
	}
	if n, err := p.goqu.From(p.tableAuthChallenges).CountContext(t.Context()); err != nil || n != 0 {
		t.Fatal("expired record retained", n, err)
	}
	for i := range 5 {
		c.Hash = fmt.Sprint(i)
		if err := p.SaveAuthChallenge(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	c.Hash = "sixth"
	if err := p.SaveAuthChallenge(t.Context(), c); err == nil {
		t.Fatal("per-user cap bypassed")
	}
	if _, err := p.goqu.Update(p.tableAuthChallenges).Set(goqu.Record{"expires_at": time.Now().Add(-time.Minute)}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveAuthChallenge(t.Context(), c); err != nil {
		t.Fatal("prune on begin failed", err)
	}
	if n, err := p.goqu.From(p.tableAuthChallenges).CountContext(t.Context()); err != nil || n != 1 {
		t.Fatal("prune count", n, err)
	}
	// Fill the global pool directly to avoid thousands of serialized inserts.
	rows := make([]goqu.Record, 4095)
	for i := range rows {
		rows[i] = goqu.Record{"hash": fmt.Sprintf("global-%d", i), "user_id": u.ID, "expires_at": time.Now().Add(time.Minute), "data": goqu.L("'{}'::jsonb")}
	}
	if _, err := p.goqu.Insert(p.tableAuthChallenges).Rows(rows).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "other", PasswordHash: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	c.UserID, c.Hash = second.ID, "global-full"
	if err := p.SaveAuthChallenge(t.Context(), c); err == nil {
		t.Fatal("global cap bypassed")
	}
}

func TestNativePasskeyCounterCASPostgres(t *testing.T) {
	p, u, c, k := passkeyStoreFixture(t)
	if err := p.CreateAuthPasskey(t.Context(), c, k); err != nil {
		t.Fatal(err)
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || len(keys) != 1 {
		t.Fatal(keys, err)
	}
	id := keys[0].ID
	for range 2 {
		if err := p.AdvanceAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, 0, 0, c.Data.Expires); err != nil {
			t.Fatal("stable zero rejected", err)
		}
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if p.AdvanceAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, 0, 1, c.Data.Expires) == nil {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("counter CAS winners %d", winners.Load())
	}
	if err := p.AdvanceAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, 1, 0, c.Data.Expires); err == nil {
		t.Fatal("counter reset accepted")
	}
	if err := p.AdvanceAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, 1, 1, c.Data.Expires); err == nil {
		t.Fatal("non-increasing counter accepted")
	}
	if err := p.AdvanceAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, 1, ^uint32(0), c.Data.Expires); err != nil {
		t.Fatal("uint32 overflow", err)
	}
	keys, err = p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || keys[0].Credential.SignCount != ^uint32(0) || keys[0].LastUsedAt == nil {
		t.Fatal("counter/last used persistence", keys, err)
	}
	// A stale verified login can never gain a new version during issuance.
	if err := p.DeleteAuthPasskey(t.Context(), u.ID, id, u.SessionVersion, "session"); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "stale", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("stale issuance accepted")
	}
}

func TestNativePasskeyChallengePoolIsolationPostgres(t *testing.T) {
	p, u, enroll, _ := passkeyStoreFixture(t)
	login := enroll
	login.Purpose, login.SessionHash = "login", ""
	for i := range 6 {
		login.Hash = fmt.Sprintf("anonymous-%d", i)
		if err := p.SaveAuthChallenge(t.Context(), login); err != nil {
			t.Fatal("anonymous login imposed per-user quota", err)
		}
	}
	if err := p.SaveAuthChallenge(t.Context(), enroll); err != nil {
		t.Fatal("anonymous logins starved enrollment", err)
	}
	login.Hash = "legitimate-login"
	if err := p.SaveAuthChallenge(t.Context(), login); err != nil {
		t.Fatal("anonymous logins starved legitimate login at per-user threshold", err)
	}
	// Saturate only the anonymous pool. The last 256 global slots cannot be
	// allocated by login begins, even when they all name the same victim.
	rows := make([]goqu.Record, 3840-7)
	for i := range rows {
		rows[i] = goqu.Record{"hash": fmt.Sprintf("pool-%d", i), "user_id": u.ID, "expires_at": enroll.Data.Expires, "data": goqu.L(`'{"Purpose":"login"}'::jsonb`)}
	}
	if _, err := p.goqu.Insert(p.tableAuthChallenges).Rows(rows).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	login.Hash = "anonymous-pool-full"
	if err := p.SaveAuthChallenge(t.Context(), login); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("anonymous pool cap: %v", err)
	}
	for i := range 4 {
		enroll.Hash = fmt.Sprintf("reserved-enroll-%d", i)
		if err := p.SaveAuthChallenge(t.Context(), enroll); err != nil {
			t.Fatal("anonymous pool consumed reserved enrollment capacity", err)
		}
	}
	enroll.Hash = "enrollment-sixth"
	if err := p.SaveAuthChallenge(t.Context(), enroll); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("authenticated enrollment per-user cap: %v", err)
	}
}

func TestNativePasskeyBinaryCredentialPostgres(t *testing.T) {
	p, u, c, key := passkeyStoreFixture(t)
	key.ID = make([]byte, 256)
	for i := range key.ID {
		key.ID[i] = byte(i)
	}
	if err := p.CreateAuthPasskey(t.Context(), c, key); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	found, err := p.goqu.From(p.tableAuthPasskeys).Select("credential_id").Where(goqu.Ex{"user_id": u.ID}).ScanValContext(t.Context(), &stored)
	if err != nil || !found || !bytes.Equal(stored, key.ID) {
		t.Fatalf("binary credential ID did not round-trip: found=%t error=%v", found, err)
	}
}

func TestNativePasskeyEnrollmentSessionExpiresDuringLockPostgres(t *testing.T) {
	p, u, c, key := passkeyStoreFixture(t)
	tx, err := p.goqu.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id string
	if _, err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.Ex{"id": u.ID}).ForUpdate(goqu.Wait).ScanValContext(t.Context(), &id); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- p.CreateAuthPasskey(t.Context(), c, key) }()
	waitAuthTestLock(t, p, fmt.Sprint(p.tableAuthUsers.GetTable()))
	// This expiration is later than the waiting transaction's start timestamp,
	// but earlier than its eventual admission. CURRENT_TIMESTAMP would be unsafe.
	if _, err := tx.Update(p.tableAuthSessions).Set(goqu.Record{"expires_at": time.Now().UTC()}).Where(goqu.Ex{"hash": "session"}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("lock wait extended enrollment session lifetime")
	}
}

func waitAuthTestLock(t *testing.T, p *Postgres, table string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		err := p.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE $1`, "%"+table+"%").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authentication did not wait for lock on", table)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativePasskeyLoginExpiresDuringAdmissionLocksPostgres(t *testing.T) {
	for _, stage := range []string{"counter user lock", "counter credential lock", "session issuance user lock"} {
		t.Run(stage, func(t *testing.T) {
			p, u, c, key := passkeyStoreFixture(t)
			if err := p.CreateAuthPasskey(t.Context(), c, key); err != nil {
				t.Fatal(err)
			}
			keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
			if err != nil {
				t.Fatal(err)
			}
			var deadline time.Time
			if err := p.db.QueryRowContext(t.Context(), `SELECT clock_timestamp() + interval '2 seconds'`).Scan(&deadline); err != nil {
				t.Fatal(err)
			}
			issuance := stage == "session issuance user lock"
			if issuance {
				if err := p.AdvanceAuthPasskey(t.Context(), u.ID, keys[0].ID, u.SessionVersion, 0, 1, deadline); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := p.goqu.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			table, id := p.tableAuthUsers, u.ID
			if stage == "counter credential lock" {
				table, id = p.tableAuthPasskeys, keys[0].ID
			}
			var locked string
			if _, err := tx.From(table).Select("id").Where(goqu.Ex{"id": id}).ForUpdate(goqu.Wait).ScanValContext(t.Context(), &locked); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				if issuance {
					result <- p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "late-login", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour), AdmissionDeadline: deadline})
				} else {
					result <- p.AdvanceAuthPasskey(t.Context(), u.ID, keys[0].ID, u.SessionVersion, 0, 1, deadline)
				}
			}()
			waitAuthTestLock(t, p, fmt.Sprint(table.GetTable()))
			// Synchronize to the database deadline, not an arbitrary sleep. The
			// transaction starts before expiry, then resumes only after expiry.
			if _, err := p.db.ExecContext(t.Context(), `SELECT pg_sleep(GREATEST(EXTRACT(EPOCH FROM ($1::timestamptz - clock_timestamp())), 0))`, deadline); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, service.ErrAuthConflict) {
				t.Fatalf("expired admission: %v", err)
			}
			keys, err = p.ListAuthPasskeys(t.Context(), u.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := uint32(0)
			if issuance {
				want = 1
			}
			if keys[0].Credential.SignCount != want {
				t.Fatal("expired counter transaction was not rolled back")
			}
			if live, _, err := p.ResolveAuthSession(t.Context(), "late-login"); err != nil || live != nil {
				t.Fatalf("expired issuance left session: %+v %v", live, err)
			}
			if n, err := p.goqu.From(p.tableAuthSessions).Where(goqu.Ex{"hash": "late-login"}).CountContext(t.Context()); err != nil || n != 0 {
				t.Fatal("expired issuance persisted a session", n, err)
			}
		})
	}
}

func TestNativePasskeyEnrollmentBoundsPostgres(t *testing.T) {
	p, u, c, k := passkeyStoreFixture(t)
	for i := range 20 {
		k.ID = []byte(fmt.Sprint(i))
		if err := p.CreateAuthPasskey(t.Context(), c, k); err != nil {
			t.Fatal(err)
		}
	}
	k.ID = []byte("21")
	if err := p.CreateAuthPasskey(t.Context(), c, k); err == nil {
		t.Fatal("credential cap bypassed")
	}
	other, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "other", PasswordHash: "test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "other-session", UserID: other.ID, Version: other.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	otherChallenge := c
	otherChallenge.UserID, otherChallenge.SessionHash = other.ID, "other-session"
	k.ID, k.UserHandle = []byte("0"), []byte(other.ID)
	if err := p.CreateAuthPasskey(t.Context(), otherChallenge, k); err == nil {
		t.Fatal("cross-user duplicate credential accepted")
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteAuthPasskey(t.Context(), other.ID, keys[0].ID, other.SessionVersion, "other-session"); err == nil {
		t.Fatal("cross-user deletion accepted")
	}
	if err := p.DeleteAuthSession(t.Context(), "other-session"); err != nil {
		t.Fatal(err)
	}
	k.ID = []byte("new")
	if err := p.CreateAuthPasskey(t.Context(), otherChallenge, k); err == nil {
		t.Fatal("logged-out enrollment accepted")
	}
}

func TestNativePasskeyRevocationRacePostgres(t *testing.T) {
	p, u, c, k := passkeyStoreFixture(t)
	for i := range 20 {
		u, err := p.GetAuthUserByID(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		c.Version = u.SessionVersion
		if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "session", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil && i != 0 {
			t.Fatal(err)
		}
		if err := p.CreateAuthPasskey(t.Context(), c, k); err != nil {
			t.Fatal(err)
		}
		keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() {
			if p.AdvanceAuthPasskey(t.Context(), u.ID, keys[0].ID, u.SessionVersion, 0, 1, c.Data.Expires) == nil {
				_ = p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "raced", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)})
			}
		})
		wg.Go(func() {
			if err := p.DeleteAuthPasskey(t.Context(), u.ID, keys[0].ID, u.SessionVersion, "session"); err != nil {
				t.Error(err)
			}
		})
		wg.Wait()
		if live, _, err := p.ResolveAuthSession(t.Context(), "raced"); err != nil || live != nil {
			t.Fatal("revocation race left live session", live, err)
		}
	}
}
