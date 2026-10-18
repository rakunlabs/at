package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Background runs answer a Chats conversation on the server, so the turn keeps
// going after the page is closed. The browser loop stays the default; a
// conversation opts in with config.background. Tools that live in the browser
// (local MCP servers, local providers, extensions, the question prompt) do not
// exist here and are never offered.

const (
	// chatRunMaxIterations matches the browser loop's tool-step budget.
	chatRunMaxIterations = 20
	// chatRunToolTimeout bounds one tool call; agent-bound skills and
	// agent_run are whole agent loops and are bounded by the run instead.
	chatRunToolTimeout = 5 * time.Minute
	// chatRunResultMaxBytes keeps a stored tool result under the 1 MiB row cap.
	chatRunResultMaxBytes = 512 << 10
	// chatRunHistoryMax bounds the rows one run loads. A longer conversation
	// must be compacted first, exactly like a browser completion.
	chatRunHistoryMax = 2000
	chatRunLoadSkill  = "load_skill"
	chatRunStatus     = "agent_run_status"
	chatRunTodoWrite  = "todo_write"
	chatRunTodoRead   = "todo_read"
	chatRunQuestion   = "question"
)

// chatRunSetup is the part of a conversation's opaque config a run reads.
type chatRunSetup struct {
	MCPSets         []string `json:"mcp_sets"`
	Skills          []string `json:"skills"`
	BuiltinTools    []string `json:"builtin_tools"`
	FrontendTools   []string `json:"frontend_tools"`
	ReasoningEffort string   `json:"reasoning_effort"`
}

func chatRunSetupFromConfig(config map[string]any) chatRunSetup {
	var setup chatRunSetup
	if raw, err := json.Marshal(config); err == nil {
		_ = json.Unmarshal(raw, &setup)
	}
	return setup
}

// ChatRunAPI handles POST /api/v1/chats/conversations/{id}/run. The caller has
// already saved the user message; the run answers the saved transcript.
func (s *Server) ChatRunAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	if _, ok := s.store.(service.PlaygroundRunStorer); !ok {
		nativeError(w, http.StatusServiceUnavailable, "background runs are unavailable")
		return
	}
	if _, err := store.GetPlaygroundConversation(r.Context(), owner, r.PathValue("id")); err != nil {
		playgroundError(w, err)
		return
	}
	// Runs are long and detached, and share the replica's replay capacity with
	// every other chat surface, so one account may not take all of it.
	principal, _ := service.AccessPrincipalFromContext(r.Context())
	if s.liveChatRuns(owner, principal.WorkspaceID) >= chatRunMaxPerOwner {
		nativeError(w, http.StatusTooManyRequests, fmt.Sprintf("at most %d background runs at a time; wait for one to finish or stop it", chatRunMaxPerOwner))
		return
	}
	if r.Header.Get("X-AT-Stream-ID") == "" {
		r.Header.Set("X-AT-Stream-ID", ulid.Make().String())
	}
	s.startReplayStream(w, r, s.runChatConversation, replayChatRun)
}

func (s *Server) runChatConversation(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	runs, _ := s.store.(service.PlaygroundRunStorer)
	conversationID := r.PathValue("id")
	var req struct {
		TraceID string `json:"trace_id"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			nativeError(w, http.StatusBadRequest, "invalid run request")
			return
		}
	}
	if len(req.TraceID) > 128 {
		nativeError(w, http.StatusBadRequest, "trace_id exceeds 128 bytes")
		return
	}
	ctx, err := s.bindRuntimePrincipal(withUserUsage(r.Context(), "chats"), "chat")
	if err != nil {
		nativeError(w, http.StatusForbidden, "runtime identity unavailable")
		return
	}
	if err := s.persistRuntimeRun(ctx); err != nil {
		nativeError(w, http.StatusForbidden, "execution is not allowed in this workspace")
		return
	}
	provenance, _, _ := service.ExecutionFromContext(ctx)

	runID := ulid.Make().String()
	if err := runs.AcquirePlaygroundRun(ctx, owner, conversationID, runID, provenance.WorkspaceID); err != nil {
		playgroundError(w, err)
		return
	}
	ctx = service.ContextWithPlaygroundRun(ctx, runID)
	ctx, cleanupTurn := s.registerChatTurn(ctx)
	defer cleanupTurn()
	// A stale receipt may be taken over by the next run (writes are fenced by
	// run ID), so only lost ownership or a requested cancellation stops this
	// one; a transient database error does not. A renewal that fails is
	// followed by the check, which reports why on the next poll.
	// Renewal fails only when the receipt is gone (taken over or deleted) or
	// a cancellation was requested; the check tells the two apart.
	var replaced atomic.Bool
	ctx, release := holdRunReceipt(ctx, runReceipt{
		Label: "chats:" + conversationID,
		Renew: func(ctx context.Context) error {
			// The check classifies a lost receipt; renewal only keeps it alive.
			_ = runs.HeartbeatPlaygroundRun(ctx, owner, conversationID, runID)
			return nil
		},
		Check: func(ctx context.Context) error {
			run, err := runs.GetPlaygroundRun(ctx, owner, conversationID)
			switch {
			case errors.Is(err, service.ErrPlaygroundNotFound), err == nil && (run == nil || run.ID != runID):
				replaced.Store(true)
				return service.ErrPlaygroundRunBusy
			case err != nil:
				return nil
			case run.CancelRequested:
				return service.ErrPlaygroundRunBusy
			}
			return nil
		},
		Release: func(ctx context.Context) error {
			return runs.ReleasePlaygroundRun(ctx, owner, conversationID, runID)
		},
	})
	defer release()

	events, ok := newRunEventStream(w)
	if !ok {
		return
	}
	defer events.keepAlive(ctx)()

	traceID := req.TraceID
	if traceID == "" {
		traceID = "chats-run-" + ulid.Make().String()
	}
	events.send(map[string]any{"type": "status", "run_id": runID, "trace_id": traceID})
	run := &chatRun{server: s, store: store, owner: owner, conversationID: conversationID, traceID: traceID, events: events}
	err = run.execute(ctx)
	stopped := errors.Is(ctx.Err(), context.Canceled)
	release()
	switch {
	case errors.Is(err, errChatRunWaiting):
		// The question is the saved, unanswered tool call. Nothing waits for
		// the answer: the page saves it and starts the next run.
		events.send(map[string]any{"type": "done", "waiting": true})
	case err == nil:
		events.send(map[string]any{"type": "done"})
	case stopped && replaced.Load():
		// Not a Stop: another run took the conversation over (after this
		// one stopped renewing) or it was deleted. This run may write no more.
		events.send(map[string]any{"type": "error", "error": "This run lost the conversation to another run and stopped. Completed steps are saved."})
	case stopped:
		events.send(map[string]any{"type": "error", "error": "Stopped. Completed steps are saved.", "cancelled": true})
	default:
		slog.Warn("chats background run failed", "conversation", conversationID, "error", err)
		events.send(map[string]any{"type": "error", "error": err.Error()})
	}
}

var errChatRunWaiting = errors.New("background run is waiting for an answer")

type chatRunTool struct {
	kind    string // mcp, builtin, skill, todo, question
	setName string
}

// chatRun is one server-side answer to a conversation.
type chatRun struct {
	server         *Server
	store          service.PlaygroundStorer
	owner          string
	conversationID string
	traceID        string
	events         *runEventStream

	providerKey, model string
	history            []wire.OpenAIMessage
	tools              []service.Tool
	sources            map[string]chatRunTool
	mcp                *agentMCPTools
	backgroundRuns     []string
	reportedRuns       map[string]bool
	artifacts          []chatArtifact
	todos              any
}

func (c *chatRun) execute(ctx context.Context) error {
	s := c.server
	conversation, err := c.store.GetPlaygroundConversation(ctx, c.owner, c.conversationID)
	if err != nil {
		return err
	}
	c.providerKey, c.model = conversation.ProviderKey, conversation.Model
	if c.providerKey == "" || c.model == "" {
		return errors.New("choose a model for this conversation first")
	}
	setup := chatRunSetupFromConfig(conversation.Config)
	ctx = contextWithChatMediaRefs(contextWithChatTraceID(contextWithSessionID(ctx, c.conversationID), c.traceID))

	if _, _, err := c.resolveProvider(ctx); err != nil {
		return err
	}

	stored, err := c.loadHistory(ctx)
	if err != nil {
		return err
	}
	resolver := newStoredMessageResolver(s, c.owner)
	for _, m := range stored {
		msg, err := resolver.message(ctx, m)
		if err != nil {
			return fmt.Errorf("load saved message: %w", err)
		}
		c.history = append(c.history, msg)
	}
	c.closeInterruptedCalls(ctx)
	c.artifacts = c.pendingArtifactsFromHistory()

	c.mcp = s.newAgentMCPTools("chats background run", nil)
	defer c.mcp.Close()
	skillPrompts := c.discoverTools(ctx, setup)
	systemPrompt := strings.Join(slices.DeleteFunc(append([]string{strings.TrimSpace(conversation.SystemPrompt)}, skillPrompts...), func(v string) bool { return v == "" }), "\n\n")

	recordObservation := s.recordObservationFunc()
	observation := agentloop.ObservationContext{
		Source: "chats", TraceID: c.traceID, SessionID: c.conversationID, RunID: c.traceID,
		Provider: c.providerKey, Model: c.model, ReasoningEffort: setup.ReasoningEffort,
	}
	ctx, span := agentloop.StartRun(ctx, &observation, "Chats background run")
	span.Metadata = map[string]any{"conversation_id": c.conversationID, "background": true}
	var runOutput string
	var runErr error
	if recordObservation != nil {
		defer func() {
			recordObservation(context.WithoutCancel(ctx), span.Finish(c.lastUserText(), runOutput, runErr))
		}()
	}

	for iteration := 0; iteration < chatRunMaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			runErr = err
			return err
		}
		// Re-admitted before every call, exactly like a browser completion: a
		// run can last half an hour, and revoking a member, disabling the
		// provider or narrowing the policy must stop it at the next step.
		provider, providerType, err := c.resolveProvider(ctx)
		if err != nil {
			runErr = err
			return err
		}
		request := c.history
		if systemPrompt != "" {
			request = append([]wire.OpenAIMessage{{Role: "system", Content: jsonString(systemPrompt)}}, c.history...)
		}
		messages, _ := s.buildProviderMessages(providerType, request, nil)
		var partial strings.Builder
		resp, callMessages, latency, err := agentloop.CallProviderStream(ctx, nil, provider, c.model, "chats:"+c.conversationID, c.conversationID, messages, c.tools, setup.ReasoningEffort, func(delta agentloop.StreamDelta) {
			partial.WriteString(delta.Content)
			event := map[string]any{"type": "delta"}
			if delta.Content != "" {
				event["content"] = delta.Content
			}
			if delta.Reasoning != "" {
				event["reasoning"] = delta.Reasoning
			}
			c.events.send(event)
		})
		fullModel := c.providerKey + "/" + c.model
		if err != nil {
			s.noteProviderError(c.providerKey, err)
			s.recordUsageAsync(ctx, nil, fullModel, service.Usage{}, latency, "error", classifyHTTPError(err), err.Error())
		} else {
			header := resp.Header
			if streamed, ok := provider.(*chatRunStreamProvider); ok && streamed.header != nil {
				header = streamed.header
			}
			s.noteProviderResponse(c.providerKey, header)
			s.recordUsageAsync(ctx, nil, fullModel, resp.Usage, latency, "ok", "", "")
		}
		genID := ""
		if recordObservation != nil {
			genID = recordObservation(ctx, agentloop.NewGenerationObservation(agentloop.GenerationObservationParams{
				Context: observation, Messages: callMessages, Tools: c.tools, Response: resp, LatencyMs: latency,
				Iteration: iteration, Err: err, Metadata: map[string]any{"background": true},
				Started: time.Now().Add(-time.Duration(latency) * time.Millisecond),
			}))
		}
		if err != nil {
			if text := partial.String(); text != "" {
				_ = c.appendAssistant(context.WithoutCancel(ctx), map[string]any{"content": text}, nil)
			}
			runErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("model call failed: %w", err)
		}
		s.cacheThoughtSignatures(resp.ToolCalls)
		content := resp.Content
		if content == "" && resp.Refusal != "" {
			content = resp.Refusal
		}
		usage := map[string]any{"prompt": resp.Usage.PromptTokens, "completion": resp.Usage.CompletionTokens}
		if cost, ok := s.estimateGatewayUsageCost(context.WithoutCancel(ctx), c.providerKey, c.model, fullModel, resp.Usage); ok {
			usage["cost_cents"] = cost
		}

		if len(resp.ToolCalls) > 0 {
			if err := c.appendAssistant(ctx, map[string]any{"content": content, "tool_calls": wireToolCalls(resp.ToolCalls)}, usage); err != nil {
				runErr = err
				return err
			}
			if err := c.runTools(ctx, resp.ToolCalls, observation, genID, iteration); err != nil {
				if errors.Is(err, errChatRunWaiting) {
					runOutput = "waiting for the user's answer"
					// Files produced in this step stay in its saved tool
					// results; the run that continues after the answer
					// collects them (pendingArtifactsFromHistory).
					return err
				}
				runErr = err
				return err
			}
			continue
		}

		// The model answered while background skill runs are unreported:
		// wait for them and let it answer again with what they produced.
		if pending := c.unreportedRuns(); len(pending) > 0 {
			if err := c.appendAssistant(ctx, map[string]any{"content": content}, usage); err != nil {
				runErr = err
				return err
			}
			c.events.send(map[string]any{"type": "tool_progress", "content": "waiting for background skill runs to finish"})
			report, err := c.collectSkillRuns(ctx, pending)
			if err != nil {
				runErr = err
				return err
			}
			if err := c.appendUser(ctx, "Background skill runs finished:\n\n"+report+"\n\nReply to me using these results."); err != nil {
				runErr = err
				return err
			}
			continue
		}

		data := map[string]any{"content": content}
		if len(c.artifacts) > 0 {
			data["content"] = withArtifactParts(content, c.artifacts)
			c.artifacts = nil
		}
		if err := c.appendAssistant(ctx, data, usage); err != nil {
			runErr = err
			return err
		}
		runOutput = content
		return nil
	}
	notice := fmt.Sprintf("Stopped after %d tool call iterations to prevent infinite loops.", chatRunMaxIterations)
	runOutput = notice
	data := map[string]any{"content": notice}
	if len(c.artifacts) > 0 {
		data["content"] = withArtifactParts(notice, c.artifacts)
		c.artifacts = nil
	}
	return c.appendAssistant(ctx, data, nil)
}

// resolveProvider admits and resolves the conversation's model the way
// /chats/completions does: the caller's workspace catalog, grants, disabled
// state and budgets. The result streams when the adapter can.
func (c *chatRun) resolveProvider(ctx context.Context) (service.LLMProvider, string, error) {
	info, err := c.server.chatProviderInfo(ctx, c.providerKey, c.model)
	if err != nil {
		return nil, "", fmt.Errorf("provider %q or model %q is not available: %w", c.providerKey, c.model, err)
	}
	if len(info.models) > 0 && !info.hasModel(c.model) {
		return nil, "", fmt.Errorf("model %q is not available for provider %q", c.model, c.providerKey)
	}
	if streamer, ok := info.provider.(service.LLMStreamProvider); ok {
		return &chatRunStreamProvider{LLMProvider: info.provider, streamer: streamer}, info.providerType, nil
	}
	return info.provider, info.providerType, nil
}

// chatRunStreamProvider lets agentloop stream through the same provider the
// browser completion endpoint resolves, without the scoped execution wrapper
// (whose provider admission is narrower than Chats'). The response headers of
// the last stream feed provider cooldown, like the gateway's.
type chatRunStreamProvider struct {
	service.LLMProvider
	streamer service.LLMStreamProvider
	header   http.Header
}

func (p *chatRunStreamProvider) StreamChat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, error) {
	chunks, header, err := p.streamer.ChatStream(ctx, model, messages, tools, opts)
	p.header = header
	return chunks, err
}

// ValidateReasoningEffort forwards the adapter's own check, which embedding
// the interface would otherwise hide from agentloop.
func (p *chatRunStreamProvider) ValidateReasoningEffort(effort string) error {
	if validator, ok := p.LLMProvider.(service.ReasoningEffortValidator); ok {
		return validator.ValidateReasoningEffort(effort)
	}
	return nil
}

// loadHistory reads the transcript from the latest compaction summary on,
// which is what a browser completion sends.
func (c *chatRun) loadHistory(ctx context.Context) ([]service.PlaygroundMessage, error) {
	var rows []service.PlaygroundMessage
	before := ""
	for {
		page, err := c.store.ListPlaygroundMessages(ctx, c.owner, c.conversationID, before, playgroundLimitMax)
		if err != nil {
			return nil, err
		}
		rows = append(page, rows...)
		for i := len(page) - 1; i >= 0; i-- {
			if compacted, _ := page[i].Data["compaction"].(bool); compacted {
				return rows[i:], nil
			}
		}
		if len(rows) > chatRunHistoryMax {
			return nil, fmt.Errorf("conversation exceeds %d messages; run /compact first", chatRunHistoryMax)
		}
		if len(page) < playgroundLimitMax {
			return rows, nil
		}
		before = page[0].ID
	}
}

// closeInterruptedCalls answers tool calls an interrupted turn left open,
// which every provider rejects. Calls in the middle of the history (a new
// message was sent after the interruption) are answered in the request only;
// calls at the end of the transcript are answered in storage as well.
func (c *chatRun) closeInterruptedCalls(ctx context.Context) {
	var repaired []wire.OpenAIMessage
	var trailing []wire.OpenAIToolCall
	for i, m := range c.history {
		repaired = append(repaired, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		answered := map[string]bool{}
		end := i + 1
		for ; end < len(c.history) && c.history[end].Role == "tool"; end++ {
			answered[c.history[end].ToolCallID] = true
		}
		for _, call := range m.ToolCalls {
			if answered[call.ID] {
				continue
			}
			if end == len(c.history) {
				trailing = append(trailing, call)
				continue
			}
			// Results must directly follow their calls, so the synthetic
			// answers go before the real ones that do exist.
			result := chatRunInterruptedResult
			if call.Function.Name == chatRunQuestion {
				result = chatRunUnansweredQuestion
			}
			repaired = append(repaired, wire.OpenAIMessage{Role: "tool", ToolCallID: call.ID, Content: jsonString(result)})
		}
	}
	c.history = repaired
	for _, call := range trailing {
		result := chatRunInterruptedResult
		if call.Function.Name == chatRunQuestion {
			result = chatRunUnansweredQuestion
		}
		_ = c.appendTool(ctx, call.ID, result)
	}
}

const (
	chatRunInterruptedResult  = "Interrupted by the user before this tool finished. It may or may not have taken effect; check before repeating it."
	chatRunUnansweredQuestion = "The user did not answer this question and wrote a new message instead."
)

func (c *chatRun) lastUserText() string {
	for i := len(c.history) - 1; i >= 0; i-- {
		if c.history[i].Role == "user" {
			return extractChatRunText(c.history[i].Content)
		}
	}
	return ""
}

func extractChatRunText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func jsonString(v string) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func wireToolCalls(calls []service.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		args, _ := json.Marshal(call.Arguments)
		if call.Arguments == nil {
			args = []byte("{}")
		}
		entry := map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(args)}}
		if call.ThoughtSignature != "" {
			entry["thought_signature"] = call.ThoughtSignature
		}
		out = append(out, entry)
	}
	return out
}

func (c *chatRun) save(ctx context.Context, role string, data map[string]any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if len(encoded) > playgroundPayloadMaxBytes {
		return fmt.Errorf("a %s message exceeds the 1 MiB history limit", role)
	}
	stored, err := c.store.AppendPlaygroundMessages(ctx, c.owner, c.conversationID, []service.PlaygroundMessage{{
		ClientID: ulid.Make().String(), Role: role, ProviderKey: c.providerKey, Model: c.model, Data: data,
	}})
	if err != nil {
		return fmt.Errorf("save %s message: %w", role, err)
	}
	withRole := make(map[string]any, len(data)+1)
	for k, v := range data {
		withRole[k] = v
	}
	withRole["role"] = role
	var msg wire.OpenAIMessage
	if raw, err := json.Marshal(withRole); err != nil || json.Unmarshal(raw, &msg) != nil {
		return fmt.Errorf("encode %s message", role)
	}
	c.history = append(c.history, msg)
	for _, row := range stored {
		c.events.send(map[string]any{"type": "message", "message": row})
	}
	return nil
}

func (c *chatRun) appendAssistant(ctx context.Context, data map[string]any, usage map[string]any) error {
	if usage != nil {
		data["at_usage"] = usage
	}
	return c.save(ctx, "assistant", data)
}

func (c *chatRun) appendUser(ctx context.Context, text string) error {
	return c.save(ctx, "user", map[string]any{"content": text})
}

func (c *chatRun) appendTool(ctx context.Context, callID, result string) error {
	if !utf8.ValidString(result) {
		result = strings.ToValidUTF8(result, "")
	}
	if len(result) > chatRunResultMaxBytes {
		cut := chatRunResultMaxBytes
		for cut > 0 && !utf8.RuneStart(result[cut]) {
			cut--
		}
		result = result[:cut] + "\n… [result truncated at 512 KiB; ask for a narrower result]"
	}
	return c.save(ctx, "tool", map[string]any{"content": result, "tool_call_id": callID})
}

// withArtifactParts attaches delivered files to the final answer the way the
// browser loop does, so they render and persist identically.
func withArtifactParts(content string, artifacts []chatArtifact) []any {
	parts := []any{}
	if content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": content})
	}
	for _, a := range artifacts {
		if slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, a.ContentType) {
			parts = append(parts, map[string]any{"type": "image", "media_id": a.MediaID, "name": a.Name, "bytes": a.SizeBytes})
			continue
		}
		parts = append(parts, map[string]any{"type": "file", "media_id": a.MediaID, "name": a.Name, "bytes": a.SizeBytes, "mime_type": a.ContentType})
	}
	return parts
}

// discoverTools mirrors the browser's discovery order: MCP sets, skills,
// built-ins, then the supported browser helpers. Earlier sources keep a name.
func (c *chatRun) discoverTools(ctx context.Context, setup chatRunSetup) []string {
	s := c.server
	c.sources = map[string]chatRunTool{}
	add := func(tool service.Tool, source chatRunTool) {
		if _, taken := c.sources[tool.Name]; taken {
			return
		}
		c.sources[tool.Name] = source
		c.tools = append(c.tools, service.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}

	c.mcp.Connect(ctx, setup.MCPSets, nil)
	for _, tool := range c.mcp.Tools() {
		setName, _ := c.mcp.SetName(tool.Name)
		add(tool, chatRunTool{kind: "mcp", setName: setName})
	}

	var prompts []string
	var forkSkills []*service.Skill
	if lookup := s.executionSkillLookup(ctx); lookup != nil {
		for _, name := range setup.Skills {
			skill, err := lookup(name)
			if err != nil || skill == nil {
				slog.Warn("chats background run: skill unavailable", "skill", name, "error", err)
				continue
			}
			if skill.Context == "fork" && strings.TrimSpace(skill.Agent) != "" {
				forkSkills = append(forkSkills, skill)
				continue
			}
			if skill.SystemPrompt != "" {
				prompts = append(prompts, skill.SystemPrompt)
			}
		}
	}
	if len(forkSkills) > 0 {
		names := make([]string, 0, len(forkSkills))
		var catalog strings.Builder
		for _, skill := range forkSkills {
			names = append(names, skill.Name)
			description := skill.Description
			if description == "" {
				description = "(no description)"
			}
			fmt.Fprintf(&catalog, "- `%s` — %s\n  Runs in an isolated context using its own agent; pass a self-contained `task` and choose `run_mode` (`foreground` when the result is needed now, `background` for independent concurrent work).\n", skill.Name, description)
		}
		prompts = append(prompts, fmt.Sprintf("## Available Skills\n\n%s\nCall `%s` with the skill name to use one; do not attempt its work yourself. Background runs return a run_id; call `%s` with it to wait for the result.", catalog.String(), chatRunLoadSkill, chatRunStatus))
		add(service.Tool{
			Name:        chatRunLoadSkill,
			Description: "Activate an attached skill. A catalog entry marked isolated runs through its configured subagent and requires a self-contained task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill_name": map[string]any{"type": "string", "enum": names, "description": "The name of the skill to load. Must match one of the catalog entries listed in your system prompt."},
					"task":       map[string]any{"type": "string", "description": "Task for an isolated skill. Required when the selected skill has context: fork."},
					"context":    map[string]any{"type": "string", "description": "Optional background and constraints for the isolated skill run."},
					"run_mode":   map[string]any{"type": "string", "enum": []string{"foreground", "background"}, "description": "Execution mode for an isolated skill. Use background for independent concurrent work. Defaults to foreground."},
				},
				"required": []string{"skill_name"},
			},
		}, chatRunTool{kind: "skill"})
		add(service.Tool{
			Name:        chatRunStatus,
			Description: "Wait for a background skill run to finish and return its final result.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"run_id": map[string]any{"type": "string"}}, "required": []string{"run_id"}},
		}, chatRunTool{kind: "skill"})
	}

	for _, name := range setup.BuiltinTools {
		// The browser offers its own visible todo list instead of these.
		if name == chatRunTodoWrite || name == chatRunTodoRead {
			continue
		}
		for _, tool := range agentBuiltinTools(ctx, []string{name}, "chats background run") {
			add(tool, chatRunTool{kind: "builtin"})
		}
	}

	for _, name := range setup.FrontendTools {
		switch name {
		case chatRunTodoWrite:
			add(service.Tool{
				Name:        chatRunTodoWrite,
				Description: "Create or update the visible todo list for this chat. Replaces the entire list with the provided items. Each item has content (description), status (pending/in_progress/completed/cancelled), and priority (high/medium/low).",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{"todos": map[string]any{
						"type": "array", "description": "The updated todo list",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"content":  map[string]any{"type": "string", "description": "Brief description of the task"},
								"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}, "description": "Current status"},
								"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "description": "Priority level"},
							},
							"required": []string{"content", "status", "priority"},
						},
					}},
					"required": []string{"todos"},
				},
			}, chatRunTool{kind: "todo"})
		case chatRunTodoRead:
			add(service.Tool{
				Name:        chatRunTodoRead,
				Description: "Read the visible todo list for this chat. Returns all items with their content, status, and priority.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}, chatRunTool{kind: "todo"})
		case chatRunQuestion:
			add(service.Tool{
				Name:        chatRunQuestion,
				Description: "Ask the user a question with predefined options. Pauses execution until the user responds; the user may answer much later. Use for gathering preferences, clarifying requirements, or getting decisions.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{"type": "string", "description": "The question to ask the user"},
						"header":   map[string]any{"type": "string", "description": "Short label for the question (max 30 chars)"},
						"options": map[string]any{
							"type": "array", "description": "Available choices",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label":       map[string]any{"type": "string", "description": "Display text (1-5 words)"},
									"description": map[string]any{"type": "string", "description": "Explanation of choice"},
								},
								"required": []string{"label"},
							},
						},
						"multiple": map[string]any{"type": "boolean", "description": "Allow selecting multiple choices"},
						"custom":   map[string]any{"type": "boolean", "description": "Allow typing a custom answer (default true)"},
					},
					"required": []string{"question", "options"},
				},
			}, chatRunTool{kind: "question"})
		}
	}
	c.todos = c.latestTodos()
	return prompts
}

// latestTodos rebuilds the list from the last todo_write in the loaded
// history, as the page does, so todo_read agrees with what the reader sees.
func (c *chatRun) latestTodos() any {
	for i := len(c.history) - 1; i >= 0; i-- {
		calls := c.history[i].ToolCalls
		for j := len(calls) - 1; j >= 0; j-- {
			if calls[j].Function.Name != chatRunTodoWrite {
				continue
			}
			var args struct {
				Todos []any `json:"todos"`
			}
			if json.Unmarshal([]byte(calls[j].Function.Arguments), &args) == nil && args.Todos != nil {
				return args.Todos
			}
		}
	}
	return []any{}
}

func (c *chatRun) runTools(ctx context.Context, calls []service.ToolCall, observation agentloop.ObservationContext, genID string, iteration int) error {
	recordObservation := c.server.recordObservationFunc()
	waiting := false
	for i, call := range calls {
		// A question stays an unanswered call in the transcript; the rest of
		// the step still runs, so the answer is the only thing left to add.
		if c.sources[call.Name].kind == "question" {
			c.events.send(map[string]any{"type": "question", "tool_call_id": call.ID})
			waiting = true
			continue
		}
		if err := ctx.Err(); err != nil {
			// Close the calls this step will never answer, so the transcript
			// stays valid for the next turn.
			for _, rest := range calls[i:] {
				_ = c.appendTool(context.WithoutCancel(ctx), rest.ID, chatRunInterruptedResult)
			}
			return err
		}
		c.events.send(map[string]any{"type": "tool_start", "tool_call_id": call.ID, "name": call.Name})
		started := time.Now()
		toolObsID := ulid.Make().String()
		toolCtx, _ := service.WithTraceParent(ctx, observation.TraceID, toolObsID, observation.SessionID)
		toolCtx = contextWithSubagentProgress(toolCtx, func(agentName, summary string) {
			c.events.send(map[string]any{"type": "tool_progress", "tool_call_id": call.ID, "content": agentName + ": " + summary})
		})
		result, err := c.callTool(toolCtx, call)
		if err != nil {
			if ctx.Err() != nil {
				for _, rest := range calls[i:] {
					_ = c.appendTool(context.WithoutCancel(ctx), rest.ID, chatRunInterruptedResult)
				}
				return ctx.Err()
			}
			result = "Error: " + err.Error()
		}
		c.noteArtifacts(result)
		if recordObservation != nil {
			obs := agentloop.NewToolObservation(agentloop.ToolObservationParams{
				Context: observation, ParentObservationID: genID, Tool: call, Output: result,
				LatencyMs: time.Since(started).Milliseconds(), Iteration: iteration, Err: err,
				Metadata: agentRunToolMetadata(call.Name, result), Started: started,
			})
			obs.ID = toolObsID
			recordObservation(ctx, obs)
		}
		if result == "" {
			result = "Tool executed successfully (no output)"
		}
		if err := c.appendTool(ctx, call.ID, result); err != nil {
			return err
		}
	}
	if waiting {
		return errChatRunWaiting
	}
	return nil
}

func (c *chatRun) callTool(ctx context.Context, call service.ToolCall) (string, error) {
	s := c.server
	source, ok := c.sources[call.Name]
	if !ok {
		return "", fmt.Errorf("tool %q is not available in a background run", call.Name)
	}
	args := call.Arguments
	if args == nil {
		args = map[string]any{}
	}
	switch source.kind {
	case "mcp":
		if source.setName != "" {
			if err := workflow.AuthorizeMCPSetTool(ctx, source.setName, call.Name); err != nil {
				return "", err
			}
			return c.mcp.Call(ctx, call.Name, args, chatRunToolTimeout)
		}
		toolCtx, cancel := context.WithTimeout(ctx, chatRunToolTimeout)
		defer cancel()
		return c.mcp.Call(toolCtx, call.Name, args, 0)
	case "builtin":
		toolCtx, cancel := ctx, context.CancelFunc(func() {})
		if call.Name != "agent_run" {
			toolCtx, cancel = context.WithTimeout(ctx, chatRunToolTimeout)
		}
		defer cancel()
		return s.dispatchBuiltinTool(service.ContextWithBuiltinToolScope(toolCtx, func(name string) bool { return c.sources[name].kind == "builtin" }), call.Name, args)
	case "skill":
		if call.Name == chatRunStatus {
			id := strings.TrimSpace(stringArg(args, "run_id"))
			if id == "" {
				return "", errors.New("run_id is required")
			}
			return c.collectSkillRuns(ctx, []string{id})
		}
		background := stringArg(args, "run_mode") == "background"
		resp := s.runChatSkill(ctx, chatSkillRunRequest{
			Skill: stringArg(args, "skill_name"), Task: stringArg(args, "task"), Context: stringArg(args, "context"),
			Background: background, TraceID: c.traceID,
		})
		if resp.Error != "" {
			return resp.Result, errors.New(resp.Error)
		}
		if background {
			var started struct {
				RunID string `json:"run_id"`
			}
			if json.Unmarshal([]byte(resp.Result), &started) == nil && started.RunID != "" {
				c.backgroundRuns = append(c.backgroundRuns, started.RunID)
			}
		}
		return resp.Result, nil
	case "todo":
		if call.Name == chatRunTodoRead {
			data, _ := json.Marshal(map[string]any{"todos": c.todos})
			return string(data), nil
		}
		todos, ok := args["todos"].([]any)
		if !ok {
			return "", errors.New("todos must be an array")
		}
		c.todos = todos
		data, _ := json.Marshal(map[string]any{"success": true, "count": len(todos)})
		return string(data), nil
	}
	return "", fmt.Errorf("tool %q has no handler", call.Name)
}

// pendingArtifactsFromHistory recovers files delivered by tool results of
// the turn this run continues — after a question was answered, the turn's
// earlier steps already ran in a previous run. It scans back to the turn's
// start (the last user message or final answer) and collects from there.
// A turn that ended with a final answer already attached its own files.
func (c *chatRun) pendingArtifactsFromHistory() []chatArtifact {
	if n := len(c.history); n == 0 || c.history[n-1].Role != "tool" {
		return nil
	}
	start := 0
	for i := len(c.history) - 1; i >= 0; i-- {
		m := c.history[i]
		if m.Role == "user" || (m.Role == "assistant" && len(m.ToolCalls) == 0) {
			start = i + 1
			break
		}
	}
	saved := c.artifacts
	c.artifacts = nil
	for _, m := range c.history[start:] {
		if m.Role == "tool" {
			c.noteArtifacts(extractChatRunText(m.Content))
		}
	}
	found := c.artifacts
	c.artifacts = saved
	return found
}

func (c *chatRun) noteArtifacts(result string) {
	var parsed struct {
		Artifacts []chatArtifact `json:"artifacts"`
	}
	if json.Unmarshal([]byte(result), &parsed) != nil {
		return
	}
	for _, a := range parsed.Artifacts {
		if a.MediaID != "" && a.ContentType != "" {
			c.artifacts = append(c.artifacts, a)
		}
	}
}

func (c *chatRun) unreportedRuns() []string {
	var out []string
	for _, id := range c.backgroundRuns {
		if !c.reportedRuns[id] {
			out = append(out, id)
		}
	}
	return out
}

// collectSkillRuns waits for background skill runs and renders their results
// in the browser loop's format.
func (c *chatRun) collectSkillRuns(ctx context.Context, ids []string) (string, error) {
	if c.reportedRuns == nil {
		c.reportedRuns = map[string]bool{}
	}
	var out []string
	for _, id := range ids {
		run, err := c.server.backgroundSubagentForContext(ctx, id)
		if err != nil {
			return "", err
		}
		select {
		case <-run.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		c.reportedRuns[id] = true
		run.mu.Lock()
		c.artifacts = append(c.artifacts, run.Artifacts...)
		text := run.Result
		if run.Error != "" {
			text = "Error: " + run.Error
		}
		if text == "" {
			text = "(no result)"
		}
		var files strings.Builder
		if len(run.Artifacts) > 0 {
			files.WriteString("\nDelivered files (to show one, copy its markdown into your answer; never write a server path):\n")
			for _, a := range run.Artifacts {
				files.WriteString("- " + a.Name)
				if a.Markdown != "" {
					files.WriteString(": " + a.Markdown)
				}
				files.WriteString("\n")
			}
		}
		if run.ArtifactsNote != "" {
			files.WriteString("\n" + run.ArtifactsNote)
		}
		out = append(out, fmt.Sprintf("## %s (%s) — %s\n%s%s", run.AgentName, run.ID, run.Status, text, files.String()))
		run.mu.Unlock()
	}
	return strings.Join(out, "\n\n"), nil
}

// ChatRunStatusAPI handles GET /api/v1/chats/conversations/{id}/run: whether a
// run holds the conversation, and the live replay ID when it runs here.
func (s *Server) ChatRunStatusAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	runs, ok := s.store.(service.PlaygroundRunStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "background runs are unavailable")
		return
	}
	id := r.PathValue("id")
	run, err := runs.GetPlaygroundRun(r.Context(), owner, id)
	if err != nil {
		playgroundError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"run": run, "stream_id": s.activeReplayStream(chatRunStreams(r, owner, id))}, http.StatusOK)
}

// chatRunMaxPerOwner bounds one account's concurrent background runs per replica.
const chatRunMaxPerOwner = 4

func (s *Server) liveChatRuns(owner, workspace string) int {
	s.chatStreams.mu.Lock()
	defer s.chatStreams.mu.Unlock()
	live := 0
	for _, stream := range s.chatStreams.streams {
		stream.mu.Lock()
		if stream.kind == replayChatRun && !stream.done && stream.owner == owner && stream.workspace == workspace {
			live++
		}
		stream.mu.Unlock()
	}
	return live
}

func chatRunStreams(r *http.Request, owner, conversationID string) replayStreamMatch {
	principal, _ := service.AccessPrincipalFromContext(r.Context())
	return replayStreamMatch{kind: replayChatRun, session: conversationID, owner: owner, workspace: principal.WorkspaceID}
}

// CancelChatRunAPI handles DELETE /api/v1/chats/conversations/{id}/run. The
// owning replica stops at its next check; a local run stops at once.
func (s *Server) CancelChatRunAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	runs, ok := s.store.(service.PlaygroundRunStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "background runs are unavailable")
		return
	}
	id := r.PathValue("id")
	runID := r.URL.Query().Get("run_id")
	if len(runID) > 128 {
		nativeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}
	if err := runs.CancelPlaygroundRun(r.Context(), owner, id, runID); err != nil {
		playgroundError(w, err)
		return
	}
	// A run on this replica stops at once; one elsewhere at its next check.
	if runID == "" {
		s.cancelReplayStreams(chatRunStreams(r, owner, id))
	} else if run, err := runs.GetPlaygroundRun(r.Context(), owner, id); err == nil && run != nil && run.ID == runID {
		s.cancelReplayStreams(chatRunStreams(r, owner, id))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ChatRunStreamAPI handles GET/DELETE
// /api/v1/chats/conversations/{id}/streams/{stream}.
func (s *Server) ChatRunStreamAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	if r.Method == http.MethodDelete {
		s.CancelChatRunAPI(w, r)
		return
	}
	s.replayRunStream(w, r, chatRunStreams(r, owner, r.PathValue("id")))
}
