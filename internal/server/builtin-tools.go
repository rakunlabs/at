package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service/workflow"
)

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

// builtinTools is the static list of server-side built-in tools.
var builtinTools = []builtinToolDef{
	{
		Name:        "http_request",
		Description: "Make an HTTP request. Supports GET, POST, PUT, DELETE, PATCH, HEAD methods. Returns the response status, headers, and body. Useful for calling APIs, fetching data, or testing endpoints.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method": map[string]any{
					"type":        "string",
					"description": "HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD)",
					"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"},
				},
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to send the request to",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "Optional HTTP headers as key-value pairs",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Optional request body (for POST, PUT, PATCH)",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Request timeout in seconds (default 30, max 120)",
				},
			},
			"required": []string{"method", "url"},
		},
	},
	{
		Name:        "bash_execute",
		Description: "Execute a bash command on the server. The command runs in a sandboxed shell (/bin/bash -c). Returns stdout. Use for file operations, system queries, running scripts, or any command-line task.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Execution timeout in seconds (default 60, max 300)",
				},
			},
			"required": []string{"command"},
		},
	},
	{
		Name:        "js_execute",
		Description: "Execute JavaScript code in a sandboxed Goja VM. Available globals: httpGet(url, headers?), httpPost(url, body?, headers?), httpPut(url, body?, headers?), httpDelete(url, headers?), jsonParse(v), toString(v), btoa(v), atob(s), JSON_stringify(v), log.info/warn/error/debug(msg, ...kvPairs). Return a value to produce the tool result.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "JavaScript code to execute. Use 'return <value>' to produce a result.",
				},
			},
			"required": []string{"code"},
		},
	},
	{
		Name:        "url_fetch",
		Description: "Fetch the content of a URL and return it as text. Simpler than http_request — just provide a URL and get the content back. Good for reading web pages, documentation, or API responses.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch content from",
				},
				"max_size": map[string]any{
					"type":        "integer",
					"description": "Maximum content size in bytes (default 102400 = 100KB, max 1048576 = 1MB)",
				},
			},
			"required": []string{"url"},
		},
	},
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

	switch req.Name {
	case "http_request":
		result, execErr = s.execHTTPRequest(r, req.Arguments)
	case "bash_execute":
		result, execErr = s.execBash(r, req.Arguments)
	case "js_execute":
		result, execErr = s.execJS(r, req.Arguments)
	case "url_fetch":
		result, execErr = s.execURLFetch(r, req.Arguments)
	default:
		httpResponse(w, fmt.Sprintf("unknown built-in tool: %q", req.Name), http.StatusBadRequest)
		return
	}

	resp := builtinCallResponse{Result: result}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("builtin tool: execution failed", "tool", req.Name, "error", execErr)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}

// ─── Tool Executors ───

// execHTTPRequest executes the http_request built-in tool.
func (s *Server) execHTTPRequest(r *http.Request, args map[string]any) (string, error) {
	method, _ := args["method"].(string)
	url, _ := args["url"].(string)

	if method == "" {
		return "", fmt.Errorf("method is required")
	}
	if url == "" {
		return "", fmt.Errorf("url is required")
	}

	method = strings.ToUpper(method)

	// Timeout: default 30s, max 120s.
	timeout := 30 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
	}

	// Build request body.
	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), method, url, bodyReader)
	if err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	// Apply headers.
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			httpReq.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body with 1MB size limit.
	const maxBody = 1048576
	limitReader := io.LimitReader(resp.Body, maxBody+1)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	truncated := false
	if len(body) > maxBody {
		body = body[:maxBody]
		truncated = true
	}

	// Build response headers map.
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}

	result := map[string]any{
		"status":      resp.StatusCode,
		"status_text": resp.Status,
		"headers":     respHeaders,
		"body":        string(body),
	}
	if truncated {
		result["truncated"] = true
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(data), nil
}

// execBash executes the bash_execute built-in tool.
func (s *Server) execBash(r *http.Request, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Timeout: default 60s, max 300s.
	timeout := 60 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}

	// Build variable lister for env injection (if variable store available).
	var varLister workflow.VarLister
	if s.variableStore != nil {
		varLister = func() (map[string]string, error) {
			vars, err := s.variableStore.ListVariables(r.Context(), nil)
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

	return workflow.ExecuteBashHandler(r.Context(), command, nil, varLister, timeout)
}

// execJS executes the js_execute built-in tool.
func (s *Server) execJS(r *http.Request, args map[string]any) (string, error) {
	code, _ := args["code"].(string)
	if code == "" {
		return "", fmt.Errorf("code is required")
	}

	// Build variable lookup (if variable store available).
	var varLookup workflow.VarLookup
	if s.variableStore != nil {
		varLookup = func(key string) (string, error) {
			v, err := s.variableStore.GetVariableByKey(r.Context(), key)
			if err != nil {
				return "", err
			}
			if v == nil {
				return "", fmt.Errorf("variable %q not found", key)
			}
			return v.Value, nil
		}
	}

	return workflow.ExecuteJSHandler(code, nil, varLookup)
}

// execURLFetch executes the url_fetch built-in tool.
func (s *Server) execURLFetch(r *http.Request, args map[string]any) (string, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return "", fmt.Errorf("url is required")
	}

	// Only allow HTTP/HTTPS.
	urlLower := strings.ToLower(url)
	if !strings.HasPrefix(urlLower, "http://") && !strings.HasPrefix(urlLower, "https://") {
		return "", fmt.Errorf("only HTTP/HTTPS URLs are supported, got: %s", url)
	}

	// Max size: default 100KB, max 1MB.
	maxSize := 102400
	if n, ok := args["max_size"].(float64); ok && int(n) > 0 {
		maxSize = int(n)
	}
	if maxSize > 1048576 {
		maxSize = 1048576
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("URL returned HTTP %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, int64(maxSize+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return "", fmt.Errorf("failed to read content: %w", err)
	}

	truncated := false
	if len(body) > maxSize {
		body = body[:maxSize]
		truncated = true
	}

	text := string(body)
	if truncated {
		text += fmt.Sprintf("\n\n[Content truncated at %d bytes]", maxSize)
	}

	return text, nil
}
