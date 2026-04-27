package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

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

// execTaskCreate creates a persistent task in the database.
//
// When called from inside a delegation loop (i.e. the executing agent is
// already working on a task), missing parent_id and organization_id fields
// are auto-inherited from the current task. This prevents agents from
// accidentally creating orphaned, unscoped tasks when they forget to pass
// these fields explicitly.
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

	// Auto-inherit parent_id and organization_id from the current task in
	// context when the caller (agent) didn't supply them. This keeps
	// agent-created subtasks correctly linked instead of orphaned.
	if currentTaskID := taskIDFromContext(ctx); currentTaskID != "" {
		needParent := task.ParentID == ""
		needOrg := task.OrganizationID == ""
		if needParent || needOrg {
			if currentTask, err := s.taskStore.GetTask(ctx, currentTaskID); err == nil && currentTask != nil {
				if needParent {
					task.ParentID = currentTask.ID
					slog.Debug("task_create: inherited parent_id from current task",
						"parent_id", currentTask.ID, "title", title)
				}
				if needOrg {
					task.OrganizationID = currentTask.OrganizationID
					slog.Debug("task_create: inherited organization_id from current task",
						"organization_id", currentTask.OrganizationID, "title", title)
				}
			} else if err != nil {
				slog.Warn("task_create: failed to look up current task for inheritance",
					"current_task_id", currentTaskID, "error", err)
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

	record, err := s.taskStore.CreateTask(ctx, task)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
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
				_, _ = s.taskStore.UpdateTask(delegCtx, task.ID, service.Task{
					Status: service.TaskStatusCancelled,
					Result: fmt.Sprintf("delegation failed: %v", err),
				})
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
