package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListTasksAPI handles GET /api/v1/tasks.
func (s *Server) ListTasksAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.taskStore.ListTasks(r.Context(), q)
	if err != nil {
		slog.Error("list tasks failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tasks: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Task]{Data: []service.Task{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// TaskWithSubtasks wraps a Task with its child sub-tasks for tree retrieval.
type TaskWithSubtasks struct {
	service.Task
	SubTasks []TaskWithSubtasks `json:"sub_tasks,omitempty"`
}

// buildTaskTree recursively builds a task tree from a root task ID.
// Uses maxDepth to prevent runaway recursion on malformed data.
func (s *Server) buildTaskTree(ctx context.Context, taskID string, maxDepth int) (*TaskWithSubtasks, error) {
	if maxDepth <= 0 {
		return nil, nil
	}

	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return nil, err
	}

	result := &TaskWithSubtasks{Task: *task}

	children, err := s.taskStore.ListChildTasks(ctx, taskID)
	if err != nil {
		return result, nil // return task without children on error
	}

	for _, child := range children {
		childTree, err := s.buildTaskTree(ctx, child.ID, maxDepth-1)
		if err != nil {
			continue
		}
		if childTree != nil {
			result.SubTasks = append(result.SubTasks, *childTree)
		}
	}

	return result, nil
}

// GetTaskAPI handles GET /api/v1/tasks/{id}.
func (s *Server) GetTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	include := r.URL.Query().Get("include")
	if include == "subtasks" {
		tree, err := s.buildTaskTree(r.Context(), id, 20)
		if err != nil {
			slog.Error("get task tree failed", "id", id, "error", err)
			httpResponse(w, fmt.Sprintf("failed to get task tree: %v", err), http.StatusInternalServerError)
			return
		}
		if tree == nil {
			httpResponse(w, fmt.Sprintf("task %q not found", id), http.StatusNotFound)
			return
		}
		httpResponseJSON(w, tree, http.StatusOK)
		return
	}

	record, err := s.taskStore.GetTask(r.Context(), id)
	if err != nil {
		slog.Error("get task failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get task: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("task %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateTaskAPI handles POST /api/v1/tasks.
func (s *Server) CreateTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Task
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		httpResponse(w, "title is required", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "open"
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.taskStore.CreateTask(r.Context(), req)
	if err != nil {
		slog.Error("create task failed", "title", req.Title, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create task: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateTaskAPI handles PUT /api/v1/tasks/{id}.
// It performs a true partial update: only fields present in the JSON body are
// changed; omitted fields keep their existing values.
func (s *Server) UpdateTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	// Fetch existing task so omitted fields are preserved.
	existing, err := s.taskStore.GetTask(r.Context(), id)
	if err != nil {
		slog.Error("get task for update failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get task: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, fmt.Sprintf("task %q not found", id), http.StatusNotFound)
		return
	}

	// Decode into a map to know exactly which fields the caller sent.
	var fields map[string]any
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Merge: start from existing, overlay only the fields present in the request.
	merged := *existing
	applyTaskFields(&merged, fields)
	merged.UpdatedBy = s.getUserEmail(r)

	record, err := s.taskStore.UpdateTask(r.Context(), id, merged)
	if err != nil {
		slog.Error("update task failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update task: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("task %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// applyTaskFields overlays only the JSON keys present in fields onto the task.
func applyTaskFields(t *service.Task, fields map[string]any) {
	if v, ok := fields["organization_id"]; ok {
		t.OrganizationID, _ = v.(string)
	}
	if v, ok := fields["project_id"]; ok {
		t.ProjectID, _ = v.(string)
	}
	if v, ok := fields["goal_id"]; ok {
		t.GoalID, _ = v.(string)
	}
	if v, ok := fields["parent_id"]; ok {
		t.ParentID, _ = v.(string)
	}
	if v, ok := fields["assigned_agent_id"]; ok {
		t.AssignedAgentID, _ = v.(string)
	}
	if v, ok := fields["identifier"]; ok {
		t.Identifier, _ = v.(string)
	}
	if v, ok := fields["title"]; ok {
		t.Title, _ = v.(string)
	}
	if v, ok := fields["description"]; ok {
		t.Description, _ = v.(string)
	}
	if v, ok := fields["status"]; ok {
		t.Status, _ = v.(string)
	}
	if v, ok := fields["priority_level"]; ok {
		t.PriorityLevel, _ = v.(string)
	}
	if v, ok := fields["priority"]; ok {
		switch n := v.(type) {
		case float64:
			t.Priority = int(n)
		case int:
			t.Priority = n
		}
	}
	if v, ok := fields["result"]; ok {
		t.Result, _ = v.(string)
	}
	if v, ok := fields["billing_code"]; ok {
		t.BillingCode, _ = v.(string)
	}
	if v, ok := fields["request_depth"]; ok {
		switch n := v.(type) {
		case float64:
			t.RequestDepth = int(n)
		case int:
			t.RequestDepth = n
		}
	}
	if v, ok := fields["max_iterations"]; ok {
		switch n := v.(type) {
		case float64:
			t.MaxIterations = int(n)
		case int:
			t.MaxIterations = n
		}
	}
	if v, ok := fields["checked_out_by"]; ok {
		t.CheckedOutBy, _ = v.(string)
	}
	if v, ok := fields["checked_out_at"]; ok {
		t.CheckedOutAt, _ = v.(string)
	}
	if v, ok := fields["started_at"]; ok {
		t.StartedAt, _ = v.(string)
	}
	if v, ok := fields["completed_at"]; ok {
		t.CompletedAt, _ = v.(string)
	}
	if v, ok := fields["cancelled_at"]; ok {
		t.CancelledAt, _ = v.(string)
	}
	if v, ok := fields["hidden_at"]; ok {
		t.HiddenAt, _ = v.(string)
	}
}

// DeleteTaskAPI handles DELETE /api/v1/tasks/{id}.
func (s *Server) DeleteTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	if err := s.taskStore.DeleteTask(r.Context(), id); err != nil {
		slog.Error("delete task failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete task: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ProcessTaskAPI handles POST /api/v1/tasks/{id}/process.
// Triggers org delegation on an existing task that has an organization_id.
// The task must belong to an organization with a head agent configured.
// Accepts an optional JSON body with a "message" field to add a comment before processing.
// Returns 202 Accepted and runs delegation in a background goroutine.
func (s *Server) ProcessTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil || s.organizationStore == nil || s.orgAgentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Parse optional request body for a message to add as a comment.
	var reqBody struct {
		Message string `json:"message"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
	}

	// Fetch the task.
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		slog.Error("process task: get task failed", "id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get task: %v", err), http.StatusInternalServerError)
		return
	}
	if task == nil {
		httpResponse(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}

	// If a message was provided, add it as a user comment on the task.
	if reqBody.Message != "" && s.issueCommentStore != nil {
		_, commentErr := s.issueCommentStore.CreateComment(ctx, service.IssueComment{
			ID:         ulid.Make().String(),
			TaskID:     task.ID,
			AuthorType: "user",
			AuthorID:   "process_task_api",
			Body:       reqBody.Message,
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		if commentErr != nil {
			slog.Warn("process task: failed to add comment", "task_id", task.ID, "error", commentErr)
		}
	}

	// Task must belong to an organization.
	if task.OrganizationID == "" {
		httpResponse(w, "task has no organization_id", http.StatusUnprocessableEntity)
		return
	}

	// Fetch the organization.
	org, err := s.organizationStore.GetOrganization(ctx, task.OrganizationID)
	if err != nil {
		slog.Error("process task: get organization failed", "org_id", task.OrganizationID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if org == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", task.OrganizationID), http.StatusNotFound)
		return
	}

	// Organization must have a head agent.
	if org.HeadAgentID == "" {
		httpResponse(w, "organization has no head agent", http.StatusUnprocessableEntity)
		return
	}

	// Validate head agent is an active member.
	member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, org.HeadAgentID)
	if err != nil {
		slog.Error("process task: get head agent membership failed", "org_id", org.ID, "agent_id", org.HeadAgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to validate head agent: %v", err), http.StatusInternalServerError)
		return
	}
	if member == nil {
		httpResponse(w, "head agent is not a member of this organization", http.StatusUnprocessableEntity)
		return
	}
	if member.Status != "active" {
		httpResponse(w, "head agent is not active", http.StatusUnprocessableEntity)
		return
	}

	// Assign the task to the head agent if not already assigned.
	if task.AssignedAgentID != org.HeadAgentID {
		task.AssignedAgentID = org.HeadAgentID
		task.Status = service.TaskStatusOpen
		task, err = s.taskStore.UpdateTask(ctx, taskID, *task)
		if err != nil {
			slog.Error("process task: update task failed", "id", taskID, "error", err)
			httpResponse(w, fmt.Sprintf("failed to update task: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Fire async delegation in a tracked, cancellable background goroutine.
	go func() {
		delegCtx, cleanup := s.registerDelegation(context.Background(), task.ID, org.HeadAgentID, org.ID)
		defer cleanup()

		// Audit: task processing triggered.
		if recordAudit := s.recordAuditFunc(); recordAudit != nil {
			_ = recordAudit(delegCtx, service.AuditEntry{
				ActorType:      "system",
				ActorID:        "process_task_api",
				Action:         "task_process_triggered",
				ResourceType:   "task",
				ResourceID:     task.ID,
				OrganizationID: org.ID,
				Details: map[string]any{
					"task_title":    task.Title,
					"head_agent_id": org.HeadAgentID,
					"org_name":      org.Name,
				},
			})
		}

		if err := s.runOrgDelegation(delegCtx, org, task, org.HeadAgentID, 0); err != nil {
			slog.Error("process task: org-delegation failed",
				"org_id", org.ID,
				"task_id", task.ID,
				"error", err,
			)
			// Update task status to reflect failure.
			if s.taskStore != nil {
				_ = s.taskStore.UpdateTaskStatus(delegCtx, task.ID, service.TaskStatusCancelled, fmt.Sprintf("delegation failed: %v", err))
			}
		}
	}()

	httpResponseJSON(w, map[string]string{
		"id":     task.ID,
		"status": "processing",
	}, http.StatusAccepted)
}

// ListTasksByAgentAPI handles GET /api/v1/agents/{id}/tasks.
func (s *Server) ListTasksByAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	records, err := s.taskStore.ListTasksByAgent(r.Context(), agentID)
	if err != nil {
		slog.Error("list tasks by agent failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tasks by agent: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Task{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// checkoutTaskRequest represents the JSON body for task checkout.
type checkoutTaskRequest struct {
	AgentID string `json:"agent_id"`
}

// CheckoutTaskAPI handles POST /api/v1/tasks/{id}/checkout.
func (s *Server) CheckoutTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	var req checkoutTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.AgentID == "" {
		httpResponse(w, "agent_id is required", http.StatusBadRequest)
		return
	}

	if err := s.taskStore.CheckoutTask(r.Context(), taskID, req.AgentID); err != nil {
		slog.Error("checkout task failed", "task_id", taskID, "agent_id", req.AgentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to checkout task: %v", err), http.StatusConflict)
		return
	}

	httpResponse(w, "task checked out", http.StatusOK)
}

// ReleaseTaskAPI handles POST /api/v1/tasks/{id}/release.
func (s *Server) ReleaseTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	if err := s.taskStore.ReleaseTask(r.Context(), taskID); err != nil {
		slog.Error("release task failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to release task: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "task released", http.StatusOK)
}

// CreateTaskChatAPI handles POST /api/v1/tasks/{id}/chat.
// Creates (or returns existing) a chat session linked to a task, enabling
// interactive communication with the agent assigned to the task.
// If the task has saved conversation state from a previous delegation run,
// the messages are imported into the chat session.
func (s *Server) CreateTaskChatAPI(w http.ResponseWriter, r *http.Request) {
	if s.taskStore == nil || s.chatSessionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Fetch the task.
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		slog.Error("task chat: get task failed", "id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get task: %v", err), http.StatusInternalServerError)
		return
	}
	if task == nil {
		httpResponse(w, fmt.Sprintf("task %q not found", taskID), http.StatusNotFound)
		return
	}

	// Check if a chat session already exists for this task.
	existing, err := s.chatSessionStore.GetChatSessionByTaskID(ctx, taskID)
	if err != nil {
		slog.Error("task chat: lookup existing session failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to lookup session: %v", err), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		httpResponseJSON(w, existing, http.StatusOK)
		return
	}

	// Determine the agent to use: task's assigned agent, or org's head agent.
	agentID := task.AssignedAgentID
	if agentID == "" && task.OrganizationID != "" && s.organizationStore != nil {
		org, orgErr := s.organizationStore.GetOrganization(ctx, task.OrganizationID)
		if orgErr == nil && org != nil {
			agentID = org.HeadAgentID
		}
	}
	if agentID == "" {
		httpResponse(w, "task has no assigned agent and no organization head agent", http.StatusUnprocessableEntity)
		return
	}

	// Verify agent exists.
	if s.agentStore != nil {
		agent, agentErr := s.agentStore.GetAgent(ctx, agentID)
		if agentErr != nil || agent == nil {
			httpResponse(w, fmt.Sprintf("agent %q not found", agentID), http.StatusUnprocessableEntity)
			return
		}
	}

	// Build session name from task.
	sessionName := task.Title
	if task.Identifier != "" {
		sessionName = task.Identifier + ": " + task.Title
	}

	// Create the chat session.
	session, err := s.chatSessionStore.CreateChatSession(ctx, service.ChatSession{
		AgentID:        agentID,
		TaskID:         taskID,
		OrganizationID: task.OrganizationID,
		Name:           sessionName,
		CreatedBy:      s.getUserEmail(r),
		UpdatedBy:      s.getUserEmail(r),
	})
	if err != nil {
		slog.Error("task chat: create session failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create session: %v", err), http.StatusInternalServerError)
		return
	}

	// Import conversation state from task if available.
	var importedCount int
	if s.issueCommentStore != nil {
		comments, commentErr := s.issueCommentStore.ListCommentsByTask(ctx, taskID)
		if commentErr == nil {
			for _, c := range comments {
				if !strings.HasPrefix(c.Body, conversationStatePrefix) {
					continue
				}
				stateJSON := strings.TrimPrefix(c.Body, conversationStatePrefix)
				var restored []service.Message
				if err := json.Unmarshal([]byte(stateJSON), &restored); err != nil {
					slog.Warn("task chat: failed to parse conversation state", "task_id", taskID, "error", err)
					continue
				}

				// Convert service.Message to ChatMessage and persist.
				var chatMsgs []service.ChatMessage
				for _, msg := range restored {
					var data service.ChatMessageData
					switch v := msg.Content.(type) {
					case string:
						data.Content = v
					default:
						data.Content = v
					}
					chatMsgs = append(chatMsgs, service.ChatMessage{
						SessionID: session.ID,
						Role:      msg.Role,
						Data:      data,
					})
				}

				if len(chatMsgs) > 0 {
					if err := s.chatSessionStore.CreateChatMessages(ctx, chatMsgs); err != nil {
						slog.Warn("task chat: failed to import conversation messages", "task_id", taskID, "error", err)
					} else {
						importedCount = len(chatMsgs)
						slog.Info("task chat: imported conversation state",
							"task_id", taskID, "session_id", session.ID, "messages", importedCount)
					}
				}

				// Delete the state comment after importing.
				_ = s.issueCommentStore.DeleteComment(ctx, c.ID)
				break
			}
		}
	}

	// If no conversation state but task has a result, add it as context.
	if importedCount == 0 && task.Result != "" {
		contextMsg := service.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Data: service.ChatMessageData{
				Content: fmt.Sprintf("## Task Context\n\n**Task**: %s\n**Status**: %s\n\n**Previous Result**:\n%s\n\nPlease continue working on this task.", task.Title, task.Status, task.Result),
			},
		}
		if _, err := s.chatSessionStore.CreateChatMessage(ctx, contextMsg); err != nil {
			slog.Warn("task chat: failed to add context message", "task_id", taskID, "error", err)
		}
	}

	slog.Info("task chat: session created",
		"task_id", taskID, "session_id", session.ID, "agent_id", agentID, "imported_messages", importedCount)

	httpResponseJSON(w, session, http.StatusCreated)
}
