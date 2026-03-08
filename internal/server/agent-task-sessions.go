package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListAgentTaskSessionsAPI handles GET /api/v1/agents/{id}/task-sessions.
func (s *Server) ListAgentTaskSessionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	records, err := s.agentTaskSessionStore.ListAgentTaskSessions(r.Context(), agentID)
	if err != nil {
		slog.Error("list agent task sessions failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list agent task sessions: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AgentTaskSession{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentTaskSessionAPI handles GET /api/v1/agents/{id}/task-sessions/{task_key}.
func (s *Server) GetAgentTaskSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	taskKey := r.PathValue("task_key")
	if taskKey == "" {
		httpResponse(w, "task key is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentTaskSessionStore.GetAgentTaskSession(r.Context(), agentID, taskKey)
	if err != nil {
		slog.Error("get agent task session failed", "agent_id", agentID, "task_key", taskKey, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent task session: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent task session %q/%q not found", agentID, taskKey), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// UpsertAgentTaskSessionAPI handles PUT /api/v1/agents/{id}/task-sessions/{task_key}.
func (s *Server) UpsertAgentTaskSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	taskKey := r.PathValue("task_key")
	if taskKey == "" {
		httpResponse(w, "task key is required", http.StatusBadRequest)
		return
	}

	var req service.AgentTaskSession
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.AgentID = agentID
	req.TaskKey = taskKey

	if err := s.agentTaskSessionStore.UpsertAgentTaskSession(r.Context(), req); err != nil {
		slog.Error("upsert agent task session failed", "agent_id", agentID, "task_key", taskKey, "error", err)
		httpResponse(w, fmt.Sprintf("failed to upsert agent task session: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "ok", http.StatusOK)
}

// DeleteAgentTaskSessionAPI handles DELETE /api/v1/agents/{id}/task-sessions/{task_key}.
func (s *Server) DeleteAgentTaskSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	taskKey := r.PathValue("task_key")
	if taskKey == "" {
		httpResponse(w, "task key is required", http.StatusBadRequest)
		return
	}

	if err := s.agentTaskSessionStore.DeleteAgentTaskSession(r.Context(), agentID, taskKey); err != nil {
		slog.Error("delete agent task session failed", "agent_id", agentID, "task_key", taskKey, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete agent task session: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
