package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// agentsResponse wraps a list of agent records for JSON output.
type agentsResponse struct {
	Agents []service.Agent `json:"agents"`
}

// ListAgentsAPI handles GET /api/v1/agents.
func (s *Server) ListAgentsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.agentStore.ListAgents(r.Context(), q)
	if err != nil {
		slog.Error("list agents failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list agents: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Agent]{Data: []service.Agent{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentAPI handles GET /api/v1/agents/:id.
func (s *Server) GetAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentStore.GetAgent(r.Context(), id)
	if err != nil {
		slog.Error("get agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateAgentAPI handles POST /api/v1/agents.
func (s *Server) CreateAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Agent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}

	// Set defaults if missing
	if req.MaxIterations == 0 {
		req.MaxIterations = 10
	}
	if req.ToolTimeout == 0 {
		req.ToolTimeout = 60
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.agentStore.CreateAgent(r.Context(), req)
	if err != nil {
		slog.Error("create agent failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateAgentAPI handles PUT /api/v1/agents/:id.
func (s *Server) UpdateAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.Agent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail
	// Ensure UpdateTime is handled by the store, but we set user.

	record, err := s.agentStore.UpdateAgent(r.Context(), id, req)
	if err != nil {
		slog.Error("update agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update agent: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteAgentAPI handles DELETE /api/v1/agents/:id.
func (s *Server) DeleteAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	if err := s.agentStore.DeleteAgent(r.Context(), id); err != nil {
		slog.Error("delete agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
