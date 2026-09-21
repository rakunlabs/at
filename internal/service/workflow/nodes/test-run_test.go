package nodes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func testRunGraph() service.WorkflowGraph {
	return service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{ID: "input", Type: "input"},
			{ID: "model", Type: "llm_call", Data: map[string]any{"provider": "fake"}},
			{ID: "format", Type: "template", Data: map[string]any{"template": "Summary: {{.data}}"}},
			{ID: "output", Type: "output"},
		},
		Edges: []service.WorkflowEdge{
			{Source: "input", SourceHandle: "data", Target: "model", TargetHandle: "prompt"},
			{Source: "model", SourceHandle: "response", Target: "format", TargetHandle: "data"},
			{Source: "format", SourceHandle: "text", Target: "output", TargetHandle: "input"},
		},
	}
}

func completedTestEvent(t *testing.T, events <-chan workflow.NodeEvent, id string) workflow.NodeEvent {
	t.Helper()
	var found workflow.NodeEvent
	for len(events) > 0 {
		event := <-events
		if event.NodeID == id && event.EventType == "completed" {
			found = event
		}
	}
	if found.ExecutionID == "" {
		t.Fatalf("no completed event for %s", id)
	}
	return found
}

func TestWorkflowTestRunPartialPinsAndProductionIsolation(t *testing.T) {
	calls := 0
	provider := &mockProvider{chatFunc: func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
		calls++
		return &service.LLMResponse{Content: "generated", Finished: true}, nil
	}}
	engine := workflow.NewEngineWithDependencies(workflow.Dependencies{ProviderLookup: func(string) (service.LLMProvider, string, error) { return provider, "fake-model", nil }})
	events := make(chan workflow.NodeEvent, 64)
	engine.SetEventChannel(events)
	graph := testRunGraph()
	inputs := map[string]any{"text": "hello"}
	entries := []string{"input"}
	ctx := executiontest.Context(t)
	if _, err := engine.Run(ctx, graph, inputs, entries, nil); err != nil {
		t.Fatal(err)
	}
	model := completedTestEvent(t, events, "model")
	if model.PinSignature == "" || calls != 1 {
		t.Fatalf("pin signature missing or call count %d", calls)
	}
	pins := map[string]workflow.PinnedNode{"model": {Signature: model.PinSignature, Data: model.Data, ResultKind: model.ResultKind}}

	// Downstream editing keeps the upstream fixture valid. Invalid nodes on a
	// sibling branch and after the target must not even be validated/executed.
	graph.Nodes[2].Data["template"] = "Changed: {{.data}}"
	graph.Nodes = append(graph.Nodes, service.WorkflowNode{ID: "invalid-sibling", Type: "llm_call"}, service.WorkflowNode{ID: "invalid-tail", Type: "llm_call"})
	graph.Edges = append(graph.Edges, service.WorkflowEdge{Source: "input", Target: "invalid-sibling"}, service.WorkflowEdge{Source: "format", Target: "invalid-tail"})
	if _, err := engine.RunTest(ctx, graph, inputs, entries, workflow.TestRunOptions{TargetNodeID: "format", Pins: pins}); err != nil {
		t.Fatal(err)
	}
	format := completedTestEvent(t, events, "format")
	if calls != 1 || format.Data["text"] != "Changed: generated" {
		t.Fatalf("pin not used: calls=%d, data=%v", calls, format.Data)
	}

	// Executing a pinned target really executes that target.
	if _, err := engine.RunTest(ctx, graph, inputs, entries, workflow.TestRunOptions{TargetNodeID: "model", Pins: pins}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("selected step was replaced by its own pin")
	}

	// Run uses an isolated engine instance: a prior RunTest cannot change a
	// future production run or another request using the same Engine.
	graph = testRunGraph()
	if _, err := engine.Run(ctx, graph, inputs, entries, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("test fixtures leaked into production Run")
	}

	if _, err := engine.RunTest(context.Background(), graph, inputs, entries, workflow.TestRunOptions{Pins: pins}); err == nil {
		t.Fatal("test mode bypassed execution identity")
	}
	if calls != 3 {
		t.Fatal("denied test called provider")
	}

	graph.Nodes[1].Data["system_prompt"] = "changed upstream configuration"
	if _, err := engine.RunTest(ctx, graph, inputs, entries, workflow.TestRunOptions{TargetNodeID: "format", Pins: pins}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale pin accepted: %v", err)
	}
	if calls != 3 {
		t.Fatal("stale fixture caused a paid call before rejection")
	}
}

func TestWorkflowPinnedSelectionPreservesInactiveBranch(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"count":7}`))
	}))
	defer upstream.Close()
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{ID: "input", Type: "input"},
			{ID: "http", Type: "http_request", Data: map[string]any{"url": upstream.URL, "method": "GET"}},
			{ID: "error", Type: "template", Data: map[string]any{"template": "error {{.response.count}}"}},
			{ID: "success", Type: "template", Data: map[string]any{"template": "must not run"}},
		},
		Edges: []service.WorkflowEdge{
			{Source: "input", SourceHandle: "data", Target: "http", TargetHandle: "data"},
			{Source: "http", SourceHandle: "error", Target: "error", TargetHandle: "data"},
			{Source: "http", SourceHandle: "success", Target: "success", TargetHandle: "data"},
		},
	}
	engine := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	events := make(chan workflow.NodeEvent, 64)
	engine.SetEventChannel(events)
	ctx := executiontest.Context(t)
	inputs := map[string]any{}
	entries := []string{"input"}
	if _, err := engine.Run(ctx, graph, inputs, entries, nil); err != nil {
		t.Fatal(err)
	}
	httpEvent := completedTestEvent(t, events, "http")
	if httpEvent.ResultKind != "selection" || httpEvent.PinSignature == "" {
		t.Fatal("selection fixture metadata missing")
	}
	pin := workflow.PinnedNode{Signature: httpEvent.PinSignature, Data: httpEvent.Data, ResultKind: httpEvent.ResultKind, Selection: httpEvent.Selection}
	if _, err := engine.RunTest(ctx, graph, inputs, entries, workflow.TestRunOptions{Pins: map[string]workflow.PinnedNode{"http": pin}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("pinned HTTP node made an external request")
	}
	seenError, seenSkipped, seenPinned := false, false, false
	for len(events) > 0 {
		event := <-events
		if event.NodeID == "http" && event.EventType == "completed" {
			seenPinned = event.Pinned
		}
		if event.NodeID == "error" && event.EventType == "completed" {
			seenError = event.Data["text"] == "error 7"
		}
		if event.NodeID == "success" {
			if event.EventType == "started" || event.EventType == "completed" {
				t.Fatal("pin activated an inactive route")
			}
			seenSkipped = event.EventType == "skipped"
		}
	}
	if !seenError || !seenSkipped || !seenPinned {
		t.Fatalf("missing route or pinned status: %v %v %v", seenError, seenSkipped, seenPinned)
	}
}
