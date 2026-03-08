package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// GetAgentRuntimeStateAPI handles GET /api/v1/agents/{id}/runtime-state.
func (s *Server) GetAgentRuntimeStateAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentRuntimeStateStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentRuntimeStateStore.GetAgentRuntimeState(r.Context(), agentID)
	if err != nil {
		slog.Error("get agent runtime state failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent runtime state: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent runtime state %q not found", agentID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// UpsertAgentRuntimeStateAPI handles PUT /api/v1/agents/{id}/runtime-state.
func (s *Server) UpsertAgentRuntimeStateAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentRuntimeStateStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.AgentRuntimeState
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.AgentID = agentID

	if err := s.agentRuntimeStateStore.UpsertAgentRuntimeState(r.Context(), req); err != nil {
		slog.Error("upsert agent runtime state failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to upsert agent runtime state: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "ok", http.StatusOK)
}

// AccumulateUsageAPI handles POST /api/v1/agents/{id}/runtime-state/accumulate.
func (s *Server) AccumulateUsageAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentRuntimeStateStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var body struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
		CostCents    int64 `json:"cost_cents"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.agentRuntimeStateStore.AccumulateUsage(r.Context(), agentID, body.InputTokens, body.OutputTokens, body.CostCents); err != nil {
		slog.Error("accumulate usage failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to accumulate usage: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "ok", http.StatusOK)
}
