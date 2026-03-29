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

// ctxKeyAgentID is a context key for passing the executing agent's ID
// to builtin tool executors so they can distinguish agent-initiated actions.
type ctxKeyAgentID struct{}

// contextWithAgentID returns a new context carrying the agent ID.
func contextWithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentID{}, agentID)
}

// agentIDFromContext extracts the agent ID from context.
func agentIDFromContext(ctx context.Context) string {
	if aid, ok := ctx.Value(ctxKeyAgentID{}).(string); ok {
		return aid
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

	// ─── Workflow & Trigger Management Tools ───
	{
		Name:        "workflow_list",
		Description: "List all workflows in the system. Returns a summary of each workflow including ID, name, description, node/edge counts, and timestamps.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "workflow_get",
		Description: "Get a workflow's full details including its graph (nodes and edges). Use this to inspect an existing workflow's structure before modifying it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The workflow ID",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name: "workflow_create",
		Description: `Create a new workflow with a DAG graph of nodes and edges. Available node types:
- input: starting node (output handles: "output")
- output: terminal node (input handles: "input")
- llm_call: sends prompt to LLM (config: provider, model, system_prompt; input handles: "prompt", "context"; output handles: "response")
- agent_call: full agentic loop (config: provider, model, system_prompt, max_iterations; input handles: "prompt", "context"; output handles: "response")
- template: renders Go text/template (config: template; input handles: "input"; output handles: "output")
- conditional: JS expression routing (config: expression; input handles: "input"; output handles: "true", "false")
- loop: JS expression fan-out (config: expression; input handles: "input"; output handles: "item")
- script: arbitrary JS (config: code; input handles: "data"; output handles: "true", "false", "always")
- http_request: HTTP client (config: url, method, headers, body; input handles: "values", "data"; output handles: "success", "error", "always")
- http_trigger: HTTP webhook trigger (config: alias; output handles: "output")
- cron_trigger: cron schedule trigger (config: schedule, timezone, payload; output handles: "output")
- exec: shell command (config: command, sandbox_root; input handles: "data"; output handles: "true", "false", "always")
- email: send email via SMTP (config: config_id, to, subject, body; output handles: "success", "error", "always")
- log: log and pass through (input handles: "input"; output handles: "output")
- chat_reply: send message to a chat session (config: session_id; input handles: "message"; output handles: "success", "error", "always")
Edges connect nodes via source_handle (output handle ID of source node) and target_handle (input handle ID of target node).`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Workflow name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Workflow description",
				},
				"graph": map[string]any{
					"type":        "object",
					"description": "The workflow graph with nodes and edges arrays",
					"properties": map[string]any{
						"nodes": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id":   map[string]any{"type": "string", "description": "Unique node ID"},
									"type": map[string]any{"type": "string", "description": "Node type name"},
									"position": map[string]any{
										"type":        "object",
										"description": "Visual position {x, y}",
									},
									"data": map[string]any{
										"type":        "object",
										"description": "Node-type-specific configuration",
									},
								},
								"required": []string{"id", "type"},
							},
						},
						"edges": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id":            map[string]any{"type": "string", "description": "Unique edge ID"},
									"source":        map[string]any{"type": "string", "description": "Source node ID"},
									"target":        map[string]any{"type": "string", "description": "Target node ID"},
									"source_handle": map[string]any{"type": "string", "description": "Source output port name (default: output)"},
									"target_handle": map[string]any{"type": "string", "description": "Target input port name (default: input)"},
								},
								"required": []string{"id", "source", "target"},
							},
						},
					},
					"required": []string{"nodes", "edges"},
				},
			},
			"required": []string{"name", "graph"},
		},
	},
	{
		Name:        "workflow_update",
		Description: "Update an existing workflow. You can update the name, description, and/or the graph. Only provided fields are changed.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The workflow ID to update",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New workflow name (optional)",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New workflow description (optional)",
				},
				"graph": map[string]any{
					"type":        "object",
					"description": "New workflow graph with nodes and edges (optional)",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "workflow_delete",
		Description: "Delete a workflow and all its associated triggers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The workflow ID to delete",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "workflow_run",
		Description: "Execute a workflow. Can run synchronously (waits for output) or asynchronously (returns immediately).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The workflow ID to run",
				},
				"inputs": map[string]any{
					"type":        "object",
					"description": "Input data to pass to the workflow (optional)",
				},
				"sync": map[string]any{
					"type":        "boolean",
					"description": "If true, wait for workflow completion and return outputs (default: false)",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "trigger_list",
		Description: "List workflow triggers. Optionally filter by workflow ID and/or scope (user identity). Shows trigger type (http/cron), config, alias, and enabled status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Filter triggers by workflow ID (optional — lists all if omitted)",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "Filter triggers by scope/owner (e.g., telegram chat_id). Only shows triggers created by this scope.",
				},
			},
		},
	},

	{
		Name:        "trigger_create",
		Description: "Create a cron or HTTP trigger for a workflow. Cron triggers run workflows on a schedule (e.g., every day at 6 AM). HTTP triggers create webhook URLs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow ID to trigger",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Trigger type: 'cron' or 'http'",
					"enum":        []string{"cron", "http"},
				},
				"schedule": map[string]any{
					"type":        "string",
					"description": "Cron expression (for cron type). Examples: '0 6 * * *' (daily 6 AM), '*/30 * * * *' (every 30 min), '0 9 * * 1-5' (weekdays 9 AM)",
				},
				"timezone": map[string]any{
					"type":        "string",
					"description": "IANA timezone for cron schedule. Default: UTC. Examples: 'Europe/Istanbul', 'America/New_York'",
				},
				"payload": map[string]any{
					"type":        "object",
					"description": "JSON payload to pass as workflow inputs when triggered",
				},
				"entry_node_id": map[string]any{
					"type":        "string",
					"description": "Optional: specific input node ID to trigger (for multi-entry workflows)",
				},
				"alias": map[string]any{
					"type":        "string",
					"description": "Optional human-friendly alias (must be unique)",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "Owner scope (e.g., telegram chat_id). Used to isolate triggers per user. ALWAYS set this from the user context.",
				},
				"enabled": map[string]any{
					"type":        "boolean",
					"description": "Whether the trigger is active. Default: true",
				},
			},
			"required": []string{"workflow_id", "type"},
		},
	},
	{
		Name:        "trigger_get",
		Description: "Get details of a specific trigger by ID.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Trigger ID",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "trigger_update",
		Description: "Update a trigger's schedule, payload, or enabled status.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Trigger ID to update",
				},
				"schedule": map[string]any{
					"type":        "string",
					"description": "New cron expression",
				},
				"timezone": map[string]any{
					"type":        "string",
					"description": "New timezone",
				},
				"payload": map[string]any{
					"type":        "object",
					"description": "New payload",
				},
				"enabled": map[string]any{
					"type":        "boolean",
					"description": "Enable or disable the trigger",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "trigger_delete",
		Description: "Delete a trigger by ID.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Trigger ID to delete",
				},
			},
			"required": []string{"id"},
		},
	},

	// ─── Persistent Task (Issue Tracker) Tools ───
	{
		Name:        "task_create",
		Description: "Create a persistent task/issue in the AT database. Tasks can be assigned to agents, linked to organizations, and tracked through a full lifecycle (backlog → in_progress → in_review → done).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Task title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Task description (markdown supported)",
				},
				"organization_id": map[string]any{
					"type":        "string",
					"description": "Organization ID to scope this task to",
				},
				"assigned_agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID to assign this task to",
				},
				"priority_level": map[string]any{
					"type":        "string",
					"description": "Priority: critical, high, medium, low",
					"enum":        []string{"critical", "high", "medium", "low"},
				},
				"parent_id": map[string]any{
					"type":        "string",
					"description": "Parent task ID for sub-tasks",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Initial status (default: todo). Options: backlog, todo, in_progress, in_review, done",
				},
			},
			"required": []string{"title"},
		},
	},
	{
		Name:        "task_list",
		Description: "List persistent tasks/issues with optional filtering by status, organization, or assigned agent.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status (e.g. in_review, done, in_progress)",
				},
				"organization_id": map[string]any{
					"type":        "string",
					"description": "Filter by organization ID",
				},
				"assigned_agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by assigned agent ID",
				},
			},
		},
	},
	{
		Name:        "task_get",
		Description: "Get full details of a task/issue including subtasks and comments.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The task ID",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "task_update",
		Description: "Update a task's fields. Only provided fields are changed. Use this to change status (e.g. mark as done), reassign, update description, or set the result.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The task ID to update",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "New title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New description",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "New status: backlog, todo, in_progress, in_review, blocked, done, cancelled",
				},
				"priority_level": map[string]any{
					"type":        "string",
					"description": "New priority: critical, high, medium, low",
				},
				"assigned_agent_id": map[string]any{
					"type":        "string",
					"description": "New assigned agent ID",
				},
				"result": map[string]any{
					"type":        "string",
					"description": "Task result/output (typically set when completing a task)",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "task_add_comment",
		Description: "Add a comment to a task/issue. Useful for providing feedback, requesting changes, or noting decisions.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task ID to comment on",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Comment text (markdown supported)",
				},
				"author_name": map[string]any{
					"type":        "string",
					"description": "Name of the commenter (default: mcp-user)",
				},
			},
			"required": []string{"task_id", "body"},
		},
	},
	{
		Name:        "task_process",
		Description: "Trigger async organization delegation on a task. The task's assigned agent (or the org's head agent) will process the task using the LLM-driven delegation loop. Returns immediately (202 Accepted).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The task ID to process",
				},
			},
			"required": []string{"id"},
		},
	},

	// ─── Organization Management Tools ───
	{
		Name:        "org_create",
		Description: "Create a new organization. Organizations group agents into teams with hierarchical delegation, budgets, and issue tracking.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Organization name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Organization description",
				},
				"issue_prefix": map[string]any{
					"type":        "string",
					"description": "Prefix for issue identifiers (e.g. 'YTS' for YTS-1, YTS-2)",
				},
				"head_agent_id": map[string]any{
					"type":        "string",
					"description": "ID of the head agent who receives incoming tasks",
				},
				"budget_monthly_cents": map[string]any{
					"type":        "number",
					"description": "Monthly budget in cents (e.g. 5000 = $50)",
				},
				"max_delegation_depth": map[string]any{
					"type":        "number",
					"description": "Maximum depth of delegation chain (default: 3)",
				},
				"require_board_approval": map[string]any{
					"type":        "boolean",
					"description": "Require approval for new agent additions",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "org_list",
		Description: "List all organizations with their key details.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "org_get",
		Description: "Get an organization's full details including its agent roster (hierarchy).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The organization ID",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "org_add_agent",
		Description: "Add an agent to an organization's hierarchy. Set parent_agent_id to create reporting lines for delegation.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"organization_id": map[string]any{
					"type":        "string",
					"description": "The organization ID",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The agent ID to add",
				},
				"role": map[string]any{
					"type":        "string",
					"description": "Role in the org (e.g. head, member)",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Title/position (e.g. Content Director, Script Writer)",
				},
				"parent_agent_id": map[string]any{
					"type":        "string",
					"description": "The agent ID of the parent agent in this org's hierarchy (e.g. the Content Director's agent ID). This creates a reporting line from this agent to the parent.",
				},
			},
			"required": []string{"organization_id", "agent_id"},
		},
	},
	{
		Name:        "org_task_intake",
		Description: "Submit a task to an organization for processing. The task is assigned to the org's head agent who delegates to specialist agents. Returns immediately with the task ID while delegation runs in the background. This is the primary way to trigger agent pipelines (e.g. 'create a YouTube Short about X').",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"organization_id": map[string]any{
					"type":        "string",
					"description": "The organization ID to submit the task to",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Task title (e.g. 'Create a short about quantum computing')",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Additional context or requirements",
				},
				"priority_level": map[string]any{
					"type":        "string",
					"description": "Priority: critical, high, medium, low",
					"enum":        []string{"critical", "high", "medium", "low"},
				},
			},
			"required": []string{"organization_id", "title"},
		},
	},

	// ─── Agent Management Tools ───
	{
		Name:        "agent_create",
		Description: "Create a new AI agent with LLM provider, model, system prompt, and tool configuration.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Agent name (unique identifier)",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "LLM provider key (configured in AT providers)",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model identifier (e.g. gpt-4o, claude-sonnet-4-20250514)",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "System prompt that defines the agent's behavior and role",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Agent description",
				},
				"skills": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Skill IDs or names to assign to the agent",
				},
				"mcp_sets": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "MCP Set names (internal MCPs) to assign to the agent",
				},
				"builtin_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Built-in tool names to enable for the agent",
				},
				"max_iterations": map[string]any{
					"type":        "number",
					"description": "Maximum agentic loop iterations (default: 10)",
				},
				"tool_timeout": map[string]any{
					"type":        "number",
					"description": "Per-tool timeout in seconds (default: 60)",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "agent_list",
		Description: "List all AI agents with their key configuration details.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "agent_get",
		Description: "Get an agent's full details including its complete configuration (provider, model, system prompt, skills, tools).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The agent ID",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "agent_update",
		Description: "Update an agent's configuration. Only provided fields are changed.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The agent ID to update",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New agent name",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "New LLM provider key",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "New model identifier",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "New system prompt",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New description",
				},
				"skills": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "New skill list (replaces existing)",
				},
				"mcp_sets": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "New MCP set list (replaces existing)",
				},
				"builtin_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "New built-in tools list (replaces existing)",
				},
				"max_iterations": map[string]any{
					"type":        "number",
					"description": "New max iterations",
				},
				"tool_timeout": map[string]any{
					"type":        "number",
					"description": "New tool timeout in seconds",
				},
			},
			"required": []string{"id"},
		},
	},

	// ─── Skill Management Tools ───
	{
		Name:        "skill_list",
		Description: "List installed skills and available skill templates. Shows both what's already installed and what templates can be installed.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{
					"type":        "string",
					"description": "Filter templates by category (e.g. 'Content Creation', 'Development', 'Utilities')",
				},
			},
		},
	},
	{
		Name:        "skill_install_template",
		Description: "Install a skill from a built-in template. After installation, the skill can be assigned to agents. Check required variables in the response.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"slug": map[string]any{
					"type":        "string",
					"description": "Template slug identifier (use skill_list to see available templates)",
				},
			},
			"required": []string{"slug"},
		},
	},

	// ─── Provider Tools ───
	{
		Name:        "provider_list",
		Description: "List all configured LLM providers with their available models. Use this to discover which providers and models are available when creating or updating agents. API keys are redacted.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name:        "provider_get",
		Description: "Get detailed configuration for a specific LLM provider by key. Shows available models, default model, base URL, and auth type. API keys are redacted.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "The provider key (e.g. 'openai', 'anthropic', 'groq')",
				},
			},
			"required": []string{"key"},
		},
	},

	// ─── Approval Tools ───
	{
		Name:        "approval_list_pending",
		Description: "List pending approval requests. Approvals are governance decisions like hiring agents, budget changes, or task escalations.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"organization_id": map[string]any{
					"type":        "string",
					"description": "Filter by organization ID (optional)",
				},
			},
		},
	},
	{
		Name:        "approval_decide",
		Description: "Approve or reject a pending approval request.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The approval ID",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Decision: approved, rejected, revision_requested, cancelled",
					"enum":        []string{"approved", "rejected", "revision_requested", "cancelled"},
				},
				"decision_note": map[string]any{
					"type":        "string",
					"description": "Reason or note for the decision",
				},
			},
			"required": []string{"id", "status"},
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

	// Workflow & trigger management tools.
	case "workflow_list":
		return s.execWorkflowList(ctx, args)
	case "workflow_get":
		return s.execWorkflowGet(ctx, args)
	case "workflow_create":
		return s.execWorkflowCreate(ctx, args)
	case "workflow_update":
		return s.execWorkflowUpdate(ctx, args)
	case "workflow_delete":
		return s.execWorkflowDelete(ctx, args)
	case "workflow_run":
		return s.execWorkflowRun(ctx, args)
	case "trigger_list":
		return s.execTriggerList(ctx, args)
	case "trigger_create":
		return s.execTriggerCreate(ctx, args)
	case "trigger_get":
		return s.execTriggerGet(ctx, args)
	case "trigger_update":
		return s.execTriggerUpdate(ctx, args)
	case "trigger_delete":
		return s.execTriggerDelete(ctx, args)

	// User preference tools.
	case "set_user_preference":
		return s.execSetUserPreference(ctx, args)
	case "get_user_preferences":
		return s.execGetUserPreferences(ctx, args)

	// Persistent task tools.
	case "task_create":
		return s.execTaskCreate(ctx, args)
	case "task_list":
		return s.execTaskList(ctx, args)
	case "task_get":
		return s.execTaskGet(ctx, args)
	case "task_update":
		return s.execTaskUpdate(ctx, args)
	case "task_add_comment":
		return s.execTaskAddComment(ctx, args)
	case "task_process":
		return s.execTaskProcess(ctx, args)

	// Organization tools.
	case "org_create":
		return s.execOrgCreate(ctx, args)
	case "org_list":
		return s.execOrgList(ctx, args)
	case "org_get":
		return s.execOrgGet(ctx, args)
	case "org_add_agent":
		return s.execOrgAddAgent(ctx, args)
	case "org_task_intake":
		return s.execOrgTaskIntake(ctx, args)

	// Agent tools.
	case "agent_create":
		return s.execAgentCreate(ctx, args)
	case "agent_list":
		return s.execAgentList(ctx, args)
	case "agent_get":
		return s.execAgentGet(ctx, args)
	case "agent_update":
		return s.execAgentUpdate(ctx, args)

	// Skill tools.
	case "skill_list":
		return s.execSkillList(ctx, args)
	case "skill_install_template":
		return s.execSkillInstallTemplate(ctx, args)

	// Provider tools.
	case "provider_list":
		return s.execProviderList(ctx, args)
	case "provider_get":
		return s.execProviderGet(ctx, args)

	// Approval tools.
	case "approval_list_pending":
		return s.execApprovalListPending(ctx, args)
	case "approval_decide":
		return s.execApprovalDecide(ctx, args)

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
