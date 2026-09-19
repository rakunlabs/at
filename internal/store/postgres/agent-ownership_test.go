package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Agents come in three tiers: workspace (owner_user_id = ”), personal
// (owned, visible and writable only for the owner and platform
// administrators) and global (Default-workspace rows carrying
// config.shared_with_all_workspaces, readable from every workspace). Chat
// sessions are per-account: a member lists only sessions they own, while a
// platform administrator additionally sees ownerless bot/legacy rows.
func TestAgentOwnershipTiersPostgres(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	alice := workspaceUser(t, p, "agent-owner-alice")
	bob := workspaceUser(t, p, "agent-owner-bob")
	aliceCtx := workspaceMember(t, p, ctx, w.ID, alice, "member")
	bobCtx := workspaceMember(t, p, ctx, w.ID, bob, "member")

	shared, err := p.CreateAgent(aliceCtx, service.Agent{Name: "shared-agent"})
	if err != nil {
		t.Fatalf("create workspace agent: %v", err)
	}
	if shared.Scope != service.AgentScopeWorkspace {
		t.Fatalf("workspace agent scope = %q", shared.Scope)
	}

	personal, err := p.CreateAgent(aliceCtx, service.Agent{Name: "alice-personal", OwnerUserID: alice.ID})
	if err != nil {
		t.Fatalf("create personal agent: %v", err)
	}
	if personal.Scope != service.AgentScopePersonal {
		t.Fatalf("personal agent scope = %q", personal.Scope)
	}

	// A member cannot mint an agent owned by somebody else.
	if _, err := p.CreateAgent(bobCtx, service.Agent{Name: "spoof", OwnerUserID: alice.ID}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign-owner create: %v", err)
	}

	// Visibility: the owner and the administrator see the personal agent,
	// another member does not — in the list, and via Get.
	seen := func(list *service.ListResult[service.Agent], id string) bool {
		for _, a := range list.Data {
			if a.ID == id {
				return true
			}
		}
		return false
	}
	aliceList, err := p.ListAgents(aliceCtx, nil)
	if err != nil || !seen(aliceList, personal.ID) || !seen(aliceList, shared.ID) {
		t.Fatalf("owner list: %+v %v", aliceList, err)
	}
	bobList, err := p.ListAgents(bobCtx, nil)
	if err != nil || seen(bobList, personal.ID) || !seen(bobList, shared.ID) {
		t.Fatalf("member list leaked a personal agent: %+v %v", bobList, err)
	}
	adminList, err := p.ListAgents(ctx, nil)
	if err != nil || !seen(adminList, personal.ID) {
		t.Fatalf("administrator list: %+v %v", adminList, err)
	}
	if got, err := p.GetAgent(bobCtx, personal.ID); err != nil || got != nil {
		t.Fatalf("foreign personal agent resolvable: %+v %v", got, err)
	}

	// Writes: only the owner (or an administrator) may update/delete.
	if _, err := p.UpdateAgent(bobCtx, personal.ID, service.Agent{Name: "hijack"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign personal update: %v", err)
	}
	if err := p.DeleteAgent(bobCtx, personal.ID); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign personal delete: %v", err)
	}
	if updated, err := p.UpdateAgent(aliceCtx, personal.ID, service.Agent{Name: "alice-renamed"}); err != nil || updated == nil || updated.Name != "alice-renamed" {
		t.Fatalf("owner update: %+v %v", updated, err)
	}

	// Global tier: only a platform administrator in the Default workspace.
	if _, err := p.CreateAgent(bobCtx, service.Agent{Name: "member-global", Config: service.AgentConfig{SharedWithAllWorkspaces: true}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member global create: %v", err)
	}
	adminDefault, _, err := p.ResolveWorkspaceAccess(t.Context(), service.DefaultWorkspaceID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminDefaultCtx := service.WithAccessPrincipal(t.Context(), adminDefault)
	global, err := p.CreateAgent(adminDefaultCtx, service.Agent{Name: "global-agent", Config: service.AgentConfig{SharedWithAllWorkspaces: true}})
	if err != nil {
		t.Fatalf("create global agent: %v", err)
	}
	if global.Scope != service.AgentScopeGlobal {
		t.Fatalf("global agent scope = %q", global.Scope)
	}
	// Personal and global are mutually exclusive.
	if _, err := p.CreateAgent(adminDefaultCtx, service.Agent{Name: "both", OwnerUserID: admin.ID, Config: service.AgentConfig{SharedWithAllWorkspaces: true}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("personal+global create: %v", err)
	}

	// The global agent is readable from another workspace…
	if got, err := p.GetAgent(bobCtx, global.ID); err != nil || got == nil || got.Scope != service.AgentScopeGlobal {
		t.Fatalf("global agent not resolvable cross-workspace: %+v %v", got, err)
	}
	if list, err := p.ListAgents(bobCtx, nil); err != nil || !seen(list, global.ID) {
		t.Fatalf("global agent missing from member list: %v", err)
	}
	// …but not writable from there: the write predicate stays workspace-bound.
	if updated, err := p.UpdateAgent(bobCtx, global.ID, service.Agent{Name: "steal"}); err != nil || updated != nil {
		t.Fatalf("cross-workspace global update: %+v %v", updated, err)
	}
	// And inside the Default workspace it stays platform administration: a
	// plain member there cannot edit or unshare it.
	carol := workspaceUser(t, p, "agent-owner-carol")
	carolCtx := workspaceMember(t, p, adminDefaultCtx, service.DefaultWorkspaceID, carol, "member")
	if _, err := p.UpdateAgent(carolCtx, global.ID, service.Agent{Name: "unshare"}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member edit of global agent: %v", err)
	}
}

func TestChatSessionOwnerScopePostgres(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	alice := workspaceUser(t, p, "chat-owner-alice")
	bob := workspaceUser(t, p, "chat-owner-bob")
	aliceCtx := workspaceMember(t, p, ctx, w.ID, alice, "member")
	bobCtx := workspaceMember(t, p, ctx, w.ID, bob, "member")

	mkSession := func(owner, name string) *service.ChatSession {
		t.Helper()
		s, err := p.CreateChatSession(t.Context(), service.ChatSession{
			WorkspaceID: w.ID, OwnerUserID: owner, AgentID: "agent", Name: name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	aliceSession := mkSession(alice.ID, "alice chat")
	mkSession(bob.ID, "bob chat")
	mkSession("", "bot session") // platform/bot row: no browser owner

	list, err := p.ListChatSessions(aliceCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != aliceSession.ID || list.Data[0].OwnerUserID != alice.ID {
		t.Fatalf("member session list not owner-scoped: %+v", list.Data)
	}
	if list, err = p.ListChatSessions(bobCtx, nil); err != nil || len(list.Data) != 1 || list.Data[0].Name != "bob chat" {
		t.Fatalf("member session list not owner-scoped: %+v %v", list.Data, err)
	}
	// The administrator sees everything in the workspace, including the
	// ownerless bot row.
	if list, err = p.ListChatSessions(ctx, nil); err != nil || len(list.Data) != 3 {
		t.Fatalf("administrator session list: %d %v", len(list.Data), err)
	}
	// The stored row carries workspace and owner for the handler checks.
	got, err := p.GetChatSession(t.Context(), aliceSession.ID)
	if err != nil || got == nil || got.WorkspaceID != w.ID || got.OwnerUserID != alice.ID {
		t.Fatalf("session attribution: %+v %v", got, err)
	}
}
