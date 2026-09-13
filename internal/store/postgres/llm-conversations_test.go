package postgres

import (
	"testing"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
)

func TestLLMConversationsGroupingAndPagination(t *testing.T) {
	p := newTestStore(t, nil)
	for _, call := range []service.LLMCall{
		{TraceID: "t1", SessionID: "session-1", TokenID: "token-a", Source: "gateway_stream", InputTokens: 10, CacheReadTokens: 8, CostCents: 1},
		{TraceID: "t2", SessionID: "session-1", TokenID: "token-a", Source: "gateway", InputTokens: 20, CacheReadTokens: 15, CostCents: 2, Status: "error"},
		{TraceID: "t3", SessionID: "session-1", TokenID: "token-a", Source: "responses", InputTokens: 30, CacheReadTokens: 25, CostCents: 3},
		{TraceID: "t4", SessionID: "session-1", TokenID: "token-b", Source: "gateway_stream"},
		{TraceID: "t5", TokenID: "token-a", Source: "gateway_stream", CacheReadTokens: 48},
		{TraceID: "t6", TokenID: "token-a", Source: "gateway_stream", CacheReadTokens: 48},
		{TraceID: "t7", SessionID: "session-1", TokenID: "token-a", Source: "chat"},
	} {
		if err := p.RecordLLMCall(t.Context(), call); err != nil {
			t.Fatal(err)
		}
	}
	result, err := p.ListLLMCallConversations(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.Total != 5 || len(result.Data) != 5 {
		t.Fatalf("group count: %+v", result.Meta)
	}
	found := false
	for _, c := range result.Data {
		if c.SessionID == "session-1" && c.TokenID == "token-a" && c.Source == "gateway" {
			found = true
			if c.TraceCount != 3 || c.GenerationCount != 3 || c.InputTokens != 60 || c.CacheReadTokens != 48 || c.CostCents != 6 || c.ErrorCount != 1 {
				t.Fatalf("bad group: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("gateway group missing")
	}
	q, _ := query.Parse("source=gateway_stream&_limit=2&_offset=1")
	page, err := p.ListLLMCallConversations(t.Context(), q)
	if err != nil || page.Meta.Total != 4 || len(page.Data) != 2 {
		t.Fatalf("filtered pagination: %+v %v", page, err)
	}
	q, _ = query.Parse("session_id=session-1&token_id=token-a&source[in]=gateway,gateway_stream,responses")
	traces, err := p.ListLLMCallTraces(t.Context(), q)
	if err != nil || traces.Meta.Total != 3 {
		t.Fatalf("conversation drilldown: %+v %v", traces, err)
	}
}
