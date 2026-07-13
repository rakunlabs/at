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
		result := fmt.Sprintf("[DELEGATION_DEPTH_LIMIT] Task blocked because delegation depth %d reached the configured maximum of %d.", depth, maxDepth)
		if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusBlocked, result); err != nil {
			return fmt.Errorf("org-delegation: update task at max depth: %w", err)
		}
		return nil
	}

	// Track every in-flight task, including child delegations. Most entry
	// points already register the root task before calling runOrgDelegation;
	// child tasks do not, so register them here when absent. The derived context
	// stays tied to the parent context, so cancelling the root still cancels the
	// whole delegation tree.
	if !s.isDelegationActive(task.ID) {
		var cleanup func()
		ctx, cleanup = s.registerDelegation(ctx, task.ID, agentID, org.ID)
		defer cleanup()
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
	delegationAllowed := depth+1 < maxDepth
	if !delegationAllowed && len(reports) > 0 {
		slog.Info("org-delegation: not exposing delegate tools at depth limit",
			"org_id", org.ID, "task_id", task.ID, "agent_id", agentID, "depth", depth, "max_depth", maxDepth)
		reports = nil
	}

	// e) Build delegate tools and dispatch map.
	// Maps tool name → agent ID of the direct report.
	delegateToolMap := make(map[string]string, len(reports))
	var delegateTools []service.Tool

	// We also need the Agent records for building the system prompt.
	type reportInfo struct {
		orgAgent     service.OrganizationAgent
		agent        *service.Agent
		capabilities string   // one-line skills/tools/mcp summary
		subReports   []string // names of this report's own direct reports (sub-team)
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

		// Resolve the report's capabilities and its own sub-team so the
		// manager can see who does what, and how deep each branch goes,
		// before delegating.
		capabilities := s.agentCapabilitySummary(ctx, reportAgent)
		var subReports []string
		if subs, err := s.getDirectReports(ctx, org.ID, oa.AgentID); err == nil {
			for _, sub := range subs {
				if sa, err := s.agentStore.GetAgent(ctx, sub.AgentID); err == nil && sa != nil {
					subReports = append(subReports, sa.Name)
				}
			}
		}

		reportInfos = append(reportInfos, reportInfo{
			orgAgent:     oa,
			agent:        reportAgent,
			capabilities: capabilities,
			subReports:   subReports,
		})

		toolName := uniqueDelegateToolName(reportAgent.Name, oa.AgentID, delegateToolMap)

		// Capability-aware tool description: name + org title + free-form
		// description + resolved capabilities, so the LLM routes work to
		// the teammate actually equipped for it.
		toolDesc := fmt.Sprintf("Delegate a task to %s", reportAgent.Name)
		if oa.Title != "" {
			toolDesc += fmt.Sprintf(" (%s)", oa.Title)
		}
		toolDesc += "."
		if reportAgent.Config.Description != "" {
			toolDesc += " " + reportAgent.Config.Description
		}
		if capabilities != "" {
			toolDesc += " Capabilities — " + capabilities + "."
		}
		tool := service.Tool{
			Name:        toolName,
			Description: toolDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "The concrete task or instruction to delegate. Be specific about what you need and the expected output.",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Optional background the teammate needs: why this is needed, constraints, prior decisions, or how the result will be used. Passed to the teammate alongside the task.",
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
		bt, ok := builtinToolByName(toolName)
		if !ok {
			continue
		}
		builtinToolDefs = append(builtinToolDefs, service.Tool{
			Name:        bt.Name,
			Description: bt.Description,
			InputSchema: bt.InputSchema,
		})
		builtinToolMap[bt.Name] = builtinToolHandler{name: bt.Name}
	}

	// Task-processing agents always receive a small, scoped task tool surface.
	// This is intentionally independent of agent config: while inside a task,
	// derived work should be created/updated through the current task context,
	// not as unrelated root tasks.
	for _, t := range taskContextToolDefs() {
		if _, ok := builtinToolMap[t.Name]; ok {
			continue
		}
		builtinToolDefs = append(builtinToolDefs, t)
		builtinToolMap[t.Name] = builtinToolHandler{name: t.Name}
	}

	// e4) Load MCP-set tools for this agent (workflows exposed as wf_* tools,
	// stdio/HTTP upstream MCPs, and server-side skill/builtin/RAG/HTTP tools
	// declared via mcp_sets). The chat-session loop already does this; without
	// it, agents that rely on mcp_sets — e.g. a Video Producer whose
	// `wf_video_toolkit` workflow (entry `assemble_video`) and ElevenLabs MCP
	// live in mcp_sets — silently lose those tools when run through org
	// delegation, and every such call fails with `unknown tool`.
	mcpToolNames := make(map[string]bool)
	mcpSetToolMap := make(map[string]string) // tool name -> MCP set name (direct dispatch)
	var mcpSetTools []service.Tool
	var mcpClients []service.MCPClient
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()

	if s.mcpSetStore != nil && len(agent.Config.MCPSets) > 0 {
		var mcpURLs []string
		mcpURLs = append(mcpURLs, agent.Config.MCPs...)
		var mcpSetUpstreams []service.MCPUpstream

		for _, setName := range agent.Config.MCPSets {
			set, err := s.mcpSetStore.GetMCPSetByName(ctx, setName)
			if err != nil || set == nil {
				slog.Warn("org-delegation: MCP set not found", "set", setName, "error", err)
				continue
			}
			// MCP Server references resolve via the gateway loopback URL.
			for _, serverName := range set.Servers {
				mcpURLs = append(mcpURLs, fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp/%s",
					s.config.Port, s.config.BasePath, serverName))
			}
			mcpURLs = append(mcpURLs, set.URLs...)
			mcpSetUpstreams = append(mcpSetUpstreams, set.Config.MCPUpstreams...)

			// Server-side tools (skills/builtins/RAG/HTTP/workflows) resolve
			// directly through callMCPSetTool — no HTTP round-trip needed.
			if len(set.Config.EnabledRAGTools) > 0 || len(set.Config.HTTPTools) > 0 ||
				len(set.Config.EnabledSkills) > 0 || len(set.Config.EnabledBuiltinTools) > 0 ||
				len(set.Config.WorkflowIDs) > 0 {
				setTools, err := s.listMCPSetTools(setName)
				if err != nil {
					slog.Warn("org-delegation: failed to list MCP set tools", "set", setName, "error", err)
				} else {
					for _, t := range setTools {
						mcpToolNames[t.Name] = true
						mcpSetToolMap[t.Name] = setName
						mcpSetTools = append(mcpSetTools, t)
					}
				}
			}
		}

		// HTTP MCP endpoints (gateway loopback + custom URLs + legacy mcp_urls).
		for _, url := range mcpURLs {
			client, err := service.NewHTTPMCPClient(ctx, url)
			if err != nil {
				slog.Warn("org-delegation: failed to connect to MCP server, skipping", "url", url, "error", err)
				continue
			}
			mcpClients = append(mcpClients, client)
			tools, err := client.ListTools(ctx)
			if err != nil {
				slog.Warn("org-delegation: failed to list MCP tools, skipping", "url", url, "error", err)
				continue
			}
			for _, t := range tools {
				mcpToolNames[t.Name] = true
				mcpSetTools = append(mcpSetTools, t)
			}
		}

		// Direct upstreams (stdio/HTTP) declared on MCP sets — e.g. the
		// ElevenLabs `uvx elevenlabs-mcp` stdio server.
		for _, upstream := range mcpSetUpstreams {
			client, err := s.newMCPClient(ctx, upstream)
			if err != nil {
				slog.Warn("org-delegation: failed to connect to MCP upstream, skipping", "upstream", upstream.URL+upstream.Command, "error", err)
				continue
			}
			mcpClients = append(mcpClients, client)
			tools, err := client.ListTools(ctx)
			if err != nil {
				slog.Warn("org-delegation: failed to list MCP upstream tools, skipping", "upstream", upstream.URL+upstream.Command, "error", err)
				continue
			}
			for _, t := range tools {
				mcpToolNames[t.Name] = true
				mcpSetTools = append(mcpSetTools, t)
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
	// Org mission context: every agent shares a common frame (org name +
	// mission); the head agent additionally gets outcome-ownership framing.
	systemPrompt += orgContextPrompt(org, depth)

	systemPrompt += taskOperatingProtocolPrompt(task)

	// Delegation context: tell a child WHO delegated the task and WHY,
	// referencing the parent task. No-op for root tasks.
	systemPrompt += s.delegationContextPrompt(ctx, org, task)

	if len(reportInfos) > 0 {
		var teamSection strings.Builder
		teamSection.WriteString("\n\n## Your Team (Direct Reports)\nYou can delegate tasks to these teammates using the delegate_to_* tools. Match each piece of work to the teammate whose capabilities fit best:\n\n")
		for _, ri := range reportInfos {
			// Header line: name, role/title, description.
			roleTitle := strings.TrimSpace(strings.Trim(fmt.Sprintf("%s %s", ri.orgAgent.Role, ri.orgAgent.Title), " "))
			header := ri.agent.Name
			if roleTitle != "" {
				header += fmt.Sprintf(" (%s)", roleTitle)
			}
			desc := ri.agent.Config.Description
			if desc != "" {
				teamSection.WriteString(fmt.Sprintf("- **%s**: %s\n", header, desc))
			} else {
				teamSection.WriteString(fmt.Sprintf("- **%s**\n", header))
			}
			if ri.capabilities != "" {
				teamSection.WriteString(fmt.Sprintf("  - Capabilities: %s\n", ri.capabilities))
			}
			if len(ri.subReports) > 0 {
				teamSection.WriteString(fmt.Sprintf("  - Leads a sub-team: %s (can delegate further)\n", joinCapped(ri.subReports, maxRosterSkills)))
			}
		}
		teamSection.WriteString("\n## CRITICAL Delegation Rules\n")
		teamSection.WriteString("1. To delegate work you MUST call the delegate_to_* tool. Writing \"I'll delegate to X\" or \"Now delegating to X\" in text does NOT delegate — only tool calls do.\n")
		teamSection.WriteString("2. Do NOT finish (stop making tool calls) until ALL planned delegations are complete and you have reviewed ALL results.\n")
		teamSection.WriteString("3. After each delegation result comes back, review it and decide whether to delegate further, request revisions, or finalize.\n")
		teamSection.WriteString("4. You are the decision-maker: only YOU decide when the overall task is complete. Summarize the final outcome in your last message.\n")
		teamSection.WriteString("5. When delegating, pass a specific `task` and use the optional `context` field to give the teammate the background they need (why it matters, constraints, how you'll use the result). They cannot see your conversation — only what you pass.\n")
		systemPrompt += teamSection.String()
	} else if !delegationAllowed {
		systemPrompt += fmt.Sprintf("\n\n## Delegation Limit\nYou are at the last allowed delegation level for this organization (depth %d, max %d). Complete the task yourself; no delegate_to_* tools are available at this depth.\n", depth, maxDepth)
	}

	// f2) Inject shared workspace directory into system prompt.
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

	// Trace identity: each runOrgDelegation invocation is one trace; the
	// whole delegation tree groups into a session keyed by the root task
	// ID. A parent delegation pre-mints the child's trace ID (so the
	// delegate_to_* tool observation can cross-link it) and passes it via
	// context.
	runTraceID := orgTraceIDFromContext(ctx)
	if runTraceID == "" {
		runTraceID = ulid.Make().String()
		// Store it back so downstream helpers (completeTaskWithStatus,
		// createDelegationTask) attribute their events to this trace.
		ctx = contextWithOrgTraceID(ctx, runTraceID)
	}
	traceSessionID := s.resolveRootTaskID(ctx, task)

	// Observation: task started processing.
	s.recordLLMCallAsync(ctx, llmAuditParams{
		source:    "agent",
		obsType:   service.ObservationEvent,
		name:      "task_started",
		traceID:   runTraceID,
		sessionID: traceSessionID,
		agentID:   agentID,
		taskID:    task.ID,
		runID:     runTraceID,
		orgID:     org.ID,
		metadata: map[string]any{
			"task_title": task.Title,
			"agent_name": agent.Name,
			"depth":      depth,
			"model":      model,
			"provider":   agent.Config.Provider,
		},
	})

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
		continueMsg := "Continue processing this task from where you left off. Review your progress so far and complete the remaining work."
		switch {
		case strings.HasPrefix(task.Result, "[OUTPUT_LIMIT]"):
			continueMsg += " The previous run reached the model output-token limit; keep the final response concise and use artifact files for large outputs."
		case strings.HasPrefix(task.Result, "[EMPTY_RESPONSE]"):
			continueMsg += " The previous run returned no final content; explicitly return a concise final result this time."
		default:
			continueMsg += " The previous run reached the agent iteration limit."
		}
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
	// MCP-set tools (workflows, upstreams, server-side skill/builtin/RAG/HTTP).
	// Stripped to name/description/schema so tool handlers never reach the LLM.
	for _, t := range mcpSetTools {
		llmTools = append(llmTools, service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	var finalContent string
	var lastFinishReason string
	completedNaturally := false
	endedWithEmptyResponse := false

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			slog.Warn("org-delegation: context cancelled",
				"task_id", task.ID, "agent_id", agentID, "iteration", iteration)
			_ = s.completeTaskWithStatus(ctx, task, service.TaskStatusCancelled, fmt.Sprintf("context cancelled: %v", err))
			return fmt.Errorf("org-delegation: cancelled: %w", err)
		}

		// Check organization and agent budgets before each LLM call.
		if budgetErr := s.checkOrganizationBudget(ctx, org); budgetErr != nil {
			slog.Warn("org-delegation: organization budget exceeded",
				"org_id", org.ID, "task_id", task.ID, "error", budgetErr)
			result := fmt.Sprintf("[BUDGET_EXCEEDED] organization budget exceeded: %v", budgetErr)
			if updateErr := s.completeTaskWithStatus(ctx, task, service.TaskStatusBlocked, result); updateErr != nil {
				return fmt.Errorf("org-delegation: update task for org budget exceeded: %w", updateErr)
			}
			return nil
		}
		if s.agentBudgetStore != nil {
			checkBudget := s.checkBudgetFunc()
			if checkBudget != nil {
				if budgetErr := checkBudget(ctx, agentID); budgetErr != nil {
					slog.Warn("org-delegation: budget exceeded",
						"agent_id", agentID, "task_id", task.ID, "error", budgetErr)
					result := fmt.Sprintf("[BUDGET_EXCEEDED] agent budget exceeded: %v", budgetErr)
					if updateErr := s.completeTaskWithStatus(ctx, task, service.TaskStatusBlocked, result); updateErr != nil {
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
		var windowed []service.Message
		chatOpts := s.loopGov.ChatOptions()
		for attempt := 0; attempt < 3; attempt++ {
			windowed, _ = s.loopGov.LimitWithTools(ctx, agentID, task.ID, messages, llmTools)
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
					AgentID:        agentID,
					Model:          model,
					Provider:       agent.Config.Provider,
					OrganizationID: org.ID,
					TaskID:         task.ID,
					LatencyMs:      latencyMs,
					Status:         "error",
					ErrorCode:      classifyHTTPError(chatErr),
					ErrorMessage:   chatErr.Error(),
				})
			}
			// Observation: failed generation. Bodies are captured only
			// when the llm_audit feature is on.
			var failedReqBody []byte
			if s.llmAuditEnabled(ctx) {
				failedReqBody, _ = json.Marshal(map[string]any{"model": model, "messages": windowed, "tools": llmTools})
			}
			s.recordLLMCallAsync(ctx, llmAuditParams{
				source:         "agent",
				traceID:        runTraceID,
				sessionID:      traceSessionID,
				agentID:        agentID,
				taskID:         task.ID,
				runID:          runTraceID,
				orgID:          org.ID,
				requestedModel: agent.Config.Provider + "/" + model,
				fullModel:      agent.Config.Provider + "/" + model,
				requestBody:    failedReqBody,
				latencyMs:      latencyMs,
				status:         "error",
				level:          service.ObservationLevelError,
				errCode:        classifyHTTPError(chatErr),
				errMsg:         chatErr.Error(),
				metadata:       map[string]any{"iteration": iteration},
			})
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
		if resp.Usage.TotalTokenCount() > 0 {
			recordUsage := s.recordUsageFunc()
			if recordUsage != nil {
				if usageErr := recordUsage(ctx, workflow.UsageEvent{
					AgentID:        agentID,
					Model:          model,
					Provider:       agent.Config.Provider,
					OrganizationID: org.ID,
					TaskID:         task.ID,
					Usage:          resp.Usage,
					LatencyMs:      latencyMs,
					Status:         "ok",
				}); usageErr != nil {
					slog.Warn("org-delegation: failed to record usage",
						"agent_id", agentID, "error", usageErr)
				}
			}
		}

		// Observation: completed generation. The post-windowing request
		// (what was actually sent) and the provider response are stored
		// as bodies when the llm_audit feature is on; the skeleton
		// (tokens, cost, latency, hierarchy) is always recorded. The
		// returned observation ID parents this iteration's tool calls.
		var genReqBody, genRespBody []byte
		if s.llmAuditEnabled(ctx) {
			genReqBody, _ = json.Marshal(map[string]any{"model": model, "messages": windowed, "tools": llmTools})
			genRespBody, _ = json.Marshal(resp)
		}
		genObsID := s.recordLLMCallAsync(ctx, llmAuditParams{
			source:         "agent",
			traceID:        runTraceID,
			sessionID:      traceSessionID,
			agentID:        agentID,
			taskID:         task.ID,
			runID:          runTraceID,
			orgID:          org.ID,
			requestedModel: agent.Config.Provider + "/" + model,
			fullModel:      agent.Config.Provider + "/" + model,
			requestBody:    genReqBody,
			responseBody:   genRespBody,
			usage:          resp.Usage,
			latencyMs:      latencyMs,
			metadata: map[string]any{
				"iteration":  iteration,
				"finished":   resp.Finished,
				"tool_calls": len(resp.ToolCalls),
			},
		})
		lastFinishReason = resp.FinishReason

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
			if isOutputLimitFinishReason(resp.FinishReason) {
				messages = append(messages, service.Message{
					Role:    "user",
					Content: "Your response reached the output-token limit before completing. Continue from the partial response, keep the final answer concise, and write large structured output to the requested artifact file instead of returning it inline.",
				})
				continue
			}
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

			if resp.Content == "" {
				endedWithEmptyResponse = true
				break
			}
			finalContent = resp.Content
			completedNaturally = true
			break
		}

		// i) Execute tool calls concurrently (fan-out: one goroutine per delegation).
		toolResults := make([]service.ContentBlock, len(resp.ToolCalls))
		var wg sync.WaitGroup
		var resultMu sync.Mutex

		// recordToolObs records a tool observation parented to this
		// iteration's generation (skill / builtin / MCP / unknown tools;
		// delegation tools record inline to attach child-trace links).
		recordToolObs := func(name string, args map[string]any, output string, hasErr bool, latencyMs int64) {
			level := service.ObservationLevelDefault
			if hasErr {
				level = service.ObservationLevelError
			}
			argsJSON, _ := json.Marshal(args)
			s.recordLLMCallAsync(ctx, llmAuditParams{
				source:              "agent",
				obsType:             service.ObservationTool,
				parentObservationID: genObsID,
				name:                name,
				traceID:             runTraceID,
				sessionID:           traceSessionID,
				agentID:             agentID,
				taskID:              task.ID,
				runID:               runTraceID,
				orgID:               org.ID,
				input:               string(argsJSON),
				output:              output,
				level:               level,
				latencyMs:           latencyMs,
				metadata:            map[string]any{"iteration": iteration},
			})
		}

		for i, tc := range resp.ToolCalls {
			toolStarted := time.Now()
			slog.Debug("org-delegation: tool call",
				"tool", tc.Name, "task_id", task.ID, "iteration", iteration)

			if reportAgentID, ok := delegateToolMap[tc.Name]; ok {
				wg.Add(1)
				go func(idx int, toolCall service.ToolCall, targetAgentID string, started time.Time) {
					defer wg.Done()

					taskText, _ := toolCall.Arguments["task"].(string)
					if taskText == "" {
						taskText = task.Title
					}

					// Fold optional delegator context into the child's
					// task description so the teammate receives the
					// background the manager chose to pass.
					if contextText, _ := toolCall.Arguments["context"].(string); strings.TrimSpace(contextText) != "" {
						taskText += "\n\n## Context from delegator\n" + strings.TrimSpace(contextText)
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

					// Pre-mint the child run's trace ID so this tool
					// observation can cross-link the child trace.
					childTraceID := ulid.Make().String()
					childCtx := contextWithOrgTraceID(ctx, childTraceID)

					var result string
					if delegErr := s.runOrgDelegation(childCtx, org, childTask, targetAgentID, depth+1); delegErr != nil {
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

					// Observation: delegation tool call, parented to the
					// generation that requested it and cross-linked to the
					// child run's trace.
					argsJSON, _ := json.Marshal(toolCall.Arguments)
					s.recordLLMCallAsync(ctx, llmAuditParams{
						source:              "agent",
						obsType:             service.ObservationTool,
						parentObservationID: genObsID,
						name:                toolCall.Name,
						traceID:             runTraceID,
						sessionID:           traceSessionID,
						agentID:             agentID,
						taskID:              task.ID,
						runID:               runTraceID,
						orgID:               org.ID,
						input:               string(argsJSON),
						output:              result,
						latencyMs:           time.Since(started).Milliseconds(),
						metadata: map[string]any{
							"iteration":      iteration,
							"child_task_id":  childTask.ID,
							"child_trace_id": childTraceID,
						},
					})
				}(i, tc, reportAgentID, toolStarted)
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

				// Observation: skill tool call (JS/bash handler) with its
				// arguments and (post-truncation) result.
				recordToolObs(tc.Name, tc.Arguments, result, callErr != nil, time.Since(toolStarted).Milliseconds())
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

				// Observation: builtin tool call (task_create /
				// bash_execute / mem_save ...) with structured input and
				// the (truncated) output that got fed back into the LLM.
				recordToolObs(tc.Name, tc.Arguments, result, callErr != nil, time.Since(toolStarted).Milliseconds())
			} else if setName, ok := mcpSetToolMap[tc.Name]; ok {
				// MCP-set tool resolved server-side (workflow exposed as a
				// wf_* tool, or a skill/builtin/RAG/HTTP tool declared via
				// mcp_sets) — no HTTP round-trip.
				result, callErr := s.callMCPSetTool(ctx, setName, tc.Name, tc.Arguments)
				if callErr != nil {
					slog.Error("org-delegation: mcp-set tool call failed",
						"tool", tc.Name, "set", setName, "task_id", task.ID, "error", callErr)
					result = fmt.Sprintf("Error: %v", callErr)
				}
				result, _ = s.loopGov.TruncateToolResult(task.ID, tc.Name, result)
				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				}
				recordToolObs(tc.Name, tc.Arguments, result, callErr != nil, time.Since(toolStarted).Milliseconds())
			} else if mcpToolNames[tc.Name] {
				// MCP tool served by a connected client (HTTP endpoint or a
				// stdio/HTTP upstream such as the ElevenLabs MCP).
				result, callErr := callMCPToolFromClients(ctx, mcpClients, tc.Name, tc.Arguments)
				if callErr != nil {
					slog.Error("org-delegation: mcp tool call failed",
						"tool", tc.Name, "task_id", task.ID, "error", callErr)
					result = fmt.Sprintf("Error: %v", callErr)
				}
				result, _ = s.loopGov.TruncateToolResult(task.ID, tc.Name, result)
				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				}
				recordToolObs(tc.Name, tc.Arguments, result, callErr != nil, time.Since(toolStarted).Milliseconds())
			} else {
				// Unknown tool — handle synchronously (no goroutine needed).
				toolResults[i] = service.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("Error: unknown tool %q", tc.Name),
				}

				// Observation: unknown tool call. The output is the
				// synthetic error message we fed back to the LLM — useful
				// for spotting agents calling tools they don't have.
				recordToolObs(tc.Name, tc.Arguments, fmt.Sprintf("Error: unknown tool %q", tc.Name), true, time.Since(toolStarted).Milliseconds())
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
	iterationsExhausted := !completedNaturally

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

		resultCode := "ITERATION_LIMIT"
		resultReason := fmt.Sprintf("Task reached the maximum of %d iterations and was paused.", maxIterations)
		if isOutputLimitFinishReason(lastFinishReason) {
			resultCode = "OUTPUT_LIMIT"
			resultReason = "The model reached its output-token limit before returning a complete result."
		} else if endedWithEmptyResponse {
			resultCode = "EMPTY_RESPONSE"
			resultReason = fmt.Sprintf("The model ended with finish reason %q but returned no final content.", lastFinishReason)
		}

		slog.Warn("org-delegation: run paused — saving conversation state for continuation",
			"task_id", task.ID, "agent_id", agentID, "max_iterations", maxIterations,
			"finish_reason", lastFinishReason, "result_code", resultCode,
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
		blockedResult := fmt.Sprintf("[%s] %s Partial progress saved — re-process to continue.\n\n%s", resultCode, resultReason, resultMsg)

		if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusBlocked, blockedResult); err != nil {
			return fmt.Errorf("org-delegation: update task to blocked: %w", err)
		}

		slog.Info("org-delegation: task paused",
			"task_id", task.ID, "agent_id", agentID, "depth", depth, "result_code", resultCode)
		return nil
	}

	if err := s.completeTaskWithStatus(ctx, task, service.TaskStatusCompleted, finalContent); err != nil {
		return fmt.Errorf("org-delegation: update task to completed: %w", err)
	}

	slog.Info("org-delegation: task completed",
		"task_id", task.ID, "agent_id", agentID, "depth", depth)

	return nil
}

func isOutputLimitFinishReason(reason string) bool {
	switch strings.ToLower(reason) {
	case "length", "max_tokens":
		return true
	default:
		return false
	}
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

	// Observation: task reached a terminal state. The trace ID rides the
	// context when we're inside a delegation run; outside one the
	// recorder mints a fresh trace.
	action := "task_completed"
	if status == service.TaskStatusCancelled {
		action = "task_cancelled"
	} else if status == service.TaskStatusBlocked {
		action = "task_blocked"
	}

	metadata := map[string]any{
		"task_title": task.Title,
		"status":     status,
	}
	// Include a truncated result preview (first 200 chars).
	if result != "" {
		preview := result
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		metadata["result_preview"] = preview
	}

	runTraceID := orgTraceIDFromContext(ctx)
	s.recordLLMCallAsync(ctx, llmAuditParams{
		source:    "agent",
		obsType:   service.ObservationEvent,
		name:      action,
		traceID:   runTraceID,
		sessionID: s.resolveRootTaskID(ctx, task),
		agentID:   task.AssignedAgentID,
		taskID:    task.ID,
		runID:     runTraceID,
		orgID:     task.OrganizationID,
		metadata:  metadata,
	})

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
	maxDepth := org.MaxDelegationDepth
	if maxDepth == 0 {
		maxDepth = 10
	}
	if depth+1 >= maxDepth {
		return nil, fmt.Errorf("max delegation depth reached: next depth %d would meet or exceed max depth %d", depth+1, maxDepth)
	}

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

	// Observation: task delegated to child agent. Attributed to the
	// parent run's trace (from context).
	runTraceID := orgTraceIDFromContext(ctx)
	s.recordLLMCallAsync(ctx, llmAuditParams{
		source:    "agent",
		obsType:   service.ObservationEvent,
		name:      "task_delegated",
		traceID:   runTraceID,
		sessionID: s.resolveRootTaskID(ctx, parentTask),
		agentID:   parentTask.AssignedAgentID,
		taskID:    childTask.ID,
		runID:     runTraceID,
		orgID:     org.ID,
		metadata: map[string]any{
			"parent_task_id": parentTask.ID,
			"child_task_id":  childTask.ID,
			"identifier":     identifier,
			"assignee":       assigneeAgentID,
			"depth":          depth + 1,
			"description":    description,
		},
	})

	return childTask, nil
}

// orgTraceIDCtxKey carries the current delegation run's trace ID through
// the context so nested helpers (completeTaskWithStatus,
// createDelegationTask) and pre-minted child runs attribute their
// observations to the right trace.
type orgTraceIDCtxKey struct{}

func contextWithOrgTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, orgTraceIDCtxKey{}, traceID)
}

func orgTraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(orgTraceIDCtxKey{}).(string); ok {
		return v
	}
	return ""
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

func uniqueDelegateToolName(agentName, agentID string, existing map[string]string) string {
	base := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, agentName)
	base = strings.ToLower(base)
	if base == "" {
		base = "agent"
	}
	toolName := "delegate_to_" + base
	if _, ok := existing[toolName]; !ok {
		return toolName
	}

	suffix := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, agentID)
	suffix = strings.Trim(strings.ToLower(suffix), "_")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	suffix = strings.Trim(suffix, "_")
	if suffix == "" {
		suffix = "agent"
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%s", toolName, suffix)
		if i > 1 {
			candidate = fmt.Sprintf("%s_%s_%d", toolName, suffix, i)
		}
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}

func (s *Server) checkOrganizationBudget(ctx context.Context, org *service.Organization) error {
	if org == nil || org.BudgetMonthlyCents <= 0 {
		return nil
	}

	spendCents := float64(org.SpentMonthlyCents)
	if s.costEventStore != nil {
		summary, err := s.costEventStore.GetUsageSummary(ctx, service.UsageFilter{
			From:   orgBudgetWindowStart(org),
			OrgIDs: []string{org.ID},
		})
		if err != nil {
			return fmt.Errorf("get organization spend: %w", err)
		}
		spendCents = summary.CostCents
	}

	limitCents := float64(org.BudgetMonthlyCents)
	if spendCents >= limitCents {
		return fmt.Errorf("organization %s has exceeded monthly budget (%.2f / %.2f USD)", org.ID, spendCents/100, limitCents/100)
	}
	return nil
}

func orgBudgetWindowStart(org *service.Organization) string {
	if org != nil && org.BudgetResetAt != "" {
		if t, err := time.Parse(time.RFC3339, org.BudgetResetAt); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start.Format(time.RFC3339)
}
