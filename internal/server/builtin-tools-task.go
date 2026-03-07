package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
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
