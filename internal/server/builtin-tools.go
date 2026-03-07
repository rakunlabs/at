package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ctxKeySessionID is a context key for passing session ID to builtin tool executors.
type ctxKeySessionID struct{}

// contextWithSessionID returns a new context carrying the session ID.
func contextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionID{}, sessionID)
}

// sessionIDFromContext extracts the session ID from context. Falls back to "default".
func sessionIDFromContext(ctx context.Context) string {
	if sid, ok := ctx.Value(ctxKeySessionID{}).(string); ok && sid != "" {
		return sid
	}
	return "default"
}

// ctxKeySessionUserID is a context key for passing the platform-scoped user identity
// (e.g. "telegram::12345") to builtin tool executors.
type ctxKeySessionUserID struct{}

// contextWithSessionUserID returns a new context carrying the session user ID.
func contextWithSessionUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionUserID{}, userID)
}

// sessionUserIDFromContext extracts the session user ID from context.
func sessionUserIDFromContext(ctx context.Context) string {
	if uid, ok := ctx.Value(ctxKeySessionUserID{}).(string); ok {
		return uid
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

// builtinTools is the static list of server-side built-in tools.
var builtinTools = []builtinToolDef{
	// ─── Original Tools ───
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

	// ─── File Tools ───
	{
		Name:        "file_read",
		Description: "Read file contents from the filesystem. Supports reading specific line ranges for large files. If the path is a directory, lists its contents. Each line is prefixed with its line number.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute or relative path to the file or directory to read",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "The line number to start reading from (1-indexed, default: 1)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "The maximum number of lines to read (default: 2000)",
				},
			},
			"required": []string{"file_path"},
		},
	},
	{
		Name:        "file_write",
		Description: "Create new files or overwrite existing ones. Automatically creates parent directories if they don't exist.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute or relative path to the file to write",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The content to write to the file",
				},
			},
			"required": []string{"file_path", "content"},
		},
	},
	{
		Name:        "file_edit",
		Description: "Modify existing files using exact string replacement. Finds the old_string in the file and replaces it with new_string. Fails if old_string is not found or if multiple matches exist (unless replace_all is true).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute or relative path to the file to modify",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The exact text to find and replace",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The replacement text (must be different from old_string)",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace all occurrences of old_string (default: false)",
				},
			},
			"required": []string{"file_path", "old_string", "new_string"},
		},
	},
	{
		Name:        "file_multiedit",
		Description: "Perform multiple sequential string replacements on a single file. Each edit is applied in order. Useful for making several changes to one file in a single operation.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute or relative path to the file to modify",
				},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "The text to replace",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "The replacement text",
							},
							"replace_all": map[string]any{
								"type":        "boolean",
								"description": "Replace all occurrences (default: false)",
							},
						},
						"required": []string{"old_string", "new_string"},
					},
					"description": "Array of edit operations to perform sequentially",
				},
			},
			"required": []string{"file_path", "edits"},
		},
	},
	{
		Name:        "file_patch",
		Description: "Apply a unified diff/patch to a file. Useful for applying diffs and patches. Requires the 'patch' command on the server.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute or relative path to the file to patch",
				},
				"diff": map[string]any{
					"type":        "string",
					"description": "The unified diff content to apply",
				},
			},
			"required": []string{"file_path", "diff"},
		},
	},
	{
		Name:        "file_glob",
		Description: "Find files by glob pattern matching. Returns matching file paths sorted by modification time (newest first). Automatically skips hidden directories, node_modules, vendor, .git, etc.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The glob pattern to match files against (e.g. '*.go', '*.ts', 'README*')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "The directory to search in (default: current directory)",
				},
			},
			"required": []string{"pattern"},
		},
	},
	{
		Name:        "file_grep",
		Description: "Search file contents using regular expressions. Returns file paths and line numbers with matching content, sorted by file modification time. Automatically skips binary files and common large directories.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regex pattern to search for in file contents",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "The directory to search in (default: current directory)",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "File pattern to include (e.g. '*.go', '*.ts')",
				},
			},
			"required": []string{"pattern"},
		},
	},
	{
		Name:        "file_list",
		Description: "List files and directories in a given path with details (type, size, modification date). Supports glob pattern filtering.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The directory path to list (default: current directory)",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Optional glob pattern to filter entries (e.g. '*.go')",
				},
			},
		},
	},

	// ─── Task Management Tools ───
	{
		Name:        "todo_write",
		Description: "Create or update a task/todo list to track progress during complex multi-step operations. Each item has content, status (pending/in_progress/completed/cancelled), and priority (high/medium/low).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{
								"type":        "string",
								"description": "Brief description of the task",
							},
							"status": map[string]any{
								"type":        "string",
								"description": "Current status: pending, in_progress, completed, cancelled",
								"enum":        []string{"pending", "in_progress", "completed", "cancelled"},
							},
							"priority": map[string]any{
								"type":        "string",
								"description": "Priority level: high, medium, low",
								"enum":        []string{"high", "medium", "low"},
							},
						},
						"required": []string{"content", "status", "priority"},
					},
					"description": "The complete todo list (replaces any existing list)",
				},
			},
			"required": []string{"todos"},
		},
	},
	{
		Name:        "todo_read",
		Description: "Read the current todo list state. Returns all todo items with their content, status, and priority.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "batch_execute",
		Description: "Execute multiple built-in tools in parallel. Each tool call runs concurrently and results are collected. Maximum 25 tool calls per batch. Cannot call batch_execute recursively.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_calls": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "The name of the built-in tool to execute",
							},
							"arguments": map[string]any{
								"type":        "object",
								"description": "Arguments for the tool",
							},
						},
						"required": []string{"name"},
					},
					"description": "Array of tool calls to execute in parallel",
				},
			},
			"required": []string{"tool_calls"},
		},
	},

	// ─── User Preference Tools ───
	{
		Name:        "set_user_preference",
		Description: "Save a persistent user preference such as timezone, location, or language. The value is stored per-user and will be remembered across sessions. Use this when the user tells you their timezone, location, language, or other personal preferences that should be remembered.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Preference key (e.g. 'timezone', 'location', 'language')",
				},
				"value": map[string]any{
					"description": "Preference value — can be a string or a JSON object (e.g. 'Europe/Istanbul' or {\"city\": \"Istanbul\", \"country\": \"Turkey\"})",
				},
			},
			"required": []string{"key", "value"},
		},
	},
	{
		Name:        "get_user_preferences",
		Description: "Retrieve all stored preferences for the current user (timezone, location, language, etc.). Returns a JSON object with all saved preferences.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},

	// ─── LSP Tool ───
	{
		Name:        "lsp_query",
		Description: "Interact with Language Server Protocol (LSP) servers for code intelligence. Supports goToDefinition, findReferences, hover, documentSymbol, workspaceSymbol, goToImplementation. Automatically starts the appropriate LSP server based on file language (Go, TypeScript, JavaScript, Python, Rust, Java, C/C++).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"description": "The LSP operation to perform",
					"enum":        []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol", "goToImplementation"},
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "The absolute path to the file (required for all operations except workspaceSymbol)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (0-indexed) for position-based operations",
				},
				"character": map[string]any{
					"type":        "integer",
					"description": "Character offset (0-indexed) within the line",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (for workspaceSymbol operation)",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Programming language (auto-detected from file extension if omitted). Supported: go, typescript, javascript, python, rust, java, c, cpp",
				},
			},
			"required": []string{"operation"},
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

// ─── Tool Executors ───

// execHTTPRequest executes the http_request built-in tool.
func (s *Server) execHTTPRequest(ctx context.Context, args map[string]any) (string, error) {
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

	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
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
func (s *Server) execBash(ctx context.Context, args map[string]any) (string, error) {
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

	return workflow.ExecuteBashHandler(ctx, command, nil, varLister, timeout)
}

// execJS executes the js_execute built-in tool.
func (s *Server) execJS(ctx context.Context, args map[string]any) (string, error) {
	code, _ := args["code"].(string)
	if code == "" {
		return "", fmt.Errorf("code is required")
	}

	// Build variable lookup (if variable store available).
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

	return workflow.ExecuteJSHandler(code, nil, varLookup)
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

// dispatchBuiltinTool dispatches a tool call to the appropriate executor.
func (s *Server) dispatchBuiltinTool(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	// Original tools.
	case "http_request":
		return s.execHTTPRequest(ctx, args)
	case "bash_execute":
		return s.execBash(ctx, args)
	case "js_execute":
		return s.execJS(ctx, args)
	case "url_fetch":
		return s.execURLFetch(ctx, args)

	// File tools.
	case "file_read":
		return s.execFileRead(ctx, args)
	case "file_write":
		return s.execFileWrite(ctx, args)
	case "file_edit":
		return s.execFileEdit(ctx, args)
	case "file_multiedit":
		return s.execFileMultiEdit(ctx, args)
	case "file_patch":
		return s.execFilePatch(ctx, args)
	case "file_glob":
		return s.execFileGlob(ctx, args)
	case "file_grep":
		return s.execFileGrep(ctx, args)
	case "file_list":
		return s.execFileList(ctx, args)

	// Task management tools.
	case "todo_write":
		return s.execTodoWrite(ctx, args)
	case "todo_read":
		return s.execTodoRead(ctx, args)
	case "batch_execute":
		return s.execBatchExecute(ctx, args)

	// LSP tool.
	case "lsp_query":
		return s.execLSPQuery(ctx, args)

	// User preference tools.
	case "set_user_preference":
		return s.execSetUserPreference(ctx, args)
	case "get_user_preferences":
		return s.execGetUserPreferences(ctx, args)

	default:
		return "", nil
	}
}

// execURLFetch executes the url_fetch built-in tool.
func (s *Server) execURLFetch(ctx context.Context, args map[string]any) (string, error) {
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

// execSetUserPreference saves a per-user preference.
func (s *Server) execSetUserPreference(ctx context.Context, args map[string]any) (string, error) {
	if s.userPrefStore == nil {
		return "", fmt.Errorf("user preference store not configured")
	}

	userID := sessionUserIDFromContext(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user identity available — user preferences require a bot session or authenticated context")
	}

	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}

	value := args["value"]
	if value == nil {
		return "", fmt.Errorf("value is required")
	}

	// Marshal the value to JSON.
	var valueJSON json.RawMessage
	switch v := value.(type) {
	case string:
		// Store strings as JSON strings.
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal value: %w", err)
		}
		valueJSON = data
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal value: %w", err)
		}
		valueJSON = data
	}

	if err := s.userPrefStore.SetUserPreference(ctx, service.UserPreference{
		UserID: userID,
		Key:    key,
		Value:  valueJSON,
	}); err != nil {
		return "", fmt.Errorf("save user preference: %w", err)
	}

	result := map[string]any{
		"status": "saved",
		"key":    key,
		"value":  value,
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

// execGetUserPreferences retrieves all non-secret preferences for the current user.
func (s *Server) execGetUserPreferences(ctx context.Context, _ map[string]any) (string, error) {
	if s.userPrefStore == nil {
		return "", fmt.Errorf("user preference store not configured")
	}

	userID := sessionUserIDFromContext(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user identity available — user preferences require a bot session or authenticated context")
	}

	prefs, err := s.userPrefStore.ListUserPreferences(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("list user preferences: %w", err)
	}

	result := make(map[string]any, len(prefs))
	for _, p := range prefs {
		if p.Secret {
			continue // Don't expose secret preferences to the LLM.
		}
		var val any
		if err := json.Unmarshal(p.Value, &val); err != nil {
			val = string(p.Value)
		}
		result[p.Key] = val
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal preferences: %w", err)
	}
	return string(data), nil
}
