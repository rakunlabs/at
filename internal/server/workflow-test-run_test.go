package server

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestPrepareWorkflowStreamTestRun(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "n", Type: "llm_call"}, {ID: "detached", Type: "template"}}, Edges: []service.WorkflowEdge{{Source: "i", Target: "n"}}}
	for _, tt := range []struct {
		name    string
		request runWorkflowRequest
		wantErr bool
	}{
		{"normal", runWorkflowRequest{}, false},
		{"target", runWorkflowRequest{Test: &workflow.TestRunOptions{TargetNodeID: "n"}}, false},
		{"unknown target", runWorkflowRequest{Test: &workflow.TestRunOptions{TargetNodeID: "absent"}}, true},
		{"disconnected target", runWorkflowRequest{Test: &workflow.TestRunOptions{TargetNodeID: "detached"}}, true},
		{"invalid entry", runWorkflowRequest{EntryNodeIDs: []string{"n"}, Test: &workflow.TestRunOptions{}}, true},
		{"stale pin", runWorkflowRequest{Test: &workflow.TestRunOptions{Pins: map[string]workflow.PinnedNode{"n": {Signature: "stale", Data: map[string]any{}, ResultKind: "result"}}}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := prepareWorkflowStreamRun(graph, tt.request)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, expected error=%v", err, tt.wantErr)
			}
			if err == nil && (len(entries) != 1 || entries[0] != "i") {
				t.Fatalf("wrong entries: %v", entries)
			}
		})
	}
}
