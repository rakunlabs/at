package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
)

func layoutNode(id, typ string) service.WorkflowNode {
	return service.WorkflowNode{ID: id, Type: typ, Data: map[string]any{"label": id}}
}

func TestValidateWorkflowGraphWiring(t *testing.T) {
	nodes := []service.WorkflowNode{
		layoutNode("in", "input"), layoutNode("agent", "agent_call"), layoutNode("out", "output"),
		{ID: "note", Type: "sticky_note", Data: map[string]any{"text": "hi"}},
	}
	edge := func(id, s, sh, t, th string) service.WorkflowEdge {
		return service.WorkflowEdge{ID: id, Source: s, SourceHandle: sh, Target: t, TargetHandle: th}
	}
	tests := []struct {
		name  string
		edges []service.WorkflowEdge
		want  string
	}{
		{"valid", []service.WorkflowEdge{edge("a", "in", "data", "agent", "prompt"), edge("b", "agent", "response", "out", "input")}, ""},
		{"legacy aliases", []service.WorkflowEdge{edge("a", "in", "output", "agent", "prompt")}, ""},
		{"unknown output", []service.WorkflowEdge{edge("a", "agent", "result", "out", "input")}, "no output \"result\" (outputs: response, files, image)"},
		{"unknown input", []service.WorkflowEdge{edge("a", "in", "data", "agent", "text")}, "no input \"text\""},
		{"note", []service.WorkflowEdge{edge("a", "note", "", "agent", "prompt")}, "visual only"},
		{"missing node", []service.WorkflowEdge{edge("a", "ghost", "data", "agent", "prompt")}, "unknown source node"},
		{"cycle", []service.WorkflowEdge{edge("a", "agent", "response", "out", "input"), edge("b", "out", "", "agent", "prompt")}, "loop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkflowGraphWiring(service.WorkflowGraph{Nodes: nodes, Edges: tt.edges})
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLayoutWorkflowGraphSpreadsPiledNodes(t *testing.T) {
	note := service.WorkflowNode{ID: "note", Type: "sticky_note", Data: map[string]any{"text": "About"}}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			layoutNode("in", "input"), layoutNode("check", "conditional"),
			layoutNode("yes", "agent_call"), layoutNode("no", "log"), layoutNode("out", "output"),
			layoutNode("skills", "skill_config"), note,
		},
		Edges: []service.WorkflowEdge{
			{ID: "1", Source: "in", SourceHandle: "data", Target: "check", TargetHandle: "data"},
			{ID: "2", Source: "check", SourceHandle: "true", Target: "yes", TargetHandle: "prompt"},
			{ID: "3", Source: "check", SourceHandle: "false", Target: "no", TargetHandle: "data"},
			{ID: "4", Source: "yes", SourceHandle: "response", Target: "out", TargetHandle: "input"},
			{ID: "5", Source: "skills", SourceHandle: "skills", Target: "yes", TargetHandle: "skills"},
		},
	}
	if !graphNeedsLayout(graph) {
		t.Fatal("nodes at the origin must need layout")
	}
	laid, err := prepareAgentWorkflowGraph(&graph, "")
	if err != nil || !laid {
		t.Fatalf("laid=%v err=%v", laid, err)
	}
	pos := map[string]service.WorkflowPos{}
	for _, n := range graph.Nodes {
		pos[n.ID] = n.Position
	}
	if !(pos["in"].X < pos["check"].X && pos["check"].X < pos["yes"].X && pos["yes"].X < pos["out"].X) {
		t.Fatalf("flow is not left to right: %+v", pos)
	}
	if pos["yes"].X != pos["no"].X || pos["yes"].Y == pos["no"].Y {
		t.Fatalf("branches must share a column on separate rows: %+v %+v", pos["yes"], pos["no"])
	}
	if pos["skills"].X != pos["yes"].X || pos["skills"].Y <= pos["yes"].Y {
		t.Fatalf("resource node must sit under its agent: %+v %+v", pos["skills"], pos["yes"])
	}
	if pos["note"].Y >= pos["check"].Y-100 {
		t.Fatalf("a note covering the flow must move above it: %+v", pos["note"])
	}
	if graphNeedsLayout(graph) {
		t.Fatal("layout left overlapping steps")
	}

	// Explicit positions are kept.
	before := graph.Nodes[0].Position
	graph.Nodes[0].Position.X += 1000
	if laid, err := prepareAgentWorkflowGraph(&graph, "keep"); err != nil || laid || graph.Nodes[0].Position.X != before.X+1000 {
		t.Fatalf("keep changed positions: laid=%v err=%v", laid, err)
	}
}
