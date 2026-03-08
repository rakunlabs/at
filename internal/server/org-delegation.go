package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// runOrgDelegation is the core recursive delegation function. It takes a Task
// assigned to an agent in an organization and runs an LLM-driven agentic loop
// where the agent can delegate work to its direct reports. Each delegation
// creates a child Task and recursively invokes the same function.
func (s *Server) runOrgDelegation(ctx context.Context, org *service.Organization, task *service.Task, agentID string, depth int) error {
	// Guard: required stores must be set.
	if s.agentStore == nil || s.taskStore == nil || s.orgAgentStore == nil {
		return fmt.Errorf("org-delegation: required stores not configured")
	}

	// a) Enforce depth limit.
	maxDepth := org.MaxDelegationDepth
	if maxDepth == 0 {
		maxDepth = 10
	}
	if depth >= maxDepth {
		slog.Warn("org-delegation: max delegation depth reached",
			"org_id", org.ID, "task_id", task.ID, "agent_id", agentID, "depth", depth, "max_depth", maxDepth)
		_, err := s.taskStore.UpdateTask(ctx, task.ID, service.Task{
			Status: service.TaskStatusCompleted,
			Result: "max delegation depth reached",
		})
		if err != nil {
			return fmt.Errorf("org-delegation: update task at max depth: %w", err)
		}
		return nil
	}

	// b) Load the agent.
	agent, err := s.agentStore.GetAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("org-delegation: get agent %s: %w", agentID, err)
	}
	if agent == nil {
		slog.Warn("org-delegation: agent not found", "agent_id", agentID, "task_id", task.ID)
		_, updateErr := s.taskStore.UpdateTask(ctx, task.ID, service.Task{
			Status: service.TaskStatusCompleted,
			Result: fmt.Sprintf("agent %s not found", agentID),
		})
		if updateErr != nil {
			return fmt.Errorf("org-delegation: update task for missing agent: %w", updateErr)
		}
		return nil
	}

	// c) Resolve provider.
	info, ok := s.getProviderInfo(agent.Config.Provider)
	if !ok {
		slog.Warn("org-delegation: provider not found",
			"provider", agent.Config.Provider, "agent_id", agentID, "task_id", task.ID)
		_, updateErr := s.taskStore.UpdateTask(ctx, task.ID, service.Task{
			Status: service.TaskStatusCompleted,
			Result: fmt.Sprintf("provider %s not found", agent.Config.Provider),
		})
		if updateErr != nil {
			return fmt.Errorf("org-delegation: update task for missing provider: %w", updateErr)
		}
		return nil
	}

	model := agent.Config.Model
	if model == "" {
		model = info.defaultModel
	}

	// d) Get direct reports.
	reports, err := s.getDirectReports(ctx, org.ID, agentID)
	if err != nil {
		return fmt.Errorf("org-delegation: get direct reports: %w", err)
	}

	// e) Build delegate tools and dispatch map.
	// Maps tool name → agent ID of the direct report.
	delegateToolMap := make(map[string]string, len(reports))
	var delegateTools []service.Tool

	// We also need the Agent records for building the system prompt.
	type reportInfo struct {
		orgAgent service.OrganizationAgent
		agent    *service.Agent
	}
	var reportInfos []reportInfo

	for _, oa := range reports {
		reportAgent, err := s.agentStore.GetAgent(ctx, oa.AgentID)
		if err != nil {
			slog.Warn("org-delegation: failed to load report agent",
				"agent_id", oa.AgentID, "error", err)
			continue
		}
		if reportAgent == nil {
			slog.Warn("org-delegation: report agent not found, skipping",
				"agent_id", oa.AgentID)
			continue
		}

		reportInfos = append(reportInfos, reportInfo{orgAgent: oa, agent: reportAgent})

		// Sanitize name for tool use (alphanumeric + underscores).
		safeName := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return '_'
		}, reportAgent.Name)
		toolName := "delegate_to_" + strings.ToLower(safeName)

		toolDesc := fmt.Sprintf("Delegate a task to %s. %s", reportAgent.Name, reportAgent.Config.Description)
		tool := service.Tool{
			Name:        toolName,
			Description: toolDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "The task or instruction to delegate to the agent.",
					},
				},
				"required": []string{"task"},
			},
		}

		delegateTools = append(delegateTools, tool)
		delegateToolMap[toolName] = oa.AgentID
	}

	// f) Build enriched system prompt.
	systemPrompt := agent.Config.SystemPrompt
	if len(reportInfos) > 0 {
		var teamSection strings.Builder
		teamSection.WriteString("\n\n## Your Team (Direct Reports)\nYou can delegate tasks to these team members using the delegate_to_* tools:\n\n")
		for _, ri := range reportInfos {
			teamSection.WriteString(fmt.Sprintf("- %s (%s, %s): %s\n",
				ri.agent.Name, ri.orgAgent.Role, ri.orgAgent.Title, ri.agent.Config.Description))
		}
		systemPrompt += teamSection.String()
	}

	// g) Update task status to in_progress.
	_, err = s.taskStore.UpdateTask(ctx, task.ID, service.Task{
		Status: service.TaskStatusInProgress,
	})
	if err != nil {
		return fmt.Errorf("org-delegation: update task to in_progress: %w", err)
	}

	// h) Run agentic loop.
	maxIterations := agent.Config.MaxIterations
	if maxIterations == 0 {
		maxIterations = 10
	}

	// Build initial messages.
	var messages []service.Message
	if systemPrompt != "" {
		messages = append(messages, service.Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	userPrompt := task.Title
	if task.Description != "" {
		userPrompt += "\n\n" + task.Description
	}
	messages = append(messages, service.Message{
		Role:    "user",
		Content: userPrompt,
	})

	// Strip Handler/HandlerType from tools before sending to LLM.
	llmTools := make([]service.Tool, len(delegateTools))
	for i, t := range delegateTools {
		llmTools[i] = service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	var finalContent string

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			slog.Warn("org-delegation: context cancelled",
				"task_id", task.ID, "agent_id", agentID, "iteration", iteration)
			return fmt.Errorf("org-delegation: cancelled: %w", err)
		}

		// Check agent budget before each LLM call.
		if s.agentBudgetStore != nil {
			checkBudget := s.checkBudgetFunc()
			if checkBudget != nil {
				if budgetErr := checkBudget(ctx, agentID); budgetErr != nil {
					slog.Warn("org-delegation: budget exceeded",
						"agent_id", agentID, "task_id", task.ID, "error", budgetErr)
					_, updateErr := s.taskStore.UpdateTask(ctx, task.ID, service.Task{
						Status: service.TaskStatusCompleted,
						Result: fmt.Sprintf("budget exceeded: %v", budgetErr),
					})
					if updateErr != nil {
						return fmt.Errorf("org-delegation: update task for budget exceeded: %w", updateErr)
					}
					return nil
				}
			}
		}

		// Call LLM.
		resp, err := info.provider.Chat(ctx, model, messages, llmTools)
		if err != nil {
			slog.Error("org-delegation: chat failed",
				"agent_id", agentID, "task_id", task.ID, "iteration", iteration, "error", err)
			return fmt.Errorf("org-delegation: chat failed (iteration %d): %w", iteration, err)
		}

		// Record token usage.
		if resp.Usage.TotalTokens > 0 {
			recordUsage := s.recordUsageFunc()
			if recordUsage != nil {
				if usageErr := recordUsage(ctx, agentID, model, resp.Usage); usageErr != nil {
					slog.Warn("org-delegation: failed to record usage",
						"agent_id", agentID, "error", usageErr)
				}
			}
		}

		// Build assistant message with content blocks.
		var assistantContent []service.ContentBlock
		if resp.Content != "" {
			assistantContent = append(assistantContent, service.ContentBlock{
				Type: "text",
				Text: resp.Content,
			})
		}
		for _, tc := range resp.ToolCalls {
			input := tc.Arguments
			if input == nil {
				input = map[string]any{}
			}
			assistantContent = append(assistantContent, service.ContentBlock{
				Type:             "tool_use",
				ID:               tc.ID,
				Name:             tc.Name,
				Input:            input,
				ThoughtSignature: tc.ThoughtSignature,
			})
		}
		messages = append(messages, service.Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		// If done (no tool calls), finish.
		if resp.Finished || len(resp.ToolCalls) == 0 {
			finalContent = resp.Content
			break
		}

		// i) Execute tool calls sequentially (concurrent fan-out is Phase 3).
		var toolResults []service.ContentBlock
		for _, tc := range resp.ToolCalls {
			slog.Debug("org-delegation: tool call",
				"tool", tc.Name, "task_id", task.ID, "iteration", iteration)

			var result string
			var callErr error

			if reportAgentID, ok := delegateToolMap[tc.Name]; ok {
				// Extract the task text from the tool call arguments.
				taskText, _ := tc.Arguments["task"].(string)
				if taskText == "" {
					taskText = task.Title // Fallback to parent title.
				}

				// Create child task.
				childTask, err := s.createDelegationTask(ctx, org, task, reportAgentID, taskText, depth)
				if err != nil {
					callErr = fmt.Errorf("failed to create delegation task: %w", err)
				} else {
					// Recursively delegate.
					slog.Info("org-delegation: delegating to report",
						"parent_task", task.ID, "child_task", childTask.ID,
						"from_agent", agentID, "to_agent", reportAgentID, "depth", depth+1)

					if delegErr := s.runOrgDelegation(ctx, org, childTask, reportAgentID, depth+1); delegErr != nil {
						callErr = fmt.Errorf("delegation failed: %w", delegErr)
					} else {
						// Re-fetch the child task to get its result.
						updated, getErr := s.taskStore.GetTask(ctx, childTask.ID)
						if getErr != nil {
							result = fmt.Sprintf("Delegation completed but failed to fetch result: %v", getErr)
						} else if updated != nil && updated.Result != "" {
							result = updated.Result
						} else {
							result = "Delegation completed (no result returned)."
						}
					}
				}
			} else {
				// Unknown tool.
				callErr = fmt.Errorf("unknown tool %q", tc.Name)
			}

			if callErr != nil {
				slog.Error("org-delegation: tool call failed",
					"tool", tc.Name, "task_id", task.ID, "error", callErr)
				result = fmt.Sprintf("Error: %v", callErr)
			}

			// Record audit entry for each tool call.
			recordAudit := s.recordAuditFunc()
			if recordAudit != nil {
				auditDetails := map[string]any{
					"tool_name": tc.Name,
					"task_id":   task.ID,
					"iteration": iteration,
					"has_error": callErr != nil,
				}
				if auditErr := recordAudit(ctx, service.AuditEntry{
					ActorType:      "agent",
					ActorID:        agentID,
					Action:         "tool_call",
					ResourceType:   "tool",
					ResourceID:     tc.ID,
					OrganizationID: org.ID,
					Details:        auditDetails,
				}); auditErr != nil {
					slog.Warn("org-delegation: failed to record audit",
						"agent_id", agentID, "error", auditErr)
				}
			}

			toolResults = append(toolResults, service.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tc.ID,
				Content:   result,
			})
		}

		// Append tool results and continue loop.
		messages = append(messages, service.Message{
			Role:    "user",
			Content: toolResults,
		})
	}

	// j) On completion, update task status to completed with final LLM response.
	if finalContent == "" {
		// Extract last assistant text if loop exhausted max iterations.
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				if blocks, ok := messages[i].Content.([]service.ContentBlock); ok {
					for _, b := range blocks {
						if b.Type == "text" && b.Text != "" {
							finalContent = b.Text
							break
						}
					}
					if finalContent != "" {
						break
					}
				}
			}
		}
	}

	_, err = s.taskStore.UpdateTask(ctx, task.ID, service.Task{
		Status: service.TaskStatusCompleted,
		Result: finalContent,
	})
	if err != nil {
		return fmt.Errorf("org-delegation: update task to completed: %w", err)
	}

	slog.Info("org-delegation: task completed",
		"task_id", task.ID, "agent_id", agentID, "depth", depth)

	return nil
}

// getDirectReports returns organization agents that report directly to the given
// agent (filtered by ParentAgentID match and active status).
func (s *Server) getDirectReports(ctx context.Context, orgID, agentID string) ([]service.OrganizationAgent, error) {
	allMembers, err := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("org-delegation: list org agents: %w", err)
	}

	var reports []service.OrganizationAgent
	for _, oa := range allMembers {
		if oa.ParentAgentID == agentID && oa.Status == "active" {
			reports = append(reports, oa)
		}
	}

	return reports, nil
}

// createDelegationTask creates a child task linked to the parent task via ParentID.
// It generates a human-readable identifier from the organization's issue prefix and counter.
func (s *Server) createDelegationTask(ctx context.Context, org *service.Organization, parentTask *service.Task, assigneeAgentID, description string, depth int) (*service.Task, error) {
	// Increment issue counter.
	counter, err := s.organizationStore.IncrementIssueCounter(ctx, org.ID)
	if err != nil {
		return nil, fmt.Errorf("org-delegation: increment issue counter: %w", err)
	}

	// Build identifier.
	prefix := org.IssuePrefix
	if prefix == "" {
		// Fallback to first 4 chars of org ID.
		prefix = org.ID
		if len(prefix) > 4 {
			prefix = prefix[:4]
		}
	}
	identifier := fmt.Sprintf("%s-%d", prefix, counter)

	// Create the child task.
	childTask, err := s.taskStore.CreateTask(ctx, service.Task{
		OrganizationID:  org.ID,
		ParentID:        parentTask.ID,
		AssignedAgentID: assigneeAgentID,
		Title:           parentTask.Title,
		Description:     description,
		Status:          service.TaskStatusOpen,
		Identifier:      identifier,
		RequestDepth:    depth + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("org-delegation: create child task: %w", err)
	}

	slog.Info("org-delegation: created child task",
		"child_task_id", childTask.ID, "identifier", identifier,
		"parent_task_id", parentTask.ID, "assignee", assigneeAgentID)

	return childTask, nil
}
