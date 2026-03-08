package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// RecordHeartbeatAPI handles POST /api/v1/agents/{id}/heartbeat.
func (s *Server) RecordHeartbeatAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentHeartbeatStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req struct {
		Metadata map[string]any `json:"metadata"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
	}

	if err := s.agentHeartbeatStore.RecordHeartbeat(r.Context(), agentID, req.Metadata); err != nil {
		slog.Error("record heartbeat failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to record heartbeat: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "heartbeat recorded", http.StatusOK)
}

// GetHeartbeatAPI handles GET /api/v1/agents/{id}/heartbeat-status.
func (s *Server) GetHeartbeatAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentHeartbeatStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentHeartbeatStore.GetHeartbeat(r.Context(), agentID)
	if err != nil {
		slog.Error("get heartbeat failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get heartbeat: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("heartbeat for agent %q not found", agentID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// ListHeartbeatsAPI handles GET /api/v1/heartbeats.
func (s *Server) ListHeartbeatsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentHeartbeatStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.agentHeartbeatStore.ListHeartbeats(r.Context())
	if err != nil {
		slog.Error("list heartbeats failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list heartbeats: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AgentHeartbeat{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
