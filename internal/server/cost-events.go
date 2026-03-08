package server

import (
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
