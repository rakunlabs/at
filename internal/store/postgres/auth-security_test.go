package postgres

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestAuthSecurityEncryptionAndRotation(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "security", PasswordHash: "old"}, false)
	if err != nil {
		t.Fatal(err)
	}
	set := func(s *service.AuthSecurityUpdate) error { s.State.Secret = "super-secret"; return nil }
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, set); err == nil {
		t.Fatal("plaintext factor accepted")
	}
	key := []byte(strings.Repeat("k", 32))
	p.SetEncryptionKey(key)
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, set); err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if _, err := p.goqu.From(p.authSecurityTable("auth_security")).Select("data").Where(goqu.Ex{"user_id": u.ID}).ScanValContext(t.Context(), &blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "super-secret") || !strings.Contains(string(blob), "enc:") {
		t.Fatal("factor not encrypted")
	}
	newKey := []byte(strings.Repeat("n", 32))
	tx, err := p.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.rotateAuthSecurityKey(t.Context(), tx, key, newKey); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p.SetEncryptionKey(newKey)
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		if s.State.Secret != "super-secret" {
			t.Fatal("rotation lost secret")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tx, err = p.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.rotateAuthSecurityKey(t.Context(), tx, newKey, nil); err == nil {
		t.Fatal("encryption disabled with active factor")
	}
}

func TestAuthSecurityAtomicConsumptionAndReset(t *testing.T) {
	p := newTestStore(t, []byte(strings.Repeat("k", 32)))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "security", PasswordHash: "old", Admin: true, Disabled: false}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "old-family", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour), AccessHash: "old-access", RefreshHash: "old-refresh", AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.tableAuthPasskeys).Rows(goqu.Record{"id": "old-key", "user_id": u.ID, "name": "old key", "credential_id": []byte("old-credential"), "credential": goqu.L("'{}'::jsonb"), "sign_count": 0}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.authSecurityTable("auth_identity_providers")).Rows(goqu.Record{"id": "old-provider", "config": goqu.L("'{}'::jsonb")}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Insert(p.authSecurityTable("auth_identity_links")).Rows(goqu.Record{"id": "old-link", "provider_id": "old-provider", "issuer": "https://old.example", "subject": "old-subject", "user_id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.State.Transactions = []service.AuthTransaction{{Hash: "ticket", Purpose: "recovery", Version: u.SessionVersion, Expires: s.Now.Add(time.Minute)}}
		s.State.Secret = "secret"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			err := p.UpdateAuthSecurity(t.Context(), u.ID, "", -1, func(s *service.AuthSecurityUpdate) error {
				if len(s.State.Transactions) != 1 {
					return service.ErrAuthConflict
				}
				s.ResetPassword = "new"
				s.Revoke = true
				return nil
			})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, service.ErrAuthConflict) {
				t.Errorf("reset: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("reset winners %d", wins.Load())
	}
	for _, name := range []string{"auth_sessions", "auth_passkeys", "auth_identity_links", "auth_challenges", "auth_external_flows"} {
		count, err := p.goqu.From(p.authSecurityTable(name)).Where(goqu.Ex{"user_id": u.ID}).CountContext(t.Context())
		if err != nil || count != 0 {
			t.Fatalf("old %s survived: %d %v", name, count, err)
		}
	}
	if user, _, err := p.ResolveAuthAccess(t.Context(), "old-access"); err != nil || user != nil {
		t.Fatalf("old access survived: %v %v", user, err)
	}
	live, err := p.GetAuthUserByID(t.Context(), u.ID)
	if err != nil || live.PasswordHash != "new" || !live.Admin || live.SessionVersion != u.SessionVersion+1 {
		t.Fatalf("reset: %+v %v", live, err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(*service.AuthSecurityUpdate) error { return nil }); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("stale account version accepted")
	}
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
		t.Fatal(err)
	}
	// Full replacement does not enable an explicitly disabled account.
	if _, err := p.goqu.Update(p.tableAuthUsers).Set(goqu.Record{"disabled": true}).Where(goqu.Ex{"id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", -1, func(s *service.AuthSecurityUpdate) error { s.ResetPassword = "third"; return nil }); err != nil {
		t.Fatal(err)
	}
	live, _ = p.GetAuthUserByID(t.Context(), u.ID)
	if !live.Disabled || !live.Admin {
		t.Fatal("reset changed role/status")
	}
}

func TestAuthSecurityPostLockExpiryAndSessionBinding(t *testing.T) {
	p := newTestStore(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "security", PasswordHash: "old"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := service.AuthSession{Hash: "session", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "wrong-session", u.SessionVersion, func(*service.AuthSecurityUpdate) error { return nil }); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("wrong session accepted")
	}
	err = p.UpdateAuthSecurity(t.Context(), u.ID, s.Hash, u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.Deadline = s.Now.Add(10 * time.Millisecond)
		s.State.BackupHashes = []string{"must-rollback"}
		time.Sleep(25 * time.Millisecond)
		return nil
	})
	if !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("post-work expiry: %v", err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, s.Hash, u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		if len(s.State.BackupHashes) != 0 {
			t.Fatal("expired mutation committed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteAuthSession(t.Context(), s.Hash); err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, s.Hash, u.SessionVersion, func(*service.AuthSecurityUpdate) error { return nil }); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("revoked session accepted")
	}
}

func TestAuthSecuritySourceConcurrentLimit(t *testing.T) {
	p := newTestStore(t, nil)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 140 {
		wg.Go(func() {
			allowed, err := p.AdmitAuthSecuritySource(t.Context(), strings.Repeat("a", 64))
			if err != nil {
				t.Error(err)
			} else if allowed {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 120 {
		t.Fatalf("admission winners %d", wins.Load())
	}
	var attempts int
	if _, err := p.goqu.From(p.authSecurityTable("auth_security_sources")).Select("attempts").ScanValContext(t.Context(), &attempts); err != nil || attempts != 120 {
		t.Fatalf("attempts %d: %v", attempts, err)
	}
}

func TestAuthSecurityRecoveryAdministratorAuthority(t *testing.T) {
	p := newTestStore(t, nil)
	actor, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "actor", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "target", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	sid := "actor-session"
	proof := strings.Repeat("a", 64)
	ticket := strings.Repeat("b", 64)
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: sid, UserID: actor.ID, Version: actor.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	seed := func() {
		t.Helper()
		if err := p.UpdateAuthSecurity(t.Context(), actor.ID, sid, actor.SessionVersion, func(s *service.AuthSecurityUpdate) error {
			s.State.Transactions = []service.AuthTransaction{{Hash: proof, Purpose: "proof:recovery.issue", SessionID: sid, Version: actor.SessionVersion, Expires: s.Now.Add(time.Minute)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := p.IssueAuthRecovery(t.Context(), actor.ID, sid, actor.SessionVersion, proof, target.ID, ticket)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, service.ErrAuthConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("ticket winners %d", wins.Load())
	}
	seed()
	if _, err := p.goqu.Update(p.tableAuthUsers).Set(goqu.Record{"admin": false}).Where(goqu.Ex{"id": actor.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.IssueAuthRecovery(t.Context(), actor.ID, sid, actor.SessionVersion, proof, target.ID, ticket); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("non-platform actor issued recovery")
	}
	if _, err := p.goqu.Update(p.tableAuthUsers).Set(goqu.Record{"admin": true}).Where(goqu.Ex{"id": actor.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteAuthSession(t.Context(), sid); err != nil {
		t.Fatal(err)
	}
	if err := p.IssueAuthRecovery(t.Context(), actor.ID, sid, actor.SessionVersion, proof, target.ID, ticket); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatal("revoked administrator issued recovery")
	}
	var count int64
	count, err = p.goqu.From(p.authSecurityTable("auth_recovery_events")).CountContext(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("audit events %d: %v", count, err)
	}
}
