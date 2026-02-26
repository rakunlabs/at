package nodes

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// agentCallNode runs an agentic loop: it sends a prompt to an LLM provider,
// collects tool calls, executes them (via MCP, skill JS handlers, or inline JS
// handlers), feeds results back, and repeats until the LLM produces a final
// answer or the iteration limit is reached.
//
// Config (node.Data):
//
//	"provider":       string   — provider key for registry lookup (required)
//	"model":          string   — model override (optional, empty = provider default)
//	"system_prompt":  string   — system message prepended to conversation (optional)
//	"max_iterations": float64  — max tool-call rounds (default 10, 0 = unlimited)
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
//	"text"     — alias for response (convenience port)
type agentCallNode struct {
	providerKey   string
	model         string
	systemPrompt  string
	maxIterations int
	mcpURLs       []string
	skillNames    []string
	inlineTools   []service.Tool
}

func init() {
	workflow.RegisterNodeType("agent_call", newAgentCallNode)
}

func newAgentCallNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	systemPrompt, _ := node.Data["system_prompt"].(string)

	maxIterations := 10
	if v, ok := node.Data["max_iterations"].(float64); ok {
		maxIterations = int(v)
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
		providerKey:   providerKey,
		model:         model,
		systemPrompt:  systemPrompt,
		maxIterations: maxIterations,
		mcpURLs:       mcpURLs,
		skillNames:    skillNames,
		inlineTools:   inlineTools,
	}, nil
}

func (n *agentCallNode) Type() string { return "agent_call" }

func (n *agentCallNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("agent_call: 'provider' is required")
	}

	if reg.ProviderLookup == nil {
		return fmt.Errorf("agent_call: no provider lookup configured")
	}

	// Verify the provider exists.
	_, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return fmt.Errorf("agent_call: provider %q: %w", n.providerKey, err)
	}

	return nil
}

func (n *agentCallNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, defaultModel, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("agent_call: provider %q: %w", n.providerKey, err)
	}

	model := n.model
	if model == "" {
		model = defaultModel
	}

	// ─── Collect Tools ───

	// toolHandlerInfo holds handler body and type for skill/inline tools.
	type toolHandlerInfo struct {
		handler     string
		handlerType string // "js" (default) or "bash"
	}

	// toolHandlers maps tool name → handler info for skill/inline tools.
	toolHandlers := make(map[string]toolHandlerInfo)

	// mcpToolNames tracks which tool names come from MCP (dispatched via MCP client).
	mcpToolNames := make(map[string]bool)

	// mcpClients holds initialized MCP clients (closed at the end).
	var mcpClients []*service.HTTPMCPClient
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()

	var allTools []service.Tool

	// ─── Merge MCP URLs from static config + edge inputs ───

	mcpURLs := append([]string{}, n.mcpURLs...)
	if edgeMCP, ok := inputs["mcp"]; ok {
		switch v := edgeMCP.(type) {
		case []string:
			mcpURLs = append(mcpURLs, v...)
		case []any:
			for _, u := range v {
				if s, ok := u.(string); ok && s != "" {
					mcpURLs = append(mcpURLs, s)
				}
			}
		}
	}

	// 1. MCP tools
	for _, url := range mcpURLs {
		client, err := service.NewHTTPMCPClient(ctx, url)
		if err != nil {
			slog.Warn("agent_call: failed to connect to MCP server, skipping",
				"url", url, "error", err)
			continue
		}
		mcpClients = append(mcpClients, client)

		tools, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("agent_call: failed to list MCP tools, skipping",
				"url", url, "error", err)
			continue
		}

		for _, t := range tools {
			mcpToolNames[t.Name] = true
			allTools = append(allTools, t)
		}
	}

	// 2. Skill tools (also collect system prompt fragments)
	// Merge skill names from static config + edge inputs.
	skillNames := append([]string{}, n.skillNames...)
	if edgeSkills, ok := inputs["skills"]; ok {
		switch v := edgeSkills.(type) {
		case []string:
			skillNames = append(skillNames, v...)
		case []any:
			for _, s := range v {
				if name, ok := s.(string); ok && name != "" {
					skillNames = append(skillNames, name)
				}
			}
		}
	}

	var skillPromptFragments []string
	for _, nameOrID := range skillNames {
		if reg.SkillLookup == nil {
			slog.Warn("agent_call: skill lookup not configured, skipping skill", "skill", nameOrID)
			continue
		}
		skill, err := reg.SkillLookup(nameOrID)
		if err != nil {
			slog.Warn("agent_call: failed to look up skill, skipping",
				"skill", nameOrID, "error", err)
			continue
		}
		if skill == nil {
			slog.Warn("agent_call: skill not found, skipping", "skill", nameOrID)
			continue
		}

		if skill.SystemPrompt != "" {
			skillPromptFragments = append(skillPromptFragments, skill.SystemPrompt)
		}

		for _, t := range skill.Tools {
			if t.Handler != "" {
				toolHandlers[t.Name] = toolHandlerInfo{handler: t.Handler, handlerType: t.HandlerType}
			}
			allTools = append(allTools, t)
		}
	}

	// 3. Inline tools
	for _, t := range n.inlineTools {
		if t.Handler != "" {
			toolHandlers[t.Name] = toolHandlerInfo{handler: t.Handler, handlerType: t.HandlerType}
		}
		allTools = append(allTools, t)
	}

	// ─── Build System Prompt ───

	systemPrompt := n.systemPrompt
	for _, fragment := range skillPromptFragments {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += fragment
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

	// Strip handlers from tools sent to the LLM (Handler field is omitted by
	// json tag when empty, but let's be explicit).
	llmTools := make([]service.Tool, len(allTools))
	for i, t := range allTools {
		llmTools[i] = service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	for iteration := 0; n.maxIterations == 0 || iteration < n.maxIterations; iteration++ {
		// Check for cancellation between iterations.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("agent_call: cancelled: %w", err)
		}

		resp, err := provider.Chat(ctx, model, messages, llmTools)
		if err != nil {
			return nil, fmt.Errorf("agent_call: chat failed (iteration %d): %w", iteration, err)
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
			assistantContent = append(assistantContent, service.ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Arguments,
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
				"text":     resp.Content,
			}), nil
		}

		// Execute tool calls and build tool results.
		var toolResults []service.ContentBlock
		for _, tc := range resp.ToolCalls {
			slog.Debug("agent_call: tool call",
				"tool", tc.Name, "iteration", iteration)

			var result string
			var callErr error

			if mcpToolNames[tc.Name] {
				// Dispatch to MCP client.
				result, callErr = callMCPTool(ctx, mcpClients, tc.Name, tc.Arguments)
			} else if hi, ok := toolHandlers[tc.Name]; ok {
				if hi.handlerType == "bash" {
					// Execute bash handler.
					result, callErr = workflow.ExecuteBashHandler(ctx, hi.handler, tc.Arguments, reg.VarLister)
				} else {
					// Execute JS handler via Goja (default).
					result, callErr = workflow.ExecuteJSHandler(hi.handler, tc.Arguments, reg.VarLookup)
				}
			} else {
				// No handler found — return error to the LLM.
				callErr = fmt.Errorf("no handler for tool %q", tc.Name)
			}

			if callErr != nil {
				result = fmt.Sprintf("Error: %v", callErr)
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
func callMCPTool(ctx context.Context, clients []*service.HTTPMCPClient, name string, args map[string]any) (string, error) {
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
