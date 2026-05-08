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

// ─── Organization Destructive + Membership Tool Executors (Phase 2) ───
//
// execOrgUpdate mirrors UpdateOrganizationAPI's PARTIAL update
// semantics: empty `name`, `head_agent_id`, `max_delegation_depth`,
// and `canvas_layout` fall back to the existing record. Setting a
// new head_agent_id is validated against the org-agent join table
// (the head must be an active member) — same path the HTTP handler
// uses, exposed here so the agent gets the same error if it picks
// a non-member.
func (s *Server) execOrgUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil {
		return "", fmt.Errorf("organization store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	existing, err := s.organizationStore.GetOrganization(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get organization %q: %w", id, err)
	}
	if existing == nil {
		return "", fmt.Errorf("organization %q not found", id)
	}

	// Build the update record from existing + provided overrides.
	updated := *existing
	if v := stringArg(args, "name"); v != "" {
		updated.Name = v
	}
	if v, ok := args["description"].(string); ok {
		updated.Description = v
	}
	if v, ok := args["issue_prefix"].(string); ok {
		updated.IssuePrefix = v
	}
	if v := stringArg(args, "head_agent_id"); v != "" {
		updated.HeadAgentID = v
	}
	if v := optionalInt64(args, "budget_monthly_cents"); v != nil {
		updated.BudgetMonthlyCents = *v
	}
	if v, ok := args["max_delegation_depth"]; ok {
		switch n := v.(type) {
		case float64:
			if int(n) > 0 {
				updated.MaxDelegationDepth = int(n)
			}
		case int:
			if n > 0 {
				updated.MaxDelegationDepth = n
			}
		}
	}
	if v, ok := args["require_board_approval_for_new_agents"].(bool); ok {
		updated.RequireBoardApproval = v
	}
	if raw, ok := args["container_config"]; ok && raw != nil {
		data, _ := json.Marshal(raw)
		var cc service.ContainerConfig
		if err := json.Unmarshal(data, &cc); err != nil {
			return "", fmt.Errorf("container_config: %w", err)
		}
		updated.ContainerConfig = &cc
	}

	// Validate new head_agent_id against membership/status.
	if updated.HeadAgentID != existing.HeadAgentID && s.orgAgentStore != nil {
		member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, id, updated.HeadAgentID)
		if err != nil {
			return "", fmt.Errorf("validate head agent: %w", err)
		}
		if member == nil {
			return "", fmt.Errorf("head agent %q is not a member of this organization", updated.HeadAgentID)
		}
		if member.Status != "active" {
			return "", fmt.Errorf("head agent %q is not active", updated.HeadAgentID)
		}
	}

	updated.UpdatedBy = "mcp"
	record, err := s.organizationStore.UpdateOrganization(ctx, id, updated)
	if err != nil {
		return "", fmt.Errorf("update organization %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("organization %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal org: %w", err)
	}
	return string(out), nil
}

func (s *Server) execOrgDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.organizationStore == nil {
		return "", fmt.Errorf("organization store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.organizationStore.DeleteOrganization(ctx, id); err != nil {
		return "", fmt.Errorf("delete organization %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

func (s *Server) execOrgListAgents(ctx context.Context, args map[string]any) (string, error) {
	if s.orgAgentStore == nil {
		return "", fmt.Errorf("organization agent store not configured")
	}
	orgID, _ := args["organization_id"].(string)
	if orgID == "" {
		return "", fmt.Errorf("organization_id is required")
	}
	records, err := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("list organization agents: %w", err)
	}
	if records == nil {
		records = []service.OrganizationAgent{}
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal org agents: %w", err)
	}
	return string(out), nil
}

// execOrgUpdateAgent reuses validateHierarchy to keep the same
// cycle-protection guarantees as the HTTP handler.
func (s *Server) execOrgUpdateAgent(ctx context.Context, args map[string]any) (string, error) {
	if s.orgAgentStore == nil {
		return "", fmt.Errorf("organization agent store not configured")
	}
	orgID, _ := args["organization_id"].(string)
	agentID, _ := args["agent_id"].(string)
	if orgID == "" || agentID == "" {
		return "", fmt.Errorf("organization_id and agent_id are required")
	}

	existing, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, orgID, agentID)
	if err != nil {
		return "", fmt.Errorf("get org-agent membership: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("agent %q is not a member of organization %q", agentID, orgID)
	}

	// Decide what fields to overwrite. We treat the absence of a key as
	// "preserve existing"; explicit empty string IS meaningful for some
	// fields (e.g. parent_agent_id="" = make root). We can't distinguish
	// those two states for raw map[string]any, so the policy is:
	//   - Required identifiers (orgID, agentID): from path/args.
	//   - Status: empty preserves (matches HTTP).
	//   - parent_agent_id: pass-through (empty → root; only validate
	//     hierarchy when changed).
	//   - Other fields: pass-through, with empty meaning "clear".
	updated := service.OrganizationAgent{
		OrganizationID:    orgID,
		AgentID:           agentID,
		Role:              stringArg(args, "role"),
		Title:             stringArg(args, "title"),
		ParentAgentID:     stringArg(args, "parent_agent_id"),
		Status:            stringArg(args, "status"),
		HeartbeatSchedule: stringArg(args, "heartbeat_schedule"),
		MemoryModel:       stringArg(args, "memory_model"),
		MemoryProvider:    stringArg(args, "memory_provider"),
		MemoryMethod:      stringArg(args, "memory_method"),
	}

	// Hierarchy validation: only when parent is being CHANGED to a
	// non-empty value. Setting parent="" (becoming root) skips the
	// member existence check but is still cycle-safe by definition.
	if updated.ParentAgentID != "" && updated.ParentAgentID != existing.ParentAgentID {
		if err := s.validateHierarchy(ctx, orgID, agentID, updated.ParentAgentID); err != nil {
			return "", fmt.Errorf("hierarchy validation failed: %w", err)
		}
	}

	if updated.Status == "" {
		updated.Status = existing.Status
	}

	record, err := s.orgAgentStore.UpdateOrganizationAgent(ctx, existing.ID, updated)
	if err != nil {
		slog.Error("update organization agent failed", "id", existing.ID, "error", err)
		return "", fmt.Errorf("update org-agent membership: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("membership not found")
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal membership: %w", err)
	}
	return string(out), nil
}

func (s *Server) execOrgRemoveAgent(ctx context.Context, args map[string]any) (string, error) {
	if s.orgAgentStore == nil {
		return "", fmt.Errorf("organization agent store not configured")
	}
	orgID, _ := args["organization_id"].(string)
	agentID, _ := args["agent_id"].(string)
	if orgID == "" || agentID == "" {
		return "", fmt.Errorf("organization_id and agent_id are required")
	}
	if err := s.orgAgentStore.DeleteOrganizationAgentByPair(ctx, orgID, agentID); err != nil {
		return "", fmt.Errorf("remove agent from org: %w", err)
	}
	return fmt.Sprintf(`{"status":"removed","organization_id":%q,"agent_id":%q}`, orgID, agentID), nil
}
