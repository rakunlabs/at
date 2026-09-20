package postgres

import (
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func TestUserUsageWorkspaceScope(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Other", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	one := workspaceUser(t, p, "usage-one")
	two := workspaceUser(t, p, "usage-two")
	for _, event := range []struct {
		workspace, user, source string
		cost                    int
	}{
		{w.ID, one.ID, "chats", 10}, {w.ID, one.ID, "sessions", 20},
		{w.ID, two.ID, "chats", 5}, {w.ID, "", "", 2},
		{other.ID, two.ID, "sessions", 1000},
	} {
		_, err := p.goqu.Insert(p.tableCostEvents).Rows(goqu.Record{
			"id": ulid.Make().String(), "workspace_id": event.workspace, "agent_id": "",
			"user_id": event.user, "source": event.source, "provider": "p", "model": "m",
			"input_tokens": 10, "output_tokens": 5, "cache_read_tokens": 3, "cache_write_tokens": 2,
			"cost_cents": event.cost, "latency_ms": 50, "status": "ok",
			"created_at": time.Now().UTC(),
		}).Executor().ExecContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	scoped := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: one.ID, WorkspaceID: w.ID, Role: "admin", Grants: service.WorkspaceRoleGrants("admin")})
	rows, err := p.GetUsageGrouped(scoped, service.UsageFilter{}, "user", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Key != one.ID || rows[0].Label != "usage-one" || rows[0].CostCents != 30 || rows[0].TotalTokens != 40 || rows[0].TotalLatencyMs != 100 {
		t.Fatalf("wrong user grouping or scope: %+v", rows)
	}
	filter := service.UsageFilter{UserIDs: []string{one.ID}, Sources: []string{"sessions"}}
	sum, err := p.GetUsageSummary(scoped, filter)
	if err != nil || sum.RequestCount != 1 || sum.CostCents != 20 {
		t.Fatalf("filtered summary: %+v %v", sum, err)
	}
	series, err := p.GetUsageTimeSeries(scoped, filter, "day")
	if err != nil || len(series) != 1 || series[0].CostCents != 20 {
		t.Fatalf("filtered series: %+v %v", series, err)
	}
	sources, err := p.GetUsageGrouped(scoped, service.UsageFilter{UserIDs: []string{one.ID}}, "source", 0)
	if err != nil || len(sources) != 2 || sources[0].Key != "sessions" {
		t.Fatalf("source breakdown: %+v %v", sources, err)
	}
	unknown, err := p.GetUsageSummary(scoped, service.UsageFilter{UserIDs: []string{""}})
	if err != nil || unknown.RequestCount != 1 {
		t.Fatalf("historical attribution: %+v %v", unknown, err)
	}
	adminCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID, WorkspaceID: w.ID, PlatformAdmin: true})
	all, err := p.GetUsageSummary(adminCtx, service.UsageFilter{UserIDs: []string{two.ID}})
	if err != nil || all.CostCents != 1005 {
		t.Fatalf("platform scope: %+v %v", all, err)
	}
}

func TestUserUsageRecordRoundTrip(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	if err := p.RecordCostEvent(ctx, service.CostEvent{UserID: "deleted-account", Source: "chats", Provider: "p", Model: "m", InputTokens: 4}); err != nil {
		t.Fatal(err)
	}
	list, err := p.ListCostEvents(ctx, nil)
	if err != nil || len(list.Data) != 1 || list.Data[0].UserID != "deleted-account" || list.Data[0].Source != "chats" {
		t.Fatalf("round trip: %+v %v", list, err)
	}
	rows, err := p.GetUsageGrouped(t.Context(), service.UsageFilter{}, "user", 0)
	if err != nil || len(rows) != 1 || rows[0].Key != "deleted-account" || rows[0].Label != "" {
		t.Fatalf("missing account must retain accounting: %+v %v", rows, err)
	}
}
