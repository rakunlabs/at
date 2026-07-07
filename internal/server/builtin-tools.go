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
	"github.com/rakunlabs/at/internal/service/container"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// builtinTools is the static list of server-side built-in tools.
// Tool definitions are registered here; executors live in builtin-tools-*.go files;
// dispatch lives in builtin-tools-dispatch.go.
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
	{Name: "file_read", Description: "Read file contents from the filesystem. Supports reading specific line ranges for large files. If the path is a directory, lists its contents. Each line is prefixed with its line number.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string", "description": "The absolute or relative path to the file or directory to read"}, "offset": map[string]any{"type": "integer", "description": "The line number to start reading from (1-indexed, default: 1)"}, "limit": map[string]any{"type": "integer", "description": "The maximum number of lines to read (default: 2000)"}}, "required": []string{"file_path"}}},
	{Name: "file_write", Description: "Create new files or overwrite existing ones. Automatically creates parent directories if they don't exist.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string", "description": "The absolute or relative path to the file to write"}, "content": map[string]any{"type": "string", "description": "The content to write to the file"}}, "required": []string{"file_path", "content"}}},
	{Name: "file_edit", Description: "Modify existing files using exact string replacement. Finds the old_string in the file and replaces it with new_string. Fails if old_string is not found or if multiple matches exist (unless replace_all is true).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string", "description": "The absolute or relative path to the file to modify"}, "old_string": map[string]any{"type": "string", "description": "The exact text to find and replace"}, "new_string": map[string]any{"type": "string", "description": "The replacement text (must be different from old_string)"}, "replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences of old_string (default: false)"}}, "required": []string{"file_path", "old_string", "new_string"}}},
	{Name: "file_multiedit", Description: "Perform multiple sequential string replacements on a single file. Each edit is applied in order. Useful for making several changes to one file in a single operation.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string", "description": "The absolute or relative path to the file to modify"}, "edits": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"old_string": map[string]any{"type": "string", "description": "The text to replace"}, "new_string": map[string]any{"type": "string", "description": "The replacement text"}, "replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default: false)"}}, "required": []string{"old_string", "new_string"}}, "description": "Array of edit operations to perform sequentially"}}, "required": []string{"file_path", "edits"}}},
	{Name: "file_patch", Description: "Apply a unified diff/patch to a file. Useful for applying diffs and patches. Requires the 'patch' command on the server.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string", "description": "The absolute or relative path to the file to patch"}, "diff": map[string]any{"type": "string", "description": "The unified diff content to apply"}}, "required": []string{"file_path", "diff"}}},
	{Name: "file_glob", Description: "Find files by glob pattern matching. Returns matching file paths sorted by modification time (newest first). Automatically skips hidden directories, node_modules, vendor, .git, etc.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string", "description": "The glob pattern to match files against (e.g. '*.go', '*.ts', 'README*')"}, "path": map[string]any{"type": "string", "description": "The directory to search in (default: current directory)"}}, "required": []string{"pattern"}}},
	{Name: "file_grep", Description: "Search file contents using regular expressions. Returns file paths and line numbers with matching content, sorted by file modification time. Automatically skips binary files and common large directories.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string", "description": "The regex pattern to search for in file contents"}, "path": map[string]any{"type": "string", "description": "The directory to search in (default: current directory)"}, "include": map[string]any{"type": "string", "description": "File pattern to include (e.g. '*.go', '*.ts')"}}, "required": []string{"pattern"}}},
	{Name: "file_list", Description: "List files and directories in a given path with details (type, size, modification date). Supports glob pattern filtering.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "The directory path to list (default: current directory)"}, "pattern": map[string]any{"type": "string", "description": "Optional glob pattern to filter entries (e.g. '*.go')"}}}},

	// ─── Task Management Tools ───
	{Name: "todo_write", Description: "Create or update a task/todo list to track progress during complex multi-step operations. Each item has content, status (pending/in_progress/completed/cancelled), and priority (high/medium/low).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"todos": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string", "description": "Brief description of the task"}, "status": map[string]any{"type": "string", "description": "Current status: pending, in_progress, completed, cancelled", "enum": []string{"pending", "in_progress", "completed", "cancelled"}}, "priority": map[string]any{"type": "string", "description": "Priority level: high, medium, low", "enum": []string{"high", "medium", "low"}}}, "required": []string{"content", "status", "priority"}}, "description": "The complete todo list (replaces any existing list)"}}, "required": []string{"todos"}}},
	{Name: "todo_read", Description: "Read the current todo list state. Returns all todo items with their content, status, and priority.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "batch_execute", Description: "Execute multiple built-in tools in parallel. Each tool call runs concurrently and results are collected. Maximum 25 tool calls per batch. Cannot call batch_execute recursively.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"tool_calls": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "The name of the built-in tool to execute"}, "arguments": map[string]any{"type": "object", "description": "Arguments for the tool"}}, "required": []string{"name"}}, "description": "Array of tool calls to execute in parallel"}}, "required": []string{"tool_calls"}}},

	// ─── User Preference Tools ───
	{Name: "set_user_preference", Description: "Save a persistent user preference such as timezone, location, or language. The value is stored per-user and will be remembered across sessions. Use this when the user tells you their timezone, location, language, or other personal preferences that should be remembered.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string", "description": "Preference key (e.g. 'timezone', 'location', 'language')"}, "value": map[string]any{"description": "Preference value — can be a string or a JSON object (e.g. 'Europe/Istanbul' or {\"city\": \"Istanbul\", \"country\": \"Turkey\"})"}}, "required": []string{"key", "value"}}},
	{Name: "get_user_preferences", Description: "Retrieve all stored preferences for the current user (timezone, location, language, etc.). Returns a JSON object with all saved preferences.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},

	// ─── Workflow & Trigger Management Tools ───
	{Name: "workflow_list", Description: "List all workflows in the system. Returns a summary of each workflow including ID, name, description, node/edge counts, and timestamps.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "workflow_get", Description: "Get a workflow's full details including its graph (nodes and edges). Use this to inspect an existing workflow's structure before modifying it.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The workflow ID"}}, "required": []string{"id"}}},
	{Name: "workflow_create", Description: "Create a new workflow with a DAG graph of nodes and edges. Available node types:\n- input: starting node (output handles: \"output\")\n- output: terminal node (input handles: \"input\")\n- llm_call: sends prompt to LLM (config: provider, model, system_prompt; input handles: \"prompt\", \"context\"; output handles: \"response\")\n- agent_call: full agentic loop (config: provider, model, system_prompt, max_iterations; input handles: \"prompt\", \"context\"; output handles: \"response\")\n- template: renders Go text/template (config: template; input handles: \"input\"; output handles: \"output\")\n- conditional: JS expression routing (config: expression; input handles: \"input\"; output handles: \"true\", \"false\")\n- loop: JS expression fan-out (config: expression; input handles: \"input\"; output handles: \"item\")\n- script: arbitrary JS (config: code; input handles: \"data\"; output handles: \"true\", \"false\", \"always\")\n- http_request: HTTP client (config: url, method, headers, body; input handles: \"values\", \"data\"; output handles: \"success\", \"error\", \"always\")\n- http_trigger: HTTP webhook trigger (config: alias; output handles: \"output\")\n- cron_trigger: cron schedule trigger (config: schedule, timezone, payload; output handles: \"output\")\n- exec: shell command (config: command, sandbox_root; input handles: \"data\"; output handles: \"true\", \"false\", \"always\")\n- email: send email via SMTP (config: config_id, to, subject, body; output handles: \"success\", \"error\", \"always\")\n- log: log and pass through (input handles: \"input\"; output handles: \"output\")\n- chat_reply: send message to a chat session (config: session_id; input handles: \"message\"; output handles: \"success\", \"error\", \"always\")\nEdges connect nodes via source_handle (output handle ID of source node) and target_handle (input handle ID of target node).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Workflow name"}, "description": map[string]any{"type": "string", "description": "Workflow description"}, "graph": map[string]any{"type": "object", "description": "The workflow graph with nodes and edges arrays", "properties": map[string]any{"nodes": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Unique node ID"}, "type": map[string]any{"type": "string", "description": "Node type name"}, "position": map[string]any{"type": "object", "description": "Visual position {x, y}"}, "data": map[string]any{"type": "object", "description": "Node-type-specific configuration"}}, "required": []string{"id", "type"}}}, "edges": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Unique edge ID"}, "source": map[string]any{"type": "string", "description": "Source node ID"}, "target": map[string]any{"type": "string", "description": "Target node ID"}, "source_handle": map[string]any{"type": "string", "description": "Source output port name (default: output)"}, "target_handle": map[string]any{"type": "string", "description": "Target input port name (default: input)"}}, "required": []string{"id", "source", "target"}}}}, "required": []string{"nodes", "edges"}}}, "required": []string{"name", "graph"}}},
	{Name: "workflow_update", Description: "Update an existing workflow. You can update the name, description, and/or the graph. Only provided fields are changed.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The workflow ID to update"}, "name": map[string]any{"type": "string", "description": "New workflow name (optional)"}, "description": map[string]any{"type": "string", "description": "New workflow description (optional)"}, "graph": map[string]any{"type": "object", "description": "New workflow graph with nodes and edges (optional)"}}, "required": []string{"id"}}},
	{Name: "workflow_delete", Description: "Delete a workflow and all its associated triggers.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The workflow ID to delete"}}, "required": []string{"id"}}},
	{Name: "workflow_run", Description: "Execute a workflow. Can run synchronously (waits for output) or asynchronously (returns immediately).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The workflow ID to run"}, "inputs": map[string]any{"type": "object", "description": "Input data to pass to the workflow (optional)"}, "sync": map[string]any{"type": "boolean", "description": "If true, wait for workflow completion and return outputs (default: false)"}}, "required": []string{"id"}}},
	{Name: "trigger_list", Description: "List workflow triggers. Optionally filter by workflow ID and/or scope (user identity). Shows trigger type (http/cron), config, alias, and enabled status.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workflow_id": map[string]any{"type": "string", "description": "Filter triggers by workflow ID (optional — lists all if omitted)"}, "scope": map[string]any{"type": "string", "description": "Filter triggers by scope/owner (e.g., telegram chat_id). Only shows triggers created by this scope."}}}},
	{Name: "trigger_create", Description: "Create a cron or HTTP trigger for a workflow. Cron triggers run workflows on a schedule (e.g., every day at 6 AM). HTTP triggers create webhook URLs.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"workflow_id": map[string]any{"type": "string", "description": "Workflow ID to trigger"}, "type": map[string]any{"type": "string", "description": "Trigger type: 'cron' or 'http'", "enum": []string{"cron", "http"}}, "schedule": map[string]any{"type": "string", "description": "Cron expression (for cron type). Examples: '0 6 * * *' (daily 6 AM), '*/30 * * * *' (every 30 min), '0 9 * * 1-5' (weekdays 9 AM)"}, "timezone": map[string]any{"type": "string", "description": "IANA timezone for cron schedule. Default: UTC. Examples: 'Europe/Istanbul', 'America/New_York'"}, "payload": map[string]any{"type": "object", "description": "JSON payload to pass as workflow inputs when triggered"}, "entry_node_id": map[string]any{"type": "string", "description": "Optional: specific input node ID to trigger (for multi-entry workflows)"}, "alias": map[string]any{"type": "string", "description": "Optional human-friendly alias (must be unique)"}, "scope": map[string]any{"type": "string", "description": "Owner scope (e.g., telegram chat_id). Used to isolate triggers per user. ALWAYS set this from the user context."}, "enabled": map[string]any{"type": "boolean", "description": "Whether the trigger is active. Default: true"}}, "required": []string{"workflow_id", "type"}}},
	{Name: "trigger_get", Description: "Get details of a specific trigger by ID.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Trigger ID"}}, "required": []string{"id"}}},
	{Name: "trigger_update", Description: "Update a trigger's schedule, payload, or enabled status.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Trigger ID to update"}, "schedule": map[string]any{"type": "string", "description": "New cron expression"}, "timezone": map[string]any{"type": "string", "description": "New timezone"}, "payload": map[string]any{"type": "object", "description": "New payload"}, "enabled": map[string]any{"type": "boolean", "description": "Enable or disable the trigger"}}, "required": []string{"id"}}},
	{Name: "trigger_delete", Description: "Delete a trigger by ID.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Trigger ID to delete"}}, "required": []string{"id"}}},

	// ─── Persistent Task (Issue Tracker) Tools ───
	{Name: "task_create", Description: "Create a persistent task/issue in the AT database. When called while working inside an existing task, this creates a CHILD task of the current task by default. To intentionally create an unrelated root task from task context, pass root=true and a reason.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "description": "Task title"}, "description": map[string]any{"type": "string", "description": "Task description (markdown supported)"}, "organization_id": map[string]any{"type": "string", "description": "Organization ID to scope this task to"}, "assigned_agent_id": map[string]any{"type": "string", "description": "Agent ID to assign this task to"}, "priority_level": map[string]any{"type": "string", "description": "Priority: critical, high, medium, low", "enum": []string{"critical", "high", "medium", "low"}}, "parent_id": map[string]any{"type": "string", "description": "Parent task ID for sub-tasks. In task context this must be omitted or equal to the current task unless root=true is used."}, "status": map[string]any{"type": "string", "description": "Initial status (default: todo). Options: backlog, todo, in_progress, in_review, done"}, "max_iterations": map[string]any{"type": "number", "description": "Per-task override of the agent's max iterations. 0 = use agent default."}, "root": map[string]any{"type": "boolean", "description": "Only in task context: create an unrelated root task instead of a child task. Requires reason."}, "reason": map[string]any{"type": "string", "description": "Required when root=true in task context; explains why this is not a child task."}}, "required": []string{"title"}}},
	{Name: "task_list", Description: "List persistent tasks/issues with optional filtering by status, organization, or assigned agent.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string", "description": "Filter by status (e.g. in_review, done, in_progress)"}, "organization_id": map[string]any{"type": "string", "description": "Filter by organization ID"}, "assigned_agent_id": map[string]any{"type": "string", "description": "Filter by assigned agent ID"}}}},
	{Name: "task_get", Description: "Get full details of a task/issue including subtasks and comments.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The task ID"}}, "required": []string{"id"}}},
	{Name: "task_update", Description: "Update a task's fields. Only provided fields are changed. Use this to change status (e.g. mark as done), reassign, update description, or set the result.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The task ID to update"}, "title": map[string]any{"type": "string", "description": "New title"}, "description": map[string]any{"type": "string", "description": "New description"}, "status": map[string]any{"type": "string", "description": "New status: backlog, todo, in_progress, in_review, blocked, done, cancelled"}, "priority_level": map[string]any{"type": "string", "description": "New priority: critical, high, medium, low"}, "assigned_agent_id": map[string]any{"type": "string", "description": "New assigned agent ID"}, "result": map[string]any{"type": "string", "description": "Task result/output (typically set when completing a task)"}, "max_iterations": map[string]any{"type": "number", "description": "Per-task override of the agent's max iterations. 0 = use agent default."}}, "required": []string{"id"}}},
	{Name: "task_add_comment", Description: "Add a comment to a task/issue. Useful for providing feedback, requesting changes, or noting decisions.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string", "description": "The task ID to comment on"}, "body": map[string]any{"type": "string", "description": "Comment text (markdown supported)"}, "author_name": map[string]any{"type": "string", "description": "Name of the commenter (default: mcp-user)"}}, "required": []string{"task_id", "body"}}},
	{Name: "task_process", Description: "Trigger async organization delegation on a task. The task's assigned agent (or the org's head agent) will process the task using the LLM-driven delegation loop. Returns immediately (202 Accepted).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The task ID to process"}}, "required": []string{"id"}}},
	{Name: "task_current", Description: "Return the currently active task, including child tasks and comments. Only available while an agent is working inside a task context.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "task_children", Description: "List child tasks under the currently active task. Use this before creating duplicate follow-up work.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "task_create_child", Description: "Create a child task under the currently active task. This is the correct tool for any new work item derived from the current task.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "description": "Child task title"}, "description": map[string]any{"type": "string", "description": "Child task description or acceptance criteria"}, "assigned_agent_id": map[string]any{"type": "string", "description": "Agent ID to assign this child task to"}, "priority_level": map[string]any{"type": "string", "description": "Priority: critical, high, medium, low", "enum": []string{"critical", "high", "medium", "low"}}, "status": map[string]any{"type": "string", "description": "Initial status (default: todo)"}, "max_iterations": map[string]any{"type": "number", "description": "Per-task override of the assigned agent's max iterations. 0 = use agent default."}}, "required": []string{"title"}}},
	{Name: "task_update_current", Description: "Update the currently active task only. Use this instead of task_update while working inside a task context.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "description": "New title"}, "description": map[string]any{"type": "string", "description": "New description"}, "status": map[string]any{"type": "string", "description": "New status: backlog, todo, in_progress, in_review, blocked, done, cancelled"}, "priority_level": map[string]any{"type": "string", "description": "New priority: critical, high, medium, low"}, "assigned_agent_id": map[string]any{"type": "string", "description": "New assigned agent ID"}, "result": map[string]any{"type": "string", "description": "Task result/output"}, "max_iterations": map[string]any{"type": "number", "description": "Per-task override of the agent's max iterations. 0 = use agent default."}}}},
	{Name: "task_comment_current", Description: "Add a comment to the currently active task only.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"body": map[string]any{"type": "string", "description": "Comment text (markdown supported)"}, "author_name": map[string]any{"type": "string", "description": "Name of the commenter. Defaults to the current agent ID."}}, "required": []string{"body"}}},
	{Name: "task_complete", Description: "Mark the currently active task as completed with a final result. After calling this, provide a short final response.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"result": map[string]any{"type": "string", "description": "Final task result/output"}}, "required": []string{"result"}}},
	{Name: "task_block", Description: "Mark the currently active task as blocked with a clear reason or missing dependency.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string", "description": "Why this task is blocked and what is needed to unblock it"}}, "required": []string{"reason"}}},

	// ─── Organization Management Tools ───
	{Name: "org_create", Description: "Create a new organization. Organizations group agents into teams with hierarchical delegation, budgets, and issue tracking.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Organization name"}, "description": map[string]any{"type": "string", "description": "Organization description"}, "issue_prefix": map[string]any{"type": "string", "description": "Prefix for issue identifiers (e.g. 'YTS' for YTS-1, YTS-2)"}, "head_agent_id": map[string]any{"type": "string", "description": "ID of the head agent who receives incoming tasks"}, "budget_monthly_cents": map[string]any{"type": "number", "description": "Monthly budget in cents (e.g. 5000 = $50)"}, "max_delegation_depth": map[string]any{"type": "number", "description": "Maximum depth of delegation chain (default: 3)"}, "require_board_approval": map[string]any{"type": "boolean", "description": "Require approval for new agent additions"}}, "required": []string{"name"}}},
	{Name: "org_list", Description: "List all organizations with their key details.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "org_get", Description: "Get an organization's full details including its agent roster (hierarchy).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The organization ID"}}, "required": []string{"id"}}},
	{Name: "org_add_agent", Description: "Add an agent to an organization's hierarchy, or create a pending hire_agent approval when the organization requires board approval. Set parent_agent_id to create reporting lines for delegation.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"organization_id": map[string]any{"type": "string", "description": "The organization ID"}, "agent_id": map[string]any{"type": "string", "description": "The agent ID to add"}, "role": map[string]any{"type": "string", "description": "Role in the org (e.g. head, member)"}, "title": map[string]any{"type": "string", "description": "Title/position (e.g. Content Director, Script Writer)"}, "parent_agent_id": map[string]any{"type": "string", "description": "The agent ID of the parent agent in this org's hierarchy (e.g. the Content Director's agent ID). This creates a reporting line from this agent to the parent."}}, "required": []string{"organization_id", "agent_id"}}},
	{Name: "org_task_intake", Description: "Submit a task to an organization for processing. The task is assigned to the org's head agent who delegates to specialist agents. Returns immediately with the task ID while delegation runs in the background. This is the primary way to trigger agent pipelines (e.g. 'create a YouTube Short about X').", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"organization_id": map[string]any{"type": "string", "description": "The organization ID to submit the task to"}, "title": map[string]any{"type": "string", "description": "Task title (e.g. 'Create a short about quantum computing')"}, "description": map[string]any{"type": "string", "description": "Additional context or requirements"}, "priority_level": map[string]any{"type": "string", "description": "Priority: critical, high, medium, low", "enum": []string{"critical", "high", "medium", "low"}}, "max_iterations": map[string]any{"type": "number", "description": "Per-task override of the head agent's max iterations. 0 = use agent default."}}, "required": []string{"organization_id", "title"}}},

	// ─── Agent Management Tools ───
	{Name: "agent_create", Description: "Create a new AI agent with LLM provider, model, system prompt, and tool configuration.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Agent name (unique identifier)"}, "provider": map[string]any{"type": "string", "description": "LLM provider key (configured in AT providers)"}, "model": map[string]any{"type": "string", "description": "Model identifier (e.g. gpt-4o, claude-sonnet-4-20250514)"}, "system_prompt": map[string]any{"type": "string", "description": "System prompt that defines the agent's behavior and role"}, "description": map[string]any{"type": "string", "description": "Agent description"}, "skills": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Skill IDs or names to assign to the agent"}, "mcp_sets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "MCP Set names (internal MCPs) to assign to the agent"}, "builtin_tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Built-in tool names to enable for the agent"}, "max_iterations": map[string]any{"type": "number", "description": "Maximum agentic loop iterations (default: 10)"}, "tool_timeout": map[string]any{"type": "number", "description": "Per-tool timeout in seconds (default: 60)"}}, "required": []string{"name"}}},
	{Name: "agent_list", Description: "List all AI agents with their key configuration details.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "agent_get", Description: "Get an agent's full details including its complete configuration (provider, model, system prompt, skills, tools).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The agent ID"}}, "required": []string{"id"}}},
	{Name: "agent_update", Description: "Update an agent's configuration. Only provided fields are changed.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The agent ID to update"}, "name": map[string]any{"type": "string", "description": "New agent name"}, "provider": map[string]any{"type": "string", "description": "New LLM provider key"}, "model": map[string]any{"type": "string", "description": "New model identifier"}, "system_prompt": map[string]any{"type": "string", "description": "New system prompt"}, "description": map[string]any{"type": "string", "description": "New description"}, "skills": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "New skill list (replaces existing)"}, "mcp_sets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "New MCP set list (replaces existing)"}, "builtin_tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "New built-in tools list (replaces existing)"}, "max_iterations": map[string]any{"type": "number", "description": "New max iterations"}, "tool_timeout": map[string]any{"type": "number", "description": "New tool timeout in seconds"}}, "required": []string{"id"}}},

	// ─── Skill Management Tools ───
	{Name: "skill_list", Description: "List installed skills and available skill templates. Shows both what's already installed and what templates can be installed.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"category": map[string]any{"type": "string", "description": "Filter templates by category (e.g. 'Content Creation', 'Development', 'Utilities')"}}}},
	{Name: "skill_get", Description: "Get a skill's full details by ID, including its system prompt and the complete list of tool definitions (with handlers).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The skill ID"}}, "required": []string{"id"}}},
	{Name: "skill_install_template", Description: "Install a skill from a built-in template. After installation, the skill can be assigned to agents. Check required variables in the response.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"slug": map[string]any{"type": "string", "description": "Template slug identifier (use skill_list to see available templates)"}}, "required": []string{"slug"}}},
	{Name: "skill_create", Description: "Create a new custom skill with a system prompt fragment and a list of tools. Each tool has a name, description, JSON-schema input_schema, and a handler (JavaScript code by default, or bash if handler_type='bash'). Skills are reusable: once created, they can be assigned to any agent. Useful when an agent needs to author a domain-specific capability set rather than a single tool.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string", "description": "Skill name (unique). Lowercase letters, numbers, underscores recommended."},
			"description":   map[string]any{"type": "string", "description": "Short description shown in skill listings"},
			"category":      map[string]any{"type": "string", "description": "Optional category (e.g. 'Content Creation', 'Development', 'Utilities')"},
			"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional tags for grouping/filtering"},
			"system_prompt": map[string]any{"type": "string", "description": "Prompt fragment appended to the agent's system prompt when this skill is loaded"},
			"tools": map[string]any{
				"type":        "array",
				"description": "Tool definitions exposed by this skill. Each tool may include a JS or bash handler that runs when an agent calls it.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":         map[string]any{"type": "string", "description": "Tool name (unique within the skill)"},
						"description":  map[string]any{"type": "string", "description": "Description shown to the LLM when picking tools"},
						"input_schema": map[string]any{"type": "object", "description": "JSON Schema describing the tool's arguments"},
						"handler":      map[string]any{"type": "string", "description": "Handler source. JS by default: write a function body; arguments are available as the `args` object; return a string. Bash: write a shell script; arguments are exposed as ARG_<UPPER_KEY> env vars; stdout becomes the result."},
						"handler_type": map[string]any{"type": "string", "description": "'js' (default) or 'bash'", "enum": []string{"js", "bash"}},
					},
					"required": []string{"name", "description"},
				},
			},
		},
		"required": []string{"name"},
	}},
	{Name: "skill_update", Description: "Update an existing skill. The full skill (name, description, system_prompt, tools) is replaced; pass the complete intended state. To make a small edit, fetch with skill_get first, modify the returned object, and pass the result back.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            map[string]any{"type": "string", "description": "The skill ID to update"},
			"name":          map[string]any{"type": "string", "description": "Skill name"},
			"description":   map[string]any{"type": "string", "description": "Short description"},
			"category":      map[string]any{"type": "string", "description": "Category"},
			"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags"},
			"system_prompt": map[string]any{"type": "string", "description": "Prompt fragment appended to the agent's system prompt"},
			"version":       map[string]any{"type": "string", "description": "Skill version (kept from the existing skill when omitted)"},
			"author":        map[string]any{"type": "string", "description": "Author attribution (kept from the existing skill when omitted)"},
			"license":       map[string]any{"type": "string", "description": "License identifier (kept from the existing skill when omitted)"},
			"tools": map[string]any{
				"type":        "array",
				"description": "Full replacement of the tool list. Same shape as skill_create.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":         map[string]any{"type": "string"},
						"description":  map[string]any{"type": "string"},
						"input_schema": map[string]any{"type": "object"},
						"handler":      map[string]any{"type": "string"},
						"handler_type": map[string]any{"type": "string", "enum": []string{"js", "bash"}},
					},
					"required": []string{"name", "description"},
				},
			},
		},
		"required": []string{"id", "name"},
	}},
	{Name: "skill_delete", Description: "Delete a skill by ID. Agents using this skill will lose access to its tools and system-prompt fragment on their next run.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The skill ID to delete"}}, "required": []string{"id"}}},
	{Name: "skill_test_handler", Description: "Test-execute a single tool handler (JS or bash) with sample arguments without persisting it. Useful for iterating on a handler before saving the skill. Returns the handler's stdout/return value plus duration.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"handler":      map[string]any{"type": "string", "description": "Handler source code (JS body or bash script)"},
			"handler_type": map[string]any{"type": "string", "description": "'js' (default) or 'bash'", "enum": []string{"js", "bash"}},
			"arguments":    map[string]any{"type": "object", "description": "Sample arguments object to pass to the handler"},
		},
		"required": []string{"handler"},
	}},
	{Name: "skill_export", Description: "Export a skill as a portable JSON document (no IDs/timestamps). Use the result as input to skill_import on another instance, or to back up a skill before editing.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The skill ID to export"}}, "required": []string{"id"}}},
	{Name: "skill_import", Description: "Import a skill from a portable JSON document (the shape produced by skill_export). Creates a new skill with a fresh ID. Pass the export object directly.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string", "description": "Skill name"},
			"description":   map[string]any{"type": "string", "description": "Description"},
			"system_prompt": map[string]any{"type": "string", "description": "System prompt fragment"},
			"tools":         map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Tool definitions (same shape as skill_create.tools)"},
			"version":       map[string]any{"type": "string", "description": "Skill version declared by the author (semver recommended)"},
			"author":        map[string]any{"type": "string", "description": "Author attribution"},
			"license":       map[string]any{"type": "string", "description": "SPDX-style license identifier (e.g. MIT)"},
		},
		"required": []string{"name"},
	}},
	{Name: "skill_import_url", Description: "Fetch a skill from a URL and install it. Auto-detects JSON (skill_export format) and Anthropic SKILL.md (markdown with frontmatter) formats. Useful for installing skills from a Git raw URL or a marketplace.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "description": "URL to a skill JSON or SKILL.md file"}}, "required": []string{"url"}}},
	{Name: "skill_import_skillmd", Description: "Import a skill by parsing raw Anthropic SKILL.md content. Frontmatter must contain at least `name` and `description`; the body becomes the skill's system_prompt.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string", "description": "Raw SKILL.md content"}}, "required": []string{"content"}}},

	// ─── MCP Server / MCP Set Management Tools ───
	{Name: "mcp_server_list", Description: "List all general (gateway-facing) MCP servers. These expose composed tool sets (HTTP tools, upstream MCPs, RAG, skills, builtins, workflows) over a gateway MCP endpoint. Endpoints require bearer auth unless public is true.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "mcp_server_get", Description: "Get full details of a gateway-facing MCP server, including its config (HTTP tools, upstream MCPs, enabled skills/builtins, RAG settings).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The MCP server ID"}}, "required": []string{"id"}}},
	{Name: "mcp_server_create", Description: "Create a new gateway-facing MCP server. The `config` object can declare HTTP tools, upstream MCP servers (HTTP or stdio), enabled skill names, enabled builtin tool names, RAG collections, and workflow IDs. Once created, agents and external MCP clients can call its tools.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "MCP server name (unique, used in the public URL)"},
			"description": map[string]any{"type": "string", "description": "Description"},
			"public":      map[string]any{"type": "boolean", "description": "Allow unauthenticated MCP clients to access this server"},
			"config": map[string]any{
				"type":        "object",
				"description": "MCP server configuration",
				"properties": map[string]any{
					"description":           map[string]any{"type": "string"},
					"http_tools":            map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Custom HTTP tools: [{name, description, method, url, headers?, body_template?, input_schema}]"},
					"mcp_upstreams":         map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Upstream MCP servers to proxy: [{url, headers?} or {command, args?, env?}]"},
					"enabled_skills":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Skill names whose tools should be exposed"},
					"enabled_builtin_tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Builtin tool names to expose (e.g. 'http_request', 'task_create')"},
					"workflow_ids":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workflow IDs to expose as named tools"},
					"enabled_rag_tools":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "RAG tool names to expose (e.g. 'rag_search', 'rag_list_collections')"},
					"collection_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "RAG collection IDs scoped to this server"},
					"fetch_mode":            map[string]any{"type": "string"},
					"default_num_results":   map[string]any{"type": "integer"},
					"token_variable":        map[string]any{"type": "string"},
					"token_user":            map[string]any{"type": "string"},
					"ssh_key_variable":      map[string]any{"type": "string"},
				},
			},
			"servers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional list of upstream server names referenced by config"},
			"urls":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional list of public URLs"},
		},
		"required": []string{"name"},
	}},
	{Name: "mcp_server_update", Description: "Update a gateway-facing MCP server. Full replacement: pass the complete intended state. Fetch with mcp_server_get first, mutate, then submit.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "The MCP server ID to update"},
			"name":        map[string]any{"type": "string", "description": "MCP server name"},
			"description": map[string]any{"type": "string"},
			"public":      map[string]any{"type": "boolean", "description": "Allow unauthenticated MCP clients to access this server"},
			"config":      map[string]any{"type": "object", "description": "Same shape as mcp_server_create.config"},
			"servers":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"urls":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "name"},
	}},
	{Name: "mcp_server_delete", Description: "Delete a gateway-facing MCP server by ID. Existing MCP clients pointed at its URL will start receiving 404 on their next request.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The MCP server ID to delete"}}, "required": []string{"id"}}},

	{Name: "mcp_set_list", Description: "List all MCP Sets — internal MCP configurations agents can be assigned via the `mcp_sets` field on agent_create/agent_update. Each set composes builtins, skills, HTTP tools, upstream MCPs, RAG, and workflows.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "mcp_set_get", Description: "Get full details of an MCP Set by ID, including its config.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The MCP Set ID"}}, "required": []string{"id"}}},
	{Name: "mcp_set_create", Description: "Create a new MCP Set. Same config shape as mcp_server_create — the difference is that MCP Sets are consumed internally by agents (referenced by name in agent.mcp_sets) rather than exposed as a public gateway endpoint.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string", "description": "MCP Set name (unique; agents reference it by this name)"},
			"description": map[string]any{"type": "string"},
			"category":    map[string]any{"type": "string"},
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"config": map[string]any{
				"type":        "object",
				"description": "Same shape as mcp_server_create.config — see that tool's docs for fields.",
			},
			"servers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"urls":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"name"},
	}},
	{Name: "mcp_set_update", Description: "Update an MCP Set. Full replacement; fetch with mcp_set_get first, mutate, then submit.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "The MCP Set ID to update"},
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"category":    map[string]any{"type": "string"},
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"config":      map[string]any{"type": "object"},
			"servers":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"urls":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "name"},
	}},
	{Name: "mcp_set_delete", Description: "Delete an MCP Set by ID. Agents currently referencing it by name will lose its tools on their next run.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The MCP Set ID to delete"}}, "required": []string{"id"}}},

	// ─── Provider Tools ───
	{Name: "provider_list", Description: "List all configured LLM providers with their available models. Use this to discover which providers and models are available when creating or updating agents. API keys are redacted.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "provider_get", Description: "Get detailed configuration for a specific LLM provider by key. Shows available models, default model, base URL, and auth type. API keys are redacted.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string", "description": "The provider key (e.g. 'openai', 'anthropic', 'groq')"}}, "required": []string{"key"}}},

	// ─── Approval Tools ───
	{Name: "approval_list_pending", Description: "List pending approval requests. Approvals are governance decisions like hiring agents, budget changes, or task escalations.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"organization_id": map[string]any{"type": "string", "description": "Filter by organization ID (optional)"}}}},
	{Name: "approval_decide", Description: "Approve or reject a pending approval request.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The approval ID"}, "status": map[string]any{"type": "string", "description": "Decision: approved, rejected, revision_requested, cancelled", "enum": []string{"approved", "rejected", "revision_requested", "cancelled"}}, "decision_note": map[string]any{"type": "string", "description": "Reason or note for the decision"}}, "required": []string{"id", "status"}}},

	// ─── LSP Tool ───
	{Name: "lsp_query", Description: "Interact with Language Server Protocol (LSP) servers for code intelligence. Supports goToDefinition, findReferences, hover, documentSymbol, workspaceSymbol, goToImplementation. Automatically starts the appropriate LSP server based on file language (Go, TypeScript, JavaScript, Python, Rust, Java, C/C++).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"type": "string", "description": "The LSP operation to perform", "enum": []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol", "goToImplementation"}}, "file_path": map[string]any{"type": "string", "description": "The absolute path to the file (required for all operations except workspaceSymbol)"}, "line": map[string]any{"type": "integer", "description": "Line number (0-indexed) for position-based operations"}, "character": map[string]any{"type": "integer", "description": "Character offset (0-indexed) within the line"}, "query": map[string]any{"type": "string", "description": "Search query (for workspaceSymbol operation)"}, "language": map[string]any{"type": "string", "description": "Programming language (auto-detected from file extension if omitted). Supported: go, typescript, javascript, python, rust, java, c, cpp"}}, "required": []string{"operation"}}},

	// ─── Bot Config Management Tools ───
	// Tokens are redacted in all responses; pass the real token only when
	// you intentionally need to update it. Most common use case: updating
	// the `custom_commands` array on a bot to add/edit slash commands like
	// /asmr or /silent without going through curl.
	{Name: "bot_list", Description: "List all bot configurations (Telegram, Discord). Tokens are redacted in the response — never returns raw bot tokens. Use this to discover bot IDs before calling bot_get or bot_update.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "bot_get", Description: "Get a single bot config by ID, including its current custom_commands list, allowed users/agents, and routing settings. Token is redacted.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "The bot config ID (e.g. '01KQ3AGX7TQY275NBFH8A23751')"}}, "required": []string{"id"}}},
	{Name: "bot_update", Description: "Update a bot configuration. PARTIAL update — only the fields you provide are changed; everything else is preserved. The most common use case is editing the `custom_commands` array to add/edit slash commands like /asmr, /silent on a Telegram bot. Pass `custom_commands` as a complete array (full replacement of the list). Each command entry is `{command: 'asmr', description?: 'short text', organization_id?: '01K...', agent_id?: '01K...', brief?: 'task description template, supports {args} substitution', title_prefix?: '[ASMR]', max_iterations?: 0}`. Either organization_id (routes via org head agent) OR agent_id (routes to a specific agent) — if neither is set, the bot's default_agent_id handles the command. Returns the updated config (token redacted).", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                map[string]any{"type": "string", "description": "The bot config ID to update"},
			"name":              map[string]any{"type": "string", "description": "New bot display name"},
			"platform":          map[string]any{"type": "string", "description": "New platform: 'telegram' or 'discord'"},
			"token":             map[string]any{"type": "string", "description": "New bot token (e.g. Telegram BotFather token). Refused if you accidentally pass the redaction placeholder."},
			"default_agent_id":  map[string]any{"type": "string", "description": "Default agent that handles unknown commands and chat messages"},
			"allowed_agent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Agents the user can /switch to (full replacement of the list)"},
			"channel_agents":    map[string]any{"type": "object", "description": "chat_id → agent_id overrides (full replacement of the map)"},
			"custom_commands": map[string]any{
				"type":        "array",
				"description": "Full replacement of the bot's custom slash-command list. Each entry: {command, description?, organization_id?, agent_id?, brief?, title_prefix?, max_iterations?}. The 'command' field is stored without leading slash (a leading '/' is auto-stripped if present).",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command":         map[string]any{"type": "string", "description": "Slash command name without the leading slash, e.g. 'asmr'"},
						"description":     map[string]any{"type": "string", "description": "Short text shown next to the command in /help"},
						"organization_id": map[string]any{"type": "string", "description": "When set, the command creates a task assigned to this org's head agent (preferred for pipeline orgs)"},
						"agent_id":        map[string]any{"type": "string", "description": "When set (and organization_id is not), the command creates a task directly assigned to this agent"},
						"brief":           map[string]any{"type": "string", "description": "Task description template. The literal token '{args}' is replaced with whatever the user typed after the command."},
						"title_prefix":    map[string]any{"type": "string", "description": "Prefix prepended to the new task's title, e.g. '[ASMR]'"},
						"max_iterations":  map[string]any{"type": "number", "description": "Per-task override of the agent's max_iterations. 0 = use agent default."},
					},
					"required": []string{"command"},
				},
			},
			"access_mode":      map[string]any{"type": "string", "description": "Access policy: 'public', 'allowlist', or 'pending'"},
			"pending_approval": map[string]any{"type": "boolean", "description": "Require admin approval for new users"},
			"allowed_users":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowlist of telegram/discord user IDs (full replacement)"},
			"pending_users":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Users waiting on approval (full replacement)"},
			"enabled":          map[string]any{"type": "boolean", "description": "Whether the bot polls/runs. Toggle to start/stop."},
			"user_containers":  map[string]any{"type": "boolean", "description": "Whether each user gets a sandboxed container"},
			"container_image":  map[string]any{"type": "string", "description": "Container image for user_containers mode"},
			"container_cpu":    map[string]any{"type": "string", "description": "CPU limit for user containers, e.g. '500m'"},
			"container_memory": map[string]any{"type": "string", "description": "Memory limit for user containers, e.g. '1Gi'"},
			"speech_to_text":   map[string]any{"type": "string", "description": "STT provider for voice messages: 'none', 'openai', 'whisper'"},
			"whisper_model":    map[string]any{"type": "string", "description": "Whisper model name when speech_to_text is set"},
		},
		"required": []string{"id"},
	}},

	// ─── Bot Lifecycle Tools (Phase 2) ───
	// These complement bot_list/get/update with the lifecycle ops the UI
	// exposes: create a brand-new bot from an external token, delete one,
	// and explicitly start/stop the polling goroutine. The token field is
	// REQUIRED on create — the caller must source a real Telegram/Discord
	// token from outside the system. Every response that echoes a bot
	// record redacts the token field via redactBotToken (same behaviour as
	// bot_get).
	{Name: "bot_create", Description: "Create a new bot configuration. Requires `platform` ('telegram' or 'discord') and `token`. If `enabled=true` (default) and a token is present, the polling goroutine is started immediately. Returns the created record with the token redacted.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"platform":          map[string]any{"type": "string", "description": "'telegram' or 'discord'", "enum": []string{"telegram", "discord"}},
			"name":              map[string]any{"type": "string", "description": "Display name for the bot"},
			"token":             map[string]any{"type": "string", "description": "Bot token from Telegram BotFather or Discord developer portal. Required."},
			"default_agent_id":  map[string]any{"type": "string", "description": "Agent that handles unknown commands and chat messages"},
			"allowed_agent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Agents the user can /switch to"},
			"channel_agents":    map[string]any{"type": "object", "description": "chat_id → agent_id overrides (object with string values)"},
			"custom_commands":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Slash command list, same shape as bot_update.custom_commands"},
			"access_mode":       map[string]any{"type": "string", "description": "'public', 'allowlist', or 'pending'"},
			"pending_approval":  map[string]any{"type": "boolean"},
			"allowed_users":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"enabled":           map[string]any{"type": "boolean", "description": "Whether the bot polls. Defaults to true; if false, the bot is stored but not started."},
			"user_containers":   map[string]any{"type": "boolean"},
			"container_image":   map[string]any{"type": "string"},
			"container_cpu":     map[string]any{"type": "string"},
			"container_memory":  map[string]any{"type": "string"},
			"speech_to_text":    map[string]any{"type": "string"},
			"whisper_model":     map[string]any{"type": "string"},
		},
		"required": []string{"platform", "token"},
	}},
	{Name: "bot_delete", Description: "Delete a bot configuration by ID. Stops the polling goroutine first if running. Use this to permanently remove a bot — for a temporary stop without losing config, use bot_stop.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Bot config ID"}}, "required": []string{"id"}}},
	{Name: "bot_start", Description: "Start the polling goroutine for a bot. Returns 409-equivalent error if already running. Sets enabled=true on the stored record so the bot auto-starts on the next AT restart.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Bot config ID"}}, "required": []string{"id"}}},
	{Name: "bot_stop", Description: "Stop the polling goroutine for a bot. Returns 409-equivalent error if not running. Sets enabled=false on the stored record so the bot does NOT auto-start on the next AT restart.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Bot config ID"}}, "required": []string{"id"}}},
	{Name: "bot_status", Description: "Get the live runtime status of a bot. Returns {running, platform, started_at} when running, or {running:false} otherwise. This reflects the in-memory polling state, not the `enabled` flag in the DB.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Bot config ID"}}, "required": []string{"id"}}},

	// ─── Provider Write Tools (Phase 2) ───
	// Read-only provider_list / provider_get were already exposed; these
	// add full create/update/delete plus model discovery. Provider records
	// are keyed by `key` (string), NOT by ULID — the UI uses keys like
	// "openai-prod" or "anthropic-main". `api_key` and `refresh_token`
	// are auto-redacted to "***" in any response that echoes the config,
	// matching the HTTP API behaviour. On Update, empty `api_key` /
	// `refresh_token` preserve the existing stored secrets so a fetch +
	// edit + write round-trip can't accidentally clobber them.
	{Name: "provider_create", Description: "Register a new LLM provider. The provider is hot-reloaded into the live registry on success — no restart needed. The `config.type` field selects the adapter (openai, anthropic, gemini, minimax, vertex). API keys are encrypted at rest. Returns the created record with secrets redacted.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{"type": "string", "description": "Provider key (unique). Used in agent.provider and the public /gateway/v1 routing."},
			"config": map[string]any{
				"type":        "object",
				"description": "LLMConfig",
				"properties": map[string]any{
					"type":          map[string]any{"type": "string", "description": "Adapter type", "enum": service.SupportedProviderTypes},
					"api_key":       map[string]any{"type": "string", "description": "Provider API key (stored encrypted)"},
					"base_url":      map[string]any{"type": "string", "description": "Override base URL (e.g. for OpenAI-compatible self-hosted endpoints)"},
					"model":         map[string]any{"type": "string", "description": "Default model ID (returned as 'default_model' in read responses)"},
					"models":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowlist of model IDs"},
					"auth_type":     map[string]any{"type": "string", "description": "For Anthropic-style providers: e.g. 'oauth' or empty"},
					"extra_headers": map[string]any{"type": "object", "description": "Extra HTTP headers added on every request"},
					"proxy":         map[string]any{"type": "string", "description": "HTTP/HTTPS proxy URL"},
					"refresh_token": map[string]any{"type": "string", "description": "Refresh token (stored encrypted) for OAuth-based providers"},
					"rate_limit": map[string]any{
						"type":        "object",
						"description": "Per-provider rate limits (all >= 0; retry_after_cap_ms may be -1 = no cap)",
						"properties": map[string]any{
							"requests_per_minute":     map[string]any{"type": "integer"},
							"input_tokens_per_minute": map[string]any{"type": "integer"},
							"max_concurrent":          map[string]any{"type": "integer"},
							"wait_timeout_ms":         map[string]any{"type": "integer"},
							"retry_after_cap_ms":      map[string]any{"type": "integer", "description": "-1 = no cap, 0 = default, > 0 = explicit cap"},
						},
					},
					"insecure_skip_verify": map[string]any{"type": "boolean"},
				},
				"required": []string{"type"},
			},
		},
		"required": []string{"key", "config"},
	}},
	{Name: "provider_update", Description: "Update an existing LLM provider config. Empty `api_key` or `refresh_token` preserve the current stored secrets (so callers can edit other fields without re-supplying tokens). The provider is hot-reloaded on success.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":    map[string]any{"type": "string", "description": "Provider key to update"},
			"config": map[string]any{"type": "object", "description": "Same shape as provider_create.config. config.type is required."},
		},
		"required": []string{"key", "config"},
	}},
	{Name: "provider_delete", Description: "Delete an LLM provider by key. The provider is also removed from the in-memory registry; agents referencing it will fail until they're updated.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string", "description": "Provider key to delete"}}, "required": []string{"key"}}},
	{Name: "provider_discover_models", Description: "Discover available model IDs for a provider config by calling its model-listing API. Supported types: openai, anthropic, gemini, minimax. Pass an existing `key` to fall back to the stored API key if `config.api_key` is empty (useful when editing a provider whose key is redacted). Returns {models: [...]}.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"config": map[string]any{
				"type":        "object",
				"description": "LLMConfig — `type` is required, plus `api_key` / `auth_type` / `base_url` as needed",
				"properties": map[string]any{
					"type":          map[string]any{"type": "string"},
					"api_key":       map[string]any{"type": "string"},
					"base_url":      map[string]any{"type": "string"},
					"auth_type":     map[string]any{"type": "string"},
					"extra_headers": map[string]any{"type": "object"},
					"proxy":         map[string]any{"type": "string"},
				},
				"required": []string{"type"},
			},
			"key": map[string]any{"type": "string", "description": "Optional existing provider key to fall back to its stored api_key/auth_type"},
		},
		"required": []string{"config"},
	}},

	// ─── API Token Tools (Phase 2) ───
	// Gateway API tokens authenticate inbound `/gateway/v1/*` calls. The
	// raw token value is returned ONLY ONCE on create — subsequent reads
	// only show `token_prefix` (first 8 chars). Generation, hashing, and
	// validation mirror the HTTP handler at api-tokens.go:113-125.
	{Name: "apitoken_list", Description: "List all gateway API tokens. The raw token values are NEVER returned — only `token_prefix` (first 8 chars, e.g. 'at_xxxxx') and metadata. Use the prefix for display; the prefix alone cannot authenticate.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "apitoken_create", Description: "Create a new gateway API token. The raw token (`at_<64hex>`, 67 chars total) is returned ONCE in the response — store it immediately, it cannot be recovered later. The token is hashed (SHA-256) before storage; the plaintext never touches the DB. Allow-list modes: 'all' (default), 'none', 'list' (use the corresponding `allowed_*` array).", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":                   map[string]any{"type": "string", "description": "Human-readable name"},
			"allowed_providers_mode": map[string]any{"type": "string", "enum": []string{"all", "none", "list"}, "description": "default: all"},
			"allowed_providers":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Provider keys allowed when mode=list"},
			"allowed_models_mode":    map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_models":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allowed_webhooks_mode":  map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_webhooks":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allowed_rag_mcps_mode":  map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_rag_mcps":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"expires_at":             map[string]any{"type": "string", "description": "RFC3339 timestamp; omit for no expiry"},
			"total_token_limit":      map[string]any{"type": "integer", "description": "Max total LLM tokens; omit for unlimited"},
			"limit_reset_interval":   map[string]any{"type": "string", "description": "Duration string ('24h', '7d', '30d') or omit for manual reset"},
		},
		"required": []string{"name"},
	}},
	{Name: "apitoken_update", Description: "Update an existing gateway API token's metadata (name, allow lists, expiry, limits). The raw token value cannot be changed — to rotate, delete and create a new one.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                     map[string]any{"type": "string", "description": "Token ID"},
			"name":                   map[string]any{"type": "string"},
			"allowed_providers_mode": map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_providers":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allowed_models_mode":    map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_models":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allowed_webhooks_mode":  map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_webhooks":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"allowed_rag_mcps_mode":  map[string]any{"type": "string", "enum": []string{"all", "none", "list"}},
			"allowed_rag_mcps":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"expires_at":             map[string]any{"type": "string"},
			"total_token_limit":      map[string]any{"type": "integer"},
			"limit_reset_interval":   map[string]any{"type": "string"},
		},
		"required": []string{"id", "name"},
	}},
	{Name: "apitoken_delete", Description: "Delete a gateway API token. Inbound calls using this token start failing with 401 immediately.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "apitoken_get_usage", Description: "Get token usage records (requests, input/output tokens, costs) for an API token. Useful for auditing.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "apitoken_reset_usage", Description: "Reset usage counters for an API token (zero out the rolling counters used by `total_token_limit`). Used to manually reopen a token that hit its limit.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},

	// ─── Variable Management Tools (Phase 2) ───
	// Variables are the key-value store backing skill/workflow handlers.
	// Secret variables are encrypted at rest and redacted to "***" in
	// list responses (Get returns the unredacted value, matching the UI
	// behaviour). Create is an upsert by key — if a variable with the
	// same key exists, it's updated instead of erroring (mirrors HTTP).
	{Name: "variable_list", Description: "List all variables. Secret variable values are redacted to '***' in this response; use variable_get to retrieve a specific secret unredacted.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "variable_get", Description: "Get a single variable by ID, INCLUDING its full unredacted value (even for secrets). Use this when a skill/workflow handler needs the live value — it's the same code path the UI's edit form uses.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Variable ID"}}, "required": []string{"id"}}},
	{Name: "variable_create", Description: "Create or upsert a variable. If a variable with the same `key` already exists, it's UPDATED instead of erroring (the value/description/secret flag are replaced). Use `secret: true` for tokens/passwords — these are encrypted at rest and redacted in list responses.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":         map[string]any{"type": "string", "description": "Variable key. Skills reference variables by key (case-sensitive)."},
			"value":       map[string]any{"type": "string", "description": "Variable value"},
			"description": map[string]any{"type": "string", "description": "Optional description"},
			"secret":      map[string]any{"type": "boolean", "description": "If true, value is encrypted at rest and redacted in list responses"},
		},
		"required": []string{"key", "value"},
	}},
	{Name: "variable_update", Description: "Update a variable by ID. Pass the full intended state (key + value at minimum). Use variable_create to upsert by key without knowing the ID.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Variable ID"},
			"key":         map[string]any{"type": "string"},
			"value":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"secret":      map[string]any{"type": "boolean"},
		},
		"required": []string{"id", "key"},
	}},
	{Name: "variable_delete", Description: "Delete a variable by ID. Skills/workflows referencing the key will start failing on next access.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},

	// ─── Connection Management Tools (Phase 2) ───
	// Connections are named credential bundles for external services
	// (YouTube, Twitter, OpenRouter, etc.). Credentials are AES-256-GCM
	// encrypted at rest. Secret fields (client_secret, refresh_token,
	// api_key, extra) are redacted to "*_set: true" booleans in every
	// response by default; pass `reveal: true` to connection_get to
	// receive the actual values (use sparingly — they're returned in
	// plaintext to the agent's tool-result history). On Update, empty
	// secret fields preserve the existing stored secret so renames
	// don't clobber tokens.
	{Name: "connection_list", Description: "List all connections (or filter by `provider`). Secret fields are always redacted in this response (returned as `*_set: true` booleans). Use connection_get with reveal=true to get actual values.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"provider": map[string]any{"type": "string", "description": "Filter by provider (e.g. 'youtube', 'twitter', 'openrouter')"},
		},
	}},
	{Name: "connection_get", Description: "Get a connection by ID. By default secrets are redacted (only `*_set` booleans returned). Pass `reveal: true` to receive the actual `client_secret`, `refresh_token`, `api_key`, and `extra` values — use this only when the agent genuinely needs them, since the response is logged in tool history.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":     map[string]any{"type": "string", "description": "Connection ID"},
			"reveal": map[string]any{"type": "boolean", "description": "If true, return secret values unredacted. Default: false."},
		},
		"required": []string{"id"},
	}},
	{Name: "connection_create", Description: "Create a new connection (named credential bundle for an external service). Required: `provider` and `name` (unique within provider). Credentials are encrypted at rest. Use `metadata` for non-secret context like scopes or expires_at.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"provider":      map[string]any{"type": "string", "description": "Provider key (e.g. 'youtube', 'twitter', or a skill slug)"},
			"name":          map[string]any{"type": "string", "description": "Connection name (unique within provider, e.g. 'Main Channel')"},
			"account_label": map[string]any{"type": "string", "description": "Human-readable identity (channel title, email, etc.)"},
			"description":   map[string]any{"type": "string"},
			"credentials": map[string]any{
				"type":        "object",
				"description": "Secret bundle. All fields optional; populate the subset relevant to the provider.",
				"properties": map[string]any{
					"client_id":     map[string]any{"type": "string"},
					"client_secret": map[string]any{"type": "string"},
					"refresh_token": map[string]any{"type": "string"},
					"api_key":       map[string]any{"type": "string"},
					"extra":         map[string]any{"type": "object", "description": "Free-form bag for additional secrets, keyed by original variable names"},
				},
			},
			"metadata": map[string]any{"type": "object", "description": "Non-secret metadata (scopes, expires_at, etc.)"},
		},
		"required": []string{"provider", "name"},
	}},
	{Name: "connection_update", Description: "Update a connection by ID. Secret fields with empty/missing values PRESERVE the existing stored secret — so callers can rename or relabel without re-supplying tokens. Pass non-empty secret fields to overwrite them.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            map[string]any{"type": "string", "description": "Connection ID"},
			"provider":      map[string]any{"type": "string"},
			"name":          map[string]any{"type": "string"},
			"account_label": map[string]any{"type": "string"},
			"description":   map[string]any{"type": "string"},
			"credentials":   map[string]any{"type": "object", "description": "Same shape as connection_create.credentials. Empty values preserve existing."},
			"metadata":      map[string]any{"type": "object"},
		},
		"required": []string{"id"},
	}},
	{Name: "connection_delete", Description: "Delete a connection by ID. By default fails (returns an error) if any agent references it; pass `force: true` to delete and atomically strip references from affected agents.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "string", "description": "Connection ID"},
			"force": map[string]any{"type": "boolean", "description": "If true, strip references from agents and delete. Default: false."},
		},
		"required": []string{"id"},
	}},
	{Name: "connection_import_from_variables", Description: "Scan the variables table for known OAuth provider key triples (e.g. `youtube_client_id` + `youtube_client_secret` + `youtube_refresh_token`) and create a Connection named 'Imported' for each complete set. The original variables are left in place. Useful for migrating from variables-based credentials to the connections model.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},

	// ─── Node Config Tools (Phase 2) ───
	// Node configs hold reusable per-node-type configuration (e.g. SMTP
	// settings for the email workflow node). The `data` field is a JSON
	// blob whose schema depends on `type`. Sensitive fields inside `data`
	// (currently: email→password) are redacted in list responses; Get
	// returns the full data for editing.
	{Name: "node_config_list", Description: "List node configs. Optional `type` filter (e.g. 'email'). Sensitive fields inside the `data` JSON blob (e.g. email passwords) are redacted to '***' in this response.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type": map[string]any{"type": "string", "description": "Filter by config type (e.g. 'email')"},
		},
	}},
	{Name: "node_config_get", Description: "Get a node config by ID, including its full `data` blob with sensitive fields unredacted. Used for editing.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "node_config_create", Description: "Create a new node config. The `data` field is a JSON-encoded string (NOT an object) whose internal schema depends on `type`. For email: `{\"host\":\"smtp.example.com\",\"port\":587,\"username\":\"...\",\"password\":\"...\",\"from\":\"...\"}`.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Node config name"},
			"type": map[string]any{"type": "string", "description": "Config type (e.g. 'email'). Selects which schema applies to `data`."},
			"data": map[string]any{"type": "string", "description": "JSON-encoded config data (a string containing JSON, not an object)"},
		},
		"required": []string{"name", "type"},
	}},
	{Name: "node_config_update", Description: "Update a node config by ID. Pass the full intended state (name + type required).", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "string"},
			"name": map[string]any{"type": "string"},
			"type": map[string]any{"type": "string"},
			"data": map[string]any{"type": "string", "description": "JSON-encoded config data"},
		},
		"required": []string{"id", "name", "type"},
	}},
	{Name: "node_config_delete", Description: "Delete a node config by ID. Workflow nodes referencing it (by config_id) will fail at run time.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},

	// ─── Guide Tools (Phase 2) ───
	// User-authored markdown documentation surfaced inside the AT UI. No
	// sensitive fields, no redaction.
	{Name: "guide_list", Description: "List user-authored markdown guides shown in the AT UI's docs section.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "guide_get", Description: "Get a guide by ID, including its full markdown content.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "guide_create", Description: "Create a new user-authored guide. `content` is raw markdown (rendered client-side). `icon` is a lucide-svelte icon name (e.g. 'BookOpen', 'Hammer', 'Sparkles').", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"icon":        map[string]any{"type": "string", "description": "lucide-svelte icon name"},
			"content":     map[string]any{"type": "string", "description": "Raw markdown body"},
		},
		"required": []string{"title"},
	}},
	{Name: "guide_update", Description: "Update a guide by ID. Pass the full intended state.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string"},
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"icon":        map[string]any{"type": "string"},
			"content":     map[string]any{"type": "string"},
		},
		"required": []string{"id", "title"},
	}},
	{Name: "guide_delete", Description: "Delete a guide by ID.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},

	// ─── Agent / Org / Task Destructive + Lifecycle Tools (Phase 2) ───
	{Name: "agent_delete", Description: "Delete an agent by ID. WARNING: no cascade — referencing org memberships, connections, and chat sessions are NOT cleaned up by this tool. Use org_remove_agent first if the agent is in any organization.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},

	{Name: "org_update", Description: "Update an organization. Partial update: omitted fields are preserved; explicit empty `head_agent_id` clears the head agent. Setting a non-empty `head_agent_id` is validated: the agent must already be an active member of the org.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                                    map[string]any{"type": "string", "description": "Organization ID"},
			"name":                                  map[string]any{"type": "string"},
			"description":                           map[string]any{"type": "string"},
			"issue_prefix":                          map[string]any{"type": "string"},
			"head_agent_id":                         map[string]any{"type": "string", "description": "Must be an active member of the org"},
			"budget_monthly_cents":                  map[string]any{"type": "number"},
			"max_delegation_depth":                  map[string]any{"type": "integer"},
			"require_board_approval_for_new_agents": map[string]any{"type": "boolean"},
			"container_config": map[string]any{
				"type":        "object",
				"description": "Optional Docker container config for isolated agent execution",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
					"image":   map[string]any{"type": "string"},
					"cpu":     map[string]any{"type": "string"},
					"memory":  map[string]any{"type": "string"},
					"network": map[string]any{"type": "boolean"},
				},
			},
		},
		"required": []string{"id"},
	}},
	{Name: "org_delete", Description: "Delete an organization by ID. WARNING: no cascade by default — child tasks, agent memberships, and goals/projects are NOT removed by this call.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "org_list_agents", Description: "List the agent memberships of an organization (the join-table records, including role, title, parent_agent_id, status, etc.).", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"organization_id": map[string]any{"type": "string"}}, "required": []string{"organization_id"}}},
	{Name: "org_update_agent", Description: "Update an agent's membership in an organization (role, title, parent_agent_id, status, heartbeat_schedule). If `parent_agent_id` changes, the new hierarchy is cycle-checked; setting it to a non-member or creating a cycle returns an error. Empty `status` preserves existing.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"organization_id":    map[string]any{"type": "string"},
			"agent_id":           map[string]any{"type": "string"},
			"role":               map[string]any{"type": "string"},
			"title":              map[string]any{"type": "string"},
			"parent_agent_id":    map[string]any{"type": "string", "description": "Empty string = root node. Non-empty must be an existing member of the org and must not create a cycle."},
			"status":             map[string]any{"type": "string", "description": "e.g. 'active'. Empty preserves existing."},
			"heartbeat_schedule": map[string]any{"type": "string"},
		},
		"required": []string{"organization_id", "agent_id"},
	}},
	{Name: "org_remove_agent", Description: "Remove an agent from an organization (delete the org-agent membership record). The agent itself is not deleted; if it is the current head agent, the organization head_agent_id is cleared.", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"organization_id": map[string]any{"type": "string"},
			"agent_id":        map[string]any{"type": "string"},
		},
		"required": []string{"organization_id", "agent_id"},
	}},

	{Name: "task_delete", Description: "Delete a task by ID. WARNING: no cascade — comments, labels, child tasks, and cost events tied to this task are NOT removed by this call.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}},
	{Name: "task_cancel", Description: "Cancel a running task delegation. Sends a context-cancellation signal to the in-flight delegation goroutine for this task. Returns an error if no active delegation is running for the task.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Task ID"}}, "required": []string{"id"}}},
	{Name: "active_delegation_list", Description: "List all currently-running task delegations across the platform. Each entry has task_id, agent_id, org_id, started_at, and human-readable duration. Used for monitoring and debugging.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
}

// ─── Core Tool Executors ───

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
// If the context has a container scope and the org/bot has containers enabled,
// the command is executed inside the corresponding Docker container.
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

	// Check if we should route to a container.
	if scope, ok := workflow.ContainerScopeFromContext(ctx); ok && s.containerManager != nil {
		cfg := s.resolveContainerConfig(ctx, scope)
		if cfg != nil && cfg.Enabled {
			// Build env vars for the container
			env := make(map[string]string)
			env["PIP_BREAK_SYSTEM_PACKAGES"] = "1"
			env["UV_SYSTEM_PYTHON"] = "1"
			if workDir := workflow.WorkDirFromContext(ctx); workDir != "" {
				env["AT_WORK_DIR"] = "/workspace"
			}
			// Add variable store vars
			if s.variableStore != nil {
				vars, err := s.variableStore.ListVariables(ctx, nil)
				if err == nil {
					for _, v := range vars.Data {
						env["VAR_"+strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(v.Key))] = v.Value
					}
				}
			}

			execCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			containerCfg := container.Config{
				Enabled: true,
				Image:   cfg.Image,
				CPU:     cfg.CPU,
				Memory:  cfg.Memory,
				Network: cfg.Network,
			}

			// Determine container ID based on scope
			containerID := scope.OrgID
			if scope.UserID != "" {
				containerID = "user-" + scope.UserID
			}

			stdout, stderr, exitCode, err := s.containerManager.Exec(execCtx, containerID, containerCfg, command, env)
			if err != nil {
				return "", fmt.Errorf("container exec: %w", err)
			}

			if exitCode != 0 {
				if stderr != "" {
					return "", fmt.Errorf("bash handler failed: exit status %d: %s", exitCode, stderr)
				}
				return "", fmt.Errorf("bash handler failed: exit status %d", exitCode)
			}

			result := strings.TrimSpace(stdout)
			if stderr != "" {
				slog.Debug("container bash: stderr", "stderr", stderr[:min(500, len(stderr))])
			}
			return result, nil
		}
	}

	// No container — execute on host (default behavior).
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

// resolveContainerConfig looks up the container config for the given scope.
func (s *Server) resolveContainerConfig(ctx context.Context, scope workflow.ContainerScope) *service.ContainerConfig {
	// Check org container config
	if scope.OrgID != "" && s.organizationStore != nil {
		org, err := s.organizationStore.GetOrganization(ctx, scope.OrgID)
		if err == nil && org != nil && org.ContainerConfig != nil && org.ContainerConfig.Enabled {
			return org.ContainerConfig
		}
	}

	// Check bot/user container config (for per-user isolation)
	if scope.UserID != "" && s.botConfigStore != nil {
		// For now, return a default container config for user-scoped execution
		// Bot-level config is checked by the caller
		return nil
	}

	return nil
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
