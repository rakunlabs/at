package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/query"
)

// ─── Chat Session CRUD ───

// ListChatSessionsAPI handles GET /api/v1/chat/sessions.
func (s *Server) ListChatSessionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
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

	messages, err := s.chatSessionStore.ListChatMessages(r.Context(), id)
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
	Type     string `json:"type"`               // "content", "tool_call", "tool_result", "done", "error"
	Content  string `json:"content,omitempty"`   // for "content" events
	ToolName string `json:"tool_name,omitempty"` // for "tool_call" and "tool_result"
	ToolID   string `json:"tool_id,omitempty"`   // for "tool_call" and "tool_result"
	Result   string `json:"result,omitempty"`    // for "tool_result"
	Error    string `json:"error,omitempty"`     // for "error"
}

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
	dbMessages, err := s.chatSessionStore.ListChatMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}

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
	type toolHandlerInfo struct {
		handler     string
		handlerType string
	}
	toolHandlers := make(map[string]toolHandlerInfo)
	mcpToolNames := make(map[string]bool)
	var mcpClients []*service.HTTPMCPClient
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()

	var allTools []service.Tool

	// MCP tools
	for _, url := range agent.Config.MCPs {
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

	// Skill tools
	var skillPromptFragments []string
	if s.skillStore != nil {
		for _, nameOrID := range agent.Config.Skills {
			skill, err := s.skillStore.GetSkill(ctx, nameOrID)
			if err != nil {
				slog.Warn("agentic loop: skill lookup failed", "skill", nameOrID, "error", err)
				continue
			}
			if skill == nil {
				skill, err = s.skillStore.GetSkillByName(ctx, nameOrID)
				if err != nil || skill == nil {
					slog.Warn("agentic loop: skill not found", "skill", nameOrID)
					continue
				}
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
	}

	// 7. Build system prompt.
	systemPrompt := agent.Config.SystemPrompt
	for _, fragment := range skillPromptFragments {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += fragment
	}

	// 8. Build messages for LLM.
	var llmMessages []service.Message
	if systemPrompt != "" {
		llmMessages = append(llmMessages, service.Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}

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

	// Strip handlers from tools sent to LLM.
	llmTools := make([]service.Tool, len(allTools))
	for i, t := range allTools {
		llmTools[i] = service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	// Resolve max iterations and tool timeout.
	maxIterations := agent.Config.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 10
	}

	toolTimeout := time.Duration(agent.Config.ToolTimeout) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 60 * time.Second
	}

	// Build variable lookup/lister for bash tools.
	var varLookup workflow.VarLookup
	var varLister workflow.VarLister
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

	// 9. Agentic loop.
	for iteration := 0; iteration < maxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			onEvent(AgenticEvent{Type: "error", Error: "request cancelled"})
			return nil
		}

		resp, err := info.provider.Chat(ctx, model, llmMessages, llmTools)
		if err != nil {
			slog.Error("agentic loop: chat failed", "iteration", iteration, "error", err)
			onEvent(AgenticEvent{Type: "error", Error: fmt.Sprintf("LLM error: %v", err)})
			return nil
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
			onEvent(AgenticEvent{Type: "done"})
			return nil
		}

		// Persist assistant message with tool calls.
		s.persistAssistantMessage(ctx, sessionID, resp.Content, resp.ToolCalls)

		// Execute tool calls.
		var toolResults []service.ContentBlock
		for _, tc := range resp.ToolCalls {
			onEvent(AgenticEvent{Type: "tool_call", ToolName: tc.Name, ToolID: tc.ID})

			var result string
			var callErr error

			if mcpToolNames[tc.Name] {
				result, callErr = callMCPToolFromClients(ctx, mcpClients, tc.Name, tc.Arguments)
			} else if hi, ok := toolHandlers[tc.Name]; ok {
				if hi.handlerType == "bash" {
					result, callErr = workflow.ExecuteBashHandler(ctx, hi.handler, tc.Arguments, varLister, toolTimeout)
				} else {
					result, callErr = workflow.ExecuteJSHandler(hi.handler, tc.Arguments, varLookup)
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
		case "done":
			writeSSE("", map[string]any{"type": "done"})
		}
	}

	if err := s.RunAgenticLoop(r.Context(), sessionID, req.Content, onEvent); err != nil {
		slog.Error("send message: agentic loop failed", "session_id", sessionID, "error", err)
		writeSSE("error", map[string]string{"error": err.Error()})
	}
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

func (s *Server) persistToolResults(ctx context.Context, sessionID string, results []service.ContentBlock) {
	if s.chatSessionStore == nil {
		return
	}

	var msgs []service.ChatMessage
	for _, r := range results {
		msgs = append(msgs, service.ChatMessage{
			SessionID: sessionID,
			Role:      "tool",
			Data: service.ChatMessageData{
				Content:    r.Content,
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
func callMCPToolFromClients(ctx context.Context, clients []*service.HTTPMCPClient, name string, args map[string]any) (string, error) {
	for _, c := range clients {
		result, err := c.CallTool(ctx, name, args)
		if err != nil {
			continue
		}
		return result, nil
	}
	return "", fmt.Errorf("MCP tool %q: no server returned a result", name)
}
