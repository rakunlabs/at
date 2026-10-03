package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestParseTextArray(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "{}", []string{}},
		{"plain", "{a,b}", []string{"a", "b"}},
		{"quoted", `{"a b","c,d","e\"f"}`, []string{"a b", "c,d", `e"f`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTextArray(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %q want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %q want %q", got, tt.want)
				}
			}
		})
	}
}

// The trace explorer reads whole traces: attribute filters select a trace
// when any observation matches, while the aggregates still describe every
// observation. Trace attributes merge across observations, scores and
// bookmarks are scoped to the caller's workspace.
func TestTraceExplorerPostgres(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := func(ms int) string { return base.Add(time.Duration(ms) * time.Millisecond).Format(time.RFC3339Nano) }

	record := func(c service.LLMCall) {
		t.Helper()
		c.WorkspaceID = w.ID
		if err := p.RecordLLMCall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	record(service.LLMCall{ID: "root-a", TraceID: "trace-a", SessionID: "s1", ObservationType: service.ObservationAgent, Name: "support-agent", Source: "chat",
		StartedAt: at(0), EndedAt: at(4000), LatencyMs: 4000, UserID: admin.ID,
		Trace: &service.TraceUpdate{Name: "support-agent", Input: "Where is my order?", Tags: []string{"prod", "support"}}})
	record(service.LLMCall{ID: "gen-a", TraceID: "trace-a", SessionID: "s1", ParentObservationID: "root-a", Source: "chat", Provider: "openai", Model: "gpt-5",
		StartedAt: at(100), EndedAt: at(2100), LatencyMs: 2000, InputTokens: 1000, OutputTokens: 200, CostCents: 1.5, UserID: admin.ID, Environment: "production",
		Metadata: map[string]any{"iteration": 1}})
	record(service.LLMCall{ID: "tool-a", TraceID: "trace-a", SessionID: "s1", ParentObservationID: "gen-a", ObservationType: service.ObservationTool, Name: "search_orders", Source: "chat",
		StartedAt: at(2200), EndedAt: at(2500), LatencyMs: 300, Level: service.ObservationLevelError, Status: "error",
		Trace: &service.TraceUpdate{Output: "Your package shipped", Tags: []string{"support", "tools"}}})
	record(service.LLMCall{ID: "gen-b", TraceID: "trace-b", SessionID: "s1", Source: "chat", Provider: "anthropic", Model: "claude",
		StartedAt: at(10000), EndedAt: at(10500), LatencyMs: 500, InputTokens: 10, OutputTokens: 5,
		Trace: &service.TraceUpdate{Name: "followup", Input: "Thanks"}})

	got, err := p.ListTraces(ctx, service.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Total != 2 || got.Data[0].TraceID != "trace-b" {
		t.Fatalf("list order: %+v", got)
	}
	a := got.Data[1]
	if a.Name != "support-agent" || a.ObservationCount != 3 || a.ErrorCount != 1 || a.DurationMs != 4000 || a.TotalTokens != 1200 {
		t.Fatalf("aggregate: %+v", a)
	}
	if a.Input != "Where is my order?" || a.Output != "Your package shipped" || len(a.Tags) != 3 || a.Environment != "production" {
		t.Fatalf("trace attributes: %+v", a)
	}

	// A model filter selects the trace, not the matching rows.
	got, err = p.ListTraces(ctx, service.TraceQuery{Models: []string{"gpt-5"}})
	if err != nil || got.Meta.Total != 1 || got.Data[0].ObservationCount != 3 {
		t.Fatalf("model filter: %+v %v", got, err)
	}
	minLatency := int64(3000)
	if got, err = p.ListTraces(ctx, service.TraceQuery{MinLatencyMs: &minLatency}); err != nil || got.Meta.Total != 1 || got.Data[0].TraceID != "trace-a" {
		t.Fatalf("latency filter: %+v %v", got, err)
	}
	if got, err = p.ListTraces(ctx, service.TraceQuery{Tags: []string{"tools"}, Status: "error"}); err != nil || got.Meta.Total != 1 {
		t.Fatalf("tag/status filter: %+v %v", got, err)
	}
	if got, err = p.ListTraces(ctx, service.TraceQuery{Search: "order"}); err != nil || got.Meta.Total != 1 {
		t.Fatalf("search: %+v %v", got, err)
	}
	if got, err = p.ListTraces(ctx, service.TraceQuery{Sort: "cost", Desc: true}); err != nil || got.Data[0].TraceID != "trace-a" {
		t.Fatalf("cost sort: %+v %v", got, err)
	}

	detail, err := p.GetTrace(ctx, "trace-a")
	if err != nil || detail == nil || len(detail.Observations) != 3 || detail.Observations[0].ID != "root-a" {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	if detail.Observations[1].StartedAt != at(100) || detail.Observations[1].Metadata["iteration"] != float64(1) {
		t.Fatalf("observation timing/metadata: %+v", detail.Observations[1])
	}

	// Scores: validation, workspace scoping and aggregation.
	one := 0.9
	if _, err := p.CreateTraceScore(ctx, service.TraceScore{TraceID: "trace-a", Name: "correctness", Value: &one}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateTraceScore(ctx, service.TraceScore{TraceID: "trace-a", ObservationID: "gen-b", Name: "x", Value: &one}); !errors.Is(err, service.ErrTraceNotFound) {
		t.Fatalf("foreign observation accepted: %v", err)
	}
	if _, err := p.CreateTraceScore(ctx, service.TraceScore{TraceID: "missing", Name: "x", Value: &one}); !errors.Is(err, service.ErrTraceNotFound) {
		t.Fatalf("unknown trace accepted: %v", err)
	}
	if _, err := p.CreateTraceScore(ctx, service.TraceScore{TraceID: "trace-a", Name: "x", DataType: "boolean", Value: &one}); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	minScore := 0.5
	if got, err = p.ListTraces(ctx, service.TraceQuery{ScoreName: "correctness", MinScore: &minScore}); err != nil || got.Meta.Total != 1 || len(got.Data[0].Scores) != 1 || *got.Data[0].Scores[0].Average != 0.9 {
		t.Fatalf("score filter: %+v %v", got, err)
	}

	if err := p.SetTraceBookmark(ctx, "trace-b", true); err != nil {
		t.Fatal(err)
	}
	if got, err = p.ListTraces(ctx, service.TraceQuery{Bookmarked: true}); err != nil || got.Meta.Total != 1 || !got.Data[0].Bookmarked {
		t.Fatalf("bookmark: %+v %v", got, err)
	}

	sessions, err := p.ListTraceSessions(ctx, service.TraceSessionQuery{})
	if err != nil || sessions.Meta.Total != 1 || sessions.Data[0].TraceCount != 2 {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	session, err := p.GetTraceSession(ctx, "s1", "")
	if err != nil || session == nil || len(session.Traces) != 2 || session.Traces[0].TraceID != "trace-a" || session.Traces[1].Input != "Thanks" {
		t.Fatalf("session detail: %+v %v", session, err)
	}

	facets, err := p.GetTraceFacets(ctx, time.Time{})
	if err != nil || len(facets.Models) != 2 || len(facets.Tags) != 3 || len(facets.ScoreNames) != 1 {
		t.Fatalf("facets: %+v %v", facets, err)
	}

	// Another workspace sees none of it.
	other, err := p.CreateWorkspace(ctx, "Beta", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherAccess, _, err := p.ResolveWorkspaceAccess(t.Context(), other.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := service.WithAccessPrincipal(t.Context(), otherAccess)
	if got, err = p.ListTraces(otherCtx, service.TraceQuery{}); err != nil || got.Meta.Total != 0 {
		t.Fatalf("cross-workspace list: %+v %v", got, err)
	}
	if d, err := p.GetTrace(otherCtx, "trace-a"); err != nil || d != nil {
		t.Fatalf("cross-workspace detail: %+v %v", d, err)
	}
	if err := p.SetTraceBookmark(otherCtx, "trace-a", true); !errors.Is(err, service.ErrTraceNotFound) {
		t.Fatalf("cross-workspace bookmark: %v", err)
	}

	// Body expiry also clears trace-level previews; row expiry removes the
	// trace attributes, scores and bookmarks left without observations.
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if _, err := p.ExpireLLMCallBodiesBefore(ctx, future); err != nil {
		t.Fatal(err)
	}
	if d, _ := p.GetTrace(ctx, "trace-a"); d.Trace.Input != "" {
		t.Fatalf("trace preview survived body expiry: %q", d.Trace.Input)
	}
	if _, err := p.DeleteLLMCallsBefore(ctx, future); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"llm_traces", "trace_scores", "trace_bookmarks"} {
		if n, _ := p.goqu.From(p.workspaceTable(table)).CountContext(t.Context()); n != 0 {
			t.Fatalf("%s rows left after sweep: %d", table, n)
		}
	}
}

func TestTraceScoreForTokenPostgres(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	if err := p.RecordLLMCall(ctx, service.LLMCall{TraceID: "tt", TokenID: "tok-1", Source: "gateway", WorkspaceID: w.ID}); err != nil {
		t.Fatal(err)
	}
	v := 1.0
	bg := t.Context()
	if _, err := p.CreateTraceScoreForToken(bg, w.ID, "tok-2", service.TraceScore{TraceID: "tt", Name: "thumbs", DataType: "boolean", Value: &v}); !errors.Is(err, service.ErrTraceNotFound) {
		t.Fatalf("other token scored trace: %v", err)
	}
	s, err := p.CreateTraceScoreForToken(bg, w.ID, "tok-1", service.TraceScore{TraceID: "tt", Name: "thumbs", DataType: "boolean", Value: &v})
	if err != nil || s.Source != service.ScoreSourceAPI || s.TokenID != "tok-1" {
		t.Fatalf("token score: %+v %v", s, err)
	}
}
