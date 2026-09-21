package server

import (
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Resolve test admission against the persisted, workspace-resolved graph before
// SSE commitment. The regular runtime identity/node/provider checks still run.
func prepareWorkflowStreamRun(graph service.WorkflowGraph, req runWorkflowRequest) ([]string, error) {
	var entries []string
	allowed := make(map[string]bool)
	for _, node := range graph.Nodes {
		if node.Type == "input" {
			allowed[node.ID] = true
			entries = append(entries, node.ID)
		}
	}
	if len(req.EntryNodeIDs) > 0 {
		for _, id := range req.EntryNodeIDs {
			if !allowed[id] {
				return nil, fmt.Errorf("entry_node_id %q is not an input node", id)
			}
		}
		entries = req.EntryNodeIDs
	}
	if req.Test != nil {
		if len(entries) == 0 {
			return nil, fmt.Errorf("test runs require an Input entry node")
		}
		if err := workflow.ValidateTestRun(graph, req.Inputs, entries, *req.Test); err != nil {
			return nil, err
		}
	}
	return entries, nil
}
