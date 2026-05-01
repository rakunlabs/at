package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/query"
)

// ─── Chat Session CRUD ───

// ListChatSessionsAPI handles GET /api/v1/chat/sessions.
//
// Optional query params (filter applied after store list):
//   - bot_config_id=<id>  — only sessions tied to that BotConfig
//   - platform=<key>      — only sessions for that platform (telegram, discord, …)
//
// These two filters live in the JSON config column, which the generic query
// builder cannot index on, so we extract them up-front and post-filter the
// store result. The session count per deployment is small (hundreds at most),
// so this is acceptable for now.
func (s *Server) ListChatSessionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	rawQuery := r.URL.RawQuery
	urlVals := r.URL.Query()
	botFilter := urlVals.Get("bot_config_id")
	platformFilter := urlVals.Get("platform")

	// Strip our custom params from the raw query so query.Parse doesn't try to
	// build a SQL expression for them (there is no such column).
	if botFilter != "" || platformFilter != "" {
		urlVals.Del("bot_config_id")
		urlVals.Del("platform")
		rawQuery = urlVals.Encode()
	}

	q, err := query.Parse(rawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.chatSessionStore.ListChatSessions(r.Context(), q)
	if err != nil {
		slog.Error("list chat sessions failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list chat sessions: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.ChatSession]{Data: []service.ChatSession{}}
	}

	if botFilter != "" || platformFilter != "" {
		filtered := records.Data[:0:0]
		for _, sess := range records.Data {
			if botFilter != "" && sess.Config.BotConfigID != botFilter {
				continue
			}
			if platformFilter != "" && sess.Config.Platform != platformFilter {
				continue
			}
			filtered = append(filtered, sess)
		}
		records.Data = filtered
		records.Meta.Total = uint64(len(filtered))
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetChatSessionAPI handles GET /api/v1/chat/sessions/{id}.
func (s *Server) GetChatSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	record, err := s.chatSessionStore.GetChatSession(r.Context(), id)
	if err != nil {
		slog.Error("get chat session failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get chat session: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("session %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateChatSessionAPI handles POST /api/v1/chat/sessions.
func (s *Server) CreateChatSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.ChatSession
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		httpResponse(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	// Validate agent exists.
	if s.agentStore != nil {
		agent, err := s.agentStore.GetAgent(r.Context(), req.AgentID)
		if err != nil {
			slog.Error("validate agent for session", "agent_id", req.AgentID, "error", err)
			httpResponse(w, fmt.Sprintf("failed to validate agent: %v", err), http.StatusInternalServerError)
			return
		}
		if agent == nil {
			httpResponse(w, fmt.Sprintf("agent %q not found", req.AgentID), http.StatusBadRequest)
			return
		}
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.chatSessionStore.CreateChatSession(r.Context(), req)
	if err != nil {
		slog.Error("create chat session failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create chat session: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateChatSessionAPI handles PUT /api/v1/chat/sessions/{id}.
func (s *Server) UpdateChatSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	var req service.ChatSession
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.chatSessionStore.UpdateChatSession(r.Context(), id, req)
	if err != nil {
		slog.Error("update chat session failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update chat session: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("session %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteChatSessionAPI handles DELETE /api/v1/chat/sessions/{id}.
func (s *Server) DeleteChatSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	if err := s.chatSessionStore.DeleteChatSession(r.Context(), id); err != nil {
		slog.Error("delete chat session failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete chat session: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// DeleteChatMessagesAPI handles DELETE /api/v1/chat/sessions/{id}/messages.
func (s *Server) DeleteChatMessagesAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	if err := s.chatSessionStore.DeleteChatMessages(r.Context(), id); err != nil {
		slog.Error("delete chat messages failed", "session_id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete messages: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "messages cleared", http.StatusOK)
}

// ListChatMessagesAPI handles GET /api/v1/chat/sessions/{id}/messages.
func (s *Server) ListChatMessagesAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	// Admin/UI surface: return all messages, no governor limit.
	messages, err := s.chatSessionStore.ListChatMessages(r.Context(), id, 0)
	if err != nil {
		slog.Error("list chat messages failed", "session_id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list messages: %v", err), http.StatusInternalServerError)
		return
	}

	if messages == nil {
		messages = []service.ChatMessage{}
	}

	httpResponseJSON(w, messages, http.StatusOK)
}

// ─── Agentic Send Handler ───

// AgenticEvent represents an event emitted by the agentic loop.
type AgenticEvent struct {
	Type      string `json:"type"`                // "content", "tool_call", "tool_result", "tool_confirm", "done", "error"
	Content   string `json:"content,omitempty"`   // for "content" events
	ToolName  string `json:"tool_name,omitempty"` // for "tool_call", "tool_result", and "tool_confirm"
	ToolID    string `json:"tool_id,omitempty"`   // for "tool_call", "tool_result", and "tool_confirm"
	Result    string `json:"result,omitempty"`    // for "tool_result"
	Error     string `json:"error,omitempty"`     // for "error"
	Arguments string `json:"arguments,omitempty"` // for "tool_confirm": JSON-encoded tool arguments
}

// confirmationResult carries the human's approval decision for a tool call.
type confirmationResult struct {
	approved bool
}

// confirmationTimeout is how long the agentic loop waits for human approval
// before auto-rejecting a tool call.
const confirmationTimeout = 5 * time.Minute

// RunAgenticLoop runs the agentic loop for a chat session, calling onEvent for each event.
// This is the core loop shared by the HTTP SSE handler and bot adapters.
func (s *Server) RunAgenticLoop(ctx context.Context, sessionID, content string, onEvent func(AgenticEvent)) error {
	// 1. Load session.
	session, err := s.chatSessionStore.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session == nil {
		return fmt.Errorf("session %q not found", sessionID)
	}

	// 2. Load agent config.
	if s.agentStore == nil {
		return fmt.Errorf("agent store not configured")
	}

	agent, err := s.agentStore.GetAgent(ctx, session.AgentID)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("agent %q not found", session.AgentID)
	}

	// 3. Resolve provider.
	providerKey := agent.Config.Provider
	if providerKey == "" {
		return fmt.Errorf("agent has no provider configured")
	}

	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		return fmt.Errorf("provider %q not found", providerKey)
	}

	model := agent.Config.Model
	if model == "" {
		model = info.defaultModel
	}

	// 4. Load message history.
	// The chat-session loop applies a recency window so long-running
	// sessions don't replay an unbounded history on every user turn.
	// Older context, when needed, is reconstructed by the loop
	// governor's rolling-summary mechanism.
	historyLimit := 0
	if s.loopGov != nil {
		historyLimit = s.loopGov.ChatHistoryLimit()
	}
	dbMessages, err := s.chatSessionStore.ListChatMessages(ctx, sessionID, historyLimit)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}

	// 4b. Sanitize message history — remove orphaned tool results that don't follow a tool call.
	// This prevents "tool call result does not follow tool call" errors from LLM providers.
	dbMessages = sanitizeMessageHistory(dbMessages)

	// 5. Persist user message.
	userMsg := service.ChatMessage{
		SessionID: sessionID,
		Role:      "user",
		Data: service.ChatMessageData{
			Content: content,
		},
	}
	if _, err := s.chatSessionStore.CreateChatMessage(ctx, userMsg); err != nil {
		return fmt.Errorf("persist user message: %w", err)
	}

	// 6. Collect tools from agent config.
	//
	// toolHandlerInfo tracks non-skill tools (inline/builtin/delegate).
	// Skill tool handlers live inside the SkillRuntime (built below) and
	// only become discoverable after the LLM activates each skill via the
	// `load_skill` meta-tool — progressive disclosure.
	type toolHandlerInfo struct {
		handler     string
		handlerType string
		skillID     string // empty for non-skill tools
	}
	toolHandlers := make(map[string]toolHandlerInfo)
	mcpToolNames := make(map[string]bool)
	var mcpClients []service.MCPClient
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()

	var allTools []service.Tool
	mcpSetToolMap := make(map[string]string) // tool name -> MCP set name (for direct dispatch)

	// Collect MCP URLs from legacy mcp_urls.
	var mcpURLs []string
	mcpURLs = append(mcpURLs, agent.Config.MCPs...)

	// Resolve MCP Sets (internal MCPs) to URLs and direct clients.
	var mcpSetUpstreams []service.MCPUpstream
	if s.mcpSetStore != nil {
		for _, setName := range agent.Config.MCPSets {
			set, err := s.mcpSetStore.GetMCPSetByName(ctx, setName)
			if err != nil {
				slog.Warn("agentic loop: failed to get MCP set", "set", setName, "error", err)
				continue
			}
			if set == nil {
				slog.Warn("agentic loop: MCP set not found", "set", setName)
				continue
			}
			// Resolve MCP Server references to gateway URLs.
			for _, serverName := range set.Servers {
				gatewayURL := fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp/%s", s.config.Port, s.config.BasePath, serverName)
				mcpURLs = append(mcpURLs, gatewayURL)
			}
			// Add custom MCP endpoint URLs.
			mcpURLs = append(mcpURLs, set.URLs...)

			// Collect direct upstreams for stdio/HTTP resolution.
			mcpSetUpstreams = append(mcpSetUpstreams, set.Config.MCPUpstreams...)

			// If the MCP set has server-side tools (RAG/HTTP/Skills/Builtins),
			// resolve them directly — no HTTP loopback.
			if len(set.Config.EnabledRAGTools) > 0 || len(set.Config.HTTPTools) > 0 ||
				len(set.Config.EnabledSkills) > 0 || len(set.Config.EnabledBuiltinTools) > 0 {
				setTools, err := s.listMCPSetTools(setName)
				if err != nil {
					slog.Warn("agentic loop: failed to list MCP set tools", "set", setName, "error", err)
				} else {
					for _, t := range setTools {
						mcpToolNames[t.Name] = true
						allTools = append(allTools, t)
					}
					// Register set name for tool dispatch later.
					for _, t := range setTools {
						mcpSetToolMap[t.Name] = setName
					}
				}
			}
		}
	}

	// MCP tools — HTTP URLs.
	for _, url := range mcpURLs {
		client, err := service.NewHTTPMCPClient(ctx, url)
		if err != nil {
			slog.Warn("agentic loop: failed to connect to MCP server, skipping", "url", url, "error", err)
			continue
		}
		mcpClients = append(mcpClients, client)

		tools, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("agentic loop: failed to list MCP tools, skipping", "url", url, "error", err)
			continue
		}
		for _, t := range tools {
			mcpToolNames[t.Name] = true
			allTools = append(allTools, t)
		}
	}

	// MCP tools — direct upstreams from MCP sets (HTTP or stdio).
	for _, upstream := range mcpSetUpstreams {
		client, err := s.newMCPClient(ctx, upstream)
		if err != nil {
			slog.Warn("agentic loop: failed to connect to MCP upstream, skipping", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		mcpClients = append(mcpClients, client)

		tools, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("agentic loop: failed to list MCP upstream tools, skipping", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		for _, t := range tools {
			mcpToolNames[t.Name] = true
			allTools = append(allTools, t)
		}
	}

	// Skills — LAZY (progressive disclosure).
	//
	// SkillRuntime resolves all attached SkillRefs into an in-memory catalog
	// once. The catalog is rendered into the system prompt, and a single
	// `load_skill` meta-tool is exposed. The skill's SystemPrompt + Tools
	// are NOT injected until the LLM activates the skill.
	var skillLookup workflow.SkillLookup
	if s.skillStore != nil {
		skillLookup = func(nameOrID string) (*service.Skill, error) {
			sk, err := s.skillStore.GetSkill(ctx, nameOrID)
			if err != nil {
				return nil, err
			}
			if sk != nil {
				return sk, nil
			}
			return s.skillStore.GetSkillByName(ctx, nameOrID)
		}
	}
	skillRuntime, err := workflow.NewSkillRuntime(ctx, skillLookup, agent.Config.Skills, nil,
		func(name string, lookupErr error) {
			slog.Warn("agentic loop: skill lookup failed", "skill", name, "error", lookupErr)
		})
	if err != nil {
		return fmt.Errorf("agentic loop: skill runtime: %w", err)
	}

	// Builtin tools (from agent config).
	for _, toolName := range agent.Config.BuiltinTools {
		if !isKnownBuiltinTool(toolName) {
			slog.Warn("agentic loop: unknown builtin tool in agent config", "tool", toolName, "agent", agent.ID)
			continue
		}
		for _, bt := range builtinTools {
			if bt.Name == toolName {
				allTools = append(allTools, service.Tool{
					Name:        bt.Name,
					Description: bt.Description,
					InputSchema: bt.InputSchema,
				})
				toolHandlers[bt.Name] = toolHandlerInfo{
					handler:     bt.Name,
					handlerType: "builtin",
				}
				break
			}
		}
	}

	// 6b. Task-linked session: inject task context and delegation tools.
	var taskLinked *service.Task
	if session.TaskID != "" && s.taskStore != nil {
		taskLinked, _ = s.taskStore.GetTask(ctx, session.TaskID)
	}

	// If this session is linked to a task in an organization, load delegation tools.
	if session.OrganizationID != "" && taskLinked != nil && s.organizationStore != nil && s.orgAgentStore != nil {
		org, orgErr := s.organizationStore.GetOrganization(ctx, session.OrganizationID)
		if orgErr == nil && org != nil {
			reports, repErr := s.getDirectReports(ctx, org.ID, session.AgentID)
			if repErr == nil {
				for _, oa := range reports {
					reportAgent, agentErr := s.agentStore.GetAgent(ctx, oa.AgentID)
					if agentErr != nil || reportAgent == nil {
						continue
					}
					safeName := strings.Map(func(r rune) rune {
						if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
							return r
						}
						return '_'
					}, reportAgent.Name)
					toolName := "delegate_to_" + strings.ToLower(safeName)
					toolDesc := fmt.Sprintf("Delegate a task to %s. %s", reportAgent.Name, reportAgent.Config.Description)
					allTools = append(allTools, service.Tool{
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
					})
					// Store the delegation target for dispatch later.
					toolHandlers[toolName] = toolHandlerInfo{
						handler:     oa.AgentID,
						handlerType: "delegate",
					}
				}
			}
		}
	}

	// 7. Build system prompt.
	systemPrompt := agent.Config.SystemPrompt
	if catalog := skillRuntime.CatalogSystemPrompt(); catalog != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += catalog
	}

	// Inject task context into system prompt if this session is task-linked.
	if taskLinked != nil {
		var taskContext strings.Builder
		taskContext.WriteString("\n\n## Current Task\n")
		if taskLinked.Identifier != "" {
			taskContext.WriteString(fmt.Sprintf("**ID**: %s\n", taskLinked.Identifier))
		}
		taskContext.WriteString(fmt.Sprintf("**Title**: %s\n", taskLinked.Title))
		taskContext.WriteString(fmt.Sprintf("**Status**: %s\n", taskLinked.Status))
		if taskLinked.Description != "" {
			taskContext.WriteString(fmt.Sprintf("\n**Description**:\n%s\n", taskLinked.Description))
		}
		if taskLinked.Result != "" {
			resultPreview := taskLinked.Result
			if len(resultPreview) > 2000 {
				resultPreview = resultPreview[:2000] + "\n...(truncated)"
			}
			taskContext.WriteString(fmt.Sprintf("\n**Previous Result**:\n%s\n", resultPreview))
		}
		taskContext.WriteString("\nYou are continuing work on this task interactively. Complete the remaining work and report your results.")
		systemPrompt += taskContext.String()
	}

	// NOTE: User preferences are injected into the system prompt after
	// sessionUserID is derived (below, before the agentic loop starts).

	// 8. Build messages for LLM (system message added after user pref injection below).
	var llmMessages []service.Message

	// Convert DB messages to LLM messages.
	// Consecutive role="tool" messages are grouped into a single role="user"
	// message with tool_result content blocks (Anthropic format).
	var pendingToolResults []service.ContentBlock
	flushToolResults := func() {
		if len(pendingToolResults) > 0 {
			llmMessages = append(llmMessages, service.Message{
				Role:    "user",
				Content: pendingToolResults,
			})
			pendingToolResults = nil
		}
	}
	for _, m := range dbMessages {
		if m.Role == "tool" {
			content, _ := m.Data.Content.(string)
			pendingToolResults = append(pendingToolResults, service.ContentBlock{
				Type:      "tool_result",
				ToolUseID: m.Data.ToolCallID,
				Content:   content,
			})
			continue
		}
		flushToolResults()

		msg := service.Message{
			Role:    m.Role,
			Content: m.Data.Content,
		}

		// Reconstruct tool_use content blocks for assistant messages with tool_calls.
		if m.Role == "assistant" && m.Data.ToolCalls != nil {
			var blocks []service.ContentBlock
			if contentStr, ok := m.Data.Content.(string); ok && contentStr != "" {
				blocks = append(blocks, service.ContentBlock{Type: "text", Text: contentStr})
			}
			if tcRaw, err := json.Marshal(m.Data.ToolCalls); err == nil {
				var toolCalls []service.ToolCall
				if json.Unmarshal(tcRaw, &toolCalls) == nil {
					for _, tc := range toolCalls {
						input := tc.Arguments
						if input == nil {
							input = map[string]any{}
						}
						blocks = append(blocks, service.ContentBlock{
							Type:  "tool_use",
							ID:    tc.ID,
							Name:  tc.Name,
							Input: input,
						})
					}
				}
			}
			if len(blocks) > 0 {
				msg.Content = blocks
			}
		}

		llmMessages = append(llmMessages, msg)
	}
	flushToolResults()

	// Add current user message.
	llmMessages = append(llmMessages, service.Message{
		Role:    "user",
		Content: content,
	})

	// Strip handlers from tools sent to the LLM. Skill tools are appended
	// per-iteration via skillRuntime.ActiveSkillTools() once the LLM
	// activates them with load_skill (progressive disclosure).
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

	// Resolve max iterations and tool timeout.
	//
	// Iteration counter starts fresh at 0 for every RunAgenticLoop call.
	// For task-linked chat sessions, task.MaxIterations (when set) takes
	// precedence over agent.Config.MaxIterations so a complex task can have
	// its own budget without affecting the agent's other interactions.
	taskMax := 0
	if taskLinked != nil {
		taskMax = taskLinked.MaxIterations
	}
	maxIterations := s.loopGov.ClampIterations(agent.Config.MaxIterations, taskMax)

	toolTimeout := time.Duration(agent.Config.ToolTimeout) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 60 * time.Second
	}

	// Derive a user identity for per-user variable scoping (e.g. OAuth tokens).
	// For bot sessions this is "platform::platform_user_id"; for web sessions
	// it falls back to the session creator.
	sessionUserID := ""
	if session.Config.Platform != "" && session.Config.PlatformUserID != "" {
		sessionUserID = session.Config.Platform + "::" + session.Config.PlatformUserID
	} else if session.CreatedBy != "" {
		sessionUserID = session.CreatedBy
	}

	// Store session user ID and agent ID in context for builtin tool executors.
	ctx = contextWithSessionUserID(ctx, sessionUserID)
	ctx = contextWithAgentID(ctx, agent.ID)
	// If the session is linked to a task, expose its ID so that builtin
	// tools like task_create can auto-inherit parent_id / organization_id
	// from the active task instead of producing orphaned subtasks.
	if taskLinked != nil {
		ctx = contextWithTaskID(ctx, taskLinked.ID)
	}

	// Build variable lookup/lister for skill tools.
	// The lookup checks per-user preferences first, then per-user variables, then global.
	varLookup := s.userScopedVarLookup(ctx, sessionUserID)
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

	// Build user preference lookup for JS skill handlers.
	var userPrefLookup workflow.UserPrefLookup
	if s.userPrefStore != nil && sessionUserID != "" {
		userPrefLookup = func(key string) (string, error) {
			pref, err := s.userPrefStore.GetUserPreference(ctx, sessionUserID, key)
			if err != nil {
				return "", err
			}
			if pref == nil {
				return "", fmt.Errorf("user preference %q not found", key)
			}
			return string(pref.Value), nil
		}
	}

	// 9. Inject user preferences into system prompt (non-secret only).
	if s.userPrefStore != nil && sessionUserID != "" {
		prefs, err := s.userPrefStore.ListUserPreferences(ctx, sessionUserID)
		if err == nil && len(prefs) > 0 {
			var prefLines []string
			for _, p := range prefs {
				if !p.Secret {
					prefLines = append(prefLines, fmt.Sprintf("- %s: %s", p.Key, string(p.Value)))
				}
			}
			if len(prefLines) > 0 {
				if systemPrompt != "" {
					systemPrompt += "\n\n"
				}
				systemPrompt += "User preferences:\n" + strings.Join(prefLines, "\n")
			}
		}
	}

	// Prepend system prompt as the first message.
	if systemPrompt != "" {
		llmMessages = append([]service.Message{{
			Role:    "system",
			Content: systemPrompt,
		}}, llmMessages...)
	}

	// 10. Agentic loop.
	for iteration := 0; iteration < maxIterations; iteration++ {
		// Rebuild LLM tool list each iteration: base tools + tools from
		// any skills the LLM has activated so far.
		llmTools := append([]service.Tool{}, baseLLMTools...)
		llmTools = append(llmTools, skillRuntime.ActiveSkillTools()...)

		if err := ctx.Err(); err != nil {
			onEvent(AgenticEvent{Type: "error", Error: "request cancelled"})
			return nil
		}

		// Check agent budget before making an LLM call.
		if s.agentBudgetStore != nil {
			checkBudget := s.checkBudgetFunc()
			if checkBudget != nil {
				if budgetErr := checkBudget(ctx, session.AgentID); budgetErr != nil {
					onEvent(AgenticEvent{Type: "error", Error: fmt.Sprintf("Budget exceeded: %v", budgetErr)})
					return nil
				}
			}
		}

		// Apply the loop governor: window the message history (with a
		// rolling summary fallback) and pass an explicit MaxTokens cap
		// to bound output size.
		windowed, _ := s.loopGov.Limit(ctx, session.AgentID,
			func() string {
				if taskLinked != nil {
					return taskLinked.ID
				}
				return sessionID
			}(), llmMessages)
		chatOpts := s.loopGov.ChatOptions()
		callStart := time.Now()
		resp, err := info.provider.Chat(ctx, model, windowed, llmTools, chatOpts)
		latencyMs := time.Since(callStart).Milliseconds()
		if err != nil {
			// Recover from corrupted tool call history — sanitize and retry once.
			errStr := err.Error()
			if strings.Contains(errStr, "tool call result does not follow") ||
				strings.Contains(errStr, "tool_use content block") ||
				(strings.Contains(errStr, "tool_result") && strings.Contains(errStr, "not follow")) {
				slog.Warn("agentic loop: tool call history error, sanitizing and retrying",
					"iteration", iteration, "error", err)
				llmMessages = sanitizeLLMMessages(llmMessages)
				windowed, _ = s.loopGov.Limit(ctx, session.AgentID,
					func() string {
						if taskLinked != nil {
							return taskLinked.ID
						}
						return sessionID
					}(), llmMessages)
				callStart = time.Now()
				resp, err = info.provider.Chat(ctx, model, windowed, llmTools, chatOpts)
				latencyMs = time.Since(callStart).Milliseconds()
			}
			if err != nil {
				// Record failed call for usage dashboard.
				//
				// Use the provider KEY (the user-facing identifier configured
				// on the agent, e.g. "openai-prod" / "claude-personal") rather
				// than info.providerType (the generic API family like "openai"
				// / "anthropic"). The dashboard groups by user-facing provider.
				if recordUsage := s.recordUsageFunc(); recordUsage != nil {
					_ = recordUsage(ctx, workflow.UsageEvent{
						AgentID:      session.AgentID,
						Model:        model,
						Provider:     providerKey,
						LatencyMs:    latencyMs,
						Status:       "error",
						ErrorCode:    classifyHTTPError(err),
						ErrorMessage: err.Error(),
					})
				}
				slog.Error("agentic loop: chat failed", "iteration", iteration, "error", err)
				onEvent(AgenticEvent{Type: "error", Error: fmt.Sprintf("LLM error: %v", err)})
				return nil
			}
		}

		// Record token usage for cost tracking.
		// Same rule as the error path above: store the provider KEY, not the
		// generic API family.
		if resp.Usage.TotalTokens > 0 {
			recordUsage := s.recordUsageFunc()
			if recordUsage != nil {
				if usageErr := recordUsage(ctx, workflow.UsageEvent{
					AgentID:   session.AgentID,
					Model:     model,
					Provider:  providerKey,
					Usage:     resp.Usage,
					LatencyMs: latencyMs,
					Status:    "ok",
				}); usageErr != nil {
					slog.Warn("agentic loop: failed to record usage",
						"agent_id", session.AgentID, "error", usageErr)
				}
			}
		}

		// Emit text content.
		if resp.Content != "" {
			onEvent(AgenticEvent{Type: "content", Content: resp.Content})
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
		llmMessages = append(llmMessages, service.Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		// If done (no tool calls), persist and finish.
		if resp.Finished || len(resp.ToolCalls) == 0 {
			s.persistAssistantMessage(ctx, sessionID, resp.Content, nil)

			// Auto-sync: if this session is linked to a task, update the task result.
			if taskLinked != nil && resp.Content != "" && s.taskStore != nil {
				newStatus := service.TaskStatusCompleted
				if taskLinked.ParentID != "" {
					// Sub-tasks complete as "done" to let the parent know.
					newStatus = service.TaskStatusDone
				}
				if syncErr := s.taskStore.UpdateTaskStatus(ctx, taskLinked.ID, newStatus, resp.Content); syncErr != nil {
					slog.Warn("agentic loop: failed to sync task result",
						"task_id", taskLinked.ID, "error", syncErr)
				} else {
					slog.Info("agentic loop: task result synced from chat",
						"task_id", taskLinked.ID, "status", newStatus)
				}
			}

			onEvent(AgenticEvent{Type: "done"})
			return nil
		}

		// Persist assistant message with tool calls.
		s.persistAssistantMessage(ctx, sessionID, resp.Content, resp.ToolCalls)

		// Execute tool calls.
		var toolResults []service.ContentBlock
		for _, tc := range resp.ToolCalls {
			// Check if this tool requires human confirmation.
			if slices.Contains(agent.Config.ConfirmationRequiredTools, tc.Name) {
				// Serialize arguments for the UI.
				argsJSON, _ := json.Marshal(tc.Arguments)

				// Emit confirmation request to the UI.
				onEvent(AgenticEvent{
					Type:      "tool_confirm",
					ToolName:  tc.Name,
					ToolID:    tc.ID,
					Arguments: string(argsJSON),
				})

				// Wait for human approval.
				confirmKey := sessionID + ":" + tc.ID
				ch := make(chan confirmationResult, 1)
				s.pendingConfirmations.Store(confirmKey, ch)

				var approved bool
				select {
				case res := <-ch:
					approved = res.approved
				case <-time.After(confirmationTimeout):
					slog.Warn("agentic loop: tool confirmation timed out", "tool", tc.Name, "tool_id", tc.ID)
				case <-ctx.Done():
					s.pendingConfirmations.Delete(confirmKey)
					onEvent(AgenticEvent{Type: "error", Error: "request cancelled"})
					return nil
				}
				s.pendingConfirmations.Delete(confirmKey)

				if !approved {
					slog.Info("agentic loop: tool call rejected by user", "tool", tc.Name, "tool_id", tc.ID)
					result := "Error: User rejected this tool call. Please try a different approach or ask the user for guidance."

					onEvent(AgenticEvent{Type: "tool_call", ToolName: tc.Name, ToolID: tc.ID})
					onEvent(AgenticEvent{Type: "tool_result", ToolName: tc.Name, ToolID: tc.ID, Result: result})

					toolResults = append(toolResults, service.ContentBlock{
						Type:      "tool_result",
						ToolUseID: tc.ID,
						Content:   result,
					})
					continue
				}
			}

			onEvent(AgenticEvent{Type: "tool_call", ToolName: tc.Name, ToolID: tc.ID})

			var result string
			var callErr error

			// Resolve handler: skill tools (lazily loaded) take priority
			// over statically registered handlers (builtin/delegate/etc).
			skillHi, isSkillTool := skillRuntime.HandlerFor(tc.Name)

			if tc.Name == workflow.LoadSkillToolName {
				// Activate a skill. Its SystemPrompt is embedded inline in
				// the tool_result text so the LLM picks it up on the next
				// turn — preserving correct assistant→user tool-call
				// sequencing for Anthropic/OpenAI providers.
				result, callErr = skillRuntime.HandleLoadSkill(tc.Arguments)
			} else if setName, ok := mcpSetToolMap[tc.Name]; ok {
				// Direct MCPSet tool — no HTTP round-trip.
				result, callErr = s.callMCPSetTool(ctx, setName, tc.Name, tc.Arguments)
			} else if mcpToolNames[tc.Name] {
				result, callErr = callMCPToolFromClients(ctx, mcpClients, tc.Name, tc.Arguments)
			} else if hi, ok := toolHandlers[tc.Name]; ok || isSkillTool {
				if isSkillTool {
					hi = toolHandlerInfo{
						handler:     skillHi.Handler,
						handlerType: skillHi.HandlerType,
						skillID:     skillHi.SkillID,
					}
				}
				// Build per-tool VarLookup/VarLister that maps provider-scoped
				// keys (e.g. "youtube_refresh_token") to the agent's bound
				// Connection, falling back to the user-scoped / global lookups.
				toolVarLookup := varLookup
				toolVarLister := varLister
				if s.connectionStore != nil {
					var perSkill map[string]string
					if hi.skillID != "" {
						perSkill = skillRuntime.SkillConnOverrides(hi.skillID)
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
				} else if hi.handlerType == "builtin" {
					result, callErr = s.dispatchBuiltinTool(ctx, tc.Name, tc.Arguments)
				} else if hi.handlerType == "delegate" {
					// Delegation tool for task-linked chat sessions.
					// Creates a child task and runs org delegation synchronously.
					targetAgentID := hi.handler
					taskText, _ := tc.Arguments["task"].(string)
					if taskText == "" {
						taskText = "Delegated task"
					}
					if taskLinked != nil && session.OrganizationID != "" {
						org, _ := s.organizationStore.GetOrganization(ctx, session.OrganizationID)
						if org != nil {
							childTask, createErr := s.createDelegationTask(ctx, org, taskLinked, targetAgentID, taskText, 1)
							if createErr != nil {
								result = fmt.Sprintf("Error creating delegation task: %v", createErr)
							} else {
								// Run delegation synchronously so the chat agent gets the result.
								delegErr := s.runOrgDelegation(ctx, org, childTask, targetAgentID, 1)
								if delegErr != nil {
									result = fmt.Sprintf("Delegation failed: %v", delegErr)
								} else {
									// Re-fetch the completed child task to get its result.
									completed, _ := s.taskStore.GetTask(ctx, childTask.ID)
									if completed != nil && completed.Result != "" {
										result = completed.Result
									} else {
										result = "Delegation completed but no result was returned."
									}
								}
							}
						} else {
							result = "Error: organization not found for delegation"
						}
					} else {
						result = "Error: delegation requires a task-linked session with an organization"
					}
				} else {
					result, callErr = workflow.ExecuteJSHandlerWithOptions(hi.handler, tc.Arguments, workflow.JSHandlerOptions{
						VarLookup:      toolVarLookup,
						UserPrefLookup: userPrefLookup,
					})
				}
			} else {
				callErr = fmt.Errorf("no handler for tool %q", tc.Name)
			}

			if callErr != nil {
				slog.Error("agentic loop: tool call failed", "tool", tc.Name, "error", callErr)
				result = fmt.Sprintf("Error: %v", callErr)
			} else {
				// Truncate for logging.
				logResult := result
				if len(logResult) > 500 {
					logResult = logResult[:500] + "..."
				}
				slog.Debug("agentic loop: tool call result", "tool", tc.Name, "result_length", len(result), "result", logResult)
			}

			onEvent(AgenticEvent{Type: "tool_result", ToolName: tc.Name, ToolID: tc.ID, Result: result})

			// Apply governor truncation before the result enters the
			// LLM message history. The full payload is preserved on
			// disk under the workspace root (when configured); the LLM
			// sees a marker pointing at the file.
			runID := sessionID
			if taskLinked != nil {
				runID = taskLinked.ID
			}
			result, _ = s.loopGov.TruncateToolResult(runID, tc.Name, result)

			// Record audit entry for each tool call. Capture the input
			// arguments and the (post-truncation) output so the Audit
			// page can show what the chat-session agent ran without
			// having to re-derive it from the chat-message log.
			recordAudit := s.recordAuditFunc()
			if recordAudit != nil {
				auditDetails := map[string]any{
					"tool_name":  tc.Name,
					"session_id": sessionID,
					"iteration":  iteration,
					"has_error":  callErr != nil,
					"input":      tc.Arguments,
					"output":     service.TruncateForAudit(result),
				}
				if auditErr := recordAudit(ctx, service.AuditEntry{
					ActorType:    "agent",
					ActorID:      session.AgentID,
					Action:       "tool_call",
					ResourceType: "tool",
					ResourceID:   tc.ID,
					Details:      auditDetails,
				}); auditErr != nil {
					slog.Warn("agentic loop: failed to record audit",
						"agent_id", session.AgentID, "error", auditErr)
				}
			}

			toolResults = append(toolResults, service.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tc.ID,
				Content:   result,
			})
		}

		// Persist tool results and continue loop.
		s.persistToolResults(ctx, sessionID, toolResults)
		llmMessages = append(llmMessages, service.Message{
			Role:    "user",
			Content: toolResults,
		})
	}

	// Max iterations reached.
	onEvent(AgenticEvent{Type: "content", Content: "\n\n[Max iterations reached]"})
	onEvent(AgenticEvent{Type: "done"})
	return nil
}

// sendChatMessageRequest is the request body for SendChatMessageAPI.
type sendChatMessageRequest struct {
	Content string `json:"content"`
}

// SendChatMessageAPI handles POST /api/v1/chat/sessions/{id}/messages.
// It runs an agentic loop server-side and streams the response via SSE.
func (s *Server) SendChatMessageAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	var req sendChatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		httpResponse(w, "content is required", http.StatusBadRequest)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	writeSSE := func(event string, data any) {
		jsonData, _ := json.Marshal(data)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", jsonData)
		flusher.Flush()
	}

	onEvent := func(ev AgenticEvent) {
		switch ev.Type {
		case "error":
			writeSSE("error", map[string]string{"error": ev.Error})
		case "content":
			writeSSE("", map[string]any{"type": "content", "content": ev.Content})
		case "tool_call":
			writeSSE("", map[string]any{"type": "tool_call", "tool_name": ev.ToolName, "tool_id": ev.ToolID})
		case "tool_result":
			writeSSE("", map[string]any{"type": "tool_result", "tool_name": ev.ToolName, "tool_id": ev.ToolID, "result": ev.Result})
		case "tool_confirm":
			writeSSE("", map[string]any{"type": "tool_confirm", "tool_name": ev.ToolName, "tool_id": ev.ToolID, "arguments": ev.Arguments})
		case "done":
			writeSSE("", map[string]any{"type": "done"})
		}
	}

	if err := s.RunAgenticLoop(r.Context(), sessionID, req.Content, onEvent); err != nil {
		slog.Error("send message: agentic loop failed", "session_id", sessionID, "error", err)
		writeSSE("error", map[string]string{"error": err.Error()})
	}
}

// ─── Tool Confirmation ───

// confirmToolCallRequest is the request body for ConfirmToolCallAPI.
type confirmToolCallRequest struct {
	ToolID   string `json:"tool_id"`
	Approved bool   `json:"approved"`
}

// ConfirmToolCallAPI handles POST /api/v1/chat/sessions/{id}/confirm.
// It receives the user's approval or rejection for a pending tool call.
func (s *Server) ConfirmToolCallAPI(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		httpResponse(w, "session id is required", http.StatusBadRequest)
		return
	}

	var req confirmToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.ToolID == "" {
		httpResponse(w, "tool_id is required", http.StatusBadRequest)
		return
	}

	confirmKey := sessionID + ":" + req.ToolID
	chVal, ok := s.pendingConfirmations.LoadAndDelete(confirmKey)
	if !ok {
		httpResponse(w, "no pending confirmation for this tool call", http.StatusNotFound)
		return
	}

	ch, ok := chVal.(chan confirmationResult)
	if !ok {
		httpResponse(w, "internal error: invalid confirmation channel", http.StatusInternalServerError)
		return
	}

	ch <- confirmationResult{approved: req.Approved}

	httpResponseJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}

// ─── Helpers ───

func (s *Server) persistAssistantMessage(ctx context.Context, sessionID, content string, toolCalls []service.ToolCall) {
	if s.chatSessionStore == nil {
		return
	}

	msg := service.ChatMessage{
		SessionID: sessionID,
		Role:      "assistant",
		Data: service.ChatMessageData{
			Content: content,
		},
	}
	if len(toolCalls) > 0 {
		msg.Data.ToolCalls = toolCalls
	}

	if _, err := s.chatSessionStore.CreateChatMessage(ctx, msg); err != nil {
		slog.Error("persist assistant message failed", "error", err)
	}
}

// getToolCallIDs extracts tool call IDs from an assistant message's ToolCalls field.
func getToolCallIDs(toolCalls any) []string {
	if toolCalls == nil {
		return nil
	}
	var ids []string
	switch v := toolCalls.(type) {
	case []any:
		for _, tc := range v {
			if tcMap, ok := tc.(map[string]any); ok {
				for _, key := range []string{"id", "Id", "ID"} {
					if id, ok := tcMap[key].(string); ok && id != "" {
						ids = append(ids, id)
						break
					}
				}
			}
		}
	case []map[string]any:
		for _, tcMap := range v {
			for _, key := range []string{"id", "Id", "ID"} {
				if id, ok := tcMap[key].(string); ok && id != "" {
					ids = append(ids, id)
					break
				}
			}
		}
	}
	return ids
}

// sanitizeMessageHistory removes corrupted tool call/result sequences from message history.
// This prevents LLM "tool call result does not follow tool call" errors.
func sanitizeMessageHistory(msgs []service.ChatMessage) []service.ChatMessage {
	if len(msgs) == 0 {
		return msgs
	}

	// Step 1: Collect ALL valid tool_call IDs from ALL assistant messages
	validToolCallIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == "assistant" {
			for _, id := range getToolCallIDs(msg.Data.ToolCalls) {
				validToolCallIDs[id] = true
			}
		}
	}

	// Step 2: Collect ALL tool_call_ids from tool result messages
	presentToolResults := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == "tool" && msg.Data.ToolCallID != "" {
			presentToolResults[msg.Data.ToolCallID] = true
		}
	}

	// Step 3: Find assistant messages that are missing tool results
	// These are "broken" — their tool_call IDs are dangling
	brokenToolCallIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == "assistant" {
			ids := getToolCallIDs(msg.Data.ToolCalls)
			if len(ids) == 0 {
				continue
			}
			allPresent := true
			for _, id := range ids {
				if !presentToolResults[id] {
					allPresent = false
					break
				}
			}
			if !allPresent {
				// Mark ALL tool call IDs from this assistant as broken
				for _, id := range ids {
					brokenToolCallIDs[id] = true
				}
			}
		}
	}

	// Step 4: Build clean result — drop broken blocks and orphaned tool messages
	var result []service.ChatMessage
	for _, msg := range msgs {
		if msg.Role == "assistant" {
			ids := getToolCallIDs(msg.Data.ToolCalls)
			if len(ids) > 0 {
				// Check if any of this assistant's tool calls are broken
				isBroken := false
				for _, id := range ids {
					if brokenToolCallIDs[id] {
						isBroken = true
						break
					}
				}
				if isBroken {
					slog.Debug("sanitizeMessageHistory: dropping broken assistant message", "tool_call_ids", len(ids))
					continue
				}
			}
			result = append(result, msg)
		} else if msg.Role == "tool" {
			// Drop tool results that are orphaned or belong to broken blocks
			if msg.Data.ToolCallID == "" {
				continue
			}
			if !validToolCallIDs[msg.Data.ToolCallID] || brokenToolCallIDs[msg.Data.ToolCallID] {
				slog.Debug("sanitizeMessageHistory: dropping tool message", "tool_call_id", msg.Data.ToolCallID)
				continue
			}
			result = append(result, msg)
		} else {
			result = append(result, msg)
		}
	}

	return result
}

// sanitizeLLMMessages removes corrupted tool call/result sequences from an in-memory
// LLM message list ([]service.Message). This is used for restored conversation state
// in org-delegation where messages are not stored as ChatMessage rows.
// It drops assistant messages whose tool_use blocks lack matching tool_result responses.
func sanitizeLLMMessages(msgs []service.Message) []service.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// Helper: extract tool_use IDs from an assistant message's content blocks.
	extractToolUseIDs := func(content any) []string {
		blocks, ok := content.([]service.ContentBlock)
		if !ok {
			return nil
		}
		var ids []string
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" {
				ids = append(ids, b.ID)
			}
		}
		return ids
	}

	// Helper: extract tool_result IDs from a user message's content blocks.
	extractToolResultIDs := func(content any) []string {
		blocks, ok := content.([]service.ContentBlock)
		if !ok {
			return nil
		}
		var ids []string
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				ids = append(ids, b.ToolUseID)
			}
		}
		return ids
	}

	// Step 1: Collect ALL tool_use IDs and ALL tool_result IDs.
	validToolUseIDs := make(map[string]bool)
	presentToolResults := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == "assistant" {
			for _, id := range extractToolUseIDs(msg.Content) {
				validToolUseIDs[id] = true
			}
		} else if msg.Role == "user" {
			for _, id := range extractToolResultIDs(msg.Content) {
				presentToolResults[id] = true
			}
		}
	}

	// Step 2: Find assistant messages with incomplete tool results.
	brokenToolUseIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		ids := extractToolUseIDs(msg.Content)
		if len(ids) == 0 {
			continue
		}
		allPresent := true
		for _, id := range ids {
			if !presentToolResults[id] {
				allPresent = false
				break
			}
		}
		if !allPresent {
			for _, id := range ids {
				brokenToolUseIDs[id] = true
			}
		}
	}

	if len(brokenToolUseIDs) == 0 {
		return msgs // nothing to fix
	}

	// Step 3: Rebuild message list, dropping broken assistant and orphaned tool_result messages.
	var result []service.Message
	for _, msg := range msgs {
		if msg.Role == "assistant" {
			ids := extractToolUseIDs(msg.Content)
			if len(ids) > 0 {
				isBroken := false
				for _, id := range ids {
					if brokenToolUseIDs[id] {
						isBroken = true
						break
					}
				}
				if isBroken {
					slog.Debug("sanitizeLLMMessages: dropping broken assistant message", "tool_use_ids", len(ids))
					continue
				}
			}
			result = append(result, msg)
		} else if msg.Role == "user" {
			resultIDs := extractToolResultIDs(msg.Content)
			if len(resultIDs) > 0 {
				// Check if any tool_result references a broken tool_use.
				hasBroken := false
				for _, id := range resultIDs {
					if brokenToolUseIDs[id] || !validToolUseIDs[id] {
						hasBroken = true
						break
					}
				}
				if hasBroken {
					slog.Debug("sanitizeLLMMessages: dropping orphaned tool_result message", "tool_result_ids", len(resultIDs))
					continue
				}
			}
			result = append(result, msg)
		} else {
			result = append(result, msg)
		}
	}

	return result
}

func (s *Server) persistToolResults(ctx context.Context, sessionID string, results []service.ContentBlock) {
	if s.chatSessionStore == nil {
		return
	}

	var msgs []service.ChatMessage
	for _, r := range results {
		content := r.Content
		// Sanitize: remove non-UTF8 bytes that break PostgreSQL
		if !utf8.ValidString(content) {
			content = strings.ToValidUTF8(content, "")
		}
		// Truncate very large tool results (e.g., binary file dumps) to prevent DB bloat
		if len(content) > 50000 {
			content = content[:50000] + "\n... [truncated, " + fmt.Sprintf("%d", len(r.Content)) + " bytes total]"
		}
		msgs = append(msgs, service.ChatMessage{
			SessionID: sessionID,
			Role:      "tool",
			Data: service.ChatMessageData{
				Content:    content,
				ToolCallID: r.ToolUseID,
			},
		})
	}

	if len(msgs) > 0 {
		if err := s.chatSessionStore.CreateChatMessages(ctx, msgs); err != nil {
			slog.Error("persist tool results failed", "error", err)
		}
	}
}

// callMCPToolFromClients dispatches a tool call to the appropriate MCP client.
func callMCPToolFromClients(ctx context.Context, clients []service.MCPClient, name string, args map[string]any) (string, error) {
	for _, c := range clients {
		result, err := c.CallTool(ctx, name, args)
		if err != nil {
			continue
		}
		return result, nil
	}
	return "", fmt.Errorf("MCP tool %q: no server returned a result", name)
}
