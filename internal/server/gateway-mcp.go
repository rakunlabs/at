package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// GatewayMCPSSEHandler handles GET requests to MCP endpoints using the SSE transport.
// It opens a Server-Sent Events stream, sends the POST endpoint URL, then keeps alive.
// MCP clients that use SSE transport (e.g. OpenCode) connect via GET first.
func (s *Server) GatewayMCPSSEHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	if _, ok := s.authorizeGatewayMCPServer(w, r); !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Build the POST endpoint URL (same path the client should POST JSON-RPC to).
	postURL := r.URL.Path

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send the endpoint event — tells the client where to POST requests.
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", postURL)
	flusher.Flush()

	// Keep the SSE connection alive until the client disconnects.
	// Send periodic pings to prevent timeouts.
	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// GatewayMCPHandler handles MCP protocol requests at /gateway/v1/mcp/{name}.
// Each named endpoint can expose custom HTTP tools, skills, builtins, upstreams, or workflows.
// Auth uses the same Bearer token mechanism as the gateway chat completions endpoint unless the named server is public.
func (s *Server) GatewayMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mcpSrv, ok := s.authorizeGatewayMCPServer(w, r)
	if !ok {
		return
	}

	// Parse the JSON-RPC request.
	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Route by method.
	switch req.Method {
	case "initialize":
		s.gwGenMCPInitialize(w, req, mcpSrv)
	case "notifications/initialized":
		// Per MCP Streamable HTTP, notifications are acknowledged with 202.
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		s.gwGenMCPListTools(r.Context(), w, req, mcpSrv)
	case "tools/call":
		s.gwGenMCPCallTool(w, r, req, mcpSrv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) authorizeGatewayMCPServer(w http.ResponseWriter, r *http.Request) (*service.MCPServer, bool) {
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "mcp server name is required", http.StatusBadRequest)
		return nil, false
	}

	auth, errMsg := s.authenticateRequest(r)

	mcpSrv, err := s.mcpServerStore.GetMCPServerByName(r.Context(), name)
	if err != nil {
		slog.Error("get mcp server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up MCP server", http.StatusInternalServerError)
		return nil, false
	}

	if auth == nil {
		if mcpSrv == nil || !mcpSrv.Public {
			httpResponse(w, errMsg, http.StatusUnauthorized)
			return nil, false
		}
		return mcpSrv, true
	}

	if mcpSrv == nil {
		httpResponse(w, fmt.Sprintf("MCP server %q not found", name), http.StatusNotFound)
		return nil, false
	}
	if mcpSrv.Public {
		return mcpSrv, true
	}

	if auth.token != nil {
		mcpMode := service.ResolveAccessMode(auth.token.AllowedMCPsMode, auth.token.AllowedMCPs)
		if mcpMode == service.AccessModeNone {
			httpResponse(w, "token does not have access to any MCP servers", http.StatusForbidden)
			return nil, false
		}
		if mcpMode == service.AccessModeList && !slices.Contains(auth.token.AllowedMCPs, name) {
			httpResponse(w, fmt.Sprintf("token does not have access to MCP server %q", name), http.StatusForbidden)
			return nil, false
		}
	}

	return mcpSrv, true
}

// ─── Initialize ───

// negotiateMCPProtocolVersion echoes the client's requested protocol
// version when it is a revision AT's tools-only servers can satisfy;
// otherwise it falls back to the 2024-11-05 baseline (per MCP version
// negotiation rules the server responds with a version it supports).
func negotiateMCPProtocolVersion(params any) string {
	supported := map[string]bool{
		"2024-11-05": true,
		"2025-03-26": true,
		"2025-06-18": true,
	}
	if m, ok := params.(map[string]any); ok {
		if v, ok := m["protocolVersion"].(string); ok && supported[v] {
			return v
		}
	}
	return "2024-11-05"
}

func (s *Server) gwGenMCPInitialize(w http.ResponseWriter, req service.MCPRequest, srv *service.MCPServer) {
	description := srv.Description
	if description == "" {
		description = srv.Config.Description
	}
	if description == "" {
		description = fmt.Sprintf("MCP server: %s", srv.Name)
	}

	result := map[string]any{
		"protocolVersion": negotiateMCPProtocolVersion(req.Params),
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name":    fmt.Sprintf("at-mcp-%s", srv.Name),
			"version": "1.0.0",
		},
	}

	mcpResult(w, req.ID, result)
}

// ─── List Tools ───

func (s *Server) gwGenMCPListTools(ctx context.Context, w http.ResponseWriter, req service.MCPRequest, srv *service.MCPServer) {
	runtime := s.newMCPRuntimeBuilder().buildGateway(ctx, srv)
	defer closeMCPRuntime(ctx, runtime)
	mcpResult(w, req.ID, map[string]any{"tools": runtime.ListTools(ctx)})
}

// ─── Call Tool ───

func (s *Server) gwGenMCPCallTool(w http.ResponseWriter, r *http.Request, req service.MCPRequest, srv *service.MCPServer) {
	paramsRaw, err := json.Marshal(req.Params)
	if err != nil {
		mcpError(w, req.ID, -32602, "invalid params")
		return
	}

	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		mcpError(w, req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
		return
	}

	if params.Name == "" {
		mcpError(w, req.ID, -32602, "tool name is required")
		return
	}

	runtime := s.newMCPRuntimeBuilder().buildGateway(r.Context(), srv)
	defer closeMCPRuntime(r.Context(), runtime)
	result, err := runtime.CallTool(r.Context(), params.Name, params.Arguments)
	if err != nil {
		var notFound *mcpToolNotFoundError
		if errors.As(err, &notFound) {
			mcpError(w, req.ID, -32602, err.Error())
			return
		}
		mcpError(w, req.ID, -32000, err.Error())
		return
	}
	mcpResult(w, req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": result},
		},
	})
}

// newMCPClient creates an MCPClient for the given upstream, dispatching to
// either the stdio process manager or the HTTP client based on config.
func (s *Server) newMCPClient(ctx context.Context, upstream service.MCPUpstream) (service.MCPClient, error) {
	lease, err := s.acquireMCPClient(ctx, upstream)
	return lease.client, err
}

func (s *Server) acquireMCPClient(ctx context.Context, upstream service.MCPUpstream) (mcpClientLease, error) {
	// Resolve {{var:key}} references in env values and args.
	if s.variableStore != nil {
		upstream = s.resolveUpstreamVarsContext(ctx, upstream)
	}

	if upstream.Command != "" {
		client, err := s.stdioManager.GetOrCreate(upstream)
		return mcpClientLease{client: client, owned: false}, err
	}
	var opts []service.HTTPMCPClientOption
	if len(upstream.Headers) > 0 {
		opts = append(opts, service.WithHeaders(upstream.Headers))
	}
	client, err := service.NewHTTPMCPClient(ctx, upstream.URL, opts...)
	return mcpClientLease{client: client, owned: true}, err
}

// ─── HTTP Tool Execution ───

func (s *Server) gwGenMCPCallHTTPTool(w http.ResponseWriter, r *http.Request, id int, tool service.MCPHTTPTool, args map[string]any, srv *service.MCPServer) {
	text, err := s.callGatewayMCPHTTPTool(r.Context(), tool, args)
	if err != nil {
		mcpError(w, id, -32000, err.Error())
		return
	}
	mcpResult(w, id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
}

func (s *Server) callGatewayMCPHTTPTool(ctx context.Context, tool service.MCPHTTPTool, args map[string]any) (string, error) {
	if args == nil {
		args = make(map[string]any)
	}

	// Resolve variable values for headers (support {{var:key}} syntax).
	resolvedHeaders := make(map[string]string, len(tool.Headers))
	for k, v := range tool.Headers {
		resolved, err := s.resolveTemplateContext(ctx, v, args)
		if err != nil {
			return "", fmt.Errorf("failed to resolve header %q: %w", k, err)
		}
		resolvedHeaders[k] = resolved
	}

	// Resolve URL template.
	resolvedURL, err := s.resolveTemplateContext(ctx, tool.URL, args)
	if err != nil {
		return "", fmt.Errorf("failed to resolve URL template: %w", err)
	}

	// Resolve body template.
	var bodyReader io.Reader
	if tool.BodyTemplate != "" {
		resolvedBody, err := s.resolveTemplateContext(ctx, tool.BodyTemplate, args)
		if err != nil {
			return "", fmt.Errorf("failed to resolve body template: %w", err)
		}
		bodyReader = strings.NewReader(resolvedBody)
	} else if tool.Method == "POST" || tool.Method == "PUT" || tool.Method == "PATCH" {
		// If no body template but method expects a body, send args as JSON.
		data, _ := json.Marshal(args)
		bodyReader = bytes.NewReader(data)
	}

	method := strings.ToUpper(tool.Method)
	if method == "" {
		method = "GET"
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, resolvedURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	for k, v := range resolvedHeaders {
		httpReq.Header.Set(k, v)
	}

	// Default Content-Type for POST/PUT/PATCH if not set.
	if (method == "POST" || method == "PUT" || method == "PATCH") && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body with 1MB limit.
	const maxBody = 1048576
	limitReader := io.LimitReader(resp.Body, int64(maxBody+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	truncated := false
	if len(body) > maxBody {
		body = body[:maxBody]
		truncated = true
	}

	// Build result.
	result := map[string]any{
		"status":      resp.StatusCode,
		"status_text": resp.Status,
		"body":        string(body),
	}
	if truncated {
		result["truncated"] = true
	}

	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// executeSkillTool runs a skill tool's handler (bash or JS) and returns the result.
func (s *Server) executeSkillTool(ctx context.Context, tool *service.Tool, args map[string]any) (string, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "inline_tool", Name: tool.Name}); err != nil {
		return "", err
	}
	if tool.Handler == "" {
		return "", fmt.Errorf("tool %q has no handler", tool.Name)
	}

	if tool.HandlerType == "bash" {
		var varLister workflow.VarLister
		if s.variableStore != nil {
			varLister = func() (map[string]string, error) {
				q, err := runtimeWorkspaceQuery(ctx)
				if err != nil {
					return nil, err
				}
				vars, err := s.variableStore.ListVariables(ctx, q)
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
		return workflow.ExecuteBashHandler(ctx, tool.Handler, args, varLister, 0)
	}

	// Default: JS handler.
	var varLookup workflow.VarLookup
	if s.variableStore != nil {
		varLookup = func(key string) (string, error) {
			if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.read", ResourceID: key}); err != nil {
				return "", err
			}
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
	return workflow.ExecuteJSHandlerContext(ctx, tool.Handler, args, varLookup)
}

// resolveVarRefs resolves {{var:key}} references in val against the
// variable store. Unresolvable references are left in place (with a
// warning) so misconfiguration is visible rather than silently empty.
func (s *Server) resolveVarRefs(val string) string {
	return s.resolveVarRefsContext(context.Background(), val)
}

func (s *Server) resolveVarRefsContext(ctx context.Context, val string) string {
	if !strings.Contains(val, "{{var:") {
		return val
	}
	if s.variableStore == nil {
		return val
	}
	resolved := val
	for {
		idx := strings.Index(resolved, "{{var:")
		if idx == -1 {
			break
		}
		end := strings.Index(resolved[idx:], "}}")
		if end == -1 {
			break
		}
		key := resolved[idx+len("{{var:") : idx+end]
		v, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil || v == nil {
			slog.Warn("failed to resolve variable reference", "key", key, "error", err)
			break
		}
		resolved = resolved[:idx] + v.Value + resolved[idx+end+2:]
	}
	return resolved
}

// resolveUpstreamVars resolves {{var:key}} references in an MCPUpstream's
// env values and args. This allows MCP server configs to reference secrets
// stored in the variable store (e.g. MINIMAX_API_KEY={{var:minimax_api_key}}).
func (s *Server) resolveUpstreamVars(upstream service.MCPUpstream) service.MCPUpstream {
	return s.resolveUpstreamVarsContext(context.Background(), upstream)
}

func (s *Server) resolveUpstreamVarsContext(ctx context.Context, upstream service.MCPUpstream) service.MCPUpstream {
	resolve := func(value string) string { return s.resolveVarRefsContext(ctx, value) }

	if len(upstream.Env) > 0 {
		resolved := make(map[string]string, len(upstream.Env))
		for k, v := range upstream.Env {
			resolved[k] = resolve(v)
		}
		upstream.Env = resolved
	}

	if len(upstream.Args) > 0 {
		resolved := make([]string, len(upstream.Args))
		for i, v := range upstream.Args {
			resolved[i] = resolve(v)
		}
		upstream.Args = resolved
	}

	if upstream.URL != "" {
		upstream.URL = resolve(upstream.URL)
	}

	return upstream
}

// resolveTemplate resolves a Go text/template string with args as data.
// Also supports {{var:key}} syntax to look up variables from the variable store.
func (s *Server) resolveTemplate(tmplStr string, args map[string]any) (string, error) {
	return s.resolveTemplateContext(context.Background(), tmplStr, args)
}

func (s *Server) resolveTemplateContext(ctx context.Context, tmplStr string, args map[string]any) (string, error) {
	// First resolve {{var:key}} references.
	resolved := tmplStr
	if s.variableStore != nil && strings.Contains(resolved, "{{var:") {
		for {
			idx := strings.Index(resolved, "{{var:")
			if idx == -1 {
				break
			}
			end := strings.Index(resolved[idx:], "}}")
			if end == -1 {
				break
			}
			key := resolved[idx+len("{{var:") : idx+end]
			v, err := s.variableStore.GetVariableByKey(ctx, key)
			if err != nil {
				return "", fmt.Errorf("variable %q lookup failed: %w", key, err)
			}
			val := ""
			if v != nil {
				val = v.Value
			}
			resolved = resolved[:idx] + val + resolved[idx+end+2:]
		}
	}

	// Then resolve Go template placeholders via mugo (internal/render), which
	// brings the full sprig/mugo function set — strings, math, crypto, json,
	// yaml, regex, time, etc. See https://rytsh.io/mugo/functions/reference.html
	if !strings.Contains(resolved, "{{") {
		return resolved, nil
	}

	out, err := render.ExecuteWithData(resolved, args)
	if err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return string(out), nil
}

// ─── MCP Response Helpers ───

func mcpResult(w http.ResponseWriter, id int, result any) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		mcpError(w, id, -32603, fmt.Sprintf("marshal result: %v", err))
		return
	}

	resp := service.MCPResponse{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  resultJSON,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func mcpError(w http.ResponseWriter, id int, code int, message string) {
	resp := service.MCPResponse{
		Jsonrpc: "2.0",
		ID:      id,
		Error: &service.MCPError{
			Code:    code,
			Message: message,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
