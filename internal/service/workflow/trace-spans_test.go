package workflow

import (
	"context"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

type traceSpanTestNode struct{ record bool }

func (*traceSpanTestNode) Type() string                              { return characterizationNodeType }
func (*traceSpanTestNode) Validate(context.Context, *Registry) error { return nil }
func (n *traceSpanTestNode) Run(ctx context.Context, reg *Registry, _ map[string]any) (NodeResult, error) {
	if n.record {
		reg.RecordObservation(ctx, service.LLMCall{ObservationType: service.ObservationGeneration, Source: "workflow"})
	}
	return NewResult(map[string]any{}), nil
}

// A workflow run and its node spans are written only when an observation was
// recorded beneath them; the observation joins the run's trace beneath the
// node span, and the run span becomes the trace root named after the workflow.
func TestWorkflowTraceSpansAreLazy(t *testing.T) {
	var mu sync.Mutex
	var recorded []service.LLMCall
	record := func(ctx context.Context, obs service.LLMCall) string {
		mu.Lock()
		defer mu.Unlock()
		if tp, ok := service.TraceParentFromContext(ctx); ok && obs.TraceID == "" {
			obs.TraceID, obs.ParentObservationID = tp.TraceID, tp.ObservationID
			tp.MarkUsed()
		}
		recorded = append(recorded, obs)
		return obs.ID
	}
	run := func(node *traceSpanTestNode) {
		recorded = nil
		ctx := ContextWithWorkflowTrace(executiontest.Context(t), "wf-1", "Nightly report")
		runCtx, runSpan := startWorkflowRunSpan(ctx, record)
		st := &nodeState{node: service.WorkflowNode{ID: "n1", Data: map[string]any{"label": "Summarize"}}, noder: node}
		e := NewEngineWithDependencies(Dependencies{RecordObservation: record})
		reg := NewRegistryWithDependencies(e.ensureDependencies(), nil)
		if _, _, err := e.executeNode(runCtx, st, reg, nil, func(map[string]any, error) {}); err != nil {
			t.Fatal(err)
		}
		runSpan.finish(ctx, nil)
	}

	run(&traceSpanTestNode{})
	if len(recorded) != 0 {
		t.Fatalf("empty run recorded spans: %+v", recorded)
	}

	run(&traceSpanTestNode{record: true})
	if len(recorded) != 3 {
		t.Fatalf("want generation, node span, run span; got %+v", recorded)
	}
	gen, node, root := recorded[0], recorded[1], recorded[2]
	if node.Name != "Summarize" || node.ObservationType != service.ObservationSpan || gen.ParentObservationID != node.ID {
		t.Fatalf("node span: %+v / gen %+v", node, gen)
	}
	if root.Name != "Nightly report" || node.ParentObservationID != root.ID || root.ParentObservationID != "" || root.Trace == nil {
		t.Fatalf("run span: %+v", root)
	}
	if gen.TraceID != root.TraceID || node.TraceID != root.TraceID || root.Metadata["workflow_id"] != "wf-1" {
		t.Fatalf("trace identity: %+v", recorded)
	}
}
