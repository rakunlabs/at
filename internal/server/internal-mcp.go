package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// InternalMCPHandler handles MCP protocol requests at /internal/v1/mcp/{name}.
// It serves tools from an MCP Set's own Config (RAG/HTTP/External/Skills/Builtins).
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
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.gwGenMCPListTools(w, req, virtualSrv)
	case "tools/call":
		s.gwGenMCPCallTool(w, r, req, virtualSrv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// ─── REST API Endpoints for Chat UI ───

// ListMCPSetToolsAPI handles GET /api/v1/mcp/sets/{name}/tools.
// Returns the list of tools available in an MCP Set (skills, builtins, upstreams, RAG, HTTP).
// Used by the Chat UI to discover tools when the user selects an MCP Set.
func (s *Server) ListMCPSetToolsAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "MCP set name is required", http.StatusBadRequest)
		return
	}

	tools, err := s.listMCPSetTools(name)
	if err != nil {
		slog.Error("list mcp set tools failed", "name", name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tools: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{"tools": tools}, http.StatusOK)
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

	result, err := s.callMCPSetTool(r.Context(), name, req.ToolName, req.Arguments)
	if err != nil {
		slog.Error("call mcp set tool failed", "set", name, "tool", req.ToolName, "error", err)
		httpResponse(w, fmt.Sprintf("tool execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": result},
		},
	}, http.StatusOK)
}

// ─── Direct MCPSet Resolution (no HTTP round-trip) ───

// mcpSetToVirtualServer looks up an MCPSet by name and returns a virtual MCPServer
// that can be used with the existing gwGenMCP* handlers.
func (s *Server) mcpSetToVirtualServer(name string) (*service.MCPServer, error) {
	if s.mcpSetStore == nil {
		return nil, fmt.Errorf("mcp set store not configured")
	}

	mcpSet, err := s.mcpSetStore.GetMCPSetByName(context.Background(), name)
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
// (RAG, HTTP, skills, builtins, upstreams) without any HTTP round-trip.
func (s *Server) listMCPSetTools(setName string) ([]service.Tool, error) {
	virtualSrv, err := s.mcpSetToVirtualServer(setName)
	if err != nil {
		return nil, err
	}

	var tools []service.Tool

	// RAG tools.
	for _, toolName := range virtualSrv.Config.EnabledRAGTools {
		if t := mcpRAGToolDef(toolName); t != nil {
			tools = append(tools, *t)
		}
	}

	// HTTP tools.
	for _, ht := range virtualSrv.Config.HTTPTools {
		schema := ht.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, service.Tool{
			Name:        ht.Name,
			Description: ht.Description,
			InputSchema: schema,
		})
	}

	// Skill tools.
	if s.skillStore != nil {
		for _, skillName := range virtualSrv.Config.EnabledSkills {
			skill, err := s.skillStore.GetSkillByName(context.Background(), skillName)
			if err != nil || skill == nil {
				slog.Warn("listMCPSetTools: failed to load skill", "skill", skillName, "error", err)
				continue
			}
			for _, t := range skill.Tools {
				tools = append(tools, service.Tool{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
				})
			}
		}
	}

	// Builtin tools.
	for _, toolName := range virtualSrv.Config.EnabledBuiltinTools {
		if !isKnownBuiltinTool(toolName) {
			continue
		}
		for _, bt := range builtinTools {
			if bt.Name == toolName {
				tools = append(tools, service.Tool{
					Name:        bt.Name,
					Description: bt.Description,
					InputSchema: bt.InputSchema,
				})
				break
			}
		}
	}

	// Workflow tools.
	if s.workflowStore != nil {
		for _, wfID := range virtualSrv.Config.WorkflowIDs {
			wf, err := s.workflowStore.GetWorkflow(context.Background(), wfID)
			if err != nil || wf == nil {
				slog.Warn("listMCPSetTools: failed to load workflow", "id", wfID, "error", err)
				continue
			}
			tools = append(tools, workflowToolDef(wf))
		}
	}

	// Upstream MCP tools (stdio/HTTP — these are direct clients, not round-trips to self).
	for _, upstream := range virtualSrv.Config.MCPUpstreams {
		client, err := s.newMCPClient(context.Background(), upstream)
		if err != nil {
			slog.Warn("listMCPSetTools: failed to connect to upstream", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		upstreamTools, err := client.ListTools(context.Background())
		if err != nil {
			slog.Warn("listMCPSetTools: failed to list tools from upstream", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		tools = append(tools, upstreamTools...)
	}

	return tools, nil
}

// callMCPSetTool calls a tool on an MCPSet by directly resolving its config —
// no HTTP round-trip. Returns the tool result string or an error.
func (s *Server) callMCPSetTool(ctx context.Context, setName, toolName string, args map[string]any) (string, error) {
	virtualSrv, err := s.mcpSetToVirtualServer(setName)
	if err != nil {
		return "", err
	}

	// Skill tool — most common for internal MCPs.
	if s.skillStore != nil {
		for _, skillName := range virtualSrv.Config.EnabledSkills {
			skill, err := s.skillStore.GetSkillByName(ctx, skillName)
			if err != nil || skill == nil {
				continue
			}
			for i := range skill.Tools {
				if skill.Tools[i].Name == toolName {
					return s.executeSkillTool(ctx, &skill.Tools[i], args)
				}
			}
		}
	}

	// Builtin tool.
	if slices.Contains(virtualSrv.Config.EnabledBuiltinTools, toolName) && isKnownBuiltinTool(toolName) {
		return s.dispatchBuiltinTool(ctx, toolName, args)
	}

	// Workflow tool.
	if s.workflowStore != nil {
		for _, wfID := range virtualSrv.Config.WorkflowIDs {
			wf, err := s.workflowStore.GetWorkflow(ctx, wfID)
			if err != nil || wf == nil {
				continue
			}
			if workflowToolName(wf) == toolName {
				return s.executeWorkflowTool(ctx, wf, args)
			}
		}
	}

	// Upstream MCP tool (stdio/HTTP — direct client, no self-loopback).
	for _, upstream := range virtualSrv.Config.MCPUpstreams {
		client, err := s.newMCPClient(ctx, upstream)
		if err != nil {
			continue
		}
		result, err := client.CallTool(ctx, toolName, args)
		if err != nil {
			continue
		}
		return result, nil
	}

	// RAG tool — call through the RAG service directly.
	if slices.Contains(virtualSrv.Config.EnabledRAGTools, toolName) && s.ragService != nil {
		return s.callRAGToolInline(ctx, toolName, args, virtualSrv)
	}

	// HTTP tool — execute the HTTP request directly.
	for _, ht := range virtualSrv.Config.HTTPTools {
		if ht.Name == toolName {
			return s.callHTTPToolInline(ctx, ht, args, virtualSrv)
		}
	}

	return "", fmt.Errorf("tool %q not found in MCP set %q", toolName, setName)
}

// callRAGToolInline executes a RAG tool without going through HTTP.
func (s *Server) callRAGToolInline(ctx context.Context, toolName string, args map[string]any, srv *service.MCPServer) (string, error) {
	if s.ragService == nil {
		return "", fmt.Errorf("RAG service not configured")
	}

	if toolName == "rag_search" {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required for rag_search")
		}
		numResults := 10
		if n, ok := args["num_results"].(float64); ok {
			numResults = int(n)
		}
		collectionIDs, _ := args["collection_ids"].([]any)
		var ids []string
		for _, id := range collectionIDs {
			if str, ok := id.(string); ok {
				ids = append(ids, str)
			}
		}
		if len(ids) == 0 {
			ids = srv.Config.CollectionIDs
		}
		ragSearchFn := s.ragSearchFunc()
		if ragSearchFn == nil {
			return "", fmt.Errorf("RAG search not available")
		}
		results, err := ragSearchFn(ctx, query, ids, numResults, 0)
		if err != nil {
			return "", fmt.Errorf("rag_search failed: %w", err)
		}
		data, _ := json.Marshal(results)
		return string(data), nil
	}

	return "", fmt.Errorf("RAG tool %q not supported for inline call", toolName)
}

// callHTTPToolInline executes an HTTP tool without going through the MCP gateway.
func (s *Server) callHTTPToolInline(ctx context.Context, tool service.MCPHTTPTool, args map[string]any, srv *service.MCPServer) (string, error) {
	// Resolve template for URL and body.
	resolvedURL, err := s.resolveTemplate(tool.URL, args)
	if err != nil {
		return "", fmt.Errorf("failed to resolve URL template: %w", err)
	}

	var bodyStr string
	if tool.BodyTemplate != "" {
		bodyStr, err = s.resolveTemplate(tool.BodyTemplate, args)
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
		resolved, _ := s.resolveTemplate(v, args)
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
