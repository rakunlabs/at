package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListCostEventsAPI handles GET /api/v1/cost-events.
func (s *Server) ListCostEventsAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.costEventStore.ListCostEvents(r.Context(), q)
	if err != nil {
		slog.Error("list cost events failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list cost events: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.CostEvent]{Data: []service.CostEvent{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// RecordCostEventAPI handles POST /api/v1/cost-events.
func (s *Server) RecordCostEventAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.CostEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		httpResponse(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		httpResponse(w, "model is required", http.StatusBadRequest)
		return
	}

	if err := s.costEventStore.RecordCostEvent(r.Context(), req); err != nil {
		slog.Error("record cost event failed", "agent_id", req.AgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to record cost event: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "created", http.StatusCreated)
}

// GetCostByAgentAPI handles GET /api/v1/agents/{id}/cost.
func (s *Server) GetCostByAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	totalCost, err := s.costEventStore.GetCostByAgent(r.Context(), id)
	if err != nil {
		slog.Error("get cost by agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get cost by agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"agent_id":         id,
		"total_cost_cents": totalCost,
	}, http.StatusOK)
}

// GetCostByProjectAPI handles GET /api/v1/projects/{id}/cost.
func (s *Server) GetCostByProjectAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "project id is required", http.StatusBadRequest)
		return
	}

	totalCost, err := s.costEventStore.GetCostByProject(r.Context(), id)
	if err != nil {
		slog.Error("get cost by project failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get cost by project: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"project_id":       id,
		"total_cost_cents": totalCost,
	}, http.StatusOK)
}

// GetCostByGoalAPI handles GET /api/v1/goals/{id}/cost.
func (s *Server) GetCostByGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	totalCost, err := s.costEventStore.GetCostByGoal(r.Context(), id)
	if err != nil {
		slog.Error("get cost by goal failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get cost by goal: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"goal_id":          id,
		"total_cost_cents": totalCost,
	}, http.StatusOK)
}

// GetCostByTaskAPI handles GET /api/v1/tasks/{id}/cost.
//
// Returns the rolled-up cost for the given task PLUS every transitive
// sub-task. Pipeline tasks (Director → Script Writer → Visual Designer →
// Audio Producer → Composite) record cost events under each child's
// task_id, so summing only the root would massively under-report. The
// traversal is BFS and bounded to maxTaskDescendants nodes to defend
// against pathological trees.
func (s *Server) GetCostByTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "cost event store not configured", http.StatusServiceUnavailable)
		return
	}
	if s.taskStore == nil {
		httpResponse(w, "task store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	taskIDs, err := s.collectTaskTreeIDs(r.Context(), id)
	if err != nil {
		slog.Error("get cost by task: collect descendants failed",
			"task_id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to walk task tree: %v", err), http.StatusInternalServerError)
		return
	}

	rollup, err := s.costEventStore.GetCostByTasks(r.Context(), taskIDs)
	if err != nil {
		slog.Error("get cost by task: aggregate failed",
			"task_id", id, "task_count", len(taskIDs), "error", err)
		httpResponse(w, fmt.Sprintf("failed to aggregate cost: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"task_id":          id,
		"task_count":       len(taskIDs),
		"task_ids":         taskIDs,
		"cost_cents":       rollup.CostCents,
		"input_tokens":     rollup.InputTokens,
		"output_tokens":    rollup.OutputTokens,
		"total_tokens":     rollup.TotalTokens,
		"event_count":      rollup.EventCount,
		"total_cost_cents": rollup.CostCents, // alias for the older naming
	}, http.StatusOK)
}

// maxTaskDescendants caps the breadth-first walk of sub-tasks. A typical
// pipeline has ~10 child tasks (Director + Script Writer + Visual Designer
// + Audio Producer + a few revisions). 5000 is generous enough to handle
// pathological cases without risking unbounded queries on a runaway tree.
const maxTaskDescendants = 5000

// collectTaskTreeIDs returns the root task ID followed by every transitive
// descendant, in BFS order. The root is always first. Errors from the
// underlying ListChildTasks call are surfaced; partial walks are not
// returned (better to fail loud than silently under-count).
func (s *Server) collectTaskTreeIDs(ctx context.Context, rootID string) ([]string, error) {
	if rootID == "" {
		return nil, nil
	}
	seen := make(map[string]struct{}, 16)
	out := make([]string, 0, 16)
	queue := []string{rootID}

	for len(queue) > 0 && len(out) < maxTaskDescendants {
		cur := queue[0]
		queue = queue[1:]
		if _, ok := seen[cur]; ok {
			continue
		}
		seen[cur] = struct{}{}
		out = append(out, cur)

		children, err := s.taskStore.ListChildTasks(ctx, cur)
		if err != nil {
			return nil, fmt.Errorf("list child tasks for %s: %w", cur, err)
		}
		for _, c := range children {
			if _, ok := seen[c.ID]; ok {
				continue
			}
			queue = append(queue, c.ID)
		}
	}
	if len(out) >= maxTaskDescendants {
		slog.Warn("collectTaskTreeIDs: descendant cap reached, results may be partial",
			"root_id", rootID, "cap", maxTaskDescendants)
	}
	return out, nil
}

// GetCostByBillingCodeAPI handles GET /api/v1/cost-events/by-billing-code?code=xxx.
func (s *Server) GetCostByBillingCodeAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		httpResponse(w, "code query parameter is required", http.StatusBadRequest)
		return
	}

	totalCost, err := s.costEventStore.GetCostByBillingCode(r.Context(), code)
	if err != nil {
		slog.Error("get cost by billing code failed", "code", code, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get cost by billing code: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"billing_code":     code,
		"total_cost_cents": totalCost,
	}, http.StatusOK)
}
