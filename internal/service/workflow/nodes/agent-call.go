package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/logi"
)

// agentCallNode runs an agentic loop: it sends a prompt to an LLM provider,
// collects tool calls, executes them (via MCP, skill JS handlers, or inline JS
// handlers), feeds results back, and repeats until the LLM produces a final
// answer or the iteration limit is reached.
//
// Config (node.Data):
//
//	"agent_id":       string   — ID of a stored Agent preset (optional)
//	"provider":       string   — provider key for registry lookup (required if agent_id empty)
//	"model":          string   — model override (optional, empty = provider default)
//	"system_prompt":  string   — system message prepended to conversation (optional)
//	"max_iterations": float64  — max tool-call rounds (default 10, 0 = unlimited)
//	"tool_timeout":   float64  — bash tool execution timeout in seconds (default 60)
//	"mcp_urls":       []string — MCP server URLs to connect to (optional)
//	"skills":         []string — skill names or IDs to load (optional)
//	"tools":          []map    — inline tool definitions (optional)
//
// Input ports:
//
//	"prompt"  — the user message text (string)
//	"context" — additional context to include (optional, string)
//
// Output ports:
//
//	"response" — the final LLM response text
//	"text"     — alias for response
type agentCallNode struct {
	agentID       string
	providerKey   string
	model         string
	systemPrompt  string
	maxIterations int
	toolTimeout   time.Duration
	mcpURLs       []string
	skillNames    []string
	inlineTools   []service.Tool
}

func init() {
	workflow.RegisterNodeType("agent_call", newAgentCallNode)
}

func newAgentCallNode(node service.WorkflowNode) (workflow.Noder, error) {
	agentID, _ := node.Data["agent_id"].(string)
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	systemPrompt, _ := node.Data["system_prompt"].(string)

	maxIterations := -1
	if v, ok := node.Data["max_iterations"].(float64); ok {
		maxIterations = int(v)
	}

	var toolTimeout time.Duration
	if v, ok := node.Data["tool_timeout"].(float64); ok {
		toolTimeout = time.Duration(v) * time.Second
	} else {
		toolTimeout = -1
	}

	// Parse MCP URLs.
	var mcpURLs []string
	if raw, ok := node.Data["mcp_urls"].([]any); ok {
		for _, u := range raw {
			if s, ok := u.(string); ok && s != "" {
				mcpURLs = append(mcpURLs, s)
			}
		}
	}

	// Parse skill names/IDs.
	var skillNames []string
	if raw, ok := node.Data["skills"].([]any); ok {
		for _, s := range raw {
			if name, ok := s.(string); ok && name != "" {
				skillNames = append(skillNames, name)
			}
		}
	}

	// Parse inline tool definitions.
	var inlineTools []service.Tool
	if raw, ok := node.Data["tools"].([]any); ok {
		for _, t := range raw {
			toolMap, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tool := service.Tool{}
			if name, ok := toolMap["name"].(string); ok {
				tool.Name = name
			}
			if desc, ok := toolMap["description"].(string); ok {
				tool.Description = desc
			}
			if schema, ok := toolMap["inputSchema"].(map[string]any); ok {
				tool.InputSchema = schema
			}
			if handler, ok := toolMap["handler"].(string); ok {
				tool.Handler = handler
			}
			if handlerType, ok := toolMap["handler_type"].(string); ok {
				tool.HandlerType = handlerType
			}
			if tool.Name != "" {
				inlineTools = append(inlineTools, tool)
			}
		}
	}

	return &agentCallNode{
		agentID:       agentID,
		providerKey:   providerKey,
		model:         model,
		systemPrompt:  systemPrompt,
		maxIterations: maxIterations,
		toolTimeout:   toolTimeout,
		mcpURLs:       mcpURLs,
		skillNames:    skillNames,
		inlineTools:   inlineTools,
	}, nil
}

func (n *agentCallNode) Type() string { return "agent_call" }

func (n *agentCallNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "agent_call",
		Label:       "Agent Call",
		Category:    "processing",
		Description: "Agentic loop with tool calling (MCP, skills, inline tools)",
		Inputs: []workflow.PortMeta{
			{Name: "prompt", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Prompt", Position: "left"},
			{Name: "context", Type: workflow.PortTypeData, Label: "Context", Position: "left"},
			{Name: "skills", Type: workflow.PortTypeConfig, Label: "Skills", Position: "bottom"},
			{Name: "mcp", Type: workflow.PortTypeConfig, Label: "MCP", Position: "bottom"},
			{Name: "memory", Type: workflow.PortTypeConfig, Label: "Memory", Position: "bottom"},
			{Name: "agents", Type: workflow.PortTypeConfig, Label: "Agents", Position: "bottom"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "response", Type: workflow.PortTypeText, Label: "Response", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Description: "Model name"},
			{Name: "system_prompt", Type: "string", Description: "System prompt"},
			{Name: "max_iterations", Type: "number", Default: 10, Description: "Max tool call iterations (>= 1; clamped to platform ceiling)"},
		},
		Color: "purple",
	}
}

func (n *agentCallNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.agentID == "" && n.providerKey == "" {
		return fmt.Errorf("agent_call: 'provider' is required when 'agent_id' is not set")
	}

	if reg.ProviderLookup == nil {
		return fmt.Errorf("agent_call: no provider lookup configured")
	}

	// Reject the legacy "0 = unlimited" sentinel — runaway loops were
	// the leading cause of the 2026-04 token-burn incident. Use a real
	// number or omit the field to take the agent default. The platform
	// loop governor still clamps the effective value to its ceiling.
	if n.maxIterations == 0 {
		return fmt.Errorf("agent_call: max_iterations must be >= 1 (the legacy 0=unlimited mode is no longer supported)")
	}

	// Verify the provider exists if specified directly.
	if n.providerKey != "" {
		_, _, err := reg.ProviderLookup(n.providerKey)
		if err != nil {
			return fmt.Errorf("agent_call: provider %q: %w", n.providerKey, err)
		}
	}

	return nil
}

func (n *agentCallNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// 1. Load Agent preset if configured.
	var preset *service.Agent
	if n.agentID != "" {
		if reg.AgentLookup == nil {
			return nil, fmt.Errorf("agent_call: agent lookup not configured")
		}
		var err error
		preset, err = reg.AgentLookup(ctx, n.agentID)
		if err != nil {
			return nil, fmt.Errorf("agent_call: agent %q: %w", n.agentID, err)
		}
		if preset == nil {
			return nil, fmt.Errorf("agent_call: agent %q not found", n.agentID)
		}
	}

	// 2. Resolve provider key.
	providerKey := n.providerKey
	if providerKey == "" && preset != nil {
		providerKey = preset.Config.Provider
	}
	if providerKey == "" {
		return nil, fmt.Errorf("agent_call: no provider specified (node or agent preset)")
	}

	provider, defaultModel, err := reg.ProviderLookup(providerKey)
	if err != nil {
		return nil, fmt.Errorf("agent_call: provider %q: %w", providerKey, err)
	}

	// 3. Resolve model.
	model := n.model
	if model == "" && preset != nil {
		model = preset.Config.Model
	}
	if model == "" {
		model = defaultModel
	}

	// ─── Collect Tools ───

	// toolHandlerInfo holds handler body and type for non-skill tools
	// (builtin / workflow / sub-agent / inline). Skill tool handlers are
	// owned by skillRuntime and resolved on the fly during dispatch — see
	// progressive disclosure below.
	type toolHandlerInfo struct {
		handler     string
		handlerType string // "builtin" | "workflow" | "agent" | inline JS body
		skillID     string // empty for inline/builtin/sub-agent
	}

	// toolHandlers maps tool name → handler info for non-skill tools.
	toolHandlers := make(map[string]toolHandlerInfo)

	// mcpToolNames tracks which tool names come from MCP (dispatched via MCP client).
	mcpToolNames := make(map[string]bool)

	// mcpClients holds initialized MCP clients (closed at the end).
	var mcpClients []service.MCPClient
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()

	var allTools []service.Tool

	// ─── Merge MCP URLs from static config + agent preset + edge inputs ───

	// Log received input keys for debugging connectivity issues
	inputKeys := make([]string, 0, len(inputs))
	for k := range inputs {
		inputKeys = append(inputKeys, k)
	}
	logi.Ctx(ctx).Debug("agent_call: input keys", "keys", inputKeys)

	// Start with node config URLs
	mcpURLs := append([]string{}, n.mcpURLs...)
	// Add preset URLs
	if preset != nil {
		mcpURLs = append(mcpURLs, preset.Config.MCPs...)
	}

	if edgeMCP, ok := inputs["mcp"]; ok {
		switch v := edgeMCP.(type) {
		case string:
			if v != "" {
				mcpURLs = append(mcpURLs, strings.TrimSpace(v))
			}
		case []string:
			for _, s := range v {
				if s != "" {
					mcpURLs = append(mcpURLs, strings.TrimSpace(s))
				}
			}
		case []any:
			for _, u := range v {
				if s, ok := u.(string); ok && s != "" {
					mcpURLs = append(mcpURLs, strings.TrimSpace(s))
				}
			}
		}
	}

	// Deduplicate MCP URLs
	seenMCPs := make(map[string]bool)
	var uniqueMCPs []string
	for _, url := range mcpURLs {
		if url != "" && !seenMCPs[url] {
			seenMCPs[url] = true
			uniqueMCPs = append(uniqueMCPs, url)
		}
	}

	// 1. MCP tools
	for _, url := range uniqueMCPs {
		client, err := service.NewHTTPMCPClient(ctx, url)
		if err != nil {
			logi.Ctx(ctx).Warn("agent_call: failed to connect to MCP server, skipping",
				"url", url, "error", err)
			continue
		}
		mcpClients = append(mcpClients, client)

		tools, err := client.ListTools(ctx)
		if err != nil {
			logi.Ctx(ctx).Warn("agent_call: failed to list MCP tools, skipping",
				"url", url, "error", err)
			continue
		}

		for _, t := range tools {
			mcpToolNames[t.Name] = true
			allTools = append(allTools, t)
		}
	}

	// 2. Skill tools — LAZY (progressive disclosure).
	//
	// Skills are resolved into a SkillRuntime catalog up-front (single DB
	// pass per attached skill), but their SystemPrompt + Tools are NOT
	// injected into the LLM context until the LLM activates each one via
	// the `load_skill` meta-tool. The catalog and the meta-tool are added
	// below in the system-prompt and llmTools build sections.
	var extraSkillNames []string
	extraSkillNames = append(extraSkillNames, n.skillNames...)
	if edgeSkills, ok := inputs["skills"]; ok {
		switch v := edgeSkills.(type) {
		case string:
			if v != "" {
				extraSkillNames = append(extraSkillNames, strings.TrimSpace(v))
			}
		case []string:
			for _, s := range v {
				if s != "" {
					extraSkillNames = append(extraSkillNames, strings.TrimSpace(s))
				}
			}
		case []any:
			for _, s := range v {
				if name, ok := s.(string); ok && name != "" {
					extraSkillNames = append(extraSkillNames, strings.TrimSpace(name))
				}
			}
		}
	}

	var presetSkillRefs []service.SkillRef
	if preset != nil {
		presetSkillRefs = preset.Config.Skills
	}

	skillRuntime, err := workflow.NewSkillRuntime(ctx, reg.SkillLookup, presetSkillRefs, extraSkillNames,
		func(name string, lookupErr error) {
			logi.Ctx(ctx).Warn("agent_call: skill lookup failed, skipping",
				"skill", name, "error", lookupErr)
		})
	if err != nil {
		return nil, fmt.Errorf("agent_call: skill runtime: %w", err)
	}
	logi.Ctx(ctx).Info("agent_call: skill catalog ready",
		"count", len(skillRuntime.Catalog()))

	// 3. Builtin tools (from agent preset config).
	if preset != nil && len(preset.Config.BuiltinTools) > 0 && reg.BuiltinToolDispatcher != nil {
		enabledSet := make(map[string]bool, len(preset.Config.BuiltinTools))
		for _, name := range preset.Config.BuiltinTools {
			enabledSet[name] = true
		}
		for _, def := range reg.BuiltinToolDefs {
			if !enabledSet[def.Name] {
				continue
			}
			allTools = append(allTools, service.Tool{
				Name:        def.Name,
				Description: def.Description,
				InputSchema: def.InputSchema,
			})
			toolHandlers[def.Name] = toolHandlerInfo{
				handler:     def.Name,
				handlerType: "builtin",
			}
		}
	}

	// 3b. Workflow tools (from agent preset config).
	//
	// AgentConfig.Workflows holds workflow NAMES the agent wants to use as
	// callable tools. Each resolves to `wf_<slug>` in the LLM's tool list and
	// dispatches through reg.WorkflowExecutor. This is the direct, explicit
	// alternative to wiring workflows through an MCP set.
	if preset != nil && len(preset.Config.Workflows) > 0 && reg.WorkflowByNameLookup != nil {
		for _, wfName := range preset.Config.Workflows {
			if wfName == "" {
				continue
			}
			wf, err := reg.WorkflowByNameLookup(ctx, wfName)
			if err != nil {
				logi.Ctx(ctx).Warn("agent_call: failed to look up attached workflow",
					"workflow", wfName, "error", err)
				continue
			}
			if wf == nil {
				logi.Ctx(ctx).Warn("agent_call: attached workflow not found", "workflow", wfName)
				continue
			}
			toolDef := buildWorkflowToolDef(wf)
			allTools = append(allTools, toolDef)
			toolHandlers[toolDef.Name] = toolHandlerInfo{
				handler:     wf.ID, // store ID; executor will re-fetch if needed
				handlerType: "workflow",
			}
		}
	}

	// 4. Sub-agents (Delegates)
	// Collect agent IDs from input port "agents".
	var subAgentIDs []string
	if edgeAgents, ok := inputs["agents"]; ok {
		switch v := edgeAgents.(type) {
		case string:
			if v != "" {
				subAgentIDs = append(subAgentIDs, strings.TrimSpace(v))
			}
		case []string:
			for _, s := range v {
				if s != "" {
					subAgentIDs = append(subAgentIDs, strings.TrimSpace(s))
				}
			}
		case []any:
			for _, s := range v {
				if id, ok := s.(string); ok && id != "" {
					subAgentIDs = append(subAgentIDs, strings.TrimSpace(id))
				}
			}
		}
	}

	// Deduplicate sub-agent IDs
	seenAgents := make(map[string]bool)
	var uniqueAgentIDs []string
	for _, id := range subAgentIDs {
		if id != "" && !seenAgents[id] {
			seenAgents[id] = true
			uniqueAgentIDs = append(uniqueAgentIDs, id)
		}
	}

	for _, agentID := range uniqueAgentIDs {
		if reg.AgentLookup == nil {
			logi.Ctx(ctx).Warn("agent_call: agent lookup not configured, skipping sub-agent", "agent_id", agentID)
			continue
		}
		subAgent, err := reg.AgentLookup(ctx, agentID)
		if err != nil {
			logi.Ctx(ctx).Warn("agent_call: failed to look up sub-agent, skipping",
				"agent_id", agentID, "error", err)
			continue
		}
		if subAgent == nil {
			logi.Ctx(ctx).Warn("agent_call: sub-agent not found, skipping", "agent_id", agentID)
			continue
		}

		// Create a tool for this agent.
		// Sanitize name for tool use (alphanumeric + underscores).
		safeName := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return '_'
		}, subAgent.Name)
		toolName := "delegate_to_" + strings.ToLower(safeName)

		toolDesc := fmt.Sprintf("Delegate a task to %s. %s", subAgent.Name, subAgent.Config.Description)
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

		allTools = append(allTools, tool)

		// Register a special handler type for sub-agents.
		toolHandlers[toolName] = toolHandlerInfo{
			handler:     agentID, // Store ID as handler "body"
			handlerType: "agent", // New handler type
		}
	}

	// ─── Build System Prompt ───

	systemPrompt := n.systemPrompt
	if preset != nil && preset.Config.SystemPrompt != "" {
		if systemPrompt != "" {
			systemPrompt = preset.Config.SystemPrompt + "\n\n" + systemPrompt
		} else {
			systemPrompt = preset.Config.SystemPrompt
		}
	}
	if catalog := skillRuntime.CatalogSystemPrompt(); catalog != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += catalog
	}

	// ─── Build Initial Prompt ───

	prompt := toString(inputs["prompt"])
	if prompt == "" {
		prompt = toString(inputs["text"])
		if prompt == "" {
			prompt = toString(inputs["data"])
		}
	}
	if prompt == "" {
		return nil, fmt.Errorf("agent_call: no prompt provided")
	}

	if ctxStr := toString(inputs["context"]); ctxStr != "" {
		prompt = prompt + "\n\nContext:\n" + ctxStr
	}

	// ─── Memory Input ───
	// Memory data from an edge-connected memory_config node is appended
	// as additional context to the prompt.
	if memData := inputs["memory"]; memData != nil {
		memStr := toString(memData)
		if memStr != "" {
			prompt = prompt + "\n\nMemory:\n" + memStr
		}
	}

	// ─── Build Messages ───

	var messages []service.Message
	if systemPrompt != "" {
		messages = append(messages, service.Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}
	messages = append(messages, service.Message{
		Role:    "user",
		Content: prompt,
	})

	// ─── Agentic Loop ───

	// Resolve maxIterations.
	// -1 means the field was absent in the workflow node — defer to the
	// preset agent's MaxIterations, falling back to 10. The Validate()
	// path already rejects an explicit 0 (legacy "unlimited" sentinel)
	// at workflow create/update time. Apply the platform ceiling
	// through the loop governor so config-time clamps are observable.
	maxIterations := n.maxIterations
	if maxIterations == -1 {
		if preset != nil && preset.Config.MaxIterations > 0 {
			maxIterations = preset.Config.MaxIterations
		} else {
			maxIterations = 10
		}
	}
	if reg.LoopGov != nil {
		var presetMax int
		if preset != nil {
			presetMax = preset.Config.MaxIterations
		}
		// Pass node-configured value as taskMax (a per-call override
		// of the agent's default), preset as agentMax. ClampIterations
		// resolves precedence and applies the ceiling.
		maxIterations = reg.LoopGov.ClampIterations(presetMax, maxIterations)
	}

	// Resolve toolTimeout
	toolTimeout := n.toolTimeout
	if toolTimeout == -1 {
		if preset != nil && preset.Config.ToolTimeout > 0 {
			toolTimeout = time.Duration(preset.Config.ToolTimeout) * time.Second
		} else {
			toolTimeout = 60 * time.Second // Default
		}
	}

	// Build the base tool set sent to the LLM — handlers stripped because
	// LLM providers (OpenAI / Anthropic) only consume Name/Description/Schema.
	// Active skill tools are appended per-iteration after the LLM activates
	// each skill via load_skill (progressive disclosure).
	baseLLMTools := make([]service.Tool, 0, len(allTools)+1)
	for _, t := range allTools {
		baseLLMTools = append(baseLLMTools, service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	if skillRuntime.HasSkills() {
		baseLLMTools = append(baseLLMTools, skillRuntime.LoadSkillToolDef())
	}

	// The legacy 0 = unlimited semantics is no longer supported; the
	// loop runs at most maxIterations times and the platform ceiling
	// is enforced by the governor's ClampIterations above.
	for iteration := 0; iteration < maxIterations; iteration++ {
		// Rebuild tool list each iteration: base tools + tools from any
		// skills the LLM has activated so far.
		llmTools := append([]service.Tool{}, baseLLMTools...)
		llmTools = append(llmTools, skillRuntime.ActiveSkillTools()...)

		// Check for cancellation between iterations.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("agent_call: cancelled: %w", err)
		}

		// Check agent budget before making an LLM call.
		if n.agentID != "" && reg.CheckBudget != nil {
			if err := reg.CheckBudget(ctx, n.agentID); err != nil {
				return nil, fmt.Errorf("agent_call: budget exceeded: %w", err)
			}
		}

		// Apply the loop governor before the call: window the message
		// slice and pass an explicit MaxTokens cap.
		callMessages := messages
		var chatOpts *service.ChatOptions
		if reg.LoopGov != nil {
			windowed, _ := reg.LoopGov.Limit(ctx, n.agentID, "", messages)
			callMessages = windowed
			chatOpts = reg.LoopGov.ChatOptions()
		}
		callStart := time.Now()
		resp, err := provider.Chat(ctx, model, callMessages, llmTools, chatOpts)
		latencyMs := time.Since(callStart).Milliseconds()
		if err != nil {
			// Record the failed call for the usage dashboard before returning.
			if n.agentID != "" && reg.RecordUsage != nil {
				if usageErr := reg.RecordUsage(ctx, workflow.UsageEvent{
					AgentID:      n.agentID,
					Model:        model,
					Provider:     providerKey,
					LatencyMs:    latencyMs,
					Status:       "error",
					ErrorCode:    classifyLLMError(err),
					ErrorMessage: err.Error(),
				}); usageErr != nil {
					logi.Ctx(ctx).Warn("agent_call: failed to record error usage",
						"agent_id", n.agentID, "error", usageErr)
				}
			}
			return nil, fmt.Errorf("agent_call: chat failed (iteration %d): %w", iteration, err)
		}

		// Record token usage for cost tracking.
		if n.agentID != "" && reg.RecordUsage != nil && resp.Usage.TotalTokens > 0 {
			if usageErr := reg.RecordUsage(ctx, workflow.UsageEvent{
				AgentID:   n.agentID,
				Model:     model,
				Provider:  providerKey,
				Usage:     resp.Usage,
				LatencyMs: latencyMs,
				Status:    "ok",
			}); usageErr != nil {
				logi.Ctx(ctx).Warn("agent_call: failed to record usage",
					"agent_id", n.agentID, "error", usageErr)
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

		// If the LLM is done (no tool calls), return the final answer.
		if resp.Finished || len(resp.ToolCalls) == 0 {
			return workflow.NewResult(map[string]any{
				"response": resp.Content,
			}), nil
		}

		// Execute tool calls and build tool results.
		var toolResults []service.ContentBlock
		for _, tc := range resp.ToolCalls {
			logi.Ctx(ctx).Debug("agent_call: tool call",
				"tool", tc.Name, "iteration", iteration)

			var result string
			var callErr error

			// Resolve handler: skill tools (lazily loaded) take priority over
			// statically registered handlers. Both are independent maps.
			skillHi, isSkillTool := skillRuntime.HandlerFor(tc.Name)

			if tc.Name == workflow.LoadSkillToolName {
				// Activate a skill. Its SystemPrompt is embedded inline in
				// the tool_result text so the LLM picks it up on the next
				// turn — preserving correct assistant→user tool-call
				// sequencing for Anthropic/OpenAI providers. Subsequent
				// iterations expose the skill's tools via ActiveSkillTools.
				result, callErr = skillRuntime.HandleLoadSkill(tc.Arguments)
			} else if mcpToolNames[tc.Name] {
				// Dispatch to MCP client.
				result, callErr = callMCPTool(ctx, mcpClients, tc.Name, tc.Arguments)
			} else if hi, ok := toolHandlers[tc.Name]; ok || isSkillTool {
				if isSkillTool {
					hi = toolHandlerInfo{
						handler:     skillHi.Handler,
						handlerType: skillHi.HandlerType,
						skillID:     skillHi.SkillID,
					}
				}
				// Build per-tool VarLookup / VarLister that maps provider-scoped
				// keys (e.g. "youtube_refresh_token") to the agent's bound
				// Connection, falling back to the registry's global lookup.
				// Per-skill overrides on the owning SkillRef take priority.
				var (
					toolVarLookup = reg.VarLookup
					toolVarLister = reg.VarLister
				)
				if reg.ConnectionLookup != nil && preset != nil {
					var perSkill map[string]string
					if hi.skillID != "" {
						perSkill = skillRuntime.SkillConnOverrides(hi.skillID)
					}
					bindings := workflow.ResolveAgentConnectionBindings(
						ctx, reg.ConnectionLookup,
						preset.Config.Connections, perSkill,
					)
					if len(bindings) > 0 {
						toolVarLookup = workflow.WrapVarLookupWithConnections(reg.VarLookup, bindings)
						toolVarLister = workflow.WrapVarListerWithConnections(reg.VarLister, bindings)
					}
				}

				if hi.handlerType == "bash" {
					// Execute bash handler.
					result, callErr = workflow.ExecuteBashHandler(ctx, hi.handler, tc.Arguments, toolVarLister, toolTimeout)
				} else if hi.handlerType == "builtin" {
					// Execute builtin tool via dispatcher.
					result, callErr = reg.BuiltinToolDispatcher(ctx, tc.Name, tc.Arguments)
				} else if hi.handlerType == "workflow" {
					// Execute an agent-attached workflow via the engine-owned executor.
					if reg.WorkflowExecutor == nil || reg.WorkflowLookup == nil {
						callErr = fmt.Errorf("workflow executor not configured")
					} else {
						wf, lookupErr := reg.WorkflowLookup(ctx, hi.handler)
						if lookupErr != nil || wf == nil {
							callErr = fmt.Errorf("workflow %s not found: %w", hi.handler, lookupErr)
						} else {
							result, callErr = reg.WorkflowExecutor(ctx, wf, tc.Arguments)
						}
					}
				} else if hi.handlerType == "agent" {
					// Execute sub-agent.
					// hi.handler contains the agent ID.
					// tc.Arguments["task"] contains the prompt.
					task, _ := tc.Arguments["task"].(string)
					subAgentID := hi.handler

					// Create a temporary node configuration for the sub-agent.
					subNodeConfig := service.WorkflowNode{
						Data: map[string]any{
							"agent_id": subAgentID,
						},
					}

					subNode, err := newAgentCallNode(subNodeConfig)
					if err != nil {
						callErr = fmt.Errorf("failed to init sub-agent %s: %w", subAgentID, err)
					} else {
						// Run the sub-agent.
						subInputs := map[string]any{
							"prompt": task,
						}
						// Pass through mcp/skills/memory if we wanted to inherit context,
						// but for now let's keep it isolated to the task.

						subResult, err := subNode.Run(ctx, reg, subInputs)
						if err != nil {
							callErr = fmt.Errorf("sub-agent execution failed: %w", err)
						} else {
							// Extract response.
							if resp, ok := subResult.Data()["response"].(string); ok {
								result = resp
							} else {
								result = fmt.Sprintf("%v", subResult.Data())
							}
						}
					}
				} else {
					// Execute JS handler via Goja (default). Use the per-tool
					// VarLookup so that provider-scoped keys resolve through
					// the agent's connection bindings before global variables.
					result, callErr = workflow.ExecuteJSHandlerWithOptions(hi.handler, tc.Arguments, workflow.JSHandlerOptions{
						VarLookup:      toolVarLookup,
						UserPrefLookup: reg.UserPrefLookup,
					})
				}
			} else {
				// No handler found — return error to the LLM.
				callErr = fmt.Errorf("no handler for tool %q", tc.Name)
			}

			if callErr != nil {
				result = fmt.Sprintf("Error: %v", callErr)
			}

			// Apply governor truncation before the result enters the
			// LLM message history. The runID for dump file naming is
			// best-effort: agent_call doesn't always have a stable
			// task id, so we use the agent id as a coarse namespace.
			if reg.LoopGov != nil {
				runID := n.agentID
				if runID == "" {
					runID = "agent_call"
				}
				result, _ = reg.LoopGov.TruncateToolResult(runID, tc.Name, result)
			}

			// Record audit entry for each tool call. Capture the input
			// arguments and the post-truncation output so the workflow
			// agent_call node leaves the same kind of trace as the
			// org-delegation / chat-session loops.
			if n.agentID != "" && reg.RecordAudit != nil {
				auditDetails := map[string]any{
					"tool_name": tc.Name,
					"iteration": iteration,
					"has_error": callErr != nil,
					"input":     tc.Arguments,
					"output":    service.TruncateForAudit(result),
				}
				if auditErr := reg.RecordAudit(ctx, service.AuditEntry{
					ActorType:    "agent",
					ActorID:      n.agentID,
					Action:       "tool_call",
					ResourceType: "tool",
					ResourceID:   tc.ID,
					Details:      auditDetails,
				}); auditErr != nil {
					logi.Ctx(ctx).Warn("agent_call: failed to record audit",
						"agent_id", n.agentID, "error", auditErr)
				}
			}

			toolResults = append(toolResults, service.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tc.ID,
				Content:   result,
			})
		}

		messages = append(messages, service.Message{
			Role:    "user",
			Content: toolResults,
		})
	}

	// Max iterations reached — return whatever content we have.
	// Extract the last assistant text.
	lastContent := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			if s, ok := messages[i].Content.(string); ok {
				lastContent = s
				break
			}
			if blocks, ok := messages[i].Content.([]service.ContentBlock); ok {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						lastContent = b.Text
						break
					}
				}
				break
			}
		}
	}

	return workflow.NewResult(map[string]any{
		"response": lastContent,
		"text":     lastContent,
	}), nil
}

// ─── Tool Execution Helpers ───

// callMCPTool dispatches a tool call to the appropriate MCP client.
// It tries each client in order; the first one that has the tool wins.
func callMCPTool(ctx context.Context, clients []service.MCPClient, name string, args map[string]any) (string, error) {
	for _, c := range clients {
		result, err := c.CallTool(ctx, name, args)
		if err != nil {
			// If one server fails, try the next.
			continue
		}
		return result, nil
	}
	return "", fmt.Errorf("MCP tool %q: no server returned a result", name)
}
