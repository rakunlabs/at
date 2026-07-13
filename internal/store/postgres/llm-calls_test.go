package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func TestLLMCall_RecordAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	call := service.LLMCall{
		TraceID:        "trace-1",
		SessionID:      "sess-1",
		Source:         "gateway",
		Endpoint:       "/gateway/v1/chat/completions",
		TokenID:        "tok-1",
		Provider:       "openai",
		Model:          "gpt-4o",
		RequestedModel: "openai/gpt-4o",
		RequestBody:    `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
		ResponseBody:   `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
		RequestBytes:   64,
		ResponseBytes:  60,
		InputTokens:    10,
		OutputTokens:   5,
		CostCents:      0.25,
		LatencyMs:      1200,
		Status:         "ok",
		FinishReason:   "stop",
	}
	if err := store.RecordLLMCall(ctx, call); err != nil {
		t.Fatalf("RecordLLMCall: %v", err)
	}

	res, err := store.ListLLMCalls(ctx, &query.Query{})
	if err != nil {
		t.Fatalf("ListLLMCalls: %v", err)
	}
	if res.Meta.Total != 1 || len(res.Data) != 1 {
		t.Fatalf("expected 1 row, got total=%d len=%d", res.Meta.Total, len(res.Data))
	}
	got := res.Data[0]
	if got.TraceID != "trace-1" || got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("unexpected tokens: %+v", got)
	}

	full, err := store.GetLLMCall(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetLLMCall: %v", err)
	}
	if full == nil {
		t.Fatal("GetLLMCall returned nil")
	}
	if !strings.Contains(full.RequestBody, "hi") || !strings.Contains(full.ResponseBody, "hello") {
		t.Fatalf("bodies not preserved: req=%q resp=%q", full.RequestBody, full.ResponseBody)
	}
}

func TestLLMCall_ListPreviewClipsBody(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	big := strings.Repeat("x", service.LLMCallPreviewBytes*3)
	if err := store.RecordLLMCall(ctx, service.LLMCall{
		TraceID:     "t",
		Source:      "gateway",
		Provider:    "openai",
		Model:       "gpt-4o",
		RequestBody: big,
		Status:      "ok",
	}); err != nil {
		t.Fatalf("RecordLLMCall: %v", err)
	}

	res, err := store.ListLLMCalls(ctx, &query.Query{})
	if err != nil {
		t.Fatalf("ListLLMCalls: %v", err)
	}
	if len(res.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Data))
	}
	// List must clip to the preview length.
	if len(res.Data[0].RequestBody) > service.LLMCallPreviewBytes {
		t.Fatalf("list body not clipped: len=%d cap=%d", len(res.Data[0].RequestBody), service.LLMCallPreviewBytes)
	}

	// Detail must return the full body.
	full, err := store.GetLLMCall(ctx, res.Data[0].ID)
	if err != nil {
		t.Fatalf("GetLLMCall: %v", err)
	}
	if len(full.RequestBody) != len(big) {
		t.Fatalf("detail body clipped: len=%d want=%d", len(full.RequestBody), len(big))
	}
}

func TestLLMCall_DeleteBefore(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	old := service.LLMCall{TraceID: "old", Source: "gateway", Provider: "openai", Model: "m", Status: "ok",
		CreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}
	recent := service.LLMCall{TraceID: "new", Source: "gateway", Provider: "openai", Model: "m", Status: "ok",
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := store.RecordLLMCall(ctx, old); err != nil {
		t.Fatalf("record old: %v", err)
	}
	if err := store.RecordLLMCall(ctx, recent); err != nil {
		t.Fatalf("record recent: %v", err)
	}

	cutoff := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	n, err := store.DeleteLLMCallsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteLLMCallsBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted, got %d", n)
	}

	res, err := store.ListLLMCalls(ctx, &query.Query{})
	if err != nil {
		t.Fatalf("ListLLMCalls: %v", err)
	}
	if res.Meta.Total != 1 || res.Data[0].TraceID != "new" {
		t.Fatalf("expected only recent row to survive, got %+v", res.Data)
	}
}

func TestLLMCall_ObservationHierarchy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	gen := service.LLMCall{
		ID:              "gen-1",
		ObservationType: service.ObservationGeneration,
		TraceID:         "trace-1",
		SessionID:       "root-task",
		Source:          "agent",
		TaskID:          "task-1",
		AgentID:         "agent-1",
		Provider:        "openai",
		Model:           "gpt-4o",
		InputTokens:     100,
		OutputTokens:    20,
		CostCents:       0.5,
		Status:          "ok",
	}
	tool := service.LLMCall{
		ID:                  "tool-1",
		ObservationType:     service.ObservationTool,
		ParentObservationID: "gen-1",
		Name:                "bash_execute",
		TraceID:             "trace-1",
		SessionID:           "root-task",
		Source:              "agent",
		TaskID:              "task-1",
		AgentID:             "agent-1",
		Input:               `{"command":"ls"}`,
		Output:              "file.txt",
		Level:               service.ObservationLevelDefault,
		Metadata:            map[string]any{"iteration": 0},
	}
	event := service.LLMCall{
		ID:              "event-1",
		ObservationType: service.ObservationEvent,
		Name:            "task_started",
		TraceID:         "trace-1",
		SessionID:       "root-task",
		Source:          "agent",
		TaskID:          "task-1",
		Metadata:        map[string]any{"task_title": "do the thing"},
	}
	for _, obs := range []service.LLMCall{gen, tool, event} {
		if err := store.RecordLLMCall(ctx, obs); err != nil {
			t.Fatalf("RecordLLMCall(%s): %v", obs.ID, err)
		}
	}

	res, err := store.ListLLMCalls(ctx, mustParseQuery(t, "trace_id=trace-1"))
	if err != nil {
		t.Fatalf("ListLLMCalls: %v", err)
	}
	if res.Meta.Total != 3 {
		t.Fatalf("expected 3 observations, got %d", res.Meta.Total)
	}
	byID := map[string]service.LLMCall{}
	for _, o := range res.Data {
		byID[o.ID] = o
	}
	if byID["gen-1"].ObservationType != service.ObservationGeneration {
		t.Fatalf("gen-1 type = %q", byID["gen-1"].ObservationType)
	}
	if byID["tool-1"].ParentObservationID != "gen-1" || byID["tool-1"].Name != "bash_execute" {
		t.Fatalf("tool-1 hierarchy broken: %+v", byID["tool-1"])
	}
	if byID["tool-1"].Input == "" || byID["tool-1"].Output == "" {
		t.Fatalf("tool-1 IO missing: %+v", byID["tool-1"])
	}
	if byID["event-1"].ObservationType != service.ObservationEvent || byID["event-1"].Name != "task_started" {
		t.Fatalf("event-1 broken: %+v", byID["event-1"])
	}
	if byID["event-1"].Metadata["task_title"] != "do the thing" {
		t.Fatalf("event-1 metadata not round-tripped: %+v", byID["event-1"].Metadata)
	}
}

func TestLLMCall_DefaultObservationType(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	// Gateway callers don't set an observation type; it must default to
	// "generation" (matching the migration backfill).
	if err := store.RecordLLMCall(ctx, service.LLMCall{TraceID: "t", Source: "gateway", Provider: "p", Model: "m"}); err != nil {
		t.Fatalf("RecordLLMCall: %v", err)
	}
	res, err := store.ListLLMCalls(ctx, &query.Query{})
	if err != nil {
		t.Fatalf("ListLLMCalls: %v", err)
	}
	if res.Data[0].ObservationType != service.ObservationGeneration {
		t.Fatalf("expected default type generation, got %q", res.Data[0].ObservationType)
	}
	if res.Data[0].Level != service.ObservationLevelDefault {
		t.Fatalf("expected default level, got %q", res.Data[0].Level)
	}
}

func TestLLMCall_ListTraces(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	// Trace A: 2 generations + 1 tool + 1 event, one generation errored.
	rows := []service.LLMCall{
		{ID: "a1", ObservationType: "event", Name: "task_started", TraceID: "trace-a", SessionID: "s1", Source: "agent", TaskID: "task-a"},
		{ID: "a2", ObservationType: "generation", TraceID: "trace-a", SessionID: "s1", Source: "agent", TaskID: "task-a", InputTokens: 100, OutputTokens: 10, CostCents: 0.4, LatencyMs: 900},
		{ID: "a3", ObservationType: "tool", ParentObservationID: "a2", Name: "task_get", TraceID: "trace-a", SessionID: "s1", Source: "agent", TaskID: "task-a"},
		{ID: "a4", ObservationType: "generation", TraceID: "trace-a", SessionID: "s1", Source: "agent", TaskID: "task-a", InputTokens: 200, OutputTokens: 30, CostCents: 0.6, LatencyMs: 1100, Status: "error", ErrorCode: "http_500"},
		// Trace B: single gateway generation.
		{ID: "b1", ObservationType: "generation", TraceID: "trace-b", Source: "gateway", InputTokens: 50, OutputTokens: 5, CostCents: 0.1},
	}
	for _, r := range rows {
		if err := store.RecordLLMCall(ctx, r); err != nil {
			t.Fatalf("RecordLLMCall(%s): %v", r.ID, err)
		}
	}

	res, err := store.ListLLMCallTraces(ctx, &query.Query{})
	if err != nil {
		t.Fatalf("ListLLMCallTraces: %v", err)
	}
	if res.Meta.Total != 2 || len(res.Data) != 2 {
		t.Fatalf("expected 2 traces, got total=%d len=%d", res.Meta.Total, len(res.Data))
	}

	byTrace := map[string]service.LLMCallTrace{}
	for _, tr := range res.Data {
		byTrace[tr.TraceID] = tr
	}
	a := byTrace["trace-a"]
	if a.ObservationCount != 4 || a.GenerationCount != 2 {
		t.Fatalf("trace-a counts wrong: %+v", a)
	}
	if a.InputTokens != 300 || a.OutputTokens != 40 {
		t.Fatalf("trace-a token sums wrong: %+v", a)
	}
	if a.CostCents < 0.99 || a.CostCents > 1.01 {
		t.Fatalf("trace-a cost sum wrong: %+v", a)
	}
	if a.ErrorCount != 1 {
		t.Fatalf("trace-a error count wrong: %+v", a)
	}
	if a.SessionID != "s1" || a.Source != "agent" || a.TaskID != "task-a" {
		t.Fatalf("trace-a attribution wrong: %+v", a)
	}
	// Name = first non-empty name by id order → "task_started".
	if a.Name != "task_started" {
		t.Fatalf("trace-a name = %q, want task_started", a.Name)
	}

	// Filter by task_id must return only trace-a.
	filtered, err := store.ListLLMCallTraces(ctx, mustParseQuery(t, "task_id=task-a"))
	if err != nil {
		t.Fatalf("ListLLMCallTraces(filtered): %v", err)
	}
	if filtered.Meta.Total != 1 || filtered.Data[0].TraceID != "trace-a" {
		t.Fatalf("task_id filter wrong: %+v", filtered.Data)
	}
}

func TestLLMCall_ExpireBodiesBefore(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	oldTS := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	newTS := time.Now().UTC().Format(time.RFC3339)

	if err := store.RecordLLMCall(ctx, service.LLMCall{
		ID: "old-gen", TraceID: "t1", Source: "agent", Provider: "p", Model: "m",
		RequestBody: "req", ResponseBody: "resp", InputTokens: 10, OutputTokens: 2,
		CostCents: 0.2, CreatedAt: oldTS,
	}); err != nil {
		t.Fatalf("record old: %v", err)
	}
	if err := store.RecordLLMCall(ctx, service.LLMCall{
		ID: "new-gen", TraceID: "t2", Source: "agent", Provider: "p", Model: "m",
		RequestBody: "req2", ResponseBody: "resp2", CreatedAt: newTS,
	}); err != nil {
		t.Fatalf("record new: %v", err)
	}

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	n, err := store.ExpireLLMCallBodiesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("ExpireLLMCallBodiesBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired, got %d", n)
	}

	oldRow, err := store.GetLLMCall(ctx, "old-gen")
	if err != nil {
		t.Fatalf("GetLLMCall(old): %v", err)
	}
	if oldRow.RequestBody != "" || oldRow.ResponseBody != "" {
		t.Fatalf("old bodies not expired: %+v", oldRow)
	}
	// Skeleton survives.
	if oldRow.InputTokens != 10 || oldRow.CostCents != 0.2 || oldRow.TraceID != "t1" {
		t.Fatalf("old skeleton lost: %+v", oldRow)
	}

	newRow, err := store.GetLLMCall(ctx, "new-gen")
	if err != nil {
		t.Fatalf("GetLLMCall(new): %v", err)
	}
	if newRow.RequestBody != "req2" || newRow.ResponseBody != "resp2" {
		t.Fatalf("new bodies must survive: %+v", newRow)
	}

	// Idempotent: second run expires nothing.
	n2, err := store.ExpireLLMCallBodiesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("ExpireLLMCallBodiesBefore(2nd): %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 on second run, got %d", n2)
	}
}

func mustParseQuery(t *testing.T, raw string) *query.Query {
	t.Helper()
	q, err := query.Parse(raw)
	if err != nil {
		t.Fatalf("query.Parse(%q): %v", raw, err)
	}
	return q
}

func TestLLMCall_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	got, err := store.GetLLMCall(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("GetLLMCall: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing id, got %+v", got)
	}
}
