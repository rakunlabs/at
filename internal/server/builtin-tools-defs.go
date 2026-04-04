package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service/workflow"
)

// ctxKeySessionID is a context key for passing session ID to builtin tool executors.
type ctxKeySessionID struct{}

// contextWithSessionID stores the session ID in context.
func contextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionID{}, sessionID)
}

// sessionIDFromContext retrieves the session ID from context.
func sessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeySessionID{}).(string); ok {
		return v
	}
	return ""
}

// ctxKeySessionUserID is a context key for passing the user identity to builtin tool executors.
type ctxKeySessionUserID struct{}

// contextWithSessionUserID stores the user identity in context.
func contextWithSessionUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionUserID{}, userID)
}

// sessionUserIDFromContext retrieves the user identity from context.
func sessionUserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeySessionUserID{}).(string); ok {
		return v
	}
	return ""
}

// ctxKeyAgentID is a context key for passing the agent ID to builtin tool executors.
type ctxKeyAgentID struct{}

// contextWithAgentID stores the agent ID in context.
func contextWithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentID{}, agentID)
}

// agentIDFromContext retrieves the agent ID from context.
func agentIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyAgentID{}).(string); ok {
		return v
	}
	return ""
}

// ─── Built-in Tool Definitions ───
//
// These tools are available directly in the Chat UI without requiring an
// external MCP server or a saved Skill. They execute server-side and are
// toggled on/off by the user.

// builtinToolDef describes a built-in tool exposed to the Chat UI.
type builtinToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// knownBuiltinTools is the set of all valid builtin tool names.
var knownBuiltinTools = func() map[string]bool {
	m := make(map[string]bool, len(builtinTools))
	for _, t := range builtinTools {
		m[t.Name] = true
	}
	return m
}()

// isKnownBuiltinTool checks if a tool name is registered.
func isKnownBuiltinTool(name string) bool {
	return knownBuiltinTools[name]
}

// builtinToolDefsForWorkflow returns the builtin tool definitions in the
// workflow.BuiltinToolDef format, suitable for passing to the workflow engine.
func builtinToolDefsForWorkflow() []workflow.BuiltinToolDef {
	defs := make([]workflow.BuiltinToolDef, len(builtinTools))
	for i, bt := range builtinTools {
		defs[i] = workflow.BuiltinToolDef{
			Name:        bt.Name,
			Description: bt.Description,
			InputSchema: bt.InputSchema,
		}
	}
	return defs
}

// ─── API Handlers ───

// BuiltinToolListAPI handles GET /api/v1/mcp/builtin-tools.
// Returns the static list of server-side built-in tool definitions.
func (s *Server) BuiltinToolListAPI(w http.ResponseWriter, r *http.Request) {
	httpResponseJSON(w, map[string]any{
		"tools": builtinTools,
	}, http.StatusOK)
}

// builtinCallRequest is the request body for BuiltinToolCallAPI.
type builtinCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// builtinCallResponse is the response body for BuiltinToolCallAPI.
type builtinCallResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// BuiltinToolCallAPI handles POST /api/v1/mcp/call-builtin-tool.
// Dispatches to the appropriate built-in tool executor by name.
func (s *Server) BuiltinToolCallAPI(w http.ResponseWriter, r *http.Request) {
	var req builtinCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	var result string
	var execErr error

	if !isKnownBuiltinTool(req.Name) {
		httpResponse(w, fmt.Sprintf("unknown built-in tool: %q", req.Name), http.StatusBadRequest)
		return
	}

	ctx := contextWithSessionID(r.Context(), getSessionID(r))
	result, execErr = s.dispatchBuiltinTool(ctx, req.Name, req.Arguments)

	resp := builtinCallResponse{Result: result}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("builtin tool: execution failed", "tool", req.Name, "error", execErr)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}
