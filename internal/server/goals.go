package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListGoalsAPI handles GET /api/v1/goals.
func (s *Server) ListGoalsAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.goalStore.ListGoals(r.Context(), q)
	if err != nil {
		slog.Error("list goals failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list goals: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Goal]{Data: []service.Goal{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetGoalAPI handles GET /api/v1/goals/{id}.
func (s *Server) GetGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	record, err := s.goalStore.GetGoal(r.Context(), id)
	if err != nil {
		slog.Error("get goal failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get goal: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("goal %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateGoalAPI handles POST /api/v1/goals.
func (s *Server) CreateGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Goal
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "active"
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.goalStore.CreateGoal(r.Context(), req)
	if err != nil {
		slog.Error("create goal failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create goal: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateGoalAPI handles PUT /api/v1/goals/{id}.
func (s *Server) UpdateGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	var req service.Goal
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.goalStore.UpdateGoal(r.Context(), id, req)
	if err != nil {
		slog.Error("update goal failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update goal: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("goal %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteGoalAPI handles DELETE /api/v1/goals/{id}.
func (s *Server) DeleteGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	if err := s.goalStore.DeleteGoal(r.Context(), id); err != nil {
		slog.Error("delete goal failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete goal: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ListGoalChildrenAPI handles GET /api/v1/goals/{id}/children.
func (s *Server) ListGoalChildrenAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	records, err := s.goalStore.ListGoalsByParent(r.Context(), id)
	if err != nil {
		slog.Error("list goal children failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list goal children: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Goal{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetGoalAncestryAPI handles GET /api/v1/goals/{id}/ancestry.
func (s *Server) GetGoalAncestryAPI(w http.ResponseWriter, r *http.Request) {
	if s.goalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	records, err := s.goalStore.GetGoalAncestry(r.Context(), id)
	if err != nil {
		slog.Error("get goal ancestry failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get goal ancestry: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Goal{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
