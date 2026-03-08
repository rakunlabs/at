package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

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

	// Fetch existing task so partial updates preserve existing fields.
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

	var req service.Task
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Allow partial updates: fall back to existing title when not provided.
	if req.Title == "" {
		req.Title = existing.Title
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.taskStore.UpdateTask(r.Context(), id, req)
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
