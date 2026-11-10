package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Turning on act_for_users is workspace administration: a member holding
// tokens.write through a bundle may manage tokens but not create or toggle
// one that acts on behalf of members.
func TestAgentGatewayTokenAdministration(t *testing.T) {
	p, ctx, workspace, _ := workspaceFixture(t)
	alice := workspaceUser(t, p, "gateway-alice")
	aliceCtx := workspaceMember(t, p, ctx, workspace.ID, alice, "member")
	adminCtx := workspaceMember(t, p, ctx, workspace.ID, workspaceUser(t, p, "gateway-admin"), "admin")
	bundle, err := p.SavePermission(ctx, service.PermissionBundle{Key: "token-management", Keys: []string{"tokens.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetUserPermissions(ctx, alice.ID, []string{bundle.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateAPIToken(aliceCtx, service.APIToken{Name: "acting", ActForUsers: true}, "acting-hash"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created an act_for_users token: %v", err)
	}
	plain, err := p.CreateAPIToken(aliceCtx, service.APIToken{Name: "plain"}, "plain-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpdateAPIToken(aliceCtx, plain.ID, service.APIToken{Name: "plain", ActForUsers: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member enabled act_for_users: %v", err)
	}
	acting, err := p.CreateAPIToken(adminCtx, service.APIToken{Name: "site", ActForUsers: true, AllowedAgentsMode: "list", AllowedAgents: []string{"shop"}}, "site-hash")
	if err != nil || !acting.ActForUsers || acting.AllowedAgentsMode != "list" || len(acting.AllowedAgents) != 1 {
		t.Fatalf("admin token: %+v %v", acting, err)
	}
	// An older client omits the fields: they are kept, and the member's
	// unrelated edit is not refused.
	kept, err := p.UpdateAPIToken(aliceCtx, acting.ID, service.APIToken{Name: "site renamed", KeepAgentGateway: true})
	if err != nil || !kept.ActForUsers || len(kept.AllowedAgents) != 1 {
		t.Fatalf("keep agent-gateway fields: %+v %v", kept, err)
	}
	byID, err := p.GetAPITokenByID(t.Context(), acting.ID)
	if err != nil || byID == nil || !byID.ActForUsers || byID.WorkspaceID != workspace.ID {
		t.Fatalf("by id: %+v %v", byID, err)
	}
}
