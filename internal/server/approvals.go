package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListApprovalsAPI handles GET /api/v1/approvals.
func (s *Server) ListApprovalsAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.approvalStore.ListApprovals(r.Context(), q)
	if err != nil {
		slog.Error("list approvals failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list approvals: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Approval]{Data: []service.Approval{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetApprovalAPI handles GET /api/v1/approvals/{id}.
func (s *Server) GetApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "approval id is required", http.StatusBadRequest)
		return
	}

	record, err := s.approvalStore.GetApproval(r.Context(), id)
	if err != nil {
		slog.Error("get approval failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get approval: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("approval %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateApprovalAPI handles POST /api/v1/approvals.
func (s *Server) CreateApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Approval
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		httpResponse(w, "type is required", http.StatusBadRequest)
		return
	}

	req.Status = "pending"
	req.RequestedByType = "user"
	req.RequestedByID = s.getUserEmail(r)

	record, err := s.approvalStore.CreateApproval(r.Context(), req)
	if err != nil {
		slog.Error("create approval failed", "type", req.Type, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create approval: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateApprovalAPI handles PUT /api/v1/approvals/{id}.
func (s *Server) UpdateApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "approval id is required", http.StatusBadRequest)
		return
	}
	existing, err := s.approvalStore.GetApproval(r.Context(), id)
	if err != nil {
		slog.Error("get approval for update failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get approval: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, fmt.Sprintf("approval %q not found", id), http.StatusNotFound)
		return
	}

	var req service.Approval
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	updated := *existing
	if req.Status != "" {
		updated.Status = req.Status
	}
	if req.DecisionNote != "" {
		updated.DecisionNote = req.DecisionNote
	}

	if updated.Status == "approved" || updated.Status == "rejected" {
		updated.DecidedByUserID = s.getUserEmail(r)
		updated.DecidedAt = time.Now().UTC().Format(time.RFC3339)
	}

	record, err := s.approvalStore.UpdateApproval(r.Context(), id, updated)
	if err != nil {
		slog.Error("update approval failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update approval: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("approval %q not found", id), http.StatusNotFound)
		return
	}
	if err := s.applyApprovedApproval(r.Context(), record); err != nil {
		slog.Error("apply approval failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to apply approval: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// ListPendingApprovalsAPI handles GET /api/v1/approvals/pending.
func (s *Server) ListPendingApprovalsAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.URL.Query().Get("org_id")

	records, err := s.approvalStore.ListPendingApprovals(r.Context(), orgID)
	if err != nil {
		slog.Error("list pending approvals failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list pending approvals: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Approval{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) applyApprovedApproval(ctx context.Context, approval *service.Approval) error {
	if approval == nil || approval.Status != service.ApprovalStatusApproved {
		return nil
	}

	switch approval.Type {
	case service.ApprovalTypeHireAgent:
		if s.orgAgentStore == nil {
			return fmt.Errorf("organization agent store not configured")
		}
		orgID := approval.OrganizationID
		if orgID == "" {
			orgID = stringArg(approval.RequestDetails, "organization_id")
		}
		agentID := stringArg(approval.RequestDetails, "agent_id")
		if orgID == "" || agentID == "" {
			return fmt.Errorf("hire_agent approval missing organization_id or agent_id")
		}

		existing, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, orgID, agentID)
		if err != nil {
			return fmt.Errorf("check existing membership: %w", err)
		}
		if existing != nil {
			return nil
		}

		status := stringArg(approval.RequestDetails, "status")
		if status == "" {
			status = "active"
		}
		oa := service.OrganizationAgent{
			OrganizationID:    orgID,
			AgentID:           agentID,
			Role:              stringArg(approval.RequestDetails, "role"),
			Title:             stringArg(approval.RequestDetails, "title"),
			ParentAgentID:     stringArg(approval.RequestDetails, "parent_agent_id"),
			Status:            status,
			HeartbeatSchedule: stringArg(approval.RequestDetails, "heartbeat_schedule"),
		}
		if oa.ParentAgentID != "" {
			if err := s.validateHierarchy(ctx, orgID, agentID, oa.ParentAgentID); err != nil {
				return fmt.Errorf("hierarchy validation failed: %w", err)
			}
		}
		if _, err := s.orgAgentStore.CreateOrganizationAgent(ctx, oa); err != nil {
			return fmt.Errorf("add approved agent to organization: %w", err)
		}
	}

	return nil
}
