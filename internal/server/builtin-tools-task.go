package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Task Management Tool Executors ───
//
// These executors implement OpenCode-style task management tools.
// Todo state is stored per-session in memory.

// todoItem represents a single todo item.
type todoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`   // pending, in_progress, completed, cancelled
	Priority string `json:"priority"` // high, medium, low
}

// todoStore holds per-session todo lists.
// Key: session identifier (from request header or a default).
type todoStore struct {
	mu    sync.RWMutex
	lists map[string][]todoItem
}

// newTodoStore creates a new todo store.
func newTodoStore() *todoStore {
	return &todoStore{
		lists: make(map[string][]todoItem),
	}
}

// set replaces the entire todo list for a session.
func (ts *todoStore) set(sessionID string, items []todoItem) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lists[sessionID] = items
}

// get returns the todo list for a session.
func (ts *todoStore) get(sessionID string) []todoItem {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	items, ok := ts.lists[sessionID]
	if !ok {
		return nil
	}

	// Return a copy.
	result := make([]todoItem, len(items))
	copy(result, items)
	return result
}

// getSessionID extracts a session identifier from the request.
func getSessionID(r *http.Request) string {
	// Try common session headers.
	if sid := r.Header.Get("X-Session-ID"); sid != "" {
		return sid
	}
	if sid := r.Header.Get("X-Request-ID"); sid != "" {
		return sid
	}
	// Fallback to a default session.
	return "default"
}

// execTodoWrite creates or updates a todo list.
// Parameters: todos (array, required) — each item has content, status, priority.
func (s *Server) execTodoWrite(ctx context.Context, args map[string]any) (string, error) {
	todosRaw, ok := args["todos"]
	if !ok {
		return "", fmt.Errorf("todos is required")
	}

	todosJSON, err := json.Marshal(todosRaw)
	if err != nil {
		return "", fmt.Errorf("invalid todos format: %w", err)
	}

	var items []todoItem
	if err := json.Unmarshal(todosJSON, &items); err != nil {
		return "", fmt.Errorf("invalid todos format: %w", err)
	}

	if len(items) == 0 {
		return "", fmt.Errorf("at least one todo item is required")
	}

	// Validate statuses and priorities.
	validStatuses := map[string]bool{
		"pending": true, "in_progress": true, "completed": true, "cancelled": true,
	}
	validPriorities := map[string]bool{
		"high": true, "medium": true, "low": true,
	}

	for i, item := range items {
		if item.Content == "" {
			return "", fmt.Errorf("todo #%d: content is required", i+1)
		}
		if !validStatuses[item.Status] {
			return "", fmt.Errorf("todo #%d: invalid status %q (must be pending, in_progress, completed, or cancelled)", i+1, item.Status)
		}
		if !validPriorities[item.Priority] {
			return "", fmt.Errorf("todo #%d: invalid priority %q (must be high, medium, or low)", i+1, item.Priority)
		}
	}

	sessionID := sessionIDFromContext(ctx)
	s.todos.set(sessionID, items)

	// Build summary.
	pending := 0
	inProgress := 0
	completed := 0
	cancelled := 0
	for _, item := range items {
		switch item.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		case "cancelled":
			cancelled++
		}
	}

	return fmt.Sprintf("Todo list updated (%d items): %d pending, %d in progress, %d completed, %d cancelled",
		len(items), pending, inProgress, completed, cancelled), nil
}

// execTodoRead reads the current todo list.
// Parameters: none
func (s *Server) execTodoRead(ctx context.Context, _ map[string]any) (string, error) {
	sessionID := sessionIDFromContext(ctx)
	items := s.todos.get(sessionID)

	if len(items) == 0 {
		return "No todos found. Use todo_write to create a todo list.", nil
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize todos: %w", err)
	}

	return string(data), nil
}

// execBatchExecute executes multiple builtin tools in parallel.
// Parameters: tool_calls (array, required) — each item has name and arguments.
func (s *Server) execBatchExecute(ctx context.Context, args map[string]any) (string, error) {
	callsRaw, ok := args["tool_calls"]
	if !ok {
		return "", fmt.Errorf("tool_calls is required")
	}

	callsJSON, err := json.Marshal(callsRaw)
	if err != nil {
		return "", fmt.Errorf("invalid tool_calls format: %w", err)
	}

	var calls []struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(callsJSON, &calls); err != nil {
		return "", fmt.Errorf("invalid tool_calls format: %w", err)
	}

	if len(calls) == 0 {
		return "", fmt.Errorf("at least one tool call is required")
	}

	// Cap at 25 concurrent calls.
	const maxCalls = 25
	if len(calls) > maxCalls {
		calls = calls[:maxCalls]
	}

	// Disallowed tools (prevent recursion).
	disallowed := map[string]bool{
		"batch_execute": true,
	}

	type batchResult struct {
		Index   int    `json:"index"`
		Tool    string `json:"tool"`
		Success bool   `json:"success"`
		Result  string `json:"result,omitempty"`
		Error   string `json:"error,omitempty"`
	}

	results := make([]batchResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}) {
			defer wg.Done()

			if disallowed[c.Name] {
				results[idx] = batchResult{
					Index:   idx,
					Tool:    c.Name,
					Success: false,
					Error:   fmt.Sprintf("tool %q is not allowed in batch (prevents recursion)", c.Name),
				}
				return
			}

			if c.Arguments == nil {
				c.Arguments = make(map[string]any)
			}

			// Dispatch to the tool executor.
			result, execErr := s.dispatchBuiltinTool(ctx, c.Name, c.Arguments)
			if execErr != nil {
				results[idx] = batchResult{
					Index:   idx,
					Tool:    c.Name,
					Success: false,
					Error:   execErr.Error(),
				}
			} else {
				results[idx] = batchResult{
					Index:   idx,
					Tool:    c.Name,
					Success: true,
					Result:  result,
				}
			}
		}(i, call)
	}

	wg.Wait()

	// Count successes/failures.
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}
	failedCount := len(results) - successCount

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize results: %w", err)
	}

	summary := fmt.Sprintf("Batch execution: %d/%d succeeded, %d failed\n\n%s",
		successCount, len(results), failedCount, string(data))

	return summary, nil
}

// ─── Persistent Task (Issue Tracker) Tool Executors ───

var taskContextToolNames = []string{
	"task_current",
	"task_children",
	"task_wait",
	"task_create_child",
	"task_update_current",
	"task_comment_current",
	"task_complete",
	"task_block",
}

func builtinToolByName(name string) (builtinToolDef, bool) {
	for _, bt := range builtinTools {
		if bt.Name == name {
			return bt, true
		}
	}
	return builtinToolDef{}, false
}

func taskContextToolDefs() []service.Tool {
	tools := make([]service.Tool, 0, len(taskContextToolNames))
	for _, name := range taskContextToolNames {
		bt, ok := builtinToolByName(name)
		if !ok {
			continue
		}
		tools = append(tools, service.Tool{
			Name:        bt.Name,
			Description: bt.Description,
			InputSchema: bt.InputSchema,
		})
	}
	return tools
}

func taskOperatingProtocolPrompt(task *service.Task) string {
	if task == nil {
		return ""
	}
	label := task.ID
	if task.Identifier != "" {
		label = task.Identifier + " (" + task.ID + ")"
	}
	return fmt.Sprintf(`

## Task Operating Protocol
You are operating inside task %s: %s.
- Treat follow-up work derived from this task as child work. Use task_create_child, or task_create without root=true, for derived work items.
- Do not create unrelated root tasks while answering questions or continuing this task. Only use task_create with root=true when the user explicitly asks for an independent task, and include a reason.
- Before creating a child task, prefer task_current or task_children when you need to check existing subtasks and avoid duplicates.
- After task_process starts background work, call task_wait once. Never use bash sleep commands or repeatedly poll task_get/task_children while waiting.
- Keep updates scoped to the current task with task_update_current, task_comment_current, task_complete, or task_block whenever possible.
`, label, task.Title)
}

func boolValue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "1" || b == "yes"
	default:
		return false
	}
}

func (s *Server) currentTaskFromContext(ctx context.Context) (*service.Task, error) {
	if s.taskStore == nil {
		return nil, fmt.Errorf("task store not configured")
	}
	currentTaskID := taskIDFromContext(ctx)
	if currentTaskID == "" {
		return nil, fmt.Errorf("no active task in context")
	}

	task, err := s.taskStore.GetTask(ctx, currentTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get current task: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("current task %q not found", currentTaskID)
	}

	return task, nil
}

// execTaskCreate creates a persistent task in the database.
//
// When called from inside a delegation loop (i.e. the executing agent is
// already working on a task), it creates a child task by default. Creating
// an unrelated root task from task context requires root=true and reason.
func (s *Server) execTaskCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}

	title, _ := args["title"].(string)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	task := service.Task{
		Title:  title,
		Status: service.TaskStatusTodo,
	}

	if v, ok := args["description"].(string); ok {
		task.Description = v
	}
	if v, ok := args["organization_id"].(string); ok {
		task.OrganizationID = v
	}
	if v, ok := args["assigned_agent_id"].(string); ok {
		task.AssignedAgentID = v
	}
	if v, ok := args["priority_level"].(string); ok {
		task.PriorityLevel = v
	}
	if v, ok := args["parent_id"].(string); ok {
		task.ParentID = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		task.Status = v
	}
	if v, ok := args["priority"].(float64); ok {
		task.Priority = int(v)
	} else if v, ok := args["priority"].(int); ok {
		task.Priority = v
	}

	// In task context, derived work should be a child task by default. Agents
	// can still create an unrelated root task, but only via an explicit escape
	// hatch so accidental root tasks are easy to prevent and audit.
	if currentTaskID := taskIDFromContext(ctx); currentTaskID != "" {
		currentTask, err := s.taskStore.GetTask(ctx, currentTaskID)
		if err != nil {
			return "", fmt.Errorf("failed to get current task for task_create: %w", err)
		}
		if currentTask == nil {
			return "", fmt.Errorf("current task %q not found", currentTaskID)
		}

		if boolValue(args["root"]) {
			reason, _ := args["reason"].(string)
			if reason == "" {
				return "", fmt.Errorf("reason is required when root=true is used from task context")
			}
			if task.ParentID != "" {
				return "", fmt.Errorf("parent_id cannot be used with root=true")
			}
			if task.OrganizationID == "" {
				task.OrganizationID = currentTask.OrganizationID
			}
			if task.ProjectID == "" {
				task.ProjectID = currentTask.ProjectID
			}
			if task.GoalID == "" {
				task.GoalID = currentTask.GoalID
			}
			slog.Info("task_create: creating explicit root task from task context",
				"current_task_id", currentTask.ID, "title", title, "reason", reason)
		} else {
			if task.ParentID != "" && task.ParentID != currentTask.ID {
				return "", fmt.Errorf("task_create in task context creates a child of the current task; omit parent_id or use root=true with reason for an unrelated root task")
			}
			task.ParentID = currentTask.ID
			if task.OrganizationID == "" {
				task.OrganizationID = currentTask.OrganizationID
			}
			if task.ProjectID == "" {
				task.ProjectID = currentTask.ProjectID
			}
			if task.GoalID == "" {
				task.GoalID = currentTask.GoalID
			}
			if task.PriorityLevel == "" {
				task.PriorityLevel = currentTask.PriorityLevel
			}
			if task.Priority == 0 {
				task.Priority = currentTask.Priority
			}
		}
	}
	// max_iterations: per-task override of the agent's iteration budget.
	// Accept both float64 (JSON numbers) and int.
	if v, ok := args["max_iterations"].(float64); ok && v > 0 {
		task.MaxIterations = int(v)
	} else if v, ok := args["max_iterations"].(int); ok && v > 0 {
		task.MaxIterations = v
	}

	// Organization fallback: task_create is also reachable outside a task
	// context (bot/web chat sessions with no linked task, MCP clients,
	// workflow agent_call runs). In those paths there is no current task to
	// inherit organization_id from, which used to produce org-less orphan
	// tasks that ProcessTaskAPI later rejects. Fall back to the executing
	// agent's organization (membership first, then head-agent scan).
	if task.OrganizationID == "" {
		if orgID := s.resolveAgentOrgID(ctx, agentIDFromContext(ctx)); orgID != "" {
			task.OrganizationID = orgID
			slog.Info("task_create: inherited organization from executing agent",
				"agent_id", agentIDFromContext(ctx), "organization_id", orgID, "title", title)
		}
	}

	// Spill large descriptions to the shared task workspace and replace
	// the in-DB description with a short reference. This caps the bytes
	// the child agent re-reads on every iteration of its loop, which is
	// the dominant input-token cost in pipeline tasks (Director → child).
	task.Description, _ = s.maybeSpillBrief(ctx, task.Description, task.ParentID, task.Title)

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

func (s *Server) execTaskCurrent(ctx context.Context, _ map[string]any) (string, error) {
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	return s.execTaskGet(ctx, map[string]any{"id": currentTask.ID})
}

func (s *Server) execTaskChildren(ctx context.Context, _ map[string]any) (string, error) {
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	children, err := s.taskStore.ListChildTasks(ctx, currentTask.ID)
	if err != nil {
		return "", fmt.Errorf("failed to list child tasks: %w", err)
	}
	if children == nil {
		children = []service.Task{}
	}
	data, _ := json.MarshalIndent(map[string]any{"task_id": currentTask.ID, "children": children, "total": len(children)}, "", "  ")
	return string(data), nil
}

func (s *Server) execTaskCreateChild(ctx context.Context, args map[string]any) (string, error) {
	if _, err := s.currentTaskFromContext(ctx); err != nil {
		return "", err
	}
	if boolValue(args["root"]) {
		return "", fmt.Errorf("task_create_child cannot create root tasks")
	}
	return s.execTaskCreate(ctx, args)
}

func (s *Server) execTaskUpdateCurrent(ctx context.Context, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	if id, _ := args["id"].(string); id != "" && id != currentTask.ID {
		return "", fmt.Errorf("task_update_current can only update current task %q", currentTask.ID)
	}
	args["id"] = currentTask.ID
	return s.execTaskUpdate(ctx, args)
}

func (s *Server) execTaskCommentCurrent(ctx context.Context, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	if id, _ := args["task_id"].(string); id != "" && id != currentTask.ID {
		return "", fmt.Errorf("task_comment_current can only comment on current task %q", currentTask.ID)
	}
	args["task_id"] = currentTask.ID
	if _, ok := args["author_name"].(string); !ok || args["author_name"] == "" {
		if agentID := agentIDFromContext(ctx); agentID != "" {
			args["author_name"] = agentID
		}
	}
	return s.execTaskAddComment(ctx, args)
}

func (s *Server) execTaskComplete(ctx context.Context, args map[string]any) (string, error) {
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	result, _ := args["result"].(string)
	if result == "" {
		return "", fmt.Errorf("result is required")
	}
	if err := s.completeTaskWithStatus(ctx, currentTask, service.TaskStatusCompleted, result); err != nil {
		return "", fmt.Errorf("failed to complete current task: %w", err)
	}
	return fmt.Sprintf(`{"status":"completed","task_id":%q}`, currentTask.ID), nil
}

func (s *Server) execTaskBlock(ctx context.Context, args map[string]any) (string, error) {
	currentTask, err := s.currentTaskFromContext(ctx)
	if err != nil {
		return "", err
	}
	reason, _ := args["reason"].(string)
	if reason == "" {
		return "", fmt.Errorf("reason is required")
	}
	if err := s.completeTaskWithStatus(ctx, currentTask, service.TaskStatusBlocked, reason); err != nil {
		return "", fmt.Errorf("failed to block current task: %w", err)
	}
	return fmt.Sprintf(`{"status":"blocked","task_id":%q}`, currentTask.ID), nil
}

// execTaskList lists tasks with optional filtering.
func (s *Server) execTaskList(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}

	result, err := s.taskStore.ListTasks(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	// Apply client-side filters (store may not support all filters via query).
	statusFilter, _ := args["status"].(string)
	orgFilter, _ := args["organization_id"].(string)
	agentFilter, _ := args["assigned_agent_id"].(string)

	type taskSummary struct {
		ID              string `json:"id"`
		Identifier      string `json:"identifier,omitempty"`
		Title           string `json:"title"`
		Status          string `json:"status"`
		PriorityLevel   string `json:"priority_level,omitempty"`
		OrganizationID  string `json:"organization_id,omitempty"`
		AssignedAgentID string `json:"assigned_agent_id,omitempty"`
		ParentID        string `json:"parent_id,omitempty"`
		UpdatedAt       string `json:"updated_at"`
	}

	var summaries []taskSummary
	for _, t := range result.Data {
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		if orgFilter != "" && t.OrganizationID != orgFilter {
			continue
		}
		if agentFilter != "" && t.AssignedAgentID != agentFilter {
			continue
		}
		summaries = append(summaries, taskSummary{
			ID:              t.ID,
			Identifier:      t.Identifier,
			Title:           t.Title,
			Status:          t.Status,
			PriorityLevel:   t.PriorityLevel,
			OrganizationID:  t.OrganizationID,
			AssignedAgentID: t.AssignedAgentID,
			ParentID:        t.ParentID,
			UpdatedAt:       t.UpdatedAt,
		})
	}

	if summaries == nil {
		summaries = []taskSummary{}
	}

	out := map[string]any{
		"tasks": summaries,
		"total": len(summaries),
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// execTaskGet gets a single task with optional subtasks.
func (s *Server) execTaskGet(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	task, err := s.taskStore.GetTask(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return "", fmt.Errorf("task %q not found", id)
	}

	result := map[string]any{
		"task": task,
	}

	// Include subtasks.
	children, err := s.taskStore.ListChildTasks(ctx, id)
	if err != nil {
		slog.Warn("failed to list child tasks", "task_id", id, "error", err)
	} else if len(children) > 0 {
		result["subtasks"] = children
	}

	// Include comments if available.
	if s.issueCommentStore != nil {
		comments, err := s.issueCommentStore.ListCommentsByTask(ctx, id)
		if err != nil {
			slog.Warn("failed to list task comments", "task_id", id, "error", err)
		} else if len(comments) > 0 {
			result["comments"] = comments
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

// execTaskUpdate updates an existing task.
func (s *Server) execTaskUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	// Fetch existing for merge.
	existing, err := s.taskStore.GetTask(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("task %q not found", id)
	}

	// Merge provided fields.
	if v, ok := args["title"].(string); ok && v != "" {
		existing.Title = v
	}
	if v, ok := args["description"].(string); ok {
		existing.Description = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		existing.Status = v
	}
	if v, ok := args["priority_level"].(string); ok {
		existing.PriorityLevel = v
	}
	if v, ok := args["assigned_agent_id"].(string); ok {
		existing.AssignedAgentID = v
	}
	if v, ok := args["result"].(string); ok {
		existing.Result = v
	}
	// max_iterations: per-task override of the agent's iteration budget.
	// Pass 0 to clear the override and fall back to the agent's default.
	if v, ok := args["max_iterations"].(float64); ok {
		existing.MaxIterations = int(v)
	} else if v, ok := args["max_iterations"].(int); ok {
		existing.MaxIterations = v
	}

	record, err := s.taskStore.UpdateTask(ctx, id, *existing)
	if err != nil {
		return "", fmt.Errorf("failed to update task: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execTaskAddComment adds a comment to a task.
func (s *Server) execTaskAddComment(ctx context.Context, args map[string]any) (string, error) {
	if s.issueCommentStore == nil {
		return "", fmt.Errorf("comment store not configured")
	}

	taskID, _ := args["task_id"].(string)
	body, _ := args["body"].(string)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if body == "" {
		return "", fmt.Errorf("body is required")
	}

	authorName, _ := args["author_name"].(string)
	if authorName == "" {
		authorName = "mcp-user"
	}

	// Determine author type: if called from an agent context, mark as "agent".
	authorType := "user"
	if agentID := agentIDFromContext(ctx); agentID != "" {
		authorType = "agent"
		if authorName == "mcp-user" {
			authorName = agentID
		}
	}

	comment := service.IssueComment{
		TaskID:     taskID,
		Body:       body,
		AuthorType: authorType,
		AuthorID:   authorName,
	}

	record, err := s.issueCommentStore.CreateComment(ctx, comment)
	if err != nil {
		return "", fmt.Errorf("failed to create comment: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execTaskProcess triggers async org delegation on a task.
func (s *Server) execTaskProcess(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil || s.organizationStore == nil {
		return "", fmt.Errorf("store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	task, err := s.taskStore.GetTask(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return "", fmt.Errorf("task %q not found", id)
	}

	if task.OrganizationID == "" {
		return "", fmt.Errorf("task has no organization — cannot process without an org context")
	}

	org, err := s.organizationStore.GetOrganization(ctx, task.OrganizationID)
	if err != nil {
		return "", fmt.Errorf("failed to get organization: %w", err)
	}
	if org == nil {
		return "", fmt.Errorf("organization %q not found", task.OrganizationID)
	}

	agentID := task.AssignedAgentID
	if agentID == "" {
		agentID = org.HeadAgentID
	}
	if agentID == "" {
		return "", fmt.Errorf("no agent assigned and organization has no head agent")
	}

	// Fire async delegation.
	go func() {
		delegCtx := context.Background()
		if err := s.runOrgDelegation(delegCtx, org, task, agentID, task.RequestDepth); err != nil {
			slog.Error("org-delegation: failed",
				"org_id", org.ID,
				"task_id", task.ID,
				"error", err,
			)
			if s.taskStore != nil {
				_ = s.taskStore.UpdateTaskStatus(delegCtx, task.ID, service.TaskStatusCancelled, fmt.Sprintf("delegation failed: %v", err))
			}
		}
	}()

	result := map[string]any{
		"status":  "accepted",
		"task_id": task.ID,
		"message": "Org delegation started in background",
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

const (
	taskWaitDefaultTimeout = 5 * time.Minute
	taskWaitMaxTimeout     = 30 * time.Minute
	taskWaitPollInterval   = time.Second
)

func isTaskTerminal(status string) bool {
	switch status {
	case service.TaskStatusCompleted, service.TaskStatusDone, service.TaskStatusCancelled, service.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

// execTaskWait waits server-side for async task processing to finish. Keeping
// the wait inside AT avoids burning agent iterations on shell sleeps and
// repeated task_get calls.
func (s *Server) execTaskWait(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	timeout := taskWaitDefaultTimeout
	if value, ok := args["timeout_seconds"].(float64); ok {
		if value <= 0 {
			return "", fmt.Errorf("timeout_seconds must be greater than zero")
		}
		timeout = time.Duration(value * float64(time.Second))
	}
	if timeout > taskWaitMaxTimeout {
		timeout = taskWaitMaxTimeout
	}

	task, timedOut, err := s.waitForTask(ctx, id, timeout, taskWaitPollInterval)
	if err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(map[string]any{
		"task":      task,
		"terminal":  isTaskTerminal(task.Status),
		"timed_out": timedOut,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize task wait result: %w", err)
	}

	return string(data), nil
}

func (s *Server) waitForTask(ctx context.Context, id string, timeout, pollInterval time.Duration) (*service.Task, bool, error) {
	if timeout <= 0 {
		return nil, false, fmt.Errorf("timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return nil, false, fmt.Errorf("poll interval must be greater than zero")
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		task, err := s.taskStore.GetTask(ctx, id)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get task while waiting: %w", err)
		}
		if task == nil {
			return nil, false, fmt.Errorf("task %q not found", id)
		}
		if isTaskTerminal(task.Status) {
			return task, false, nil
		}

		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("task wait cancelled: %w", ctx.Err())
		case <-deadline.C:
			return task, true, nil
		case <-ticker.C:
		}
	}
}

// ─── Task Lifecycle Tool Executors (Phase 2) ───

func (s *Server) execTaskDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.taskStore == nil {
		return "", fmt.Errorf("task store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.taskStore.DeleteTask(ctx, id); err != nil {
		return "", fmt.Errorf("delete task %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

// execTaskCancel sends a context-cancellation signal to the in-flight
// delegation goroutine for the given task. Mirrors CancelTaskDelegationAPI:
// returns an error when no active delegation is running so callers
// can distinguish "cancelled" from "nothing to cancel".
func (s *Server) execTaskCancel(_ context.Context, args map[string]any) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if !s.cancelDelegation(id) {
		return "", fmt.Errorf("no active delegation found for task %q", id)
	}
	return fmt.Sprintf(`{"status":"cancel_signal_sent","task_id":%q}`, id), nil
}

// execActiveDelegationList enumerates the in-memory activeDelegations
// sync.Map. The shape matches ListActiveDelegationsAPI.
func (s *Server) execActiveDelegationList(_ context.Context, _ map[string]any) (string, error) {
	now := time.Now()
	var delegations []activeDelegationResponse
	s.activeDelegations.Range(func(_, value any) bool {
		d := value.(*activeDelegation)
		delegations = append(delegations, activeDelegationResponse{
			TaskID:    d.TaskID,
			AgentID:   d.AgentID,
			OrgID:     d.OrgID,
			StartedAt: d.StartedAt.UTC().Format(time.RFC3339),
			Duration:  now.Sub(d.StartedAt).Truncate(time.Second).String(),
		})
		return true
	})
	if delegations == nil {
		delegations = []activeDelegationResponse{}
	}
	out, err := json.MarshalIndent(map[string]any{"delegations": delegations}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal delegations: %w", err)
	}
	return string(out), nil
}
