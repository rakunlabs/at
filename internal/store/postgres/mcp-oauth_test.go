package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func mcpPendingHash(state string) string {
	hash := sha256.Sum256([]byte(state))
	return hex.EncodeToString(hash[:])
}

func mcpPendingSession(t *testing.T, p *Postgres, workspace string, user *service.AuthUser, sid string) context.Context {
	t.Helper()
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: sid, UserID: user.ID, Version: user.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(t.Context(), workspace, user.ID, sid)
	if err != nil {
		t.Fatal(err)
	}
	return service.WithAccessPrincipal(t.Context(), a)
}

func TestMCPOAuthPendingIsolationAndConsumption(t *testing.T) {
	p := newTestStore(t, bytes.Repeat([]byte{1}, 32))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "mcp-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpPendingSession(t, p, service.DefaultWorkspaceID, u, "owner-session")
	hash := mcpPendingHash("state")
	payload := json.RawMessage(`{"verifier":"private-verifier","client_secret":"private-secret"}`)
	if err := p.SaveMCPOAuthPending(ctx, hash, payload); err != nil {
		t.Fatal(err)
	}
	var raw string
	if _, err := p.goqu.From(p.workspaceTable("mcp_oauth_pending")).Select("payload").Where(goqu.Ex{"state_hash": hash}).ScanValContext(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	if !atcrypto.IsEncrypted(raw) || strings.Contains(raw, "private-verifier") {
		t.Fatal("pending verifier stored in plaintext")
	}
	otherSession := mcpPendingSession(t, p, service.DefaultWorkspaceID, u, "other-session")
	other := workspaceUser(t, p, "mcp-other")
	workspaceMember(t, p, ctx, service.DefaultWorkspaceID, other, "member")
	otherUser := mcpPendingSession(t, p, service.DefaultWorkspaceID, other, "other-user-session")
	w, err := p.CreateWorkspace(ctx, "Other", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(ctx, w.ID, u.ID, "owner-session")
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspace := service.WithAccessPrincipal(ctx, a)
	for _, foreign := range []context.Context{otherSession, otherUser, otherWorkspace} {
		got, err := p.TakeMCPOAuthPending(foreign, hash)
		if err != nil || got != nil {
			t.Fatalf("foreign state: %s %v", got, err)
		}
	}
	// Key rotation must not strand an authorization started on another replica.
	if err := p.RotateEncryptionKey(ctx, bytes.Repeat([]byte{2}, 32)); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := p.TakeMCPOAuthPending(ctx, hash)
			if err != nil {
				t.Error(err)
			}
			if got != nil {
				successes.Add(1)
				if !bytes.Equal(got, payload) {
					t.Errorf("payload changed: %s", got)
				}
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("state consumed %d times", successes.Load())
	}
}

func TestMCPOAuthPendingQuotaExpiryAndRevocation(t *testing.T) {
	p := newTestStore(t, bytes.Repeat([]byte{1}, 32))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "mcp-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpPendingSession(t, p, service.DefaultWorkspaceID, u, "owner-session")
	for i := range 8 {
		if err := p.SaveMCPOAuthPending(ctx, mcpPendingHash(fmt.Sprint(i)), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.SaveMCPOAuthPending(ctx, mcpPendingHash("overflow"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("pending quota bypassed")
	}
	expired := mcpPendingHash("0")
	if _, err := p.goqu.Update(p.workspaceTable("mcp_oauth_pending")).Set(goqu.Record{"expires_at": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"state_hash": expired}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := p.TakeMCPOAuthPending(ctx, expired); err != nil || got != nil {
		t.Fatalf("expired state consumed: %s %v", got, err)
	}
	if err := p.SaveMCPOAuthPending(ctx, mcpPendingHash("replacement"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.workspaceTable("auth_sessions")).Set(goqu.Record{"expires_at": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"hash": "owner-session"}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := p.TakeMCPOAuthPending(ctx, mcpPendingHash("1")); err == nil || got != nil {
		t.Fatalf("revoked session consumed state: %s %v", got, err)
	}
}
