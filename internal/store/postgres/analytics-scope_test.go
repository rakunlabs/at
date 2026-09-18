package postgres

import (
	"testing"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func insertCostEvent(t *testing.T, p *Postgres, workspace, provider string, cents int) {
	t.Helper()
	if _, err := p.goqu.Insert(p.tableCostEvents).Rows(goqu.Record{
		"id": ulid.Make().String(),
		"agent_id": "", "workspace_id": workspace, "provider": provider, "model": "m1",
		"input_tokens": 10, "output_tokens": 5, "cost_cents": cents,
		"status": "ok", "latency_ms": 12, "created_at": "2026-09-01T00:00:00Z",
	}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func insertLLMCall(t *testing.T, p *Postgres, workspace, traceID string) string {
	t.Helper()
	id := ulid.Make().String()
	if _, err := p.goqu.Insert(p.tableLLMCalls).Rows(goqu.Record{
		"id": id, "workspace_id": workspace, "observation_type": "generation",
		"trace_id": traceID, "source": "gateway", "level": "default", "status": "ok",
		"input_tokens": 10, "output_tokens": 5, "created_at": "2026-09-01T00:00:00Z",
	}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return id
}

// The analytics surfaces (Usage, Traces) became workspace-admitted: a scoped
// member must see only their own workspace's rows, while the installation
// administrator keeps the installation-wide view the dashboards always served.
// The role ladder hands usage.read and traces.read out at admin rank only, so
// a member reaches them through a bundle.
func TestWorkspacePostgresAnalyticsScope(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Beta", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	member := workspaceUser(t, p, "analytics-member")
	workspaceMember(t, p, ctx, w.ID, member, "viewer")
	bundle, err := p.SavePermission(ctx, service.PermissionBundle{Key: "analytics", Keys: []string{"usage.read", "traces.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SetUserPermissions(ctx, member.ID, []string{bundle.ID}); err != nil {
		t.Fatal(err)
	}
	memberPrincipal, _, err := p.ResolveWorkspaceAccess(t.Context(), w.ID, member.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), memberPrincipal)
	alphaCost := 100
	insertCostEvent(t, p, w.ID, "alpha-provider", alphaCost)
	insertCostEvent(t, p, w.ID, "alpha-provider", 25)
	insertCostEvent(t, p, other.ID, "beta-provider", 700)
	alphaTrace := insertLLMCall(t, p, w.ID, "tr-alpha")
	betaTrace := insertLLMCall(t, p, other.ID, "tr-beta")

	// A scoped member sees only their own workspace's spend.
	sum, err := p.GetUsageSummary(memberCtx, service.UsageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.RequestCount != 2 || sum.CostCents != 125 {
		t.Fatalf("member usage summary leaked workspaces: %+v", sum)
	}
	// The installation administrator keeps the installation-wide dashboard.
	adminCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID, WorkspaceID: "legacy-default", PlatformAdmin: true})
	adminSum, err := p.GetUsageSummary(adminCtx, service.UsageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if adminSum.RequestCount != 3 || adminSum.CostCents != 825 {
		t.Fatalf("admin usage summary lost rows: %+v", adminSum)
	}
	// Traces: the list already scopes through businessReadScope; the detail
	// and the aggregates must agree with it rather than mix scopes.
	memberTrace, err := p.GetLLMCall(memberCtx, alphaTrace)
	if err != nil || memberTrace == nil {
		t.Fatalf("member trace detail in own workspace: %+v %v", memberTrace, err)
	}
	foreign, err := p.GetLLMCall(memberCtx, betaTrace)
	if err != nil || foreign != nil {
		t.Fatalf("member trace detail leaked a foreign workspace: %+v %v", foreign, err)
	}
	traces, err := p.ListLLMCallTraces(memberCtx, nil)
	if err != nil || len(traces.Data) != 1 || traces.Data[0].TraceID != "tr-alpha" {
		t.Fatalf("member trace aggregates: %+v %v", traces, err)
	}
	conv, err := p.ListLLMCallConversations(memberCtx, nil)
	if err != nil || conv.Meta.Total != 1 {
		t.Fatalf("member conversations: %+v %v", conv, err)
	}
}

// The analytics capabilities ride the role ladder at admin rank: a member or
// viewer does not inherit them, while a workspace admin does.
func TestWorkspaceAnalyticsCapabilityRank(t *testing.T) {
	for _, role := range []string{"viewer", "member"} {
		for _, g := range service.WorkspaceRoleGrants(role) {
			if g.Capability == "usage.read" || g.Capability == "traces.read" {
				t.Fatalf("%s inherits the analytics capability %s", role, g.Capability)
			}
		}
	}
	for _, want := range []string{"usage.read", "traces.read"} {
		found := false
		for _, g := range service.WorkspaceRoleGrants("admin") {
			if g.Capability == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("admin role lacks %s", want)
		}
	}
}
