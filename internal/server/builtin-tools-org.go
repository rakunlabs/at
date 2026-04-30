package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Organization Management Tool Executors ───

// execOrgCreate creates a new organization.
func (s *Server) execOrgCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil {
		return "", fmt.Errorf("organization store not configured")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	org := service.Organization{
		Name: name,
	}

	if v, ok := args["description"].(string); ok {
		org.Description = v
	}
	if v, ok := args["issue_prefix"].(string); ok {
		org.IssuePrefix = v
	}
	if v, ok := args["head_agent_id"].(string); ok {
		org.HeadAgentID = v
	}
	if v, ok := args["budget_monthly_cents"].(float64); ok {
		org.BudgetMonthlyCents = int64(v)
	}
	if v, ok := args["max_delegation_depth"].(float64); ok {
		org.MaxDelegationDepth = int(v)
	}
	if v, ok := args["require_board_approval"].(bool); ok {
		org.RequireBoardApproval = v
	}

	record, err := s.organizationStore.CreateOrganization(ctx, org)
	if err != nil {
		return "", fmt.Errorf("failed to create organization: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execOrgList lists all organizations.
func (s *Server) execOrgList(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil {
		return "", fmt.Errorf("organization store not configured")
	}

	result, err := s.organizationStore.ListOrganizations(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list organizations: %w", err)
	}

	type orgSummary struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		Description        string `json:"description,omitempty"`
		IssuePrefix        string `json:"issue_prefix,omitempty"`
		HeadAgentID        string `json:"head_agent_id,omitempty"`
		BudgetMonthlyCents int64  `json:"budget_monthly_cents,omitempty"`
		CreatedAt          string `json:"created_at"`
	}

	summaries := make([]orgSummary, len(result.Data))
	for i, o := range result.Data {
		summaries[i] = orgSummary{
			ID:                 o.ID,
			Name:               o.Name,
			Description:        o.Description,
			IssuePrefix:        o.IssuePrefix,
			HeadAgentID:        o.HeadAgentID,
			BudgetMonthlyCents: o.BudgetMonthlyCents,
			CreatedAt:          o.CreatedAt,
		}
	}

	out := map[string]any{
		"organizations": summaries,
		"total":         result.Meta.Total,
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// execOrgGet gets a single organization with its agent roster.
func (s *Server) execOrgGet(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil {
		return "", fmt.Errorf("organization store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	org, err := s.organizationStore.GetOrganization(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get organization: %w", err)
	}
	if org == nil {
		return "", fmt.Errorf("organization %q not found", id)
	}

	// Build response with agent roster.
	result := map[string]any{
		"organization": org,
	}

	// Include agent roster if org agent store is available.
	if s.orgAgentStore != nil {
		agents, err := s.orgAgentStore.ListOrganizationAgents(ctx, id)
		if err != nil {
			slog.Warn("failed to list org agents", "org_id", id, "error", err)
		} else {
			type agentInfo struct {
				OrgAgentID    string `json:"org_agent_id"`
				AgentID       string `json:"agent_id"`
				Role          string `json:"role,omitempty"`
				Title         string `json:"title,omitempty"`
				ParentAgentID string `json:"parent_agent_id,omitempty"`
				Status        string `json:"status,omitempty"`
			}
			roster := make([]agentInfo, len(agents))
			for i, oa := range agents {
				roster[i] = agentInfo{
					OrgAgentID:    oa.ID,
					AgentID:       oa.AgentID,
					Role:          oa.Role,
					Title:         oa.Title,
					ParentAgentID: oa.ParentAgentID,
					Status:        oa.Status,
				}
			}
			result["agents"] = roster
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

// execOrgAddAgent adds an agent to an organization's hierarchy.
func (s *Server) execOrgAddAgent(ctx context.Context, args map[string]any) (string, error) {
	if s.orgAgentStore == nil {
		return "", fmt.Errorf("organization agent store not configured")
	}

	orgID, _ := args["organization_id"].(string)
	agentID, _ := args["agent_id"].(string)
	if orgID == "" {
		return "", fmt.Errorf("organization_id is required")
	}
	if agentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}

	oa := service.OrganizationAgent{
		OrganizationID: orgID,
		AgentID:        agentID,
		Status:         "active",
	}

	if v, ok := args["role"].(string); ok {
		oa.Role = v
	}
	if v, ok := args["title"].(string); ok {
		oa.Title = v
	}
	if v, ok := args["parent_agent_id"].(string); ok && v != "" {
		oa.ParentAgentID = v
		// Validate hierarchy to prevent cycles and ensure parent exists in the org.
		if err := s.validateHierarchy(ctx, orgID, agentID, v); err != nil {
			return "", fmt.Errorf("hierarchy validation failed: %w", err)
		}
	}

	record, err := s.orgAgentStore.CreateOrganizationAgent(ctx, oa)
	if err != nil {
		return "", fmt.Errorf("failed to add agent to organization: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execOrgTaskIntake submits a task to an organization's head agent for async delegation.
func (s *Server) execOrgTaskIntake(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil || s.orgAgentStore == nil || s.taskStore == nil {
		return "", fmt.Errorf("store not configured")
	}

	orgID, _ := args["organization_id"].(string)
	title, _ := args["title"].(string)
	if orgID == "" {
		return "", fmt.Errorf("organization_id is required")
	}
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	// Validate org exists.
	org, err := s.organizationStore.GetOrganization(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("failed to get organization: %w", err)
	}
	if org == nil {
		return "", fmt.Errorf("organization %q not found", orgID)
	}

	// Validate head agent.
	if org.HeadAgentID == "" {
		return "", fmt.Errorf("organization has no head agent")
	}

	member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, orgID, org.HeadAgentID)
	if err != nil {
		return "", fmt.Errorf("failed to validate head agent: %w", err)
	}
	if member == nil {
		return "", fmt.Errorf("head agent is not a member of this organization")
	}
	if member.Status != "active" {
		return "", fmt.Errorf("head agent is not active")
	}

	// Generate identifier.
	counter, err := s.organizationStore.IncrementIssueCounter(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("failed to generate identifier: %w", err)
	}

	prefix := org.IssuePrefix
	if prefix == "" {
		prefix = orgID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	// Create task.
	description, _ := args["description"].(string)
	priorityLevel, _ := args["priority_level"].(string)

	// Per-task max_iterations override (0 = use agent default).
	maxIterations := 0
	if v, ok := args["max_iterations"].(float64); ok && v > 0 {
		maxIterations = int(v)
	} else if v, ok := args["max_iterations"].(int); ok && v > 0 {
		maxIterations = v
	}

	// Spill large briefs to the shared task workspace before persisting.
	// org_task_intake is the most common entry point for pipeline-stage
	// briefs (Director → head agent of a sub-org), so this is where the
	// largest payloads land.
	description, _ = s.maybeSpillBrief(ctx, description, "", title)

	task := service.Task{
		OrganizationID:  orgID,
		AssignedAgentID: org.HeadAgentID,
		Title:           title,
		Description:     description,
		PriorityLevel:   priorityLevel,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    0,
		MaxIterations:   maxIterations,
	}

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	// Fire async delegation.
	go func() {
		delegCtx := context.Background()
		if err := s.runOrgDelegation(delegCtx, org, record, org.HeadAgentID, 0); err != nil {
			slog.Error("org-delegation: failed",
				"org_id", org.ID,
				"task_id", record.ID,
				"error", err,
			)
			if s.taskStore != nil {
				_, _ = s.taskStore.UpdateTask(delegCtx, record.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: fmt.Sprintf("delegation failed: %v", err),
				})
			}
		}
	}()

	result := map[string]any{
		"id":         record.ID,
		"identifier": record.Identifier,
		"status":     record.Status,
		"message":    fmt.Sprintf("Task %s created and delegation started", identifier),
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}
