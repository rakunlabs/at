package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// InternalMCPHandler handles MCP protocol requests at /internal/v1/mcp/{name}.
// It serves tools from an MCP Set's own Config (HTTP/External/Skills/Builtins).
// This endpoint has NO authentication — it is only reachable internally by agents
// and is not exposed under /gateway/ so external clients cannot access it.
func (s *Server) InternalMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "mcp set store not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "mcp set name is required", http.StatusBadRequest)
		return
	}

	mcpSet, err := s.mcpSetStore.GetMCPSetByName(r.Context(), name)
	if err != nil {
		slog.Error("internal mcp: get mcp set failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up MCP set", http.StatusInternalServerError)
		return
	}
	if mcpSet == nil {
		httpResponse(w, fmt.Sprintf("MCP set %q not found", name), http.StatusNotFound)
		return
	}

	// Build a virtual MCPServer from the set's config to reuse existing handlers.
	virtualSrv := &service.MCPServer{
		Name:   mcpSet.Name,
		Config: mcpSet.Config,
	}

	// Parse the JSON-RPC request.
	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Route by method — reuse the existing gateway MCP handlers.
	switch req.Method {
	case "initialize":
		s.gwGenMCPInitialize(w, req, virtualSrv)
	case "notifications/initialized":
		// Per MCP Streamable HTTP, notifications are acknowledged with 202.
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		s.gwGenMCPListTools(r.Context(), w, req, virtualSrv)
	case "tools/call":
		s.gwGenMCPCallTool(w, r, req, virtualSrv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// ─── REST API Endpoints for Chat UI ───

// ListMCPSetToolsAPI handles GET /api/v1/mcp/sets/{name}/tools.
// Returns the list of tools available in an MCP Set (skills, builtins, upstreams, HTTP).
// Used by the Chat UI to discover tools when the user selects an MCP Set.
func (s *Server) ListMCPSetToolsAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "MCP set name is required", http.StatusBadRequest)
		return
	}

	// Tool discovery opens configured upstreams and therefore belongs to the
	// execution plane, not the management/read DTO plane. Bind the caller so the
	// workspace execution policy is enforced, then resolve the unredacted set
	// through ResolveMCPSetForUse. Using GetMCPSetByName here strips upstreams for
	// callers without credentials.manage and incorrectly reports zero tools.
	ctx, bindErr := s.bindRuntimePrincipal(r.Context(), "tool")
	if bindErr != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: name}); err != nil {
		httpResponse(w, "mcp set execution denied", http.StatusForbidden)
		return
	}

	runtime, err := s.newMCPRuntimeBuilder().buildSet(ctx, name)
	if err != nil {
		slog.Error("list mcp set tools failed", "name", name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tools: %v", err), http.StatusInternalServerError)
		return
	}
	defer closeMCPRuntime(ctx, runtime)
	tools := runtime.ListTools(ctx)

	httpResponseJSON(w, map[string]any{"tools": tools, "warnings": runtime.Diagnostics()}, http.StatusOK)
}

// CallMCPSetToolAPI handles POST /api/v1/mcp/sets/{name}/call-tool.
// Executes a tool on an MCP Set and returns the result.
// Used by the Chat UI to call MCP Set tools (especially upstream tools like MiniMax
// that require server-side stdio processes).
func (s *Server) CallMCPSetToolAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "MCP set name is required", http.StatusBadRequest)
		return
	}

	var req struct {
		ToolName  string         `json:"tool_name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.ToolName == "" {
		httpResponse(w, "tool_name is required", http.StatusBadRequest)
		return
	}

	// The set's own tools admit individually inside the runtime (skill tools
	// through CheckExecution, built-ins through dispatchBuiltinTool), and all of
	// those are fail-closed on an unbound context. Bind the caller's runtime
	// identity here so the workspace execution policy decides, and gate the set
	// itself the way the agent loops do.
	ctx, bindErr := s.bindRuntimePrincipal(r.Context(), "tool")
	if bindErr != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: name}); err != nil {
		httpResponse(w, "mcp set execution denied", http.StatusForbidden)
		return
	}

	result, err := s.callMCPSetTool(ctx, name, req.ToolName, req.Arguments)
	if err != nil {
		slog.Error("call mcp set tool failed", "set", name, "tool", req.ToolName, "error", err)
		httpResponse(w, fmt.Sprintf("tool execution failed: %v", err), mcpToolErrorStatus(err))
		return
	}

	httpResponseJSON(w, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": result},
		},
	}, http.StatusOK)
}

// Runtime routing wraps and joins executor errors. Preserve their admission
// semantics rather than presenting invalid resource references as retryable 500s.
func mcpToolErrorStatus(err error) int {
	switch {
	case errors.Is(err, service.ErrAccessResourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrWorkspaceRequired):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, service.ErrWorkspaceConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// ─── Direct MCPSet Resolution (no HTTP round-trip) ───

// mcpSetToVirtualServer looks up an MCPSet by name and returns a virtual MCPServer
// that can be used with the existing gwGenMCP* handlers.
func (s *Server) mcpSetToVirtualServer(ctx context.Context, name string) (*service.MCPServer, error) {
	if s.mcpSetStore == nil {
		return nil, fmt.Errorf("mcp set store not configured")
	}

	var (
		mcpSet *service.MCPSet
		err    error
	)
	if credentials, ok := s.mcpSetStore.(service.WorkspaceCredentialStorer); ok {
		mcpSet, err = credentials.ResolveMCPSetForUse(ctx, name)
	} else {
		// Non-persistent test stores do not carry the runtime credential seam.
		mcpSet, err = s.mcpSetStore.GetMCPSetByName(ctx, name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP set %q: %w", name, err)
	}
	if mcpSet == nil {
		return nil, fmt.Errorf("MCP set %q not found", name)
	}

	return &service.MCPServer{
		Name:   mcpSet.Name,
		Config: mcpSet.Config,
	}, nil
}

// listMCPSetTools returns all tools from an MCPSet by directly resolving its config
// (HTTP, skills, builtins, upstreams) without any HTTP round-trip.
func (s *Server) listMCPSetTools(ctx context.Context, setName string) ([]service.Tool, error) {
	runtime, err := s.newMCPRuntimeBuilder().buildSet(ctx, setName)
	if err != nil {
		return nil, err
	}
	defer closeMCPRuntime(ctx, runtime)
	return runtime.ListTools(ctx), nil
}

// callMCPSetTool calls a tool on an MCPSet by directly resolving its config —
// no HTTP round-trip. Returns the tool result string or an error.
func (s *Server) callMCPSetTool(ctx context.Context, setName, toolName string, args map[string]any) (string, error) {
	runtime, err := s.newMCPRuntimeBuilder().buildSet(ctx, setName)
	if err != nil {
		return "", err
	}
	defer closeMCPRuntime(ctx, runtime)
	return runtime.CallTool(ctx, toolName, args)
}

// callHTTPToolInline executes an HTTP tool without going through the MCP gateway.
func (s *Server) callHTTPToolInline(ctx context.Context, tool service.MCPHTTPTool, args map[string]any, srv *service.MCPServer) (string, error) {
	// Resolve template for URL and body.
	resolvedURL, err := s.resolveTemplateContext(ctx, tool.URL, args)
	if err != nil {
		return "", fmt.Errorf("failed to resolve URL template: %w", err)
	}

	var bodyStr string
	if tool.BodyTemplate != "" {
		bodyStr, err = s.resolveTemplateContext(ctx, tool.BodyTemplate, args)
		if err != nil {
			return "", fmt.Errorf("failed to resolve body template: %w", err)
		}
	}

	method := tool.Method
	if method == "" {
		method = "GET"
	}

	var bodyReader *strings.Reader
	if bodyStr != "" {
		bodyReader = strings.NewReader(bodyStr)
	}

	var req *http.Request
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, resolvedURL, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, resolvedURL, nil)
	}
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range tool.Headers {
		resolved, _ := s.resolveTemplateContext(ctx, v, args)
		req.Header.Set(k, resolved)
	}
	if bodyStr != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(respBody), nil
}
