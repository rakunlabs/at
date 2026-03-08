package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListAgentConfigRevisionsAPI handles GET /api/v1/agents/{id}/config-revisions.
func (s *Server) ListAgentConfigRevisionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentConfigRevisionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	records, err := s.agentConfigRevisionStore.ListRevisions(r.Context(), agentID)
	if err != nil {
		slog.Error("list agent config revisions failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list agent config revisions: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AgentConfigRevision{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentConfigRevisionAPI handles GET /api/v1/agent-config-revisions/{id}.
func (s *Server) GetAgentConfigRevisionAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentConfigRevisionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "revision id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentConfigRevisionStore.GetRevision(r.Context(), id)
	if err != nil {
		slog.Error("get agent config revision failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent config revision: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent config revision %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// GetLatestAgentConfigRevisionAPI handles GET /api/v1/agents/{id}/config-revisions/latest.
func (s *Server) GetLatestAgentConfigRevisionAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentConfigRevisionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentConfigRevisionStore.GetLatestRevision(r.Context(), agentID)
	if err != nil {
		slog.Error("get latest agent config revision failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get latest agent config revision: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("no revisions found for agent %q", agentID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}
