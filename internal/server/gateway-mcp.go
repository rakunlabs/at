package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// GatewayMCPHandler handles MCP protocol requests at /gateway/v1/mcp/{name}.
// Each named endpoint can expose RAG tools, custom HTTP tools, or both.
// Auth uses the same Bearer token mechanism as the gateway chat completions endpoint.
func (s *Server) GatewayMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate.
	auth, errMsg := s.authenticateRequest(r)
	if auth == nil {
		httpResponse(w, errMsg, http.StatusUnauthorized)
		return
	}

	// Look up the named MCP server config.
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "mcp server name is required", http.StatusBadRequest)
		return
	}

	// Check token scoping for MCP servers.
	if auth.token != nil {
		mcpMode := service.ResolveAccessMode(auth.token.AllowedRAGMCPsMode, auth.token.AllowedRAGMCPs)
		if mcpMode == service.AccessModeNone {
			httpResponse(w, "token does not have access to any MCP servers", http.StatusForbidden)
			return
		}
		if mcpMode == service.AccessModeList {
			if !slices.Contains(auth.token.AllowedRAGMCPs, name) {
				httpResponse(w, fmt.Sprintf("token does not have access to MCP server %q", name), http.StatusForbidden)
				return
			}
		}
	}

	mcpSrv, err := s.mcpServerStore.GetMCPServerByName(r.Context(), name)
	if err != nil {
		slog.Error("get mcp server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up MCP server", http.StatusInternalServerError)
		return
	}
	if mcpSrv == nil {
		httpResponse(w, fmt.Sprintf("MCP server %q not found", name), http.StatusNotFound)
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
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.gwGenMCPListTools(w, req, mcpSrv)
	case "tools/call":
		s.gwGenMCPCallTool(w, r, req, mcpSrv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// ─── Initialize ───

func (s *Server) gwGenMCPInitialize(w http.ResponseWriter, req service.MCPRequest, srv *service.MCPServer) {
	description := srv.Config.Description
	if description == "" {
		description = fmt.Sprintf("MCP server: %s", srv.Name)
	}

	result := map[string]any{
		"protocolVersion": "2024-11-05",
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

func (s *Server) gwGenMCPListTools(w http.ResponseWriter, req service.MCPRequest, srv *service.MCPServer) {
	var tools []service.Tool

	// Add RAG tools if enabled.
	for _, toolName := range srv.Config.EnabledRAGTools {
		if t := mcpRAGToolDef(toolName); t != nil {
			tools = append(tools, *t)
		}
	}

	// Add custom HTTP tools.
	for _, ht := range srv.Config.HTTPTools {
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

	// Add skill tools.
	if s.skillStore != nil {
		for _, skillName := range srv.Config.EnabledSkills {
			skill, err := s.skillStore.GetSkillByName(context.Background(), skillName)
			if err != nil || skill == nil {
				slog.Warn("failed to load skill for MCP server", "skill", skillName, "error", err)
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

	// Add tools from upstream MCP servers.
	for _, upstream := range srv.Config.MCPUpstreams {
		client, err := s.newMCPClient(context.Background(), upstream)
		if err != nil {
			slog.Warn("failed to connect to upstream MCP server", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		upstreamTools, err := client.ListTools(context.Background())
		if err != nil {
			slog.Warn("failed to list tools from upstream MCP server", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		tools = append(tools, upstreamTools...)
	}

	// Add tools from referenced MCPs.
	if s.mcpSetStore != nil {
		for _, mcpName := range srv.Config.MCPs {
			mcpURLs := s.resolveMCPSetURLs(mcpName)
			for _, url := range mcpURLs {
				client, err := service.NewHTTPMCPClient(context.Background(), url)
				if err != nil {
					slog.Warn("failed to connect to MCP", "mcp", mcpName, "url", url, "error", err)
					continue
				}
				mcpTools, err := client.ListTools(context.Background())
				if err != nil {
					slog.Warn("failed to list tools from MCP", "mcp", mcpName, "url", url, "error", err)
					continue
				}
				tools = append(tools, mcpTools...)
			}
		}
	}

	// Add enabled builtin tools.
	for _, toolName := range srv.Config.EnabledBuiltinTools {
		if !isKnownBuiltinTool(toolName) {
			slog.Warn("unknown builtin tool in MCP server config", "tool", toolName, "server", srv.Name)
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

	mcpResult(w, req.ID, map[string]any{"tools": tools})
}

// mcpRAGToolDef returns the MCP tool definition for a RAG tool name.
func mcpRAGToolDef(name string) *service.Tool {
	switch name {
	case "rag_search":
		return &service.Tool{
			Name:        "rag_search",
			Description: "Search documents in the RAG knowledge base by semantic similarity. Returns relevant document chunks with metadata.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":           map[string]any{"type": "string", "description": "The natural language search query"},
					"collection_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional: filter by collection IDs"},
					"num_results":     map[string]any{"type": "integer", "description": "Number of results to return (default: 10)"},
					"score_threshold": map[string]any{"type": "number", "description": "Minimum similarity score (0-1)"},
				},
				"required": []string{"query"},
			},
		}
	case "rag_list_collections":
		return &service.Tool{
			Name:        "rag_list_collections",
			Description: "List all available RAG document collections with their IDs and names.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}
	case "rag_fetch_source":
		return &service.Tool{
			Name:        "rag_fetch_source",
			Description: "Fetch the original full content of a document by its source identifier.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source":     map[string]any{"type": "string", "description": "The source identifier (URL or path)"},
					"repo_url":   map[string]any{"type": "string", "description": "Git repository URL"},
					"commit_sha": map[string]any{"type": "string", "description": "Git commit SHA"},
					"path":       map[string]any{"type": "string", "description": "File path within the repository"},
				},
				"required": []string{"source"},
			},
		}
	case "rag_search_and_fetch":
		return &service.Tool{
			Name:        "rag_search_and_fetch",
			Description: "Search + automatically fetch top result files. Returns both chunks and complete original files.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":          map[string]any{"type": "string", "description": "The natural language search query"},
					"collection_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional: filter by collection IDs"},
					"num_results":    map[string]any{"type": "integer", "description": "Number of results to return (default: 10)"},
				},
				"required": []string{"query"},
			},
		}
	case "rag_search_and_fetch_org":
		return &service.Tool{
			Name:        "rag_search_and_fetch_org",
			Description: "Search + return only full source files (no chunks). Identifies relevant files via semantic search.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":          map[string]any{"type": "string", "description": "The natural language search query"},
					"collection_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional: filter by collection IDs"},
					"num_results":    map[string]any{"type": "integer", "description": "Number of results to return (default: 10)"},
				},
				"required": []string{"query"},
			},
		}
	}
	return nil
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

	// Check if it's a RAG tool.
	if slices.Contains(srv.Config.EnabledRAGTools, params.Name) {
		s.gwGenMCPCallRAGTool(w, r, req.ID, params.Name, params.Arguments, srv)
		return
	}

	// Check if it's an HTTP tool.
	for _, ht := range srv.Config.HTTPTools {
		if ht.Name == params.Name {
			s.gwGenMCPCallHTTPTool(w, r, req.ID, ht, params.Arguments, srv)
			return
		}
	}

	// Check if it's a skill tool.
	if s.skillStore != nil {
		for _, skillName := range srv.Config.EnabledSkills {
			skill, err := s.skillStore.GetSkillByName(r.Context(), skillName)
			if err != nil || skill == nil {
				continue
			}
			for i := range skill.Tools {
				if skill.Tools[i].Name == params.Name {
					result, err := s.executeSkillTool(r.Context(), &skill.Tools[i], params.Arguments)
					if err != nil {
						mcpError(w, req.ID, -32000, fmt.Sprintf("skill tool execution failed: %v", err))
						return
					}
					mcpResult(w, req.ID, map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": result},
						},
					})
					return
				}
			}
		}
	}

	// Try upstream MCP servers.
	for _, upstream := range srv.Config.MCPUpstreams {
		client, err := s.newMCPClient(r.Context(), upstream)
		if err != nil {
			slog.Warn("failed to connect to upstream MCP server for call", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		result, err := client.CallTool(r.Context(), params.Name, params.Arguments)
		if err != nil {
			slog.Warn("upstream MCP call failed", "upstream", upstream.URL+upstream.Command, "tool", params.Name, "error", err)
			continue
		}
		mcpResult(w, req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": result},
			},
		})
		return
	}

	// Try referenced MCPs.
	if s.mcpSetStore != nil {
		for _, mcpName := range srv.Config.MCPs {
			mcpURLs := s.resolveMCPSetURLs(mcpName)
			for _, url := range mcpURLs {
				client, err := service.NewHTTPMCPClient(r.Context(), url)
				if err != nil {
					continue
				}
				result, err := client.CallTool(r.Context(), params.Name, params.Arguments)
				if err != nil {
					continue
				}
				mcpResult(w, req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": result},
					},
				})
				return
			}
		}
	}

	// Try enabled builtin tools.
	if slices.Contains(srv.Config.EnabledBuiltinTools, params.Name) && isKnownBuiltinTool(params.Name) {
		result, err := s.dispatchBuiltinTool(r.Context(), params.Name, params.Arguments)
		if err != nil {
			mcpError(w, req.ID, -32000, fmt.Sprintf("builtin tool execution failed: %v", err))
			return
		}
		mcpResult(w, req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": result},
			},
		})
		return
	}

	mcpError(w, req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
}

// resolveMCPSetURLs resolves an MCP name to a list of gateway URLs
// (from referenced MCP servers, custom URLs, and own-tools gateway).
func (s *Server) resolveMCPSetURLs(mcpName string) []string {
	if s.mcpSetStore == nil {
		return nil
	}

	set, err := s.mcpSetStore.GetMCPSetByName(context.Background(), mcpName)
	if err != nil || set == nil {
		slog.Warn("failed to resolve MCP", "mcp", mcpName, "error", err)
		return nil
	}

	var urls []string
	for _, serverName := range set.Servers {
		urls = append(urls, fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp/%s", s.config.Port, s.config.BasePath, serverName))
	}
	urls = append(urls, set.URLs...)
	if mcpSetHasOwnTools(set.Config) {
		urls = append(urls, fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp-set/%s", s.config.Port, s.config.BasePath, mcpName))
	}
	return urls
}

// newMCPClient creates an MCPClient for the given upstream, dispatching to
// either the stdio process manager or the HTTP client based on config.
func (s *Server) newMCPClient(ctx context.Context, upstream service.MCPUpstream) (service.MCPClient, error) {
	if upstream.Command != "" {
		return s.stdioManager.GetOrCreate(upstream)
	}
	var opts []service.HTTPMCPClientOption
	if len(upstream.Headers) > 0 {
		opts = append(opts, service.WithHeaders(upstream.Headers))
	}
	return service.NewHTTPMCPClient(ctx, upstream.URL, opts...)
}

// ─── RAG Tool Dispatch ───

func (s *Server) gwGenMCPCallRAGTool(w http.ResponseWriter, r *http.Request, id int, toolName string, args map[string]any, srv *service.MCPServer) {
	if s.ragService == nil {
		mcpError(w, id, -32000, "RAG service not configured")
		return
	}

	// Build a temporary RAGMCPServer for the existing handler functions.
	ragSrv := &service.RAGMCPServer{
		Name: srv.Name,
		Config: service.RAGMCPServerConfig{
			CollectionIDs:     srv.Config.CollectionIDs,
			EnabledTools:      srv.Config.EnabledRAGTools,
			FetchMode:         srv.Config.FetchMode,
			GitCacheDir:       srv.Config.GitCacheDir,
			DefaultNumResults: srv.Config.DefaultNumResults,
			TokenVariable:     srv.Config.TokenVariable,
			TokenUser:         srv.Config.TokenUser,
			SSHKeyVariable:    srv.Config.SSHKeyVariable,
		},
	}

	switch toolName {
	case "rag_search":
		s.gwMCPSearch(w, r, id, args, ragSrv)
	case "rag_list_collections":
		s.gwMCPListCollections(w, r, id, ragSrv)
	case "rag_fetch_source":
		s.gwMCPFetchSource(w, r, id, args, ragSrv)
	case "rag_search_and_fetch":
		s.gwMCPSearchAndFetch(w, r, id, args, ragSrv)
	case "rag_search_and_fetch_org":
		s.gwMCPFetchSourcesOrg(w, r, id, args, ragSrv)
	default:
		mcpError(w, id, -32602, fmt.Sprintf("unknown RAG tool: %s", toolName))
	}
}

// ─── HTTP Tool Execution ───

func (s *Server) gwGenMCPCallHTTPTool(w http.ResponseWriter, r *http.Request, id int, tool service.MCPHTTPTool, args map[string]any, srv *service.MCPServer) {
	if args == nil {
		args = make(map[string]any)
	}

	// Resolve variable values for headers (support {{var:key}} syntax).
	resolvedHeaders := make(map[string]string, len(tool.Headers))
	for k, v := range tool.Headers {
		resolved, err := s.resolveTemplate(v, args)
		if err != nil {
			mcpError(w, id, -32000, fmt.Sprintf("failed to resolve header %q: %v", k, err))
			return
		}
		resolvedHeaders[k] = resolved
	}

	// Resolve URL template.
	resolvedURL, err := s.resolveTemplate(tool.URL, args)
	if err != nil {
		mcpError(w, id, -32000, fmt.Sprintf("failed to resolve URL template: %v", err))
		return
	}

	// Resolve body template.
	var bodyReader io.Reader
	if tool.BodyTemplate != "" {
		resolvedBody, err := s.resolveTemplate(tool.BodyTemplate, args)
		if err != nil {
			mcpError(w, id, -32000, fmt.Sprintf("failed to resolve body template: %v", err))
			return
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

	httpReq, err := http.NewRequestWithContext(r.Context(), method, resolvedURL, bodyReader)
	if err != nil {
		mcpError(w, id, -32000, fmt.Sprintf("failed to create HTTP request: %v", err))
		return
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
		mcpError(w, id, -32000, fmt.Sprintf("HTTP request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	// Read response body with 1MB limit.
	const maxBody = 1048576
	limitReader := io.LimitReader(resp.Body, int64(maxBody+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		mcpError(w, id, -32000, fmt.Sprintf("failed to read response: %v", err))
		return
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
	text := string(resultJSON)

	mcpResult(w, id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
}

// executeSkillTool runs a skill tool's handler (bash or JS) and returns the result.
func (s *Server) executeSkillTool(ctx context.Context, tool *service.Tool, args map[string]any) (string, error) {
	if tool.Handler == "" {
		return "", fmt.Errorf("tool %q has no handler", tool.Name)
	}

	if tool.HandlerType == "bash" {
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
		return workflow.ExecuteBashHandler(ctx, tool.Handler, args, varLister, 0)
	}

	// Default: JS handler.
	var varLookup workflow.VarLookup
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
	}
	return workflow.ExecuteJSHandler(tool.Handler, args, varLookup)
}

// resolveTemplate resolves a Go text/template string with args as data.
// Also supports {{var:key}} syntax to look up variables from the variable store.
func (s *Server) resolveTemplate(tmplStr string, args map[string]any) (string, error) {
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
			v, err := s.variableStore.GetVariableByKey(context.Background(), key)
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

	// Then resolve Go template placeholders.
	if !strings.Contains(resolved, "{{") {
		return resolved, nil
	}

	tmpl, err := template.New("").Option("missingkey=zero").Parse(resolved)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, args); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}
