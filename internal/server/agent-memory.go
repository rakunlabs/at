package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListOrgMemoriesAPI handles GET /api/v1/organizations/{id}/memories.
// Optional query param: ?agent_id=... to filter by agent.
func (s *Server) ListOrgMemoriesAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentMemoryStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	if orgID == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	agentID := r.URL.Query().Get("agent_id")

	var (
		records []service.AgentMemory
		err     error
	)

	if agentID != "" {
		records, err = s.agentMemoryStore.ListAgentMemories(r.Context(), agentID, orgID)
	} else {
		records, err = s.agentMemoryStore.ListOrgMemories(r.Context(), orgID)
	}

	if err != nil {
		slog.Error("list org memories failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list memories: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AgentMemory{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// SearchOrgMemoriesAPI handles POST /api/v1/organizations/{id}/memories/search.
func (s *Server) SearchOrgMemoriesAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentMemoryStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	if orgID == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	var req struct {
		Query   string `json:"query"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		httpResponse(w, "query is required", http.StatusBadRequest)
		return
	}

	records, err := s.agentMemoryStore.SearchAgentMemories(r.Context(), req.AgentID, orgID, req.Query)
	if err != nil {
		slog.Error("search org memories failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to search memories: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AgentMemory{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentMemoryAPI handles GET /api/v1/agent-memories/{id}.
func (s *Server) GetAgentMemoryAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentMemoryStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "memory id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentMemoryStore.GetAgentMemory(r.Context(), id)
	if err != nil {
		slog.Error("get agent memory failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get memory: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, "memory not found", http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// GetAgentMemoryMessagesAPI handles GET /api/v1/agent-memories/{id}/messages.
func (s *Server) GetAgentMemoryMessagesAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentMemoryStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "memory id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentMemoryStore.GetAgentMemoryMessages(r.Context(), id)
	if err != nil {
		slog.Error("get agent memory messages failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get memory messages: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, "memory messages not found", http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteAgentMemoryAPI handles DELETE /api/v1/agent-memories/{id}.
func (s *Server) DeleteAgentMemoryAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentMemoryStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "memory id is required", http.StatusBadRequest)
		return
	}

	if err := s.agentMemoryStore.DeleteAgentMemory(r.Context(), id); err != nil {
		slog.Error("delete agent memory failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete memory: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
