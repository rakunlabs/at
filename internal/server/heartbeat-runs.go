package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListHeartbeatRunsAPI handles GET /api/v1/agents/{id}/runs.
func (s *Server) ListHeartbeatRunsAPI(w http.ResponseWriter, r *http.Request) {
	if s.heartbeatRunStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.heartbeatRunStore.ListHeartbeatRuns(r.Context(), agentID, q)
	if err != nil {
		slog.Error("list heartbeat runs failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list heartbeat runs: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.HeartbeatRun]{Data: []service.HeartbeatRun{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetHeartbeatRunAPI handles GET /api/v1/heartbeat-runs/{id}.
func (s *Server) GetHeartbeatRunAPI(w http.ResponseWriter, r *http.Request) {
	if s.heartbeatRunStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "heartbeat run id is required", http.StatusBadRequest)
		return
	}

	record, err := s.heartbeatRunStore.GetHeartbeatRun(r.Context(), id)
	if err != nil {
		slog.Error("get heartbeat run failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get heartbeat run: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("heartbeat run %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateHeartbeatRunAPI handles POST /api/v1/agents/{id}/runs.
func (s *Server) CreateHeartbeatRunAPI(w http.ResponseWriter, r *http.Request) {
	if s.heartbeatRunStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.HeartbeatRun
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.AgentID = agentID

	if req.InvocationSource == "" {
		httpResponse(w, "invocation_source is required", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "queued"
	}

	record, err := s.heartbeatRunStore.CreateHeartbeatRun(r.Context(), req)
	if err != nil {
		slog.Error("create heartbeat run failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create heartbeat run: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateHeartbeatRunAPI handles PUT /api/v1/heartbeat-runs/{id}.
func (s *Server) UpdateHeartbeatRunAPI(w http.ResponseWriter, r *http.Request) {
	if s.heartbeatRunStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "heartbeat run id is required", http.StatusBadRequest)
		return
	}

	var req service.HeartbeatRun
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	record, err := s.heartbeatRunStore.UpdateHeartbeatRun(r.Context(), id, req)
	if err != nil {
		slog.Error("update heartbeat run failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update heartbeat run: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("heartbeat run %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// GetActiveRunAPI handles GET /api/v1/agents/{id}/active-run.
func (s *Server) GetActiveRunAPI(w http.ResponseWriter, r *http.Request) {
	if s.heartbeatRunStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.heartbeatRunStore.GetActiveRun(r.Context(), agentID)
	if err != nil {
		slog.Error("get active run failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get active run: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("no active run found for agent %q", agentID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}
