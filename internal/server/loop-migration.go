package server

import (
	"context"
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
)

// migrateAgentCallMaxIterations rewrites any persisted agent_call node
// whose data.max_iterations is the legacy 0 = unlimited sentinel to the
// platform default. Idempotent: subsequent runs find nothing to fix.
//
// Background: prior to the loop governor, the workflow agent_call node
// treated max_iterations: 0 as "no cap" — a foot-gun that contributed
// to the 2026-04 token-burn incident. The validator now rejects 0 at
// create/update time (see internal/service/workflow/nodes/agent-call.go),
// but existing persisted graphs still carry the old value. This pass
// brings them into compliance on startup.
//
// Safe to call when workflowStore is nil; behaves as a no-op.
func (s *Server) migrateAgentCallMaxIterations(ctx context.Context) {
	if s.workflowStore == nil {
		return
	}
	if s.loopGov == nil {
		return
	}

	// Cap the rewrite at the platform's iteration ceiling. The governor
	// will further clamp at runtime, but writing the ceiling here means
	// editors and the UI see a sane default after the migration.
	target := s.loopGov.Config().MaxIterCeiling
	if target <= 0 {
		target = 60
	}

	list, err := s.workflowStore.ListWorkflows(ctx, nil)
	if err != nil {
		slog.Warn("loopgov.workflow_migration_skipped",
			"reason", "list_failed",
			"error", err.Error())
		return
	}
	if list == nil {
		return
	}

	rewritten := 0
	for _, wf := range list.Data {
		if !rewriteAgentCallZero(&wf.Graph, target) {
			continue
		}
		updated := wf
		if _, err := s.workflowStore.UpdateWorkflow(ctx, wf.ID, updated); err != nil {
			slog.Warn("loopgov.workflow_migration_failed",
				"workflow_id", wf.ID,
				"error", err.Error())
			continue
		}
		slog.Info("loopgov.workflow_migrated",
			"workflow_id", wf.ID,
			"workflow_name", wf.Name,
			"new_max_iterations", target)
		rewritten++
	}

	if rewritten > 0 {
		slog.Info("loopgov.workflow_migration_complete", "rewritten", rewritten)
	}
}

// rewriteAgentCallZero scans the graph in place for agent_call nodes
// whose data.max_iterations is exactly 0 (the JSON number; loaded as
// float64 by encoding/json into map[string]any) and replaces them with
// target. Returns true when at least one node was rewritten.
func rewriteAgentCallZero(graph *service.WorkflowGraph, target int) bool {
	if graph == nil {
		return false
	}
	rewrote := false
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.Type != "agent_call" {
			continue
		}
		if n.Data == nil {
			continue
		}
		// JSON numbers decode as float64 in map[string]any.
		v, ok := n.Data["max_iterations"]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			if x == 0 {
				n.Data["max_iterations"] = float64(target)
				rewrote = true
			}
		case int:
			if x == 0 {
				n.Data["max_iterations"] = float64(target)
				rewrote = true
			}
		case int64:
			if x == 0 {
				n.Data["max_iterations"] = float64(target)
				rewrote = true
			}
		}
	}
	return rewrote
}
