package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ─── MCP Proxy API ───
//
// These endpoints allow the Chat UI to interact with MCP servers through the
// backend, avoiding CORS issues and reusing the existing Go MCP client.

// mcpListToolsRequest is the request body for MCPListToolsAPI.
type mcpListToolsRequest struct {
	URLs    []string          `json:"urls"`
	Headers map[string]string `json:"headers,omitempty"` // Optional headers sent with every MCP request
}

// mcpListToolsResponse is the response body for MCPListToolsAPI.
type mcpListToolsResponse struct {
	Tools []mcpToolInfo `json:"tools"`
}

// mcpToolInfo describes a tool discovered from an MCP server, including the
// server URL it came from so the UI can route tool calls back correctly.
type mcpToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	ServerURL   string         `json:"server_url"`
}

// MCPListToolsAPI handles POST /api/v1/mcp/list-tools.
// It connects to each provided MCP server URL, discovers available tools,
// and returns the merged tool list.
func (s *Server) MCPListToolsAPI(w http.ResponseWriter, r *http.Request) {
	var req mcpListToolsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if len(req.URLs) == 0 {
		httpResponseJSON(w, mcpListToolsResponse{Tools: []mcpToolInfo{}}, http.StatusOK)
		return
	}

	var allTools []mcpToolInfo
	var errors []string

	for _, mcpURL := range req.URLs {
		var opts []service.HTTPMCPClientOption
		if len(req.Headers) > 0 {
			opts = append(opts, service.WithHeaders(req.Headers))
		}
		client, err := service.NewHTTPMCPClient(r.Context(), mcpURL, opts...)
		if err != nil {
			slog.Warn("mcp proxy: failed to connect", "url", mcpURL, "error", err)
			errors = append(errors, fmt.Sprintf("%s: %v", mcpURL, err))
			continue
		}
		defer client.Close()

		tools, err := client.ListTools(r.Context())
		if err != nil {
			slog.Warn("mcp proxy: failed to list tools", "url", mcpURL, "error", err)
			errors = append(errors, fmt.Sprintf("%s: %v", mcpURL, err))
			continue
		}

		for _, t := range tools {
			allTools = append(allTools, mcpToolInfo{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
				ServerURL:   mcpURL,
			})
		}
	}

	if allTools == nil {
		allTools = []mcpToolInfo{}
	}

	resp := mcpListToolsResponse{Tools: allTools}
	if len(errors) > 0 {
		// Include errors as a separate field so the UI can show warnings
		// but still use whatever tools were discovered.
		httpResponseJSON(w, map[string]any{
			"tools":  allTools,
			"errors": errors,
		}, http.StatusOK)
		return
	}

	httpResponseJSON(w, resp, http.StatusOK)
}

// mcpCallToolRequest is the request body for MCPCallToolAPI.
type mcpCallToolRequest struct {
	ServerURL string            `json:"server_url"`
	Name      string            `json:"name"`
	Arguments map[string]any    `json:"arguments"`
	Headers   map[string]string `json:"headers,omitempty"` // Optional headers sent with the MCP request
}

// mcpCallToolResponse is the response body for MCPCallToolAPI.
type mcpCallToolResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// MCPCallToolAPI handles POST /api/v1/mcp/call-tool.
// It connects to the specified MCP server and invokes the named tool.
func (s *Server) MCPCallToolAPI(w http.ResponseWriter, r *http.Request) {
	var req mcpCallToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.ServerURL == "" {
		httpResponse(w, "server_url is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	var opts []service.HTTPMCPClientOption
	if len(req.Headers) > 0 {
		opts = append(opts, service.WithHeaders(req.Headers))
	}
	client, err := service.NewHTTPMCPClient(r.Context(), req.ServerURL, opts...)
	if err != nil {
		slog.Error("mcp proxy: failed to connect for call", "url", req.ServerURL, "error", err)
		httpResponseJSON(w, mcpCallToolResponse{
			Error: fmt.Sprintf("failed to connect to MCP server: %v", err),
		}, http.StatusOK)
		return
	}
	defer client.Close()

	result, err := client.CallTool(r.Context(), req.Name, req.Arguments)
	if err != nil {
		slog.Error("mcp proxy: tool call failed", "url", req.ServerURL, "tool", req.Name, "error", err)
		httpResponseJSON(w, mcpCallToolResponse{
			Error: fmt.Sprintf("tool call failed: %v", err),
		}, http.StatusOK)
		return
	}

	httpResponseJSON(w, mcpCallToolResponse{Result: result}, http.StatusOK)
}

// ─── Skill Tool Execution ───
//
// Reuses the same JS/bash handler execution as TestHandlerAPI but designed
// for the Chat UI's tool-calling loop.

// skillCallToolRequest is the request body for SkillCallToolAPI.
type skillCallToolRequest struct {
	SkillName string         `json:"skill_name"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments"`
}

// skillCallToolResponse is the response body for SkillCallToolAPI.
type skillCallToolResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// SkillCallToolAPI handles POST /api/v1/mcp/call-skill-tool.
// It looks up a skill by name, finds the requested tool within it,
// and executes its handler with the provided arguments.
func (s *Server) SkillCallToolAPI(w http.ResponseWriter, r *http.Request) {
	var req skillCallToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.SkillName == "" {
		httpResponse(w, "skill_name is required", http.StatusBadRequest)
		return
	}
	if req.ToolName == "" {
		httpResponse(w, "tool_name is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	// Look up the skill by name.
	if s.skillStore == nil {
		httpResponse(w, "skill store not configured", http.StatusServiceUnavailable)
		return
	}

	skill, err := s.skillStore.GetSkillByName(r.Context(), req.SkillName)
	if err != nil {
		slog.Error("skill call: lookup failed", "skill", req.SkillName, "error", err)
		httpResponseJSON(w, skillCallToolResponse{
			Error: fmt.Sprintf("failed to look up skill: %v", err),
		}, http.StatusOK)
		return
	}
	if skill == nil {
		httpResponseJSON(w, skillCallToolResponse{
			Error: fmt.Sprintf("skill %q not found", req.SkillName),
		}, http.StatusOK)
		return
	}

	// Find the tool within the skill.
	var tool *service.Tool
	for i := range skill.Tools {
		if skill.Tools[i].Name == req.ToolName {
			tool = &skill.Tools[i]
			break
		}
	}
	if tool == nil {
		httpResponseJSON(w, skillCallToolResponse{
			Error: fmt.Sprintf("tool %q not found in skill %q", req.ToolName, req.SkillName),
		}, http.StatusOK)
		return
	}

	if tool.Handler == "" {
		httpResponseJSON(w, skillCallToolResponse{
			Error: fmt.Sprintf("tool %q has no handler", req.ToolName),
		}, http.StatusOK)
		return
	}

	result, execErr := s.executeSkillTool(r.Context(), tool, req.Arguments)

	resp := skillCallToolResponse{Result: result}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("skill call: handler failed", "skill", req.SkillName, "tool", req.ToolName, "error", execErr)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}
