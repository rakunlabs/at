package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// CreateWakeupRequestAPI handles POST /api/v1/agents/{id}/wakeup.
func (s *Server) CreateWakeupRequestAPI(w http.ResponseWriter, r *http.Request) {
	if s.wakeupRequestStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.WakeupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.AgentID = agentID

	record, err := s.wakeupRequestStore.CreateOrCoalesce(r.Context(), req)
	if err != nil {
		slog.Error("create wakeup request failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create wakeup request: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// GetWakeupRequestAPI handles GET /api/v1/wakeup-requests/{id}.
func (s *Server) GetWakeupRequestAPI(w http.ResponseWriter, r *http.Request) {
	if s.wakeupRequestStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "wakeup request id is required", http.StatusBadRequest)
		return
	}

	record, err := s.wakeupRequestStore.GetWakeupRequest(r.Context(), id)
	if err != nil {
		slog.Error("get wakeup request failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get wakeup request: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("wakeup request %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// ListPendingWakeupRequestsAPI handles GET /api/v1/agents/{id}/wakeup-requests.
func (s *Server) ListPendingWakeupRequestsAPI(w http.ResponseWriter, r *http.Request) {
	if s.wakeupRequestStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	records, err := s.wakeupRequestStore.ListPendingForAgent(r.Context(), agentID)
	if err != nil {
		slog.Error("list pending wakeup requests failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list pending wakeup requests: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.WakeupRequest{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// MarkWakeupDispatchedAPI handles POST /api/v1/wakeup-requests/{id}/dispatch.
func (s *Server) MarkWakeupDispatchedAPI(w http.ResponseWriter, r *http.Request) {
	if s.wakeupRequestStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "wakeup request id is required", http.StatusBadRequest)
		return
	}

	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.wakeupRequestStore.MarkDispatched(r.Context(), id, body.RunID); err != nil {
		slog.Error("mark wakeup dispatched failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to mark wakeup dispatched: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "dispatched", http.StatusOK)
}

// PromoteDeferredWakeupAPI handles POST /api/v1/agents/{id}/wakeup-requests/promote.
func (s *Server) PromoteDeferredWakeupAPI(w http.ResponseWriter, r *http.Request) {
	if s.wakeupRequestStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	if err := s.wakeupRequestStore.PromoteDeferred(r.Context(), agentID); err != nil {
		slog.Error("promote deferred wakeup failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to promote deferred wakeup requests: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "promoted", http.StatusOK)
}
