package workflow

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestPinSignatureScope(t *testing.T) {
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "n", Type: "llm_call", Data: map[string]any{"provider": "p"}}, {ID: "o", Type: "output"}},
		Edges: []service.WorkflowEdge{{Source: "i", Target: "n", SourceHandle: "data", TargetHandle: "prompt"}, {Source: "n", Target: "o"}},
	}
	inputs := map[string]any{"text": "hello"}
	base := NodePinSignature(graph, inputs, []string{"i"}, "n")
	if base == "" {
		t.Fatal("expected pinnable node")
	}
	graph.Nodes[1].Position.X = 900
	graph.Nodes[1].Data["label"] = "new label"
	graph.Nodes[2].Data = map[string]any{"fields": []string{"changed"}}
	if got := NodePinSignature(graph, inputs, []string{"i"}, "n"); got != base {
		t.Fatal("presentation/downstream edit invalidated pin")
	}
	if got := NodePinSignature(graph, map[string]any{"text": "different"}, []string{"i"}, "n"); got == base {
		t.Fatal("changed run input kept pin valid")
	}
	graph.Edges[0].TargetHandle = "context"
	if got := NodePinSignature(graph, inputs, []string{"i"}, "n"); got == base {
		t.Fatal("changed input wiring kept pin valid")
	}
	graph.Nodes[0].Type = "loop"
	if got := NodePinSignature(graph, inputs, nil, "n"); got != "" {
		t.Fatal("fan-out invocation offered as complete fixture")
	}
}

func TestTestRunRejectsInvalidFixtureBeforeExecution(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "n", Type: "llm_call"}}, Edges: []service.WorkflowEdge{{Source: "i", Target: "n"}}}
	signature := NodePinSignature(graph, nil, []string{"i"}, "n")
	for _, tt := range []struct {
		name    string
		options TestRunOptions
	}{
		{"missing target", TestRunOptions{TargetNodeID: "missing"}},
		{"stale", TestRunOptions{Pins: map[string]PinnedNode{"n": {Signature: "old", Data: map[string]any{}, ResultKind: "result"}}}},
		{"oversized", TestRunOptions{Pins: map[string]PinnedNode{"n": {Signature: signature, Data: map[string]any{"text": strings.Repeat("x", nodePreviewMaxBytes+1)}, ResultKind: "result"}}}},
		{"fanout", TestRunOptions{Pins: map[string]PinnedNode{"n": {Signature: signature, Data: map[string]any{}, ResultKind: "fan_out"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateTestRun(graph, nil, []string{"i"}, tt.options); err == nil {
				t.Fatal("invalid test run accepted")
			}
		})
	}
}

func TestTestRunBoundsUnusedFixtures(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "input", Type: "input"}}}
	if err := ValidateTestRun(graph, nil, nil, TestRunOptions{TargetNodeID: "input", Pins: map[string]PinnedNode{
		"input": {Data: map[string]any{"value": strings.Repeat("x", nodePreviewMaxBytes+1)}},
	}}); err == nil {
		t.Fatal("selected-target fixture bypassed size validation")
	}
	pins := make(map[string]PinnedNode)
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		pins[id] = PinnedNode{Data: map[string]any{"value": strings.Repeat("x", 60*1024)}}
	}
	if err := ValidateTestRun(graph, nil, nil, TestRunOptions{Pins: pins}); err == nil {
		t.Fatal("unused fixtures bypassed combined size validation")
	}
}
