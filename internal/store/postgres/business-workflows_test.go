package postgres

import (
	"errors"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestBusinessWorkflowsVersionsAndProviderGrants(t *testing.T) {
	p, ctx, a, admin := workspaceFixture(t)
	b, err := p.CreateWorkspace(ctx, "B", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	principalB, _, err := p.ResolveWorkspaceAccess(ctx, b.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctxB := service.WithAccessPrincipal(ctx, principalB)
	pa, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "same", Config: config.LLMConfig{Type: "openai", APIKey: "secret-a", Model: "model"}})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := p.CreateProvider(ctxB, service.ProviderRecord{Key: "same", Config: config.LLMConfig{Type: "openai", APIKey: "secret-b", Model: "model"}})
	if err != nil {
		t.Fatal(err)
	}
	if pa.WorkspaceID == pb.WorkspaceID {
		t.Fatal("provider namespace lost")
	}
	if v, e := p.GetProvider(ctx, "same"); e != nil || v.Config.APIKey != "secret-a" {
		t.Fatalf("provider lookup A: %+v %v", v, e)
	}
	if v, e := p.GetProvider(ctxB, "same"); e != nil || v.Config.APIKey != "secret-b" {
		t.Fatalf("provider lookup B: %+v %v", v, e)
	}
	u := workspaceUser(t, p, "model-member")
	member := workspaceMember(t, p, ctx, a.ID, u, "member")
	if v, e := p.GetProvider(member, "same"); e != nil || v.Config.APIKey != "" {
		t.Fatalf("credential DTO leaked: %+v %v", v, e)
	}
	a.ExecutionEnabled = true
	if _, err = p.UpdateWorkspace(ctx, *a); err != nil {
		t.Fatal(err)
	}
	if v, e := p.ResolveWorkspaceProviderForUse(member, "same", "model"); e != nil || v.Config.APIKey != "secret-a" {
		t.Fatalf("own provider use: %+v %v", v, e)
	}
	legacy := service.WithLegacyWorkspaceAccess(t.Context())
	platform, err := p.CreateProvider(legacy, service.ProviderRecord{Key: "shared", Config: config.LLMConfig{Type: "openai", APIKey: "platform-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(member, "shared", "model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("ungranted platform provider: %v", err)
	}
	grant := service.WorkspaceProviderGrant{ProviderID: platform.ID, ModelPatterns: []string{"model*"}}
	if err = p.SaveWorkspaceProviderGrant(member, grant); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created platform use grant: %v", err)
	}
	if err = p.SaveWorkspaceProviderGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(member, "shared", "other"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("model pattern bypass: %v", err)
	}
	if v, e := p.ResolveWorkspaceProviderForUse(member, "shared", "model"); e != nil || v.ID != platform.ID {
		t.Fatalf("granted use: %+v %v", v, e)
	}
	if v, e := p.GetProvider(member, "shared"); e != nil || v != nil {
		t.Fatalf("use grant exposed provider credentials: %+v %v", v, e)
	}
	if err = p.DeleteWorkspaceProviderGrant(ctx, platform.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(member, "shared", "model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("revoked model grant survived: %v", err)
	}
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "input", Type: "input"}, {ID: "llm", Type: "llm_call", Data: map[string]any{"provider": "same"}}}, Edges: []service.WorkflowEdge{{Source: "input", Target: "llm"}}}
	wa, err := p.CreateWorkflow(ctx, service.Workflow{Name: "same", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	wb, err := p.CreateWorkflow(ctxB, service.Workflow{Name: "same", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	if v, e := p.GetWorkflowByName(ctx, "same"); e != nil || v.ID != wa.ID {
		t.Fatalf("workflow name escaped scope: %+v %v", v, e)
	}
	if v, e := p.GetWorkflow(ctx, wb.ID); e != nil || v != nil {
		t.Fatalf("workflow ID escaped scope: %+v %v", v, e)
	}
	if _, err = p.CreateWorkflowVersion(ctx, service.WorkflowVersion{WorkflowID: wb.ID, Graph: graph}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign workflow version: %v", err)
	}
	var wg sync.WaitGroup
	versions := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := p.CreateWorkflowVersion(ctx, service.WorkflowVersion{WorkflowID: wa.ID, Graph: graph})
			if e != nil {
				t.Errorf("version create: %v", e)
				return
			}
			versions <- v.Version
		}()
	}
	wg.Wait()
	close(versions)
	seen := map[int]bool{}
	for v := range versions {
		seen[v] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("version serialization: %v", seen)
	}
	if err = p.SetActiveVersion(ctx, wa.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = p.SetActiveVersion(ctx, wa.ID, 99); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("missing active version: %v", err)
	}
	if err = p.SetActiveVersion(ctx, wb.ID, 1); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign active version: %v", err)
	}
	tr, err := p.CreateTrigger(ctx, service.Trigger{WorkflowID: wa.ID, Type: "cron", Enabled: true, EntryNodeID: "input"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpdateTrigger(ctx, tr.ID, service.Trigger{WorkflowID: wb.ID, Type: "http"}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign trigger target: %v", err)
	}
	if _, err = p.CreateTrigger(ctx, service.Trigger{WorkflowID: wa.ID, Type: "http", EntryNodeID: "unknown"}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("unknown entry node: %v", err)
	}
	triggers, err := p.ListAllTriggers(ctxB)
	if err != nil || len(triggers) != 0 {
		t.Fatalf("trigger list leaked: %+v %v", triggers, err)
	}
	nc, err := p.CreateNodeConfig(ctx, service.NodeConfig{Name: "same", Type: "email", Data: `{"password":"private"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.CreateNodeConfig(ctxB, service.NodeConfig{Name: "same", Type: "email", Data: `{}`}); err != nil {
		t.Fatal(err)
	}
	if v, e := p.GetNodeConfig(member, nc.ID); e != nil || v.Data != "{}" {
		t.Fatalf("node-config credentials leaked: %+v %v", v, e)
	}
	if v, e := p.GetNodeConfig(ctxB, nc.ID); e != nil || v != nil {
		t.Fatalf("foreign node-config: %+v %v", v, e)
	}
	bad := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "child", Type: "workflow_call", Data: map[string]any{"workflow_id": wb.ID}}}}
	if _, err = p.UpdateWorkflow(ctx, wa.ID, service.Workflow{Name: "same", Graph: bad}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign graph reference: %v", err)
	}
}
