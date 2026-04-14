package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// intakeTaskRequest is the request body for POST /api/v1/organizations/{id}/tasks.
type intakeTaskRequest struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	GoalID        string `json:"goal_id,omitempty"`
	PriorityLevel string `json:"priority_level,omitempty"`
}

// intakeTaskResponse is the minimal 202 response for task intake.
type intakeTaskResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Status     string `json:"status"`
}

// IntakeTaskAPI handles POST /api/v1/organizations/{id}/tasks.
// Creates a task assigned to the org's head agent and returns 202 Accepted.
// After task creation, fires runOrgDelegation in a background goroutine.
func (s *Server) IntakeTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil || s.orgAgentStore == nil || s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.PathValue("id")
	if orgID == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Validate org exists.
	org, err := s.organizationStore.GetOrganization(ctx, orgID)
	if err != nil {
		slog.Error("get organization failed", "id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if org == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", orgID), http.StatusNotFound)
		return
	}

	// Validate head agent is set.
	if org.HeadAgentID == "" {
		httpResponse(w, "organization has no head agent", http.StatusUnprocessableEntity)
		return
	}

	// Validate head agent is an active member.
	member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, orgID, org.HeadAgentID)
	if err != nil {
		slog.Error("get head agent membership failed", "org_id", orgID, "agent_id", org.HeadAgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to validate head agent: %v", err), http.StatusInternalServerError)
		return
	}
	if member == nil {
		httpResponse(w, "head agent is not a member of this organization", http.StatusUnprocessableEntity)
		return
	}
	if member.Status != "active" {
		httpResponse(w, "head agent is not active", http.StatusUnprocessableEntity)
		return
	}

	// Decode request body.
	var req intakeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		httpResponse(w, "title is required", http.StatusBadRequest)
		return
	}

	// Generate org-scoped identifier.
	counter, err := s.organizationStore.IncrementIssueCounter(ctx, orgID)
	if err != nil {
		slog.Error("increment issue counter failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to generate identifier: %v", err), http.StatusInternalServerError)
		return
	}

	prefix := org.IssuePrefix
	if prefix == "" {
		// Fallback: use first 4 chars of org ID.
		prefix = orgID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	// Create task.
	task := service.Task{
		OrganizationID:  orgID,
		AssignedAgentID: org.HeadAgentID,
		Title:           req.Title,
		Description:     req.Description,
		GoalID:          req.GoalID,
		PriorityLevel:   req.PriorityLevel,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    0,
	}

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		slog.Error("create task failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create task: %v", err), http.StatusInternalServerError)
		return
	}

	// Fire async delegation in a tracked, cancellable background goroutine.
	go func() {
		delegCtx, cleanup := s.registerDelegation(context.Background(), record.ID, org.HeadAgentID, org.ID)
		defer cleanup()

		if err := s.runOrgDelegation(delegCtx, org, record, org.HeadAgentID, 0); err != nil {
			slog.Error("org-delegation: failed",
				"org_id", org.ID,
				"task_id", record.ID,
				"error", err,
			)
			// Update task status to reflect failure.
			if s.taskStore != nil {
				_, _ = s.taskStore.UpdateTask(delegCtx, record.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: fmt.Sprintf("delegation failed: %v", err),
				})
			}
		}
	}()

	httpResponseJSON(w, intakeTaskResponse{
		ID:         record.ID,
		Identifier: record.Identifier,
		Status:     record.Status,
	}, http.StatusAccepted)
}
