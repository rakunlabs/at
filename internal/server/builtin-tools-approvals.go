package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Approval Management Tool Executors ───

// execApprovalListPending lists pending approvals, optionally filtered by org.
func (s *Server) execApprovalListPending(ctx context.Context, args map[string]any) (string, error) {
	if s.approvalStore == nil {
		return "", fmt.Errorf("approval store not configured")
	}

	orgID, _ := args["organization_id"].(string)

	approvals, err := s.approvalStore.ListPendingApprovals(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("failed to list pending approvals: %w", err)
	}

	if approvals == nil {
		approvals = []service.Approval{}
	}

	out := map[string]any{
		"pending_approvals": approvals,
		"count":             len(approvals),
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// execApprovalDecide approves or rejects an approval request.
func (s *Server) execApprovalDecide(ctx context.Context, args map[string]any) (string, error) {
	if s.approvalStore == nil {
		return "", fmt.Errorf("approval store not configured")
	}

	id, _ := args["id"].(string)
	status, _ := args["status"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if status == "" {
		return "", fmt.Errorf("status is required")
	}

	validStatuses := map[string]bool{
		service.ApprovalStatusApproved:          true,
		service.ApprovalStatusRejected:          true,
		service.ApprovalStatusRevisionRequested: true,
		service.ApprovalStatusApprovalCancelled: true,
	}
	if !validStatuses[status] {
		return "", fmt.Errorf("invalid status %q (must be approved, rejected, revision_requested, or cancelled)", status)
	}

	// Fetch existing approval.
	existing, err := s.approvalStore.GetApproval(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get approval: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("approval %q not found", id)
	}

	// Update with decision.
	decisionNote, _ := args["decision_note"].(string)
	now := time.Now().UTC().Format(time.RFC3339)

	existing.Status = status
	existing.DecisionNote = decisionNote
	existing.DecidedAt = now

	record, err := s.approvalStore.UpdateApproval(ctx, id, *existing)
	if err != nil {
		return "", fmt.Errorf("failed to update approval: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}
