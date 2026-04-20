package sqlite3

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// seedCostEvents inserts a handful of cost events spanning two days, two
// providers, two models, and a mix of ok/error statuses so the aggregation
// tests have something predictable to assert on.
func seedCostEvents(t *testing.T, store *SQLite) {
	t.Helper()
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []service.CostEvent{
		// day 1: 2 openai gpt-4o ok calls
		{AgentID: "a1", Provider: "openai", Model: "gpt-4o", InputTokens: 100, OutputTokens: 50, CostCents: 0.5, LatencyMs: 1000, Status: "ok"},
		{AgentID: "a1", Provider: "openai", Model: "gpt-4o", InputTokens: 200, OutputTokens: 100, CostCents: 1.0, LatencyMs: 2000, Status: "ok"},
		// day 1: 1 anthropic claude ok
		{AgentID: "a2", Provider: "anthropic", Model: "claude-haiku-4", InputTokens: 50, OutputTokens: 30, CostCents: 0.2, LatencyMs: 500, Status: "ok"},
		// day 1: 1 openai gpt-4o error (rate_limit)
		{AgentID: "a1", Provider: "openai", Model: "gpt-4o", InputTokens: 0, OutputTokens: 0, CostCents: 0, LatencyMs: 100, Status: "error", ErrorCode: "rate_limit", ErrorMessage: "rate limited"},
	}

	// We rely on RecordCostEvent setting created_at to now; override by
	// tweaking the DB afterward via direct update so we can control timestamps.
	for i, e := range events {
		if err := store.RecordCostEvent(ctx, e); err != nil {
			t.Fatalf("RecordCostEvent[%d]: %v", i, err)
		}
	}

	// Re-stamp timestamps deterministically.
	table := store.tableCostEvents.GetTable()
	rows, err := store.db.QueryContext(ctx, "SELECT id FROM "+table+" ORDER BY id ASC")
	if err != nil {
		t.Fatalf("select ids: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	if len(ids) != len(events) {
		t.Fatalf("expected %d seeded rows, got %d", len(events), len(ids))
	}
	// Assign created_at in order: 3 on day 1 (base), 1 on day 2 (base+24h).
	offsets := []time.Duration{0, 1 * time.Hour, 2 * time.Hour, 26 * time.Hour}
	for i, id := range ids {
		ts := base.Add(offsets[i]).Format(time.RFC3339)
		if _, err := store.db.ExecContext(ctx, "UPDATE "+table+" SET created_at = ? WHERE id = ?", ts, id); err != nil {
			t.Fatalf("re-stamp: %v", err)
		}
	}
}

func TestUsage_GetUsageSummary(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)
	seedCostEvents(t, store)

	got, err := store.GetUsageSummary(ctx, service.UsageFilter{})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}

	if got.RequestCount != 4 {
		t.Errorf("RequestCount: got %d, want 4", got.RequestCount)
	}
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount: got %d, want 1", got.ErrorCount)
	}
	if got.InputTokens != 350 {
		t.Errorf("InputTokens: got %d, want 350", got.InputTokens)
	}
	if got.OutputTokens != 180 {
		t.Errorf("OutputTokens: got %d, want 180", got.OutputTokens)
	}
	if got.TotalTokens != 530 {
		t.Errorf("TotalTokens: got %d, want 530", got.TotalTokens)
	}
	// 0.5 + 1.0 + 0.2 = 1.7 cents
	if diff := got.CostCents - 1.7; diff > 0.01 || diff < -0.01 {
		t.Errorf("CostCents: got %f, want ~1.7", got.CostCents)
	}
}

func TestUsage_StatusFilter(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)
	seedCostEvents(t, store)

	got, err := store.GetUsageSummary(ctx, service.UsageFilter{Status: "error"})
	if err != nil {
		t.Fatalf("GetUsageSummary(error): %v", err)
	}
	if got.RequestCount != 1 || got.ErrorCount != 1 {
		t.Errorf("error filter: got req=%d err=%d, want 1/1", got.RequestCount, got.ErrorCount)
	}
}

func TestUsage_GetUsageGrouped_Provider(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)
	seedCostEvents(t, store)

	rows, err := store.GetUsageGrouped(ctx, service.UsageFilter{}, "provider", 0)
	if err != nil {
		t.Fatalf("GetUsageGrouped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(rows))
	}

	by := map[string]service.UsageSummary{}
	for _, r := range rows {
		by[r.Key] = r
	}
	openai := by["openai"]
	if openai.RequestCount != 3 {
		t.Errorf("openai RequestCount: got %d, want 3", openai.RequestCount)
	}
	if openai.ErrorCount != 1 {
		t.Errorf("openai ErrorCount: got %d, want 1", openai.ErrorCount)
	}
	anthropic := by["anthropic"]
	if anthropic.RequestCount != 1 {
		t.Errorf("anthropic RequestCount: got %d, want 1", anthropic.RequestCount)
	}
}

func TestUsage_GetUsageGrouped_InvalidGroupBy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	_, err := store.GetUsageGrouped(ctx, service.UsageFilter{}, "nonsense", 0)
	if err == nil {
		t.Fatal("expected error for invalid group_by, got nil")
	}
}

func TestUsage_GetUsageTimeSeries_Day(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)
	seedCostEvents(t, store)

	points, err := store.GetUsageTimeSeries(ctx, service.UsageFilter{}, "day")
	if err != nil {
		t.Fatalf("GetUsageTimeSeries: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(points))
	}
	// Seeded offsets place the first 3 events on day 1 and the error on day 2.
	if points[0].RequestCount != 3 {
		t.Errorf("day1 RequestCount: got %d, want 3", points[0].RequestCount)
	}
	if points[0].ErrorCount != 0 {
		t.Errorf("day1 ErrorCount: got %d, want 0", points[0].ErrorCount)
	}
	if points[1].RequestCount != 1 {
		t.Errorf("day2 RequestCount: got %d, want 1", points[1].RequestCount)
	}
	if points[1].ErrorCount != 1 {
		t.Errorf("day2 ErrorCount: got %d, want 1", points[1].ErrorCount)
	}
}

func TestUsage_DateRangeFilter(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)
	seedCostEvents(t, store)

	// "To" excluding day 2.
	got, err := store.GetUsageSummary(ctx, service.UsageFilter{
		From: "2026-01-01T00:00:00Z",
		To:   "2026-01-02T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("GetUsageSummary(range): %v", err)
	}
	if got.RequestCount != 3 {
		t.Errorf("RequestCount for day 1 only: got %d, want 3", got.RequestCount)
	}
}
