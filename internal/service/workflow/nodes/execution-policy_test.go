package nodes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestWorkflowCommonRetryHTTPDoesNotMultiplyLegacyRetries(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(503) }))
	defer upstream.Close()
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "http", Type: "http_request", Data: map[string]any{"url": upstream.URL, "retry": true, "execution": map[string]any{"max_attempts": 3, "retry_delay_ms": 0}}}},
		Edges: []service.WorkflowEdge{{Source: "i", SourceHandle: "data", Target: "http", TargetHandle: "data"}},
	}
	_, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), graph, nil, []string{"i"}, nil)
	if err == nil || calls != 3 {
		t.Fatalf("wanted 3 engine-owned attempts, calls=%d error=%v", calls, err)
	}
}

func TestWorkflowFailurePoliciesPreserveBranchSemantics(t *testing.T) {
	for _, mode := range []string{"stop", "continue", "error_output"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			provider := &mockProvider{chatFunc: func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
				calls++
				return nil, &service.UpstreamError{StatusCode: 503, Message: "unavailable"}
			}}
			e := workflow.NewEngineWithDependencies(workflow.Dependencies{ProviderLookup: func(string) (service.LLMProvider, string, error) { return provider, "fake", nil }})
			events := make(chan workflow.NodeEvent, 64)
			e.SetEventChannel(events)
			graph := service.WorkflowGraph{
				Nodes: []service.WorkflowNode{
					{ID: "i", Type: "input"},
					{ID: "model", Type: "llm_call", Data: map[string]any{"provider": "p", "execution": map[string]any{"max_attempts": 2, "retry_delay_ms": 0, "on_error": mode}}},
					{ID: "normal", Type: "template", Data: map[string]any{"template": "must not execute"}},
					{ID: "recovery", Type: "template", Data: map[string]any{"template": "failed {{.node_id}} after {{.attempts}}"}},
					{ID: "z-independent", Type: "template", Data: map[string]any{"template": "still runs"}},
				},
				Edges: []service.WorkflowEdge{
					{Source: "i", SourceHandle: "data", Target: "model", TargetHandle: "prompt"},
					{Source: "i", SourceHandle: "data", Target: "z-independent", TargetHandle: "data"},
					{Source: "model", SourceHandle: "response", Target: "normal", TargetHandle: "data"},
				},
			}
			if mode == "error_output" {
				graph.Edges = append(graph.Edges, service.WorkflowEdge{Source: "model", SourceHandle: "__error", Target: "recovery", TargetHandle: "data"})
			}
			_, err := e.Run(executiontest.Context(t), graph, map[string]any{"text": "hello"}, []string{"i"}, nil)
			if (err != nil) != (mode == "stop") || calls != 2 {
				t.Fatalf("mode=%s calls=%d err=%v", mode, calls, err)
			}
			completed := map[string]workflow.NodeEvent{}
			handled := false
			for len(events) > 0 {
				event := <-events
				if event.EventType == "completed" {
					completed[event.NodeID] = event
				}
				if event.EventType == "error_handled" {
					handled = true
					if event.PinSignature != "" {
						t.Fatal("handled failure offered as a successful pin")
					}
				}
			}
			if _, ok := completed["normal"]; ok {
				t.Fatal("failed node activated its success output")
			}
			if mode != "stop" && (!handled || completed["z-independent"].Data["text"] != "still runs") {
				t.Fatal("handled error stopped an independent branch")
			}
			if mode == "error_output" && completed["recovery"].Data["text"] != "failed model after 2" {
				t.Fatalf("bad recovery data: %v", completed["recovery"].Data)
			}
		})
	}
}

func TestWorkflowFailurePortRequiresExplicitPolicyAndStaysInactiveOnSuccess(t *testing.T) {
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "t", Type: "template", Data: map[string]any{"template": "ok"}}, {ID: "recovery", Type: "template", Data: map[string]any{"template": "wrong"}}},
		Edges: []service.WorkflowEdge{{Source: "i", SourceHandle: "data", Target: "t", TargetHandle: "data"}, {Source: "t", SourceHandle: "__error", Target: "recovery", TargetHandle: "data"}},
	}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	if _, err := e.Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err == nil {
		t.Fatal("failure edge without error_output policy accepted")
	}
	graph.Nodes[1].Data["execution"] = map[string]any{"on_error": "error_output"}
	events := make(chan workflow.NodeEvent, 32)
	e.SetEventChannel(events)
	if _, err := e.Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err != nil {
		t.Fatal(err)
	}
	for len(events) > 0 {
		event := <-events
		if event.NodeID == "recovery" && event.EventType != "skipped" {
			t.Fatalf("success activated failure port: %+v", event)
		}
	}
}
