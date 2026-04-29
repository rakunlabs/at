package server

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestRewriteAgentCallZero(t *testing.T) {
	graph := &service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{
				ID:   "n1",
				Type: "agent_call",
				Data: map[string]any{"max_iterations": float64(0), "label": "A"},
			},
			{
				ID:   "n2",
				Type: "agent_call",
				Data: map[string]any{"max_iterations": float64(15), "label": "B"},
			},
			{
				ID:   "n3",
				Type: "llm_call",
				Data: map[string]any{"max_iterations": float64(0)},
			},
			{
				ID:   "n4",
				Type: "agent_call",
				Data: nil,
			},
			{
				ID:   "n5",
				Type: "agent_call",
				Data: map[string]any{"max_iterations": int(0)},
			},
		},
	}

	rewritten := rewriteAgentCallZero(graph, 30)
	if !rewritten {
		t.Fatal("expected at least one rewrite")
	}

	// n1 (agent_call, 0) → 30
	if v, _ := graph.Nodes[0].Data["max_iterations"].(float64); v != 30 {
		t.Errorf("n1: got %v want 30", graph.Nodes[0].Data["max_iterations"])
	}
	// n2 (agent_call, 15) → 15 (unchanged)
	if v, _ := graph.Nodes[1].Data["max_iterations"].(float64); v != 15 {
		t.Errorf("n2: got %v want 15 (unchanged)", graph.Nodes[1].Data["max_iterations"])
	}
	// n3 (llm_call, 0) → 0 (unchanged; not agent_call)
	if v, _ := graph.Nodes[2].Data["max_iterations"].(float64); v != 0 {
		t.Errorf("n3: got %v want 0 (unchanged, llm_call)", graph.Nodes[2].Data["max_iterations"])
	}
	// n4 (nil data) — no change, no panic
	if graph.Nodes[3].Data != nil {
		t.Errorf("n4: data should remain nil")
	}
	// n5 (int 0) → 30
	if v, _ := graph.Nodes[4].Data["max_iterations"].(float64); v != 30 {
		t.Errorf("n5: got %v want 30", graph.Nodes[4].Data["max_iterations"])
	}
}

func TestRewriteAgentCallZeroIdempotent(t *testing.T) {
	graph := &service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{
				ID:   "n1",
				Type: "agent_call",
				Data: map[string]any{"max_iterations": float64(15)},
			},
		},
	}
	if rewriteAgentCallZero(graph, 30) {
		t.Fatal("graph with no zeroes should report no rewrites")
	}
}

func TestRewriteAgentCallZeroNilGraph(t *testing.T) {
	if rewriteAgentCallZero(nil, 30) {
		t.Fatal("nil graph should not report rewrites")
	}
}
