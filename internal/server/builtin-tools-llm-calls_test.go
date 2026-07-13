package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type llmTraceToolStore struct {
	fakeLLMCallStore
	observations *service.ListResult[service.LLMCall]
	traces       *service.ListResult[service.LLMCallTrace]
	detail       *service.LLMCall
	callQuery    *query.Query
	traceQueries []*query.Query
}

func (s *llmTraceToolStore) ListLLMCalls(_ context.Context, q *query.Query) (*service.ListResult[service.LLMCall], error) {
	s.callQuery = q
	return s.observations, nil
}

func (s *llmTraceToolStore) ListLLMCallTraces(_ context.Context, q *query.Query) (*service.ListResult[service.LLMCallTrace], error) {
	s.traceQueries = append(s.traceQueries, q)
	return s.traces, nil
}

func (s *llmTraceToolStore) GetLLMCall(_ context.Context, _ string) (*service.LLMCall, error) {
	return s.detail, nil
}

func TestExecLLMTraceList_FiltersAndCapsLimit(t *testing.T) {
	store := &llmTraceToolStore{
		traces: &service.ListResult[service.LLMCallTrace]{
			Data: []service.LLMCallTrace{{TraceID: "trace-1", TaskID: "task-1"}},
			Meta: service.ListMeta{Total: 1},
		},
	}
	s := &Server{llmCallStore: store}

	out, err := s.execLLMTraceList(context.Background(), map[string]any{
		"task_id": "task-1",
		"limit":   float64(500),
	})
	if err != nil {
		t.Fatalf("execLLMTraceList returned error: %v", err)
	}
	if len(store.traceQueries) != 1 {
		t.Fatalf("trace query count = %d, want 1", len(store.traceQueries))
	}
	q := store.traceQueries[0]
	if got := q.GetValue("task_id"); got != "task-1" {
		t.Fatalf("task_id filter = %q, want task-1", got)
	}
	if got := q.GetLimit(); got != 100 {
		t.Fatalf("limit = %d, want capped value 100", got)
	}
	if !strings.Contains(out, `"trace_id": "trace-1"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestExecLLMTraceGet_CompactByDefault(t *testing.T) {
	store := &llmTraceToolStore{
		observations: &service.ListResult[service.LLMCall]{
			Data: []service.LLMCall{{
				ID:           "obs-1",
				TraceID:      "trace-1",
				Input:        "secret tool input",
				Output:       "large tool output",
				RequestBody:  "full prompt",
				ResponseBody: "full response",
				InputTokens:  123,
			}},
			Meta: service.ListMeta{Total: 1},
		},
		traces: &service.ListResult[service.LLMCallTrace]{
			Data: []service.LLMCallTrace{{TraceID: "trace-1", ObservationCount: 1}},
		},
	}
	s := &Server{llmCallStore: store}

	out, err := s.execLLMTraceGet(context.Background(), map[string]any{"trace_id": "trace-1"})
	if err != nil {
		t.Fatalf("execLLMTraceGet returned error: %v", err)
	}
	if got := store.callQuery.GetValue("trace_id"); got != "trace-1" {
		t.Fatalf("trace_id filter = %q, want trace-1", got)
	}
	if len(store.callQuery.Sort) != 1 || store.callQuery.Sort[0].Field != "created_at" || store.callQuery.Sort[0].Desc {
		t.Fatalf("unexpected observation sort: %+v", store.callQuery.Sort)
	}
	if strings.Contains(out, "secret tool input") || strings.Contains(out, "full prompt") {
		t.Fatalf("compact trace output leaked bodies: %s", out)
	}
	if !strings.Contains(out, `"input_tokens": 123`) {
		t.Fatalf("compact trace output lost metrics: %s", out)
	}
}

func TestExecLLMTraceGet_CanIncludeBodyPreviews(t *testing.T) {
	store := &llmTraceToolStore{
		observations: &service.ListResult[service.LLMCall]{Data: []service.LLMCall{{
			ID: "obs-1", TraceID: "trace-1", RequestBody: "prompt preview", ResponseBody: "response preview",
		}}},
		traces: &service.ListResult[service.LLMCallTrace]{Data: []service.LLMCallTrace{{TraceID: "trace-1"}}},
	}
	s := &Server{llmCallStore: store}

	out, err := s.execLLMTraceGet(context.Background(), map[string]any{
		"trace_id":       "trace-1",
		"include_bodies": true,
	})
	if err != nil {
		t.Fatalf("execLLMTraceGet returned error: %v", err)
	}
	if !strings.Contains(out, "prompt preview") || !strings.Contains(out, "response preview") {
		t.Fatalf("trace output omitted requested body previews: %s", out)
	}
}

func TestExecLLMObservationGet_RehydratesSpilledToolIO(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.json")
	outputPath := filepath.Join(dir, "output.json")
	if err := os.WriteFile(inputPath, []byte(`{"full":"input"}`), 0o600); err != nil {
		t.Fatalf("write input spill: %v", err)
	}
	if err := os.WriteFile(outputPath, []byte(`{"full":"output"}`), 0o600); err != nil {
		t.Fatalf("write output spill: %v", err)
	}

	store := &llmTraceToolStore{detail: &service.LLMCall{
		ID:              "obs-1",
		ObservationType: service.ObservationTool,
		Input:           "preview input",
		Output:          "preview output",
		RequestRef:      inputPath,
		ResponseRef:     outputPath,
	}}
	s := &Server{llmCallStore: store}

	out, err := s.execLLMObservationGet(context.Background(), map[string]any{"id": "obs-1"})
	if err != nil {
		t.Fatalf("execLLMObservationGet returned error: %v", err)
	}
	var call service.LLMCall
	if err := json.Unmarshal([]byte(out), &call); err != nil {
		t.Fatalf("unmarshal observation: %v", err)
	}
	if call.Input != `{"full":"input"}` || call.Output != `{"full":"output"}` {
		t.Fatalf("spill content not rehydrated: input=%q output=%q", call.Input, call.Output)
	}
}
