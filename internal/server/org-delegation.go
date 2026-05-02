package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const conversationStatePrefix = "[CONVERSATION_STATE]"

// defaultTaskWorkspaceBase is the fallback base directory under which
// per-task workspaces are created when no other configuration applies.
// In production the actual base comes from
// `loopgov.Config.WorkspaceRoot` via `(*Server).taskWorkspaceBase()`,
// which itself reads the bootstrap-time `server.workspace.root` block
// in at.yaml. This constant exists only so legacy tests and the
// startup self-check have a deterministic value to point at.
const defaultTaskWorkspaceBase = "/tmp/at-tasks"

// taskWorkspaceBase returns the configured base directory for per-task
// workspaces. Resolution order:
//
//  1. `loopgov.Config.WorkspaceRoot` (set from at.yaml's
//     `server.workspace.root` field by `loopgovConfigFromYAML`)
//  2. `defaultTaskWorkspaceBase` constant (`/tmp/at-tasks`)
//
// Production deployments on small VMs (e.g. GCE with a 10 GB boot
// disk) should set `server.workspace.root: /mnt/disk/at-tasks` in
// at.yaml so the video pipeline writes to a mounted data disk
// instead of the root filesystem.
func (s *Server) taskWorkspaceBase() string {
	if s.loopGov != nil {
		if root := s.loopGov.Config().WorkspaceRoot; root != "" {
			return root
		}
	}
	return defaultTaskWorkspaceBase
}

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
		if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, "max delegation depth reached"); err != nil {
			return fmt.Errorf("org-delegation: update task at max depth: %w", err)
		}
		return nil
	}

	// a2) Ensure a shared workspace directory exists for the entire delegation chain.
	// If the context already carries a workspace (set by a parent delegation), reuse it.
	// Otherwise, resolve the root task ID and create a workspace directory.
	taskWorkDir := workflow.WorkDirFromContext(ctx)
	if taskWorkDir == "" {
		rootID := s.resolveRootTaskID(ctx, task)
		taskWorkDir = filepath.Join(s.taskWorkspaceBase(), rootID)
		if err := os.MkdirAll(taskWorkDir, 0o755); err != nil {
			slog.Warn("org-delegation: failed to create task workspace",
				"task_id", task.ID, "root_id", rootID, "path", taskWorkDir, "error", err)
			// Non-fatal: agents can still run, they just won't have a shared directory.
		} else {
			slog.Info("org-delegation: task workspace ready",
				"task_id", task.ID, "root_id", rootID, "path", taskWorkDir)
		}
		ctx = workflow.ContextWithWorkDir(ctx, taskWorkDir)
	}

	// a3) Set container scope if the org has container isolation enabled.
	if _, hasScope := workflow.ContainerScopeFromContext(ctx); !hasScope {
		if org.ContainerConfig != nil && org.ContainerConfig.Enabled {
			ctx = workflow.ContextWithContainerScope(ctx, workflow.ContainerScope{
				OrgID: org.ID,
			})
		}
	}

	// a4) Inject the executing agent and current task into context so that
	// builtin tool executors (notably task_create) can auto-inherit
	// parent_id and organization_id from the active task. Without this,
	// agents that forget to pass parent_id end up creating orphaned tasks.
	ctx = contextWithAgentID(ctx, agentID)
	ctx = contextWithTaskID(ctx, task.ID)

	// b) Load the agent.
	agent, err := s.agentStore.GetAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("org-delegation: get agent %s: %w", agentID, err)
	}
	if agent == nil {
		slog.Warn("org-delegation: agent not found", "agent_id", agentID, "task_id", task.ID)
		if updateErr := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, fmt.Sprintf("agent %s not found", agentID)); updateErr != nil {
			return fmt.Errorf("org-delegation: update task for missing agent: %w", updateErr)
		}
		return nil
	}

	// c) Resolve provider.
	info, ok := s.getProviderInfo(agent.Config.Provider)
	if !ok {
		slog.Warn("org-delegation: provider not found",
			"provider", agent.Config.Provider, "agent_id", agentID, "task_id", task.ID)
		if updateErr := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, fmt.Sprintf("provider %s not found", agent.Config.Provider)); updateErr != nil {
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

	// e2) Load skill tools for this agent.
	type skillToolHandler struct {
		handler     string
		handlerType string
		skillID     string
	}
	skillToolMap := make(map[string]skillToolHandler)
	var skillTools []service.Tool
	var skillPromptFragments []string

	// skillConnOverrides maps skill ID to per-skill connection bindings
	// declared on the agent's SkillRef entries.
	skillConnOverrides := map[string]map[string]string{}
	for _, sr := range agent.Config.Skills {
		if sr.ID != "" && len(sr.Connections) > 0 {
			skillConnOverrides[sr.ID] = sr.Connections
		}
	}

	if s.skillStore != nil {
		for _, skillRef := range agent.Config.Skills {
			nameOrID := skillRef.ID
			skill, err := s.skillStore.GetSkill(ctx, nameOrID)
			if err != nil {
				slog.Warn("org-delegation: skill lookup failed", "skill", nameOrID, "error", err)
				continue
			}
			if skill == nil {
				skill, err = s.skillStore.GetSkillByName(ctx, nameOrID)
				if err != nil || skill == nil {
					slog.Warn("org-delegation: skill not found", "skill", nameOrID)
					continue
				}
			}

			if skill.SystemPrompt != "" {
				skillPromptFragments = append(skillPromptFragments, skill.SystemPrompt)
			}
			for _, t := range skill.Tools {
				if t.Handler != "" {
					skillToolMap[t.Name] = skillToolHandler{
						handler:     t.Handler,
						handlerType: t.HandlerType,
						skillID:     skill.ID,
					}
				}
				skillTools = append(skillTools, t)
			}
		}
	}

	// e3) Load builtin tools for this agent.
	type builtinToolHandler struct {
		name string
	}
	builtinToolMap := make(map[string]builtinToolHandler)
	var builtinToolDefs []service.Tool

	for _, toolName := range agent.Config.BuiltinTools {
		if !isKnownBuiltinTool(toolName) {
			slog.Warn("org-delegation: unknown builtin tool in agent config", "tool", toolName, "agent", agentID)
			continue
		}
		for _, bt := range builtinTools {
			if bt.Name == toolName {
				builtinToolDefs = append(builtinToolDefs, service.Tool{
					Name:        bt.Name,
					Description: bt.Description,
					InputSchema: bt.InputSchema,
				})
				builtinToolMap[bt.Name] = builtinToolHandler{name: bt.Name}
				break
			}
		}
	}

	// Build variable lookup/lister for skill tool execution.
	var varLookup workflow.VarLookup
	if s.variableStore != nil {
		varLookup = func(key string) (string, error) {
			v, err := s.variableStore.GetVariableByKey(ctx, key)
			if err != nil {
				return "", err
			}
			if v == nil {
				return "", fmt.Errorf("variable %q not found", key)
			}
			return v.Value, nil
		}
	}
	var varLister workflow.VarLister
	if s.variableStore != nil {
		varLister = func() (map[string]string, error) {
			vars, err := s.variableStore.ListVariables(ctx, nil)
			if err != nil {
				return nil, err
			}
			m := make(map[string]string, len(vars.Data))
			for _, v := range vars.Data {
				m[v.Key] = v.Value
			}
			return m, nil
		}
	}

	toolTimeout := time.Duration(agent.Config.ToolTimeout) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 60 * time.Second
	}

	// f) Build enriched system prompt.
	systemPrompt := agent.Config.SystemPrompt

	// Append skill system prompt fragments.
	if len(skillPromptFragments) > 0 {
		systemPrompt += "\n\n" + strings.Join(skillPromptFragments, "\n\n")
	}

	if len(reportInfos) > 0 {
		var teamSection strings.Builder
		teamSection.WriteString("\n\n## Your Team (Direct Reports)\nYou can delegate tasks to these team members using the delegate_to_* tools:\n\n")
		for _, ri := range reportInfos {
			teamSection.WriteString(fmt.Sprintf("- %s (%s, %s): %s\n",
				ri.agent.Name, ri.orgAgent.Role, ri.orgAgent.Title, ri.agent.Config.Description))
		}
		teamSection.WriteString("\n## CRITICAL Delegation Rules\n")
		teamSection.WriteString("1. To delegate work you MUST call the delegate_to_* tool. Writing \"I'll delegate to X\" or \"Now delegating to X\" in text does NOT delegate — only tool calls do.\n")
		teamSection.WriteString("2. Do NOT finish (stop making tool calls) until ALL planned delegations are complete and you have reviewed ALL results.\n")
		teamSection.WriteString("3. After each delegation result comes back, review it and decide whether to delegate further, request revisions, or finalize.\n")
		teamSection.WriteString("4. You are the decision-maker: only YOU decide when the overall task is complete. Summarize the final outcome in your last message.\n")
		systemPrompt += teamSection.String()
	}

	// f2) Recall relevant past memories and inject into system prompt.
	if memorySection := s.recallAgentMemories(ctx, org, task, agent, agentID); memorySection != "" {
		systemPrompt += memorySection
	}

	// f3) Inject shared workspace directory into system prompt.
	if taskWorkDir != "" {
		systemPrompt += fmt.Sprintf("\n\n## Workspace\nAll agents in this task chain share the workspace directory: `%s`\n"+
			"Use this directory for all file operations (reading, writing, temporary files). "+
			"Files created by other agents in this task will be available here. "+
			"Do NOT create your own temp directories — always use this shared workspace.\n", taskWorkDir)
	}

	// g) Update task status to in_progress.
	if err := s.taskStore.UpdateTaskStatus(ctx, task.ID, service.TaskStatusInProgress, ""); err != nil {
		return fmt.Errorf("org-delegation: update task to in_progress: %w", err)
	}

	// Audit: task started processing.
	if recordAudit := s.recordAuditFunc(); recordAudit != nil {
		_ = recordAudit(ctx, service.AuditEntry{
			ActorType:      "agent",
			ActorID:        agentID,
			Action:         "task_started",
			ResourceType:   "task",
			ResourceID:     task.ID,
			OrganizationID: org.ID,
			Details: map[string]any{
				"task_title": task.Title,
				"agent_name": agent.Name,
				"depth":      depth,
				"model":      model,
				"provider":   agent.Config.Provider,
			},
		})
	}

	// h) Run agentic loop.
	//
	// Iteration counter starts fresh at 0 for every runOrgDelegation call —
	// each task gets its own budget; nothing carries over from previous tasks.
	//
	// max_iterations resolution order:
	//   1. task.MaxIterations (per-task override, e.g. complex task gets 50)
	//   2. agent.Config.MaxIterations (per-agent default)
	//   3. fallback to 10
	//
	// 0 in the task means "use the agent default". 0 in the agent means
	// "use the hard-coded fallback".
	// Resolve and clamp iteration count via the loop governor. The
	// governor enforces the platform ceiling (e.g. 30) regardless of
	// agent or task config, and logs `loopgov.iter_clamped` whenever
	// a configured value is reduced.
	maxIterations := s.loopGov.ClampIterations(agent.Config.MaxIterations, task.MaxIterations)

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

	// Inject previous result if the task is being re-processed (revision workflow).
	if task.Result != "" {
		userPrompt += "\n\n## Previous Result\nThis task was processed before. Here is the previous output:\n\n" + task.Result
	}

	// Inject review feedback (comments) if any exist on this task.
	// Also look for saved conversation state from a previous exhausted run.
	var savedConversation []service.Message
	if s.issueCommentStore != nil {
		comments, err := s.issueCommentStore.ListCommentsByTask(ctx, task.ID)
		if err != nil {
			slog.Warn("org-delegation: failed to load task comments",
				"task_id", task.ID, "error", err)
		} else if len(comments) > 0 {
			var feedback strings.Builder
			hasFeedback := false
			for _, c := range comments {
				// Check for conversation state (saved from exhausted iterations).
				if strings.HasPrefix(c.Body, conversationStatePrefix) {
					stateJSON := strings.TrimPrefix(c.Body, conversationStatePrefix)
					var restored []service.Message
					if err := json.Unmarshal([]byte(stateJSON), &restored); err == nil && len(restored) > 0 {
						savedConversation = restored
						slog.Info("org-delegation: restored conversation state from previous run",
							"task_id", task.ID, "messages_count", len(restored))
					}
					// Delete the conversation state comment after loading it.
					_ = s.issueCommentStore.DeleteComment(ctx, c.ID)
					continue
				}
				// Regular feedback comment.
				if !hasFeedback {
					feedback.WriteString("\n\n## Review Feedback\nThe following comments were left by reviewers. Address their feedback:\n\n")
					hasFeedback = true
				}
				author := c.AuthorID
				if c.AuthorType != "" {
					author = fmt.Sprintf("%s (%s)", c.AuthorID, c.AuthorType)
				}
				feedback.WriteString(fmt.Sprintf("**%s** [%s]:\n%s\n\n", author, c.CreatedAt, c.Body))
			}
			if hasFeedback {
				userPrompt += feedback.String()
			}
		}
	}

	// If we have saved conversation state, resume from it instead of starting fresh.
	if len(savedConversation) > 0 {
		// Sanitize restored messages — a previous interrupted run may have left
		// assistant tool_use blocks without matching tool_result responses.
		messages = sanitizeLLMMessages(savedConversation)
		// Add a continuation prompt so the agent knows to pick up where it left off.
		continueMsg := "Continue processing this task from where you left off. Your previous run was interrupted because it reached the iteration limit. Review your progress so far and complete the remaining work."
		if task.Result != "" {
			continueMsg += "\n\nYour partial result was:\n" + task.Result
		}
		messages = append(messages, service.Message{
			Role:    "user",
			Content: continueMsg,
		})
		slog.Info("org-delegation: continuing from saved conversation",
			"task_id", task.ID, "agent_id", agentID, "restored_messages", len(savedConversation))
	} else {
		messages = append(messages, service.Message{
			Role:    "user",
			Content: userPrompt,
		})
	}

	// Strip Handler/HandlerType from tools before sending to LLM.
	// Include delegate tools, skill tools, and builtin tools.
	llmTools := make([]service.Tool, 0, len(delegateTools)+len(skillTools)+len(builtinToolDefs))
	for _, t := range delegateTools {
		llmTools = append(llmTools, service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	for _, t := range skillTools {
		llmTools = append(llmTools, service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	llmTools = append(llmTools, builtinToolDefs...)

	var finalContent string

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			slog.Warn("org-delegation: context cancelled",
				"task_id", task.ID, "agent_id", agentID, "iteration", iteration)
			_ = s.completeTaskWithStatus(ctx, task, service.TaskStatusCancelled, fmt.Sprintf("context cancelled: %v", err))
			return fmt.Errorf("org-delegation: cancelled: %w", err)
		}

		// Check agent budget before each LLM call.
		if s.agentBudgetStore != nil {
			checkBudget := s.checkBudgetFunc()
			if checkBudget != nil {
				if budgetErr := checkBudget(ctx, agentID); budgetErr != nil {
					slog.Warn("org-delegation: budget exceeded",
						"agent_id", agentID, "task_id", task.ID, "error", budgetErr)
					if updateErr := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, fmt.Sprintf("budget exceeded: %v", budgetErr)); updateErr != nil {
						return fmt.Errorf("org-delegation: update task for budget exceeded: %w", updateErr)
					}
					return nil
				}
			}
		}

		// Call LLM with retry on transient errors (5xx, rate limits).
		// The loop governor windows the message slice (with rolling
		// summary fallback) and supplies a per-call MaxTokens cap.
		var resp *service.LLMResponse
		var chatErr error
		var latencyMs int64
		chatOpts := s.loopGov.ChatOptions()
		for attempt := 0; attempt < 3; attempt++ {
			windowed, _ := s.loopGov.Limit(ctx, agentID, task.ID, messages)
			callStart := time.Now()
			resp, chatErr = info.provider.Chat(ctx, model, windowed, llmTools, chatOpts)
			latencyMs = time.Since(callStart).Milliseconds()
			if chatErr == nil {
				break
			}
			errStr := chatErr.Error()
			// Recover from corrupted tool call history — sanitize messages and retry once.
			if attempt == 0 && isToolPairingError(chatErr) {
				slog.Warn("org-delegation: tool call history error, sanitizing and retrying",
					"agent_id", agentID, "task_id", task.ID, "error", chatErr)
				messages = sanitizeLLMMessages(messages)
				continue
			}
			// Honour upstream Retry-After when the provider returned a
			// typed *RateLimitError (anthropic/openai/gemini/vertex all
			// produce this on 429). The sleep is capped per
			// LLMConfig.RateLimit.RetryAfterCap (default 60s, configurable
			// in the UI; -1 = no cap).
			var rle *service.RateLimitError
			if errors.As(chatErr, &rle) {
				sleep := rle.RetryAfter
				if sleep <= 0 {
					sleep = time.Duration(attempt+1) * 3 * time.Second
				}
				if cap := info.RetryAfterCap(); cap > 0 && sleep > cap {
					slog.Warn("org-delegation: capping upstream Retry-After",
						"agent_id", agentID, "task_id", task.ID, "attempt", attempt+1,
						"requested", rle.RetryAfter, "capped_to", cap, "provider", rle.Provider)
					sleep = cap
				}
				slog.Warn("org-delegation: rate-limit, retrying",
					"agent_id", agentID, "task_id", task.ID, "attempt", attempt+1,
					"sleep", sleep, "provider", rle.Provider, "retry_after", rle.RetryAfter)
				time.Sleep(sleep)
				continue
			}
			// Retry on 5xx server errors and rate limits (string-match
			// fallback for providers that don't yet return *RateLimitError,
			// e.g. minimax wrapping antropic, or arbitrary status-500s).
			if strings.Contains(errStr, "status 500") || strings.Contains(errStr, "status 502") ||
				strings.Contains(errStr, "status 503") || strings.Contains(errStr, "status 520") ||
				strings.Contains(errStr, "status 429") || strings.Contains(errStr, "unknown error") {
				slog.Warn("org-delegation: transient LLM error, retrying",
					"agent_id", agentID, "task_id", task.ID, "attempt", attempt+1, "error", chatErr)
				time.Sleep(time.Duration(attempt+1) * 3 * time.Second) // 3s, 6s, 9s backoff
				continue
			}
			break // non-retryable error
		}
		if chatErr != nil {
			// Record failed call for usage dashboard.
			//
			// Use the agent's configured provider KEY (e.g. "openai-prod",
			// "claude-personal") rather than info.providerType (which is the
			// generic API type like "openai" / "anthropic"). The dashboard
			// groups by the user-facing provider, not by upstream API family.
			if recordUsage := s.recordUsageFunc(); recordUsage != nil {
				_ = recordUsage(ctx, workflow.UsageEvent{
					AgentID:      agentID,
					Model:        model,
					Provider:     agent.Config.Provider,
					TaskID:       task.ID,
					LatencyMs:    latencyMs,
					Status:       "error",
					ErrorCode:    classifyHTTPError(chatErr),
					ErrorMessage: chatErr.Error(),
				})
			}
			slog.Error("org-delegation: chat failed",
				"agent_id", agentID, "task_id", task.ID, "iteration", iteration, "error", chatErr)
			_ = s.completeTaskWithStatus(ctx, task, service.TaskStatusCancelled, fmt.Sprintf("chat failed: %v", chatErr))
			return fmt.Errorf("org-delegation: chat failed (iteration %d): %w", iteration, chatErr)
		}

		// Record token usage.
		//
		// As above, attribute the cost to the agent's configured provider KEY
		// (the user-facing identifier like "openai-prod") rather than the
		// generic API type. Without this the Usage dashboard's "by provider"
		// breakdown collapses every OpenAI-compatible config to "openai" and
		// every Anthropic-shaped one to "anthropic", hiding which named
		// provider account actually drove the spend.
		if resp.Usage.TotalTokens > 0 {
			recordUsage := s.recordUsageFunc()
			if recordUsage != nil {
				if usageErr := recordUsage(ctx, workflow.UsageEvent{
					AgentID:   agentID,
					Model:     model,
					Provider:  agent.Config.Provider,
					TaskID:    task.ID,
					Usage:     resp.Usage,
					LatencyMs: latencyMs,
					Status:    "ok",
				}); usageErr != nil {
					slog.Warn("org-delegation: failed to record usage",
						"agent_id", agentID, "error", usageErr)
				}
			}
		}

		// Audit: LLM call completed. We attach a truncated assistant
		// content preview and a compact summary of any tool calls
		// (name + arguments) so the Audit page can show what the model
		// actually did on this iteration without us re-shipping the
		// entire prompt history.
		if recordAudit := s.recordAuditFunc(); recordAudit != nil {
			llmDetails := map[string]any{
				"task_id":      task.ID,
				"iteration":    iteration,
				"model":        model,
				"provider":     agent.Config.Provider,
				"finished":     resp.Finished,
				"tool_calls":   len(resp.ToolCalls),
				"has_content":  resp.Content != "",
				"total_tokens": resp.Usage.TotalTokens,
			}
			if resp.Content != "" {
				llmDetails["content_preview"] = service.TruncateForAudit(resp.Content)
			}
			if len(resp.ToolCalls) > 0 {
				summaries := make([]map[string]any, 0, len(resp.ToolCalls))
				for _, tc := range resp.ToolCalls {
					summaries = append(summaries, map[string]any{
						"id":        tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
				llmDetails["tool_calls_detail"] = summaries
			}
			_ = recordAudit(ctx, service.AuditEntry{
				ActorType:      "agent",
				ActorID:        agentID,
				Action:         "llm_call",
				ResourceType:   "task",
				ResourceID:     task.ID,
				OrganizationID: org.ID,
				Details:        llmDetails,
			})
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

		// If done (no tool calls), check for unfulfilled delegation intent before finishing.
		if resp.Finished || len(resp.ToolCalls) == 0 {
			// Detect if the agent mentioned delegating but didn't actually call a delegate tool.
			// This catches cases like "Now delegating to Video Producer..." without a tool call.
			if len(delegateToolMap) > 0 && resp.Content != "" && detectUnfulfilledDelegation(resp.Content, delegateToolMap) {
				slog.Warn("org-delegation: agent mentioned delegation in text but made no tool call — nudging to use tool",
					"task_id", task.ID, "agent_id", agentID, "iteration", iteration)
				messages = append(messages, service.Message{
					Role:    "user",
					Content: "You mentioned delegating to a team member but did not call any delegate_to_* tool. Describing delegation in text does NOT execute it. You MUST call the appropriate delegate_to_* tool now to actually delegate the work. Do not repeat your analysis — just make the tool call.",
				})
				continue
			}

			finalContent = resp.Content
			break
		}

		// i) Execute tool calls concurrently (fan-out: one goroutine per delegation).
		toolResults := make([]service.ContentBlock, len(resp.ToolCalls))
		var wg sync.WaitGroup
		var resultMu sync.Mutex

		for i, tc := range resp.ToolCalls {
			slog.Debug("org-delegation: tool call",
				"tool", tc.Name, "task_id", task.ID, "iteration", iteration)

			if reportAgentID, ok := delegateToolMap[tc.Name]; ok {
				wg.Add(1)
				go func(idx int, toolCall service.ToolCall, targetAgentID string) {
					defer wg.Done()

					taskText, _ := toolCall.Arguments["task"].(string)
					if taskText == "" {
						taskText = task.Title
					}

					childTask, err := s.createDelegationTask(ctx, org, task, targetAgentID, taskText, depth)
					if err != nil {
						resultMu.Lock()
						toolResults[idx] = service.ContentBlock{
							Type:      "tool_result",
							ToolUseID: toolCall.ID,
							Content:   fmt.Sprintf("Error: failed to create delegation task: %v", err),
						}
						resultMu.Unlock()
						return
					}

					slog.Info("org-delegation: delegating to report",
						"parent_task", task.ID, "child_task", childTask.ID,
						"from_agent", agentID, "to_agent", targetAgentID, "depth", depth+1)

					var result string
					if delegErr := s.runOrgDelegation(ctx, org, childTask, targetAgentID, depth+1); delegErr != nil {
						result = fmt.Sprintf("Error: delegation failed: %v", delegErr)
					} else {
						updated, getErr := s.taskStore.GetTask(ctx, childTask.ID)
						if getErr != nil {
							result = fmt.Sprintf("Delegation completed but failed to fetch result: %v", getErr)
						} else if updated != nil && updated.Result != "" {
							result = updated.Result
						} else {
							result = "Delegation completed (no result returned)."
						}
					}

					// Cap the child-task result before it enters the
					// parent's LLM message history. Long child results
					// otherwise compound through the delegation chain.
					result, _ = s.loopGov.TruncateToolResult(task.ID, toolCall.Name, result)

					resultMu.Lock()
					toolResults[idx] = service.ContentBlock{
						Type:      "tool_result",
						ToolUseID: toolCall.ID,
						Content:   result,
					}
					resultMu.Unlock()

					// Record audit entry for delegation tool call. We attach
					// the tool input (delegation arguments) and the truncated
					// child-task result so the Audit page can show what was
					// actually delegated and what came back, not just a count.
					recordAudit := s.recordAuditFunc()
					if recordAudit != nil {
						auditDetails := map[string]any{
							"tool_name": toolCall.Name,
							"task_id":   task.ID,
							"iteration": iteration,
							"has_error": false,
							"input":     toolCall.Arguments,
							"output":    service.TruncateForAudit(result),
						}
						if auditErr := recordAudit(ctx, service.AuditEntry{
							ActorType:      "agent",
							ActorID:        agentID,
							Action:         "tool_call",
							ResourceType:   "tool",
							ResourceID:     toolCall.ID,
							OrganizationID: org.ID,
							Details:        auditDetails,
						}); auditErr != nil {
							slog.Warn("org-delegation: failed to record audit",
								"agent_id", agentID, "error", auditErr)
						}
					}
				}(i, tc, reportAgentID)
			} else if hi, ok := skillToolMap[tc.Name]; ok {
				// Skill tool — execute the handler synchronously.
				var result string
				var callErr error

				// Wrap VarLookup/VarLister with connection bindings so that
				// provider-scoped keys resolve through the agent's bound
				// Connection, falling back to global variables.
				toolVarLookup := varLookup
				toolVarLister := varLister
				if s.connectionStore != nil {
					var perSkill map[string]string
					if hi.skillID != "" {
						perSkill = skillConnOverrides[hi.skillID]
					}
					bindings := workflow.ResolveAgentConnectionBindings(
						ctx, s.connectionLookupFunc(),
						agent.Config.Connections, perSkill,
					)
					if len(bindings) > 0 {
						toolVarLookup = workflow.WrapVarLookupWithConnections(varLookup, bindings)
						toolVarLister = workflow.WrapVarListerWithConnections(varLister, bindings)
					}
				}

				if hi.handlerType == "bash" {
					result, callErr = workflow.ExecuteBashHandler(ctx, hi.handler, tc.Arguments, toolVarLister, toolTimeout)
				} else {
					result, callErr = workflow.ExecuteJSHandler(hi.handler, tc.Arguments, toolVarLookup)
				}

				if callErr != nil {
					slog.Error("org-delegation: skill tool call failed",
						"tool", tc.Name, "task_id", task.ID, "error", callErr)
					result = fmt.Sprintf("Error: %v", callErr)
				} else {
					logResult := result
					if len(logResult) > 500 {
						logResult = logResult[:500] + "..."
					}
					slog.Debug("org-delegation: skill tool call result",
						"tool", tc.Name, "task_id", task.ID, "result_length", len(result), "result", logResult)
				}

				// Apply governor truncation before the result is
				// appended to the message history. Skill JS handlers
				// are unbounded by default; this cap is the only
				// thing standing between a noisy handler and a O(K²)
				// context blow-up.
				result, _ = s.loopGov.TruncateToolResult(task.ID, tc.Name, result)

				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				}

				// Record audit for skill tool call. Capture the JS handler
				// arguments and the (post-truncation) result so the Audit
				// page can show what was passed and what came back.
				if recordAudit := s.recordAuditFunc(); recordAudit != nil {
					auditDetails := map[string]any{
						"tool_name": tc.Name,
						"task_id":   task.ID,
						"iteration": iteration,
						"has_error": callErr != nil,
						"input":     tc.Arguments,
						"output":    service.TruncateForAudit(result),
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
			} else if _, ok := builtinToolMap[tc.Name]; ok {
				// Builtin tool — execute via dispatchBuiltinTool.
				var result string
				var callErr error

				result, callErr = s.dispatchBuiltinTool(ctx, tc.Name, tc.Arguments)

				if callErr != nil {
					slog.Error("org-delegation: builtin tool call failed",
						"tool", tc.Name, "task_id", task.ID, "error", callErr)
					result = fmt.Sprintf("Error: %v", callErr)
				} else {
					logResult := result
					if len(logResult) > 500 {
						logResult = logResult[:500] + "..."
					}
					slog.Debug("org-delegation: builtin tool call result",
						"tool", tc.Name, "task_id", task.ID, "result_length", len(result), "result", logResult)
				}

				// Apply governor truncation. bash_execute is the most
				// common offender — full stdout would otherwise be
				// re-shipped on every subsequent iteration.
				result, _ = s.loopGov.TruncateToolResult(task.ID, tc.Name, result)

				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				}

				// Record audit for builtin tool call. Builtins like
				// task_create / bash_execute / mem_save accept structured
				// input we want to inspect later, and their (truncated)
				// output is what got fed back into the LLM history.
				if recordAudit := s.recordAuditFunc(); recordAudit != nil {
					auditDetails := map[string]any{
						"tool_name": tc.Name,
						"task_id":   task.ID,
						"iteration": iteration,
						"has_error": callErr != nil,
						"input":     tc.Arguments,
						"output":    service.TruncateForAudit(result),
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
			} else {
				// Unknown tool — handle synchronously (no goroutine needed).
				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("Error: unknown tool %q", tc.Name),
				}

				// Record audit for unknown tool call. The "output" here is
				// the synthetic error message we fed back to the LLM —
				// useful for spotting agents calling tools they don't have.
				if recordAudit := s.recordAuditFunc(); recordAudit != nil {
					auditDetails := map[string]any{
						"tool_name": tc.Name,
						"task_id":   task.ID,
						"iteration": iteration,
						"has_error": true,
						"input":     tc.Arguments,
						"output":    fmt.Sprintf("Error: unknown tool %q", tc.Name),
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
			}
		}

		wg.Wait()

		// Append tool results and continue loop.
		messages = append(messages, service.Message{
			Role:    "user",
			Content: toolResults,
		})
	}

	// j) On completion, determine if we finished naturally or hit the iteration limit.
	iterationsExhausted := finalContent == ""

	if iterationsExhausted {
		// Extract last assistant text from the exhausted conversation.
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

		slog.Warn("org-delegation: max iterations exhausted — saving conversation state for continuation",
			"task_id", task.ID, "agent_id", agentID, "max_iterations", maxIterations,
			"messages_count", len(messages))

		// Save conversation state as a system comment so re-processing can continue.
		if s.issueCommentStore != nil {
			// Serialize the conversation (skip the system message to save space — it will be rebuilt).
			var toSave []service.Message
			for _, m := range messages {
				if m.Role != "system" {
					toSave = append(toSave, m)
				}
			}
			if stateJSON, err := json.Marshal(toSave); err == nil {
				_, commentErr := s.issueCommentStore.CreateComment(ctx, service.IssueComment{
					ID:         ulid.Make().String(),
					TaskID:     task.ID,
					AuthorType: "system",
					AuthorID:   "org-delegation",
					Body:       conversationStatePrefix + string(stateJSON),
				})
				if commentErr != nil {
					slog.Warn("org-delegation: failed to save conversation state",
						"task_id", task.ID, "error", commentErr)
				} else {
					slog.Info("org-delegation: conversation state saved for continuation",
						"task_id", task.ID, "saved_messages", len(toSave))
				}
			}
		}

		// Mark the task as blocked (not completed) so it's clear it needs continuation.
		resultMsg := finalContent
		if resultMsg == "" {
			resultMsg = "(no output yet)"
		}
		blockedResult := fmt.Sprintf("[ITERATION_LIMIT] Task reached the maximum of %d iterations and was paused. Partial progress saved — re-process to continue.\n\n%s", maxIterations, resultMsg)

		if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusBlocked, blockedResult); err != nil {
			return fmt.Errorf("org-delegation: update task to blocked: %w", err)
		}

		slog.Info("org-delegation: task paused at iteration limit",
			"task_id", task.ID, "agent_id", agentID, "depth", depth)
		return nil
	}

	// Extract and persist memory from the conversation (non-fatal on error).
	s.extractAndPersistMemory(ctx, org, task, agent, agentID, messages)

	if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, finalContent); err != nil {
		return fmt.Errorf("org-delegation: update task to completed: %w", err)
	}

	slog.Info("org-delegation: task completed",
		"task_id", task.ID, "agent_id", agentID, "depth", depth)

	return nil
}

// propagateStatusToParent logs a debug message when a child task reaches a terminal state.
// It does NOT auto-complete the parent task — the parent's own delegation loop (runOrgDelegation)
// is responsible for deciding when the parent task is complete. Auto-completing the parent would
// race with the head agent's decision-making and could mark it done before the agent processes
// all delegation results.
func (s *Server) propagateStatusToParent(ctx context.Context, task *service.Task) {
	if task.ParentID == "" {
		return // root task, nothing to propagate
	}

	slog.Debug("org-delegation: child task reached terminal state, parent agent will decide completion",
		"child_id", task.ID, "parent_id", task.ParentID, "child_status", task.Status)
}

// completeTaskWithStatus updates a task's status and result, then propagates
// the status change to its parent task (if any).
func (s *Server) completeTaskWithStatus(ctx context.Context, task *service.Task, status, result string) error {
	if err := s.taskStore.UpdateTaskStatus(ctx, task.ID, status, result); err != nil {
		return err
	}

	// Audit: task status changed to terminal state.
	if recordAudit := s.recordAuditFunc(); recordAudit != nil {
		action := "task_completed"
		if status == service.TaskStatusCancelled {
			action = "task_cancelled"
		}

		details := map[string]any{
			"task_title": task.Title,
			"status":     status,
		}
		if task.AssignedAgentID != "" {
			details["agent_id"] = task.AssignedAgentID
		}
		// Include a truncated result preview (first 200 chars).
		if result != "" {
			preview := result
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			details["result_preview"] = preview
		}

		_ = recordAudit(ctx, service.AuditEntry{
			ActorType:      "agent",
			ActorID:        task.AssignedAgentID,
			Action:         action,
			ResourceType:   "task",
			ResourceID:     task.ID,
			OrganizationID: task.OrganizationID,
			Details:        details,
		})
	}

	s.propagateStatusToParent(ctx, task)

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

	// Audit: task delegated to child agent.
	if recordAudit := s.recordAuditFunc(); recordAudit != nil {
		_ = recordAudit(ctx, service.AuditEntry{
			ActorType:      "agent",
			ActorID:        parentTask.AssignedAgentID,
			Action:         "task_delegated",
			ResourceType:   "task",
			ResourceID:     childTask.ID,
			OrganizationID: org.ID,
			Details: map[string]any{
				"parent_task_id": parentTask.ID,
				"child_task_id":  childTask.ID,
				"identifier":     identifier,
				"assignee":       assigneeAgentID,
				"depth":          depth + 1,
				"description":    description,
			},
		})
	}

	return childTask, nil
}

// resolveRootTaskID walks up the ParentID chain to find the root task ID.
// This is used to create a shared workspace directory for the entire delegation chain.
func (s *Server) resolveRootTaskID(ctx context.Context, task *service.Task) string {
	current := task
	for current.ParentID != "" {
		parent, err := s.taskStore.GetTask(ctx, current.ParentID)
		if err != nil || parent == nil {
			// Can't walk further — use current as the root.
			break
		}
		current = parent
	}
	return current.ID
}

// detectUnfulfilledDelegation checks if the agent's response text mentions
// delegating to a team member without actually calling a delegate_to_* tool.
// This catches LLM hallucinations like "Now delegating to Video Producer..."
// where the model writes about delegation intent but doesn't make the tool call.
func detectUnfulfilledDelegation(content string, delegateToolMap map[string]string) bool {
	contentLower := strings.ToLower(content)
	// Must contain some form of "delegat" (delegating, delegate, delegation).
	if !strings.Contains(contentLower, "delegat") {
		return false
	}
	for toolName := range delegateToolMap {
		// Check for the agent name part (e.g. "video_producer" from "delegate_to_video_producer").
		agentPart := strings.TrimPrefix(toolName, "delegate_to_")
		// Also match natural language like "delegating to Video Producer".
		naturalName := strings.ReplaceAll(agentPart, "_", " ")
		if strings.Contains(contentLower, naturalName) || strings.Contains(contentLower, agentPart) {
			return true
		}
	}
	return false
}
