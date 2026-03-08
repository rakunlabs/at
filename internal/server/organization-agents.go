package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListOrganizationAgentsAPI handles GET /api/v1/organizations/{id}/agents.
func (s *Server) ListOrganizationAgentsAPI(w http.ResponseWriter, r *http.Request) {
	if s.orgAgentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	if orgID == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	records, err := s.orgAgentStore.ListOrganizationAgents(r.Context(), orgID)
	if err != nil {
		slog.Error("list organization agents failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list organization agents: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.OrganizationAgent{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// AddAgentToOrganizationAPI handles POST /api/v1/organizations/{id}/agents.
func (s *Server) AddAgentToOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.orgAgentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	if orgID == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	var req service.OrganizationAgent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		httpResponse(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	req.OrganizationID = orgID

	// Check for existing membership.
	existing, err := s.orgAgentStore.GetOrganizationAgentByPair(r.Context(), orgID, req.AgentID)
	if err != nil {
		slog.Error("check existing membership failed", "org_id", orgID, "agent_id", req.AgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to check membership: %v", err), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		httpResponse(w, "agent is already a member of this organization", http.StatusConflict)
		return
	}

	record, err := s.orgAgentStore.CreateOrganizationAgent(r.Context(), req)
	if err != nil {
		slog.Error("add agent to organization failed", "org_id", orgID, "agent_id", req.AgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to add agent to organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateOrganizationAgentAPI handles PUT /api/v1/organizations/{id}/agents/{agent_id}.
func (s *Server) UpdateOrganizationAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.orgAgentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	agentID := r.PathValue("agent_id")
	if orgID == "" || agentID == "" {
		httpResponse(w, "organization id and agent id are required", http.StatusBadRequest)
		return
	}

	// Find the membership by pair.
	existing, err := s.orgAgentStore.GetOrganizationAgentByPair(r.Context(), orgID, agentID)
	if err != nil {
		slog.Error("get organization agent failed", "org_id", orgID, "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get membership: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, "agent is not a member of this organization", http.StatusNotFound)
		return
	}

	var req service.OrganizationAgent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Preserve status if not provided.
	if req.Status == "" {
		req.Status = existing.Status
	}

	record, err := s.orgAgentStore.UpdateOrganizationAgent(r.Context(), existing.ID, req)
	if err != nil {
		slog.Error("update organization agent failed", "id", existing.ID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update membership: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, "membership not found", http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// RemoveAgentFromOrganizationAPI handles DELETE /api/v1/organizations/{id}/agents/{agent_id}.
func (s *Server) RemoveAgentFromOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.orgAgentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	agentID := r.PathValue("agent_id")
	if orgID == "" || agentID == "" {
		httpResponse(w, "organization id and agent id are required", http.StatusBadRequest)
		return
	}

	if err := s.orgAgentStore.DeleteOrganizationAgentByPair(r.Context(), orgID, agentID); err != nil {
		slog.Error("remove agent from organization failed", "org_id", orgID, "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to remove agent from organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "removed", http.StatusOK)
}
