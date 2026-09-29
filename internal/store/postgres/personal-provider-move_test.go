package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// A personal provider moved into a workspace must become an ordinary
// workspace provider: listed there, gone from the owner's personal list,
// resolvable by key, and still referenced by what used it before.
func TestMovePersonalProviderToWorkspace(t *testing.T) {
	p, adminCtx, workspace, _ := workspaceFixture(t)
	workspace.ExecutionEnabled = true
	if _, err := p.UpdateWorkspace(adminCtx, *workspace); err != nil {
		t.Fatal(err)
	}
	alice := workspaceUser(t, p, "move-provider-alice")
	bob := workspaceUser(t, p, "move-provider-bob")
	aliceCtx := workspaceMember(t, p, adminCtx, workspace.ID, alice, "admin")
	bobCtx := workspaceMember(t, p, adminCtx, workspace.ID, bob, "member")

	cfg := config.LLMConfig{Type: "openai", Model: "m1", Models: []string{"m1", "m2"}, APIKey: "alice-secret"}
	personal, err := p.CreatePersonalProvider(aliceCtx, service.ProviderRecord{Key: "vertex-main", Config: cfg})
	if err != nil {
		t.Fatalf("create personal provider: %v", err)
	}
	// A previous workspace-scope publication must not survive the move.
	if _, err = p.SetPersonalProviderScope(aliceCtx, personal.ID, service.ProviderScopeWorkspace, alice.Username); err != nil {
		t.Fatal(err)
	}

	agent, err := p.CreateAgent(aliceCtx, service.Agent{Name: "uses-personal", Config: service.AgentConfig{Provider: personal.Reference, Model: "m1"}})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	workflow, err := p.CreateWorkflow(aliceCtx, service.Workflow{Name: "wf-personal", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "a", Type: "llm_call", Data: map[string]any{"provider": personal.Reference, "model": "m1"}},
		{ID: "b", Type: "llm_call", Data: map[string]any{"template": "keep"}},
	}}})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	// Members without provider management cannot move a provider in.
	bobProvider, err := p.CreatePersonalProvider(bobCtx, service.ProviderRecord{Key: "bob", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.MovePersonalProviderToWorkspace(bobCtx, bobProvider.ID, bob.Username); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member move: %v", err)
	}
	// Nobody moves another account's credential.
	if _, err := p.MovePersonalProviderToWorkspace(adminCtx, personal.ID, "admin"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign move: %v", err)
	}

	moved, err := p.MovePersonalProviderToWorkspace(aliceCtx, personal.ID, alice.Username)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if moved.ID != personal.ID || moved.Key != "vertex-main" || moved.WorkspaceID != workspace.ID || moved.OwnerUserID != "" || moved.Config.APIKey != "alice-secret" {
		t.Fatalf("moved record: %+v", moved)
	}

	if list, err := p.ListPersonalProviders(aliceCtx, nil); err != nil || len(list.Data) != 0 {
		t.Fatalf("personal list after move: %+v %v", list, err)
	}
	list, err := p.ListProviders(aliceCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, rec := range list.Data {
		listed = listed || rec.ID == personal.ID
	}
	if !listed {
		t.Fatalf("moved provider missing from workspace list: %+v", list.Data)
	}

	if resolved, err := p.ResolveWorkspaceProviderForUse(bobCtx, "vertex-main", "m2"); err != nil || resolved.ID != personal.ID {
		t.Fatalf("resolve moved provider by key: %+v %v", resolved, err)
	}
	if _, err := p.ResolveWorkspaceProviderForUse(aliceCtx, personal.Reference, "m1"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("old personal reference still resolves: %v", err)
	}

	gotAgent, err := p.GetAgent(aliceCtx, agent.ID)
	if err != nil || gotAgent.Config.Provider != "vertex-main" {
		t.Fatalf("agent reference not rewritten: %+v %v", gotAgent, err)
	}
	gotWorkflow, err := p.GetWorkflow(aliceCtx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotWorkflow.Graph.Nodes[0].Data["provider"]; got != "vertex-main" {
		t.Fatalf("workflow node reference = %v", got)
	}
	if got := gotWorkflow.Graph.Nodes[1].Data["template"]; got != "keep" || gotWorkflow.Graph.Nodes[0].ID != "a" {
		t.Fatalf("unrelated workflow node changed: %+v", gotWorkflow.Graph.Nodes)
	}

	// A key already taken in the workspace is a conflict, and nothing moves.
	second, err := p.CreatePersonalProvider(aliceCtx, service.ProviderRecord{Key: "vertex-main", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.MovePersonalProviderToWorkspace(aliceCtx, second.ID, alice.Username); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("colliding move: %v", err)
	}
	if still, err := p.GetPersonalProvider(aliceCtx, second.ID); err != nil || still == nil {
		t.Fatalf("colliding provider was not left personal: %+v %v", still, err)
	}
}
