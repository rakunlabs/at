package server

import (
	"context"
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

	// Validate hierarchy if parent is specified.
	if req.ParentAgentID != "" {
		if err := s.validateHierarchy(r.Context(), orgID, req.AgentID, req.ParentAgentID); err != nil {
			httpResponse(w, fmt.Sprintf("hierarchy validation failed: %v", err), http.StatusBadRequest)
			return
		}
	}

	approval, requested, err := s.requestOrgAgentApprovalIfRequired(r.Context(), orgID, req, "user", s.getUserEmail(r))
	if err != nil {
		slog.Error("request organization agent approval failed", "org_id", orgID, "agent_id", req.AgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to request approval: %v", err), http.StatusInternalServerError)
		return
	}
	if requested {
		httpResponseJSON(w, approval, http.StatusAccepted)
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

	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	updated := *existing
	updated.OrganizationID = orgID
	updated.AgentID = agentID
	parentChanged := false
	if raw, ok := fields["role"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid role: %v", err), http.StatusBadRequest)
			return
		}
		updated.Role = v
	}
	if raw, ok := fields["title"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid title: %v", err), http.StatusBadRequest)
			return
		}
		updated.Title = v
	}
	if raw, ok := fields["parent_agent_id"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid parent_agent_id: %v", err), http.StatusBadRequest)
			return
		}
		updated.ParentAgentID = v
		parentChanged = true
	}
	if raw, ok := fields["status"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid status: %v", err), http.StatusBadRequest)
			return
		}
		if v != "" {
			updated.Status = v
		}
	}
	if raw, ok := fields["heartbeat_schedule"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid heartbeat_schedule: %v", err), http.StatusBadRequest)
			return
		}
		updated.HeartbeatSchedule = v
	}

	// Validate hierarchy if parent is being changed.
	if parentChanged && updated.ParentAgentID != "" && updated.ParentAgentID != existing.ParentAgentID {
		if err := s.validateHierarchy(r.Context(), orgID, agentID, updated.ParentAgentID); err != nil {
			httpResponse(w, fmt.Sprintf("hierarchy validation failed: %v", err), http.StatusBadRequest)
			return
		}
	}

	record, err := s.orgAgentStore.UpdateOrganizationAgent(r.Context(), existing.ID, updated)
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

// validateHierarchy checks that setting parentAgentID for agentID in orgID
// would not create a cycle. Returns an error if validation fails.
func (s *Server) validateHierarchy(ctx context.Context, orgID, agentID, parentAgentID string) error {
	if parentAgentID == "" {
		return nil // root node, always valid
	}

	// Load all org agents.
	agents, err := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
	if err != nil {
		return fmt.Errorf("load org agents: %w", err)
	}

	// Check parent exists in org.
	parentFound := false
	for _, a := range agents {
		if a.AgentID == parentAgentID {
			parentFound = true
			break
		}
	}
	if !parentFound {
		return fmt.Errorf("parent agent %q is not a member of this organization", parentAgentID)
	}

	// Build parent map: agentID -> parentAgentID (apply proposed change).
	parentMap := make(map[string]string)
	for _, a := range agents {
		parentMap[a.AgentID] = a.ParentAgentID
	}
	parentMap[agentID] = parentAgentID

	// Cycle detection: walk from agentID up through parents.
	visited := map[string]bool{agentID: true}
	current := parentAgentID
	for current != "" {
		if visited[current] {
			return fmt.Errorf("hierarchy cycle detected: setting parent to %q creates a loop", parentAgentID)
		}
		visited[current] = true
		current = parentMap[current]
	}

	return nil
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

	var clearHead bool
	var org *service.Organization
	if s.organizationStore != nil {
		var err error
		org, err = s.organizationStore.GetOrganization(r.Context(), orgID)
		if err != nil {
			slog.Error("get organization before removing agent failed", "org_id", orgID, "error", err)
			httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
			return
		}
		clearHead = org != nil && org.HeadAgentID == agentID
	}

	if err := s.orgAgentStore.DeleteOrganizationAgentByPair(r.Context(), orgID, agentID); err != nil {
		slog.Error("remove agent from organization failed", "org_id", orgID, "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to remove agent from organization: %v", err), http.StatusInternalServerError)
		return
	}

	if clearHead {
		org.HeadAgentID = ""
		org.UpdatedBy = s.getUserEmail(r)
		if _, err := s.organizationStore.UpdateOrganization(r.Context(), orgID, *org); err != nil {
			slog.Error("clear head agent after removal failed", "org_id", orgID, "agent_id", agentID, "error", err)
			httpResponse(w, fmt.Sprintf("failed to clear head agent: %v", err), http.StatusInternalServerError)
			return
		}
	}

	httpResponse(w, "removed", http.StatusOK)
}

func (s *Server) requestOrgAgentApprovalIfRequired(ctx context.Context, orgID string, oa service.OrganizationAgent, requestedByType, requestedByID string) (*service.Approval, bool, error) {
	if s.organizationStore == nil {
		return nil, false, nil
	}
	org, err := s.organizationStore.GetOrganization(ctx, orgID)
	if err != nil {
		return nil, false, fmt.Errorf("get organization %q: %w", orgID, err)
	}
	if org == nil || !org.RequireBoardApproval {
		return nil, false, nil
	}
	if s.approvalStore == nil {
		return nil, true, fmt.Errorf("approval store not configured")
	}
	status := oa.Status
	if status == "" {
		status = "active"
	}
	approval, err := s.approvalStore.CreateApproval(ctx, service.Approval{
		OrganizationID:  orgID,
		Type:            service.ApprovalTypeHireAgent,
		Status:          service.ApprovalStatusPending,
		RequestedByType: requestedByType,
		RequestedByID:   requestedByID,
		RequestDetails: map[string]any{
			"organization_id":    orgID,
			"agent_id":           oa.AgentID,
			"role":               oa.Role,
			"title":              oa.Title,
			"parent_agent_id":    oa.ParentAgentID,
			"status":             status,
			"heartbeat_schedule": oa.HeartbeatSchedule,
		},
	})
	if err != nil {
		return nil, true, fmt.Errorf("create hire-agent approval: %w", err)
	}
	return approval, true, nil
}
