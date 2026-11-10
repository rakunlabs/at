package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
)

// Agent gateway: POST /gateway/v1/agents/{agent}/chat runs an AT agent on
// behalf of a person, for a website or application that embeds a chatbot.
//
// Two credentials, never one:
//   - Authorization: Bearer <AT API token> — the calling backend. The token
//     must have act_for_users, and the agent must pass its allowed_agents.
//   - subject_token — a token the person's identity provider issued to them
//     (token exchange, RFC 8693 in spirit). AT verifies it against an
//     identity provider with token_exchange on, and resolves the account.
//
// The run then executes as that account, exactly like a Sessions turn: the
// account's live workspace access and permission mappings, its own MCP OAuth
// connections, its provider allowances, and its user ID on traces and cost.
// Nothing the caller sends (headers, metadata) names the user.
//
// Tools the caller runs itself (`client_tools`: the site's own JavaScript
// tools, or browser WebMCP tools relayed by the site) are offered to the
// model; when it calls one the stream ends with `client_tool_calls`, and the
// caller continues the same session with `tool_results`.

const (
	agentGatewayBodyLimit    = 8 << 20
	agentGatewayClientTools  = 64
	agentGatewayToolSchema   = 32 << 10
	agentGatewayResultLimit  = 256 << 10
	agentGatewayExchangeSkew = 30 * time.Second
)

type agentGatewayRequest struct {
	// SubjectTokenType is informational; JWTs are the only kind accepted.
	SubjectToken     string `json:"subject_token"`
	SubjectTokenType string `json:"subject_token_type,omitempty"`
	// IdentityProvider is the AT identity provider ID that issued the token.
	// Optional when exactly one provider has token exchange on.
	IdentityProvider string `json:"identity_provider,omitempty"`
	// SessionID continues a conversation started by this token for this
	// person. Empty starts a new one.
	SessionID   string                   `json:"session_id,omitempty"`
	Message     string                   `json:"message"`
	Attachments []service.ChatAttachment `json:"attachments,omitempty"`
	// ClientTools are run by the caller. Names must be unique and must not
	// start with a reserved prefix.
	ClientTools []agentGatewayClientTool `json:"client_tools,omitempty"`
	// ToolResults answer client tool calls the previous response asked for.
	ToolResults []agentGatewayToolResult `json:"tool_results,omitempty"`
}

type agentGatewayClientTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	// Parameters is accepted as an alias (OpenAI function shape).
	Parameters map[string]any `json:"parameters,omitempty"`
}

type agentGatewayToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

var clientToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ─── Client tools on the loop context ───

type clientToolsKey struct{}
type clientToolContinuationKey struct{}

func contextWithClientTools(ctx context.Context, tools []service.Tool, continuation bool) context.Context {
	ctx = context.WithValue(ctx, clientToolsKey{}, tools)
	return context.WithValue(ctx, clientToolContinuationKey{}, continuation)
}

// isAgentGatewayRun reports a run bound by an act_for_users token.
func isAgentGatewayRun(ctx context.Context) bool {
	p, _, ok := service.ExecutionFromContext(ctx)
	return ok && p.ActorTokenID != ""
}

func clientToolsFromContext(ctx context.Context) []service.Tool {
	tools, _ := ctx.Value(clientToolsKey{}).([]service.Tool)
	return tools
}

func isClientToolContinuation(ctx context.Context) bool {
	v, _ := ctx.Value(clientToolContinuationKey{}).(bool)
	return v
}

func normalizeClientTools(in []agentGatewayClientTool) ([]service.Tool, error) {
	if len(in) > agentGatewayClientTools {
		return nil, fmt.Errorf("at most %d client tools", agentGatewayClientTools)
	}
	seen := map[string]bool{}
	out := make([]service.Tool, 0, len(in))
	for _, t := range in {
		name := strings.TrimSpace(t.Name)
		if !clientToolName.MatchString(name) {
			return nil, fmt.Errorf("client tool name %q must be 1-64 letters, digits, _ or -", t.Name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate client tool %q", name)
		}
		seen[name] = true
		schema := t.InputSchema
		if schema == nil {
			schema = t.Parameters
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if raw, err := json.Marshal(schema); err != nil || len(raw) > agentGatewayToolSchema {
			return nil, fmt.Errorf("client tool %q schema is invalid or larger than %d bytes", name, agentGatewayToolSchema)
		}
		desc := t.Description
		if r := []rune(desc); len(r) > 2000 {
			desc = string(r[:2000])
		}
		out = append(out, service.Tool{Name: name, Description: desc, InputSchema: schema})
	}
	return out, nil
}

// ─── Identity ───

func (s *Server) tokenExchanger() *nativeauth.TokenExchanger {
	s.exchangerOnce.Do(func() {
		loopback := s.config.NativeAuth != nil && s.config.NativeAuth.InsecureHTTP
		s.exchanger = nativeauth.NewTokenExchanger(s.store, loopback)
	})
	return s.exchanger
}

// exchangeProvider picks the identity provider: the named one, or the only
// provider with token exchange on.
func (s *Server) exchangeProvider(ctx context.Context, named string) (string, error) {
	if named != "" {
		return named, nil
	}
	store, ok := s.store.(service.AuthExternalStorer)
	if !ok {
		return "", nativeauth.ErrExchangeRejected
	}
	providers, err := store.ListAuthIdentityProviders(ctx)
	if err != nil {
		return "", err
	}
	var ids []string
	for _, p := range providers {
		if p.Enabled && p.TokenExchange {
			ids = append(ids, p.ID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("%w: no identity provider has token exchange on", nativeauth.ErrExchangeRejected)
	}
	return "", fmt.Errorf("%w: several identity providers allow token exchange; name one in identity_provider", nativeauth.ErrExchangeRejected)
}

// bindAgentGatewayUser binds an execution as the exchanged account in the
// token's workspace. Claim admission runs inside ResolveWorkspaceAccess, so a
// person mapped by an SSO group becomes a member on first use.
func (s *Server) bindAgentGatewayUser(ctx context.Context, token *service.APIToken, userID string) (context.Context, error) {
	workspaces, ok := s.store.(service.WorkspaceStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	executions, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	live, _, err := workspaces.ResolveWorkspaceAccess(ctx, token.WorkspaceID, userID, "")
	if err != nil {
		return nil, err
	}
	policy, err := executions.GetExecutionPolicy(ctx, token.WorkspaceID)
	if err != nil || policy == nil {
		return nil, fmt.Errorf("load execution policy: %w", err)
	}
	ctx = service.WithAccessPrincipal(ctx, live)
	return s.BindRuntimeExecution(ctx, service.ExecutionProvenance{
		RunID: ulid.Make().String(), UserID: userID, WorkspaceID: token.WorkspaceID, Source: "agent-gateway",
		MembershipVersion: live.MembershipVersion, PolicyVersion: policy.Version, ActorTokenID: token.ID,
	}, s.revalidateRuntimeExecution)
}

// revalidateAgentGatewayActor is the revalidation step for agent-gateway
// runs: the acting token must still exist, be unpaused, unexpired and keep
// act_for_users, and the account must still be a live member.
func (s *Server) revalidateAgentGatewayActor(ctx context.Context, provenance service.ExecutionProvenance) (context.Context, error) {
	tokens, ok := s.store.(service.APITokenByIDStorer)
	workspaces, wok := s.store.(service.WorkspaceStorer)
	if !ok || !wok {
		return ctx, service.ErrExecutionDenied
	}
	if !agentGatewayActorLive(ctx, tokens, provenance.ActorTokenID, provenance.WorkspaceID) {
		return ctx, service.ErrExecutionDenied
	}
	live, _, err := workspaces.ResolveWorkspaceAccess(ctx, provenance.WorkspaceID, provenance.UserID, "")
	if err != nil {
		return ctx, err
	}
	return service.WithAccessPrincipal(ctx, live), nil
}

var agentGatewayActorCache sync.Map

type agentGatewayActorEntry struct {
	live    bool
	expires time.Time
}

func agentGatewayActorLive(ctx context.Context, tokens service.APITokenByIDStorer, tokenID, workspaceID string) bool {
	key := tokenID + "\x00" + workspaceID
	if v, ok := agentGatewayActorCache.Load(key); ok {
		if e := v.(agentGatewayActorEntry); time.Now().Before(e.expires) {
			return e.live
		}
	}
	token, err := tokens.GetAPITokenByID(ctx, tokenID)
	if err != nil {
		return false
	}
	live := token != nil && token.ActForUsers && !token.Paused && token.WorkspaceID == workspaceID &&
		(!token.ExpiresAt.Valid || time.Now().Before(token.ExpiresAt.V.Time))
	agentGatewayActorCache.Store(key, agentGatewayActorEntry{live: live, expires: time.Now().Add(5 * time.Second)})
	return live
}

func tokenAllowsAgent(token *service.APIToken, agent *service.Agent) bool {
	switch service.ResolveAccessMode(token.AllowedAgentsMode, token.AllowedAgents) {
	case service.AccessModeNone:
		return false
	case service.AccessModeList:
		return slices.Contains(token.AllowedAgents, agent.ID) || slices.Contains(token.AllowedAgents, agent.Name)
	}
	return true
}

// resolveGatewayAgent finds an agent by ID or name, as the bound account.
func (s *Server) resolveGatewayAgent(ctx context.Context, ref string) (*service.Agent, error) {
	agent, err := s.agentStore.GetAgent(ctx, ref)
	if err != nil || agent != nil {
		return agent, err
	}
	finder, ok := s.agentStore.(interface {
		GetAgentByName(ctx context.Context, name string) (*service.Agent, error)
	})
	if !ok {
		return nil, nil
	}
	return finder.GetAgentByName(ctx, ref)
}

// ─── Handler ───

func agentGatewayError(w http.ResponseWriter, status int, code, message string) {
	httpResponseJSON(w, map[string]any{"error": map[string]any{"message": message, "type": "invalid_request_error", "code": code}}, status)
}

// AgentGatewayChatAPI handles POST /gateway/v1/agents/{agent}/chat.
func (s *Server) AgentGatewayChatAPI(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		agentGatewayError(w, http.StatusUnauthorized, "invalid_api_key", authErr)
		return
	}
	if auth == nil || auth.token == nil || !auth.token.ActForUsers {
		agentGatewayError(w, http.StatusForbidden, "act_for_users_required", "this API token may not act on behalf of users; enable \"Act for users\" on the token")
		return
	}
	token := auth.token
	if s.agentStore == nil || s.chatSessionStore == nil {
		agentGatewayError(w, http.StatusServiceUnavailable, "unavailable", "store not configured")
		return
	}
	if limit, err := s.checkTokenLimits(r.Context(), auth); err != nil || limit != "" {
		if limit == "" {
			limit = "token limits unavailable"
		}
		agentGatewayError(w, http.StatusTooManyRequests, "token_limit_exceeded", limit)
		return
	}

	var req agentGatewayRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentGatewayBodyLimit))
	if err := dec.Decode(&req); err != nil {
		agentGatewayError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	clientTools, err := normalizeClientTools(req.ClientTools)
	if err != nil {
		agentGatewayError(w, http.StatusBadRequest, "invalid_client_tools", err.Error())
		return
	}
	if strings.TrimSpace(req.Message) == "" && len(req.Attachments) == 0 && len(req.ToolResults) == 0 {
		agentGatewayError(w, http.StatusBadRequest, "invalid_request", "send a message, attachments or tool_results")
		return
	}
	if len(req.ToolResults) > 0 && req.SessionID == "" {
		agentGatewayError(w, http.StatusBadRequest, "invalid_request", "tool_results continue a session; send its session_id")
		return
	}

	// 1. The person: verify the identity provider's token.
	providerID, err := s.exchangeProvider(r.Context(), req.IdentityProvider)
	if err != nil {
		agentGatewayError(w, http.StatusUnauthorized, "invalid_subject_token", err.Error())
		return
	}
	exchanged, err := s.tokenExchanger().Exchange(r.Context(), providerID, req.SubjectToken)
	switch {
	case errors.Is(err, nativeauth.ErrExchangeUnknownUser):
		agentGatewayError(w, http.StatusForbidden, "unknown_user", err.Error()+"; sign in to AT once, or enable provisioning on token exchange")
		return
	case errors.Is(err, nativeauth.ErrExchangeRejected):
		agentGatewayError(w, http.StatusUnauthorized, "invalid_subject_token", err.Error())
		return
	case err != nil:
		slog.Error("agent gateway: token exchange failed", "error", err)
		agentGatewayError(w, http.StatusServiceUnavailable, "identity_unavailable", "the identity provider could not be reached")
		return
	}

	// 2. Bind the run as that account in the token's workspace.
	ctx, err := s.bindAgentGatewayUser(withUserUsage(r.Context(), "agent_gateway"), token, exchanged.User.ID)
	if err != nil {
		slog.Info("agent gateway: account has no access", "user_id", exchanged.User.ID, "workspace_id", token.WorkspaceID, "error", err)
		agentGatewayError(w, http.StatusForbidden, "no_workspace_access", "this person has no access to the workspace this API token belongs to")
		return
	}
	ctx = contextWithGuardrailToken(ctx, auth)
	ctx = withPricingWorkspace(ctx, token.WorkspaceID)

	// 3. The agent, as that account; the token must allow it too.
	agent, err := s.resolveGatewayAgent(ctx, r.PathValue("agent"))
	if err != nil || agent == nil || !tokenAllowsAgent(token, agent) {
		agentGatewayError(w, http.StatusNotFound, "agent_not_found", "agent not found or not allowed for this token")
		return
	}

	// 4. The session: new, or this token's session of this person and agent.
	session, status, msg := s.agentGatewaySession(ctx, token, exchanged.User.ID, agent, req.SessionID)
	if session == nil {
		agentGatewayError(w, status, "invalid_session", msg)
		return
	}
	continuation := false
	if len(req.ToolResults) > 0 {
		if err := s.storeClientToolResults(ctx, session.ID, req.ToolResults); err != nil {
			agentGatewayError(w, http.StatusConflict, "invalid_tool_results", err.Error())
			return
		}
		continuation = strings.TrimSpace(req.Message) == "" && len(req.Attachments) == 0
	} else if open := s.openClientToolCalls(ctx, session.ID); len(open) > 0 {
		agentGatewayError(w, http.StatusConflict, "tool_results_required", fmt.Sprintf("the session is waiting for results of client tool calls %v", open))
		return
	}
	ctx = contextWithClientTools(ctx, clientTools, continuation)

	s.streamAgentGatewayTurn(w, r, ctx, session, exchanged.User.ID, req)
}

func (s *Server) agentGatewaySession(ctx context.Context, token *service.APIToken, userID string, agent *service.Agent, sessionID string) (*service.ChatSession, int, string) {
	if sessionID != "" {
		existing, err := s.chatSessionStore.GetChatSession(ctx, sessionID)
		if err != nil {
			return nil, http.StatusServiceUnavailable, "session unavailable"
		}
		// Another person's, another token's or another agent's session reads
		// as not found, so session IDs cannot be probed.
		if existing == nil || existing.OwnerUserID != userID || existing.WorkspaceID != token.WorkspaceID ||
			existing.Config.GatewayTokenID != token.ID || existing.AgentID != agent.ID {
			return nil, http.StatusNotFound, "session not found"
		}
		return existing, 0, ""
	}
	created, err := s.chatSessionStore.CreateChatSession(ctx, service.ChatSession{
		WorkspaceID: token.WorkspaceID, OwnerUserID: userID, AgentID: agent.ID,
		Name:      agent.Name + " · " + token.Name,
		Config:    service.ChatSessionConfig{GatewayTokenID: token.ID},
		CreatedBy: "token:" + token.Name, UpdatedBy: "token:" + token.Name,
	})
	if err != nil {
		slog.Error("agent gateway: create session failed", "error", err)
		return nil, http.StatusServiceUnavailable, "could not create a session"
	}
	return created, 0, ""
}

// openClientToolCalls lists client tool calls of the session's last assistant
// step that have no stored result.
func (s *Server) openClientToolCalls(ctx context.Context, sessionID string) []string {
	msgs, err := s.chatSessionStore.ListChatMessages(ctx, sessionID, 50)
	if err != nil {
		return nil
	}
	answered := map[string]bool{}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		switch m.Role {
		case "tool":
			answered[m.Data.ToolCallID] = true
		case "assistant":
			var open []string
			for _, id := range agentloop.ChatToolCallIDs(m.Data.ToolCalls) {
				if !answered[id] {
					open = append(open, id)
				}
			}
			return open
		default:
			return nil
		}
	}
	return nil
}

func (s *Server) storeClientToolResults(ctx context.Context, sessionID string, results []agentGatewayToolResult) error {
	open := s.openClientToolCalls(ctx, sessionID)
	if len(open) == 0 {
		return errors.New("the session is not waiting for client tool results")
	}
	given := map[string]bool{}
	for _, r := range results {
		if !slices.Contains(open, r.ToolCallID) {
			return fmt.Errorf("tool_call_id %q is not waiting for a result", r.ToolCallID)
		}
		if given[r.ToolCallID] {
			return fmt.Errorf("duplicate result for %q", r.ToolCallID)
		}
		given[r.ToolCallID] = true
	}
	if len(given) != len(open) {
		return fmt.Errorf("send results for every waiting call: %v", open)
	}
	blocks := make([]service.ContentBlock, 0, len(results))
	for _, r := range results {
		content := r.Content
		if len(content) > agentGatewayResultLimit {
			content = content[:agentGatewayResultLimit] + "\n... [truncated by AT]"
		}
		if r.IsError {
			content = "Error: " + content
		}
		blocks = append(blocks, service.ContentBlock{Type: "tool_result", ToolUseID: r.ToolCallID, Content: content})
	}
	s.persistToolResults(ctx, sessionID, blocks)
	return nil
}

// streamAgentGatewayTurn runs one turn and streams it as SSE. Event names are
// set on every frame so plain EventSource-style parsers can switch on them.
func (s *Server) streamAgentGatewayTurn(w http.ResponseWriter, r *http.Request, ctx context.Context, session *service.ChatSession, userID string, req agentGatewayRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		agentGatewayError(w, http.StatusInternalServerError, "unavailable", "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-AT-Session-ID", session.ID)

	var mu sync.Mutex
	write := func(event string, data any) {
		raw, _ := json.Marshal(data)
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		flusher.Flush()
	}
	write("session", map[string]any{"session_id": session.ID, "user_id": userID, "agent_id": session.AgentID})

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-r.Context().Done():
				return
			case <-t.C:
				mu.Lock()
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				mu.Unlock()
			}
		}
	}()

	var pending []map[string]any
	finished := false
	onEvent := func(ev AgenticEvent) {
		switch ev.Type {
		case "content":
			write("message", map[string]any{"content": ev.Content, "final": ev.Final})
		case "tool_call":
			write("tool_call", map[string]any{"tool_id": ev.ToolID, "tool_name": ev.ToolName})
		case "tool_progress":
			write("tool_progress", map[string]any{"tool_id": ev.ToolID, "tool_name": ev.ToolName, "content": ev.Content})
		case "tool_result":
			write("tool_result", map[string]any{"tool_id": ev.ToolID, "tool_name": ev.ToolName, "result": ev.Result})
		case "client_tool_call":
			var args map[string]any
			_ = json.Unmarshal([]byte(ev.Arguments), &args)
			pending = append(pending, map[string]any{"tool_call_id": ev.ToolID, "name": ev.ToolName, "arguments": args})
		case "waiting":
			finished = true
			write("client_tool_calls", map[string]any{"session_id": session.ID, "calls": pending})
			write("done", map[string]any{"session_id": session.ID, "status": "requires_action"})
		case "tool_confirm":
			write("tool_confirm", map[string]any{"tool_id": ev.ToolID, "tool_name": ev.ToolName, "arguments": ev.Arguments})
		case "error":
			write("error", map[string]any{"message": ev.Error})
		case "done":
			if !finished {
				finished = true
				write("done", map[string]any{"session_id": session.ID, "status": "completed"})
			}
		}
	}

	loopCtx := s.contextWithElicitation(ctx, "session:"+session.ID)
	data := service.ChatMessageData{Content: req.Message, Attachments: req.Attachments}
	if err := s.runAgenticLoopMessage(loopCtx, session.ID, data, onEvent); err != nil {
		slog.Error("agent gateway: turn failed", "session_id", session.ID, "error", err)
		write("error", map[string]any{"message": agentGatewayRunError(err)})
		if !finished {
			write("done", map[string]any{"session_id": session.ID, "status": "failed"})
		}
	}
}

func agentGatewayRunError(err error) string {
	var guard *service.GuardrailError
	switch {
	case errors.As(err, &guard):
		return guard.Error()
	case errors.Is(err, service.ErrExecutionDenied), errors.Is(err, service.ErrAccessDenied):
		return "this person may not run this agent or one of its resources"
	}
	return "the agent could not complete this turn"
}
