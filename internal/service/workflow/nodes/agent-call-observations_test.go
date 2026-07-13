package nodes_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// TestAgentCall_RecordsObservations verifies the workflow agent_call node
// records workflow-source generation and tool observations through the
// Registry's RecordObservation seam, with tools parented to the requesting
// generation.
func TestAgentCall_RecordsObservations(t *testing.T) {
	calls := 0
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, _ []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		calls++
		if calls == 1 {
			return &service.LLMResponse{
				ToolCalls: []service.ToolCall{{ID: "tc1", Name: "mystery_tool", Arguments: map[string]any{"a": 1}}},
				Usage:     service.Usage{PromptTokens: 50, CompletionTokens: 5},
			}, nil
		}
		return &service.LLMResponse{Content: "wf done", Finished: true, Usage: service.Usage{PromptTokens: 60, CompletionTokens: 6}}, nil
	}}

	reg := newTestRegistryWithProvider(mp)

	var mu sync.Mutex
	var recorded []service.LLMCall
	reg.RecordObservation = func(_ context.Context, obs service.LLMCall) string {
		mu.Lock()
		defer mu.Unlock()
		if obs.ID == "" {
			obs.ID = ulid.Make().String()
		}
		recorded = append(recorded, obs)
		return obs.ID
	}

	node := makeNode(t, "agent_call", map[string]any{
		"provider":       "test-provider",
		"max_iterations": float64(4),
	})

	res, err := node.Run(context.Background(), reg, map[string]any{"prompt": "do the work"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Data()["response"] != "wf done" {
		t.Fatalf("unexpected response: %+v", res.Data())
	}

	mu.Lock()
	defer mu.Unlock()

	var gens, tools []service.LLMCall
	for _, o := range recorded {
		if o.Source != "workflow" {
			t.Fatalf("expected source workflow, got %q", o.Source)
		}
		switch o.ObservationType {
		case service.ObservationGeneration:
			gens = append(gens, o)
		case service.ObservationTool:
			tools = append(tools, o)
		}
	}
	if len(gens) != 2 || len(tools) != 1 {
		t.Fatalf("expected 2 generations + 1 tool, got gens=%d tools=%d", len(gens), len(tools))
	}
	if tools[0].ParentObservationID != gens[0].ID {
		t.Fatalf("tool parent = %q, want first generation %q", tools[0].ParentObservationID, gens[0].ID)
	}
	if tools[0].Name != "mystery_tool" || tools[0].Level != service.ObservationLevelError {
		t.Fatalf("tool obs wrong (unknown tool must be level=error): %+v", tools[0])
	}
	if !strings.Contains(tools[0].Output, "no handler") && !strings.Contains(tools[0].Output, "Error") {
		t.Fatalf("tool output should carry the handler error: %q", tools[0].Output)
	}
	if gens[0].TraceID == "" || gens[0].TraceID != gens[1].TraceID || tools[0].TraceID != gens[0].TraceID {
		t.Fatal("all observations of one node run must share a trace ID")
	}
	// Request bodies are attached by the node; gating happens server-side.
	if gens[0].RequestBody == "" || !strings.Contains(gens[0].RequestBody, "do the work") {
		t.Fatalf("generation request body missing: %q", gens[0].RequestBody)
	}
}
