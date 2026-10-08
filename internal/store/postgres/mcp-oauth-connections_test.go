package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func mcpOAuthCredential(access, refresh string) service.ConnectionCredentials {
	return service.ConnectionCredentials{MCPOAuth: &service.MCPOAuthCredential{
		AccessToken: access, RefreshToken: refresh, ExpiresAt: time.Now().Add(time.Hour),
		ClientID: "client", Issuer: "https://as.example", TokenEndpoint: "https://as.example/token",
		MCPURL: "https://mcp.example/mcp",
	}}
}

func TestPersonalConnectionIsolationPostgres(t *testing.T) {
	p := newTestStore(t, bytes.Repeat([]byte{3}, 32))
	platform, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "conn-admin", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platform.ID})
	w, err := p.CreateWorkspace(root, "Connections", platform.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := p.ResolveWorkspaceAccess(root, w.ID, platform.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := service.WithAccessPrincipal(root, a)
	alice := workspaceUser(t, p, "conn-alice")
	bob := workspaceUser(t, p, "conn-bob")
	viewer := workspaceUser(t, p, "conn-viewer")
	aliceCtx := workspaceMember(t, p, adminCtx, w.ID, alice, "member")
	bobCtx := workspaceMember(t, p, adminCtx, w.ID, bob, "member")
	viewerCtx := workspaceMember(t, p, adminCtx, w.ID, viewer, "viewer")

	personal, err := p.CreateConnection(aliceCtx, service.Connection{OwnerUserID: alice.ID, Provider: "mcp-example", Name: "mine", Credentials: mcpOAuthCredential("alice-access", "alice-refresh")})
	if err != nil {
		t.Fatal(err)
	}
	if personal.Scope != service.ConnectionScopePersonal {
		t.Fatalf("scope: %+v", personal)
	}
	// Ordinary members cannot create workspace connections or spoof an owner;
	// viewers (no connections.use) cannot create personal ones.
	if _, err := p.CreateConnection(bobCtx, service.Connection{Provider: "mcp-example", Name: "shared"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created workspace connection: %v", err)
	}
	if _, err := p.CreateConnection(bobCtx, service.Connection{OwnerUserID: alice.ID, Provider: "mcp-example", Name: "spoof"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("spoofed owner: %v", err)
	}
	if _, err := p.CreateConnection(viewerCtx, service.Connection{OwnerUserID: viewer.ID, Provider: "mcp-example", Name: "viewer"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("viewer created personal connection: %v", err)
	}
	shared, err := p.CreateConnection(adminCtx, service.Connection{Provider: "mcp-example", Name: "shared", Credentials: mcpOAuthCredential("shared-access", "shared-refresh")})
	if err != nil {
		t.Fatal(err)
	}
	// Bob may hold his own account under the same provider and name.
	if _, err := p.CreateConnection(bobCtx, service.Connection{OwnerUserID: bob.ID, Provider: "mcp-example", Name: "mine", Credentials: mcpOAuthCredential("bob-access", "bob-refresh")}); err != nil {
		t.Fatalf("same name for another owner: %v", err)
	}

	t.Run("visibility", func(t *testing.T) {
		for name, tc := range map[string]struct {
			ctx  context.Context
			want int
		}{"owner": {aliceCtx, 2}, "other member": {bobCtx, 2}, "platform admin": {adminCtx, 3}} {
			list, err := p.ListConnections(tc.ctx, nil)
			if err != nil || len(list.Data) != tc.want || list.Meta.Total != uint64(tc.want) {
				t.Fatalf("%s list: %+v %v", name, list, err)
			}
		}
		if got, err := p.GetConnection(bobCtx, personal.ID); err != nil || got != nil {
			t.Fatalf("foreign personal connection visible: %+v %v", got, err)
		}
		got, err := p.GetConnection(aliceCtx, personal.ID)
		if err != nil || got == nil || got.Credentials.MCPOAuth == nil || got.Credentials.MCPOAuth.AccessToken != "alice-access" {
			t.Fatalf("owner read: %+v %v", got, err)
		}
		// An administrator may see that the row exists, never its secret.
		got, err = p.GetConnection(adminCtx, personal.ID)
		if err != nil || got == nil || got.Credentials.MCPOAuth != nil {
			t.Fatalf("admin read personal credentials: %+v %v", got, err)
		}
		if _, err := p.GetWorkspaceAccessResource(bobCtx, "connections", personal.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("resource metadata: %v", err)
		}
	})

	t.Run("runtime use", func(t *testing.T) {
		if got, err := p.ResolveConnectionForUse(bobCtx, personal.ID); err != nil || got != nil {
			t.Fatalf("other member used personal connection: %+v %v", got, err)
		}
		if got, err := p.ResolveConnectionForUse(adminCtx, personal.ID); err != nil || got != nil {
			t.Fatalf("admin used personal connection: %+v %v", got, err)
		}
		got, err := p.ResolvePersonalConnectionForUse(bobCtx, "mcp-example")
		if err != nil || got == nil || got.OwnerUserID != bob.ID {
			t.Fatalf("personal lookup: %+v %v", got, err)
		}
		if got, err := p.ResolvePersonalConnectionForUse(adminCtx, "mcp-example"); err != nil || got != nil {
			t.Fatalf("admin personal lookup found another account: %+v %v", got, err)
		}
		if _, err := p.WithMCPOAuthTokens(bobCtx, personal.ID, func(*service.MCPOAuthCredential) error { return nil }); !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("foreign token use: %v", err)
		}
		if _, err := p.WithMCPOAuthTokens(adminCtx, personal.ID, func(*service.MCPOAuthCredential) error { return nil }); !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("admin token use: %v", err)
		}
		if got, err := p.WithMCPOAuthTokens(bobCtx, shared.ID, func(*service.MCPOAuthCredential) error { return nil }); err != nil || got.AccessToken != "shared-access" {
			t.Fatalf("shared token use: %+v %v", got, err)
		}
		// Another workspace never sees it.
		other, err := p.CreateWorkspace(root, "Other connections", platform.ID)
		if err != nil {
			t.Fatal(err)
		}
		oa, _, err := p.ResolveWorkspaceAccess(root, other.ID, alice.ID, "")
		if err == nil {
			if got, err := p.ResolveConnectionForUse(service.WithAccessPrincipal(root, oa), personal.ID); err != nil || got != nil {
				t.Fatalf("cross-workspace use: %+v %v", got, err)
			}
		}
	})

	t.Run("writes", func(t *testing.T) {
		if _, err := p.UpdateConnection(bobCtx, personal.ID, service.Connection{Provider: "mcp-example", Name: "stolen"}); !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("foreign update: %v", err)
		}
		if _, err := p.UpdateConnection(adminCtx, personal.ID, service.Connection{Provider: "mcp-example", Name: "admin"}); !errors.Is(err, service.ErrAccessDenied) {
			t.Fatalf("admin update personal: %v", err)
		}
		if _, err := p.UpdateConnection(bobCtx, shared.ID, service.Connection{Provider: "mcp-example", Name: "x"}); !errors.Is(err, service.ErrAccessDenied) {
			t.Fatalf("member updated workspace connection: %v", err)
		}
		updated, err := p.UpdateConnection(aliceCtx, personal.ID, service.Connection{OwnerUserID: bob.ID, Provider: "mcp-example", Name: "renamed", Credentials: mcpOAuthCredential("alice-access", "alice-refresh")})
		if err != nil || updated == nil || updated.OwnerUserID != alice.ID || updated.Name != "renamed" {
			t.Fatalf("owner must be immutable: %+v %v", updated, err)
		}
		if err := p.DeleteConnection(bobCtx, personal.ID); err != nil {
			t.Fatal(err)
		}
		if got, _ := p.GetConnection(aliceCtx, personal.ID); got == nil {
			t.Fatal("foreign delete removed a personal connection")
		}
	})

	t.Run("agent binding", func(t *testing.T) {
		_, err := p.CreateAgent(bobCtx, service.Agent{Name: "steal", OwnerUserID: bob.ID, Config: service.AgentConfig{Connections: map[string]string{"mcp-example": personal.ID}}})
		if !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("agent bound another account's connection: %v", err)
		}
	})

	t.Run("account deletion", func(t *testing.T) {
		if _, err := p.DeleteAuthUser(root, alice.ID); err != nil {
			t.Fatal(err)
		}
		count, err := p.goqu.From(p.tableConnections).Where(goqu.Ex{"owner_user_id": alice.ID}).CountContext(root)
		if err != nil || count != 0 {
			t.Fatalf("personal connections survived account deletion: %d %v", count, err)
		}
	})
}

func TestMCPOAuthTokensConcurrentRefreshPostgres(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	p := newTestStore(t, key)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "refresh-owner", PasswordHash: "hash", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: u.ID})
	a, _, err := p.ResolveWorkspaceAccess(root, service.DefaultWorkspaceID, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithAccessPrincipal(root, a)
	conn, err := p.CreateConnection(ctx, service.Connection{OwnerUserID: u.ID, Provider: "mcp-example", Name: "mine", Credentials: mcpOAuthCredential("stale", "refresh-1")})
	if err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int32
	rotate := func(c *service.MCPOAuthCredential) error {
		if c.AccessToken != "stale" {
			return nil // adopt what another caller rotated
		}
		refreshes.Add(1)
		time.Sleep(20 * time.Millisecond)
		c.AccessToken, c.RefreshToken, c.ExpiresAt = "fresh", "refresh-2", time.Now().Add(time.Hour)
		return nil
	}
	var wg sync.WaitGroup
	results := make([]string, 8)
	// Each caller takes its own pooled connection, as replicas would: the
	// serialization under test is the database row lock, not process state.
	for i := range results {
		wg.Go(func() {
			got, err := p.WithMCPOAuthTokens(ctx, conn.ID, rotate)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = got.AccessToken
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refreshed %d times", refreshes.Load())
	}
	for _, r := range results {
		if r != "fresh" {
			t.Fatalf("caller ended with %q", r)
		}
	}
	var raw string
	if _, err := p.goqu.From(p.tableConnections).Select("credentials").Where(goqu.Ex{"id": conn.ID}).ScanValContext(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	if !atcrypto.IsEncrypted(raw) || strings.Contains(raw, "refresh-2") {
		t.Fatal("rotated tokens stored in plaintext")
	}

	// A pinned endpoint cannot be rewritten through the callback.
	got, err := p.WithMCPOAuthTokens(ctx, conn.ID, func(c *service.MCPOAuthCredential) error {
		c.AccessToken, c.TokenEndpoint = "other", "https://evil.example/token"
		return nil
	})
	if err != nil || got.TokenEndpoint != "https://as.example/token" {
		t.Fatalf("token endpoint rewritten: %+v %v", got, err)
	}

	// invalid_grant is recorded, and nothing is returned to use.
	if _, err := p.WithMCPOAuthTokens(ctx, conn.ID, func(*service.MCPOAuthCredential) error { return service.ErrMCPOAuthReauthRequired }); !errors.Is(err, service.ErrMCPOAuthReauthRequired) {
		t.Fatalf("reauth: %v", err)
	}
	stored, err := p.GetConnection(ctx, conn.ID)
	if err != nil || !stored.Credentials.MCPOAuth.NeedsReauth || stored.Credentials.MCPOAuth.AccessToken != "" {
		t.Fatalf("reauth not recorded: %+v %v", stored.Credentials.MCPOAuth, err)
	}
}
