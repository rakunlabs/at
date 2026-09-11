package postgres

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func externalFixture(t *testing.T) (*Postgres, *service.AuthIdentityProvider) {
	t.Helper()
	p := newTestStore(t, bytes.Repeat([]byte{1}, 32))
	secret := "provider-secret"
	v, err := p.SaveAuthIdentityProvider(t.Context(), service.AuthIdentityProvider{Label: "External", Mode: "oidc", Issuer: "https://idp.test", ClientID: "client", Scopes: []string{"openid"}, Enabled: true}, &secret)
	if err != nil {
		t.Fatal(err)
	}
	return p, v
}
func externalLink(p *service.AuthIdentityProvider, subject string) service.AuthIdentityLink {
	return service.AuthIdentityLink{ProviderID: p.ID, Issuer: p.Issuer, Subject: subject, Email: "same@example.test", AssertedPermissions: json.RawMessage(`{"roles":["admin"]}`)}
}
func externalSession(t *testing.T, p *Postgres, u *service.AuthUser, id string) service.AuthExternalAccount {
	t.Helper()
	s := service.AuthSession{Hash: id, UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if err := p.CreateAuthSession(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	return service.AuthExternalAccount{UserID: u.ID, SessionID: id, Version: u.SessionVersion}
}

func TestExternalPostgresNoEmailMergeAndImmutableNamespace(t *testing.T) {
	p, v := externalFixture(t)
	local, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "same@example.test", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	first, l, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-a"), v.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-b"), v.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == local.ID || second.ID == first.ID || first.Admin || second.Admin || first.PasswordHash != "" {
		t.Fatal("external identity merged/escalated/local password created")
	}
	otherConfig := *v
	otherConfig.ID = ""
	otherProvider, err := p.SaveAuthIdentityProvider(t.Context(), otherConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(otherProvider, "subject-a"), otherProvider.Version, nil, time.Now().Add(time.Minute))
	if err != nil || otherUser.ID == first.ID || otherUser.ID == local.ID {
		t.Fatalf("cross-provider email/subject merge: %v", err)
	}
	same, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-a"), v.Version, nil, time.Now().Add(time.Minute))
	if err != nil || same.ID != first.ID {
		t.Fatalf("stable subject changed: %v", err)
	}
	bad := externalLink(v, "subject-a")
	bad.Issuer += "/wrong"
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), bad, v.Version, nil, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("namespace mismatch: %v", err)
	}
	changed := *v
	changed.ClientID = "other"
	if _, err = p.SaveAuthIdentityProvider(t.Context(), changed, nil); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("critical edit with links: %v", err)
	}
	a := externalSession(t, p, first, "external-family")
	if err = p.UnlinkAuthIdentity(t.Context(), l.ID, a); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("removed last primary: %v", err)
	}
	localAccount := externalSession(t, p, local, "local-family")
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-a"), v.Version, &localAccount, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("linked other's subject: %v", err)
	}
	_, newLink, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-c"), v.Version, &localAccount, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.UnlinkAuthIdentity(t.Context(), newLink.ID, localAccount); err != nil {
		t.Fatal(err)
	}
	if live, _, err := p.ResolveAuthSession(t.Context(), localAccount.SessionID); err != nil || live != nil {
		t.Fatalf("unlink did not revoke: %v", err)
	}
	if _, err = p.InvalidateAuthUser(t.Context(), first.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "subject-a"), v.Version, nil, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("disabled user revived: %v", err)
	}
}

func TestExternalPostgresBrowserFlowReplayVersionAndEncryption(t *testing.T) {
	p, v := externalFixture(t)
	f := service.AuthExternalFlow{Hash: strings.Repeat("a", 64), ProviderID: v.ID, ProviderVersion: v.Version, Source: strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Minute), Payload: json.RawMessage(`{"verifier":"secret-verifier","state":"secret-state"}`)}
	if err := p.SaveAuthExternalFlow(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	var stored string
	if _, err := p.goqu.From(p.externalTable("auth_external_flows")).Select("payload").ScanValContext(t.Context(), &stored); err != nil || !atcrypto.IsEncrypted(stored) || strings.Contains(stored, "secret-verifier") {
		t.Fatalf("unencrypted flow: %v", err)
	}
	other := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers, encKey: bytes.Repeat([]byte{1}, 32)}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			flow, err := other.ConsumeAuthExternalFlow(t.Context(), f.Hash, v.ID, v.Version)
			if err == nil && flow != nil {
				winners.Add(1)
			} else if !errors.Is(err, service.ErrAuthConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("flow consumed %d times", winners.Load())
	}
	f.Hash = strings.Repeat("c", 64)
	if err := p.SaveAuthExternalFlow(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ConsumeAuthExternalFlow(t.Context(), f.Hash, v.ID, v.Version+1); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("version mismatch accepted: %v", err)
	}
	if _, err := p.ConsumeAuthExternalFlow(t.Context(), strings.Repeat("d", 64), v.ID, v.Version); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("browser mismatch accepted: %v", err)
	}
	u, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "disable-me"), v.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	a := externalSession(t, p, u, "provider-family")
	v.Enabled = false
	disabled, err := p.SaveAuthIdentityProvider(t.Context(), *v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Version != v.Version+1 {
		t.Fatal("provider edit did not bump version")
	}
	if live, _, err := p.ResolveAuthSession(t.Context(), a.SessionID); err != nil || live != nil {
		t.Fatalf("provider disable retained session: %v", err)
	}
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "disable-me"), v.Version, nil, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("disabled provider admitted: %v", err)
	}
}

func TestExternalPostgresLinkRevocationAndConcurrentProvision(t *testing.T) {
	p, v := externalFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			u, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "same-subject"), v.Version, nil, time.Now().Add(time.Minute))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- u.ID
		})
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("concurrent subject created multiple users")
		}
	}
	u, err := p.GetAuthUserByID(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	a := externalSession(t, p, u, "revoked-link-session")
	if _, err = p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "new-link"), v.Version, &a, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("revoked linking account accepted: %v", err)
	}
	if _, _, err = p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "expired"), v.Version, nil, time.Now().Add(-time.Second)); !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("expired flow admitted: %v", err)
	}
	links, err := p.ListAuthIdentityLinks(t.Context(), u.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("failed ceremony mutated links: %v", err)
	}
}

func TestExternalPostgresFlowAdmissionAndRotation(t *testing.T) {
	p, v := externalFixture(t)
	for i := range 11 {
		f := service.AuthExternalFlow{Hash: fmt.Sprintf("%064d", i), ProviderID: v.ID, ProviderVersion: v.Version, Source: strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Minute), Payload: json.RawMessage(`{"nonce":"secret"}`)}
		err := p.SaveAuthExternalFlow(t.Context(), f)
		if i < 10 && err != nil {
			t.Fatal(err)
		}
		if i == 10 && !errors.Is(err, service.ErrAuthSessionLimit) {
			t.Fatalf("source quota not enforced: %v", err)
		}
	}
	listed, err := p.ListAuthIdentityProviders(t.Context())
	if err != nil || len(listed) != 1 || listed[0].ClientSecret != "" || !listed[0].HasClientSecret {
		t.Fatalf("secret list redaction: %+v %v", listed, err)
	}
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	tx, err := p.db.BeginTx(t.Context(), &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.rotateAuthExternalSecrets(t.Context(), tx, oldKey, newKey); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p.encKeyMu.Lock()
	p.encKey = newKey
	p.encKeyMu.Unlock()
	got, err := p.GetAuthIdentityProvider(t.Context(), v.ID)
	if err != nil || got.ClientSecret != "provider-secret" {
		t.Fatalf("rotated provider: %v", err)
	}
	flow, err := p.ConsumeAuthExternalFlow(t.Context(), fmt.Sprintf("%064d", 0), v.ID, v.Version)
	if err != nil || !strings.Contains(string(flow.Payload), "nonce") {
		t.Fatalf("rotated flow: %v", err)
	}
	v.Label = "Updated"
	saved, err := p.SaveAuthIdentityProvider(t.Context(), *v, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = p.GetAuthIdentityProvider(t.Context(), v.ID)
	if err != nil || got.ClientSecret != "provider-secret" || got.Version != saved.Version {
		t.Fatal("omitted secret not preserved", err)
	}
	empty := ""
	if _, err = p.SaveAuthIdentityProvider(t.Context(), *saved, &empty); err != nil {
		t.Fatal(err)
	}
	got, err = p.GetAuthIdentityProvider(t.Context(), v.ID)
	if err != nil || got.HasClientSecret {
		t.Fatal("explicit clear failed", err)
	}
	for i := range 31 {
		f := service.AuthExternalFlow{Hash: fmt.Sprintf("%064x", 100+i), ProviderID: got.ID, ProviderVersion: got.Version, Source: strings.Repeat("d", 64), ExpiresAt: time.Now().Add(time.Minute), Payload: json.RawMessage(`{}`)}
		err := p.SaveAuthExternalFlow(t.Context(), f)
		if i == 30 {
			if !errors.Is(err, service.ErrAuthSessionLimit) {
				t.Fatalf("sequential source quota bypassed: %v", err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.ConsumeAuthExternalFlow(t.Context(), f.Hash, got.ID, got.Version); err != nil {
			t.Fatal(err)
		}
	}
	p.encKeyMu.Lock()
	p.encKey = nil
	p.encKeyMu.Unlock()
	if _, err = p.SaveAuthIdentityProvider(t.Context(), *got, nil); err == nil {
		t.Fatal("accepted missing encryption key")
	}
}

func TestExternalPostgresPostLockDeadline(t *testing.T) {
	p, v := externalFixture(t)
	tx, err := p.goqu.BeginTx(t.Context(), &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = p.lockExternalAdmission(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "late"), v.Version, nil, time.Now().Add(50*time.Millisecond))
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("post-lock expiry accepted: %v", err)
	}
	n, err := p.goqu.From(p.externalTable("auth_identity_links")).Where(goqu.Ex{"subject": "late"}).CountContext(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("expired identity persisted: %d %v", n, err)
	}
}

func TestExternalPostgresRecoveryRaceAndSessionProvenance(t *testing.T) {
	p, v := externalFixture(t)
	u, link, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "recover-subject"), v.Version, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	c := service.AuthExternalCompletion{ProviderID: v.ID, ProviderVersion: v.Version, LinkID: link.ID, Deadline: time.Now().Add(time.Minute)}
	ctx := service.ContextWithAuthExternalCompletion(t.Context(), c)
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = p.lockExternalAccount(ctx, tx, service.AuthExternalAccount{UserID: u.ID, Version: u.SessionVersion}); err != nil {
		t.Fatal(err)
	}
	s := service.AuthSession{Hash: "provenance-session", UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err = tx.Insert(p.tableAuthSessions).Rows(goqu.Record{"hash": s.Hash, "user_id": s.UserID, "version": s.Version, "expires_at": s.ExpiresAt}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.recordAuthExternalSession(ctx, tx, s); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	n, err := p.goqu.From(p.externalTable("auth_external_session_provenance")).Where(goqu.Ex{"session_id": s.Hash, "provider_id": v.ID, "link_id": link.ID}).CountContext(ctx)
	if err != nil || n != 1 {
		t.Fatalf("missing provenance %d %v", n, err)
	}
	tx, err = p.goqu.BeginTx(t.Context(), &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = p.lockExternalAccount(t.Context(), tx, service.AuthExternalAccount{UserID: u.ID, Version: u.SessionVersion}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := p.CompleteAuthExternalIdentity(t.Context(), externalLink(v, "recover-subject"), v.Version, nil, time.Now().Add(time.Minute))
		done <- err
	}()
	// Callback reads the old link, then waits on the recovery-owned user lock.
	time.Sleep(100 * time.Millisecond)
	if _, err = tx.Delete(p.externalTable("auth_identity_links")).Where(goqu.Ex{"user_id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Update(p.tableAuthUsers).Set(goqu.Record{"session_version": goqu.L("session_version + 1"), "password_hash": "recovered"}).Where(goqu.Ex{"id": u.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, service.ErrAuthConflict) {
		t.Fatalf("recovery resurrected removed identity: %v", err)
	}
}
