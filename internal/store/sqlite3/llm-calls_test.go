package sqlite3

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
