package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func TestAPITokenOwnershipPostgres(t *testing.T) {
	p, ctx, workspace, platform := workspaceFixture(t)
	alice := workspaceUser(t, p, "token-alice")
	bob := workspaceUser(t, p, "token-bob")
	admin := workspaceUser(t, p, "token-workspace-admin")
	aliceCtx := workspaceMember(t, p, ctx, workspace.ID, alice, "member")
	bobCtx := workspaceMember(t, p, ctx, workspace.ID, bob, "member")
	adminCtx := workspaceMember(t, p, ctx, workspace.ID, admin, "admin")
	bundle, err := p.SavePermission(ctx, service.PermissionBundle{Key: "token-management", Keys: []string{"tokens.write"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*service.AuthUser{alice, bob} {
		if err := p.SetUserPermissions(ctx, user.ID, []string{bundle.ID}); err != nil {
			t.Fatal(err)
		}
	}
	personal, err := p.CreateAPIToken(aliceCtx, service.APIToken{Name: "personal", OwnerUserID: alice.ID}, "personal-hash")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := p.CreateAPIToken(aliceCtx, service.APIToken{Name: "workspace"}, "workspace-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateAPIToken(bobCtx, service.APIToken{Name: "spoof", OwnerUserID: alice.ID}, "spoof"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("spoof owner: %v", err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want int
	}{{"owner", aliceCtx, 2}, {"other member", bobCtx, 1}, {"workspace admin", adminCtx, 2}, {"platform admin", ctx, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			list, err := p.ListAPITokens(tc.ctx, nil)
			if err != nil || len(list.Data) != tc.want || list.Meta.Total != uint64(tc.want) {
				t.Fatalf("list: %+v %v", list, err)
			}
		})
	}
	q, err := query.Parse("owner_user_id=" + alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if list, err := p.ListAPITokens(bobCtx, q); err != nil || len(list.Data) != 0 || list.Meta.Total != 0 {
		t.Fatalf("filter bypassed ownership: %+v %v", list, err)
	}
	for name, call := range map[string]func() error{
		"update": func() error {
			_, err := p.UpdateAPIToken(bobCtx, personal.ID, service.APIToken{Name: "stolen"})
			return err
		},
		"delete": func() error { return p.DeleteAPIToken(bobCtx, personal.ID) },
		"pause":  func() error { return p.SetAPITokenPaused(bobCtx, personal.ID, true, "bob") },
		"rotate": func() error {
			_, err := p.RotateAPIToken(bobCtx, personal.ID, "stolen-hash", "stolen", "bob")
			return err
		},
		"usage":             func() error { return p.AuthorizeAPITokenManagement(bobCtx, personal.ID, "tokens.read") },
		"reset usage":       func() error { return p.AuthorizeAPITokenManagement(bobCtx, personal.ID, "tokens.write") },
		"resource metadata": func() error { _, err := p.GetWorkspaceAccessResource(bobCtx, "tokens", personal.ID); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, service.ErrAccessResourceNotFound) {
				t.Fatalf("foreign personal token admitted: %v", err)
			}
		})
	}
	for _, actor := range []context.Context{aliceCtx, adminCtx} {
		updated, err := p.UpdateAPIToken(actor, personal.ID, service.APIToken{Name: "renamed", OwnerUserID: bob.ID})
		if err != nil || updated.OwnerUserID != alice.ID {
			t.Fatalf("update must preserve owner: %+v %v", updated, err)
		}
		if err := p.SetAPITokenPaused(actor, personal.ID, true, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	rotated, err := p.RotateAPIToken(adminCtx, personal.ID, "rotated-hash", "rotated", "admin")
	if err != nil || rotated.OwnerUserID != alice.ID || !rotated.Paused {
		t.Fatalf("rotate ownership: %+v %v", rotated, err)
	}
	if got, err := p.GetAPITokenByHash(t.Context(), "rotated-hash"); err != nil || got.OwnerUserID != alice.ID {
		t.Fatalf("gateway lookup: %+v %v", got, err)
	}
	other, err := p.CreateWorkspace(ctx, "other-token-workspace", platform.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherPrincipal, _, err := p.ResolveWorkspaceAccess(ctx, other.ID, platform.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := workspaceMember(t, p, service.WithAccessPrincipal(ctx, otherPrincipal), other.ID, admin, "admin")
	if err := p.DeleteAPIToken(otherCtx, personal.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("cross-workspace admin: %v", err)
	}
	if err := p.DeleteAPIToken(adminCtx, personal.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteAPIToken(bobCtx, shared.ID); err != nil {
		t.Fatalf("workspace token management: %v", err)
	}
}
