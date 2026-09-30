package workflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type panicNode struct{ calls *atomic.Int32 }

func (panicNode) Type() string                                       { return "test_panic" }
func (panicNode) Validate(context.Context, *workflow.Registry) error { return nil }
func (n panicNode) Run(context.Context, *workflow.Registry, map[string]any) (workflow.NodeResult, error) {
	n.calls.Add(1)
	var m map[string]any
	m["boom"] = true // nil-map write: a real runtime panic, not a panic(value) stub
	return nil, nil
}

var panicCalls atomic.Int32

func init() {
	workflow.RegisterNodeType("test_panic", func(service.WorkflowNode) (workflow.Noder, error) {
		return panicNode{calls: &panicCalls}, nil
	})
	service.RegisterExecutionCapability("node", "test_panic", false)
}

// A panicking node must fail its run like any other error instead of taking
// down the process, on the main path and inside Loop fan-out goroutines.
func TestNodePanicBecomesError(t *testing.T) {
	loop := map[string]any{"expression": "[1, 2, 3]"}
	tests := []struct {
		name  string
		graph service.WorkflowGraph
	}{
		{"main path", service.WorkflowGraph{
			Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "p", Type: "test_panic", Data: map[string]any{"execution": map[string]any{"max_attempts": 3, "retry_delay_ms": 0}}}},
			Edges: []service.WorkflowEdge{{Source: "i", SourceHandle: "data", Target: "p", TargetHandle: "data"}},
		}},
		{"fan-out branch", service.WorkflowGraph{
			Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "l", Type: "loop", Data: loop}, {ID: "p", Type: "test_panic"}},
			Edges: []service.WorkflowEdge{{Source: "i", SourceHandle: "data", Target: "l", TargetHandle: "data"}, {Source: "l", SourceHandle: "item", Target: "p", TargetHandle: "data"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panicCalls.Store(0)
			_, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), tt.graph, nil, []string{"i"}, nil)
			if !errors.Is(err, workflow.ErrNodePanic) {
				t.Fatalf("error = %v, want ErrNodePanic", err)
			}
			if tt.name == "main path" && panicCalls.Load() != 1 {
				t.Fatalf("panicking node ran %d times; a defect must not be retried", panicCalls.Load())
			}
		})
	}
}

// on_error applies to panics like any other failure.
func TestNodePanicHonoursErrorPolicy(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "i", Type: "input"},
		{ID: "p", Type: "test_panic", Data: map[string]any{"execution": map[string]any{"on_error": "error_output"}}},
		{ID: "out", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "p", TargetHandle: "data"},
		{Source: "p", SourceHandle: workflow.NodeFailurePort, Target: "out", TargetHandle: "input"},
	}}
	result, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), graph, nil, []string{"i"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	failure, _ := result.Outputs["input"].(map[string]any)
	if failure == nil || failure["node_id"] != "p" {
		t.Fatalf("outputs = %v, want the failure payload on __error", result.Outputs)
	}
}

// A busy script must stop when its run is cancelled, and cancellation must not
// be mistaken for a script error routed to Script's "false" port.
func TestJavaScriptNodesStopOnCancel(t *testing.T) {
	for _, tt := range []struct {
		kind string
		data map[string]any
	}{
		{"script", map[string]any{"code": "while (true) {}"}},
		{"conditional", map[string]any{"expression": "(function(){ while (true) {} })()"}},
		{"loop", map[string]any{"expression": "(function(){ while (true) {} })()"}},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
				{ID: "i", Type: "input"},
				{ID: "js", Type: tt.kind, Data: tt.data},
				{ID: "out", Type: "output"},
			}, Edges: []service.WorkflowEdge{
				{Source: "i", SourceHandle: "data", Target: "js", TargetHandle: "data"},
				{Source: "js", SourceHandle: "false", Target: "out", TargetHandle: "input"},
			}}
			ctx, cancel := context.WithTimeout(executiontest.Context(t), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(ctx, graph, nil, []string{"i"}, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want the run's deadline", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("script kept running after its run was cancelled")
			}
		})
	}
}
