package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListCommentsByTaskAPI handles GET /api/v1/tasks/{id}/comments.
func (s *Server) ListCommentsByTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.issueCommentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	records, err := s.issueCommentStore.ListCommentsByTask(r.Context(), taskID)
	if err != nil {
		slog.Error("list comments by task failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list comments: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.IssueComment{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetCommentAPI handles GET /api/v1/comments/{id}.
func (s *Server) GetCommentAPI(w http.ResponseWriter, r *http.Request) {
	if s.issueCommentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "comment id is required", http.StatusBadRequest)
		return
	}

	record, err := s.issueCommentStore.GetComment(r.Context(), id)
	if err != nil {
		slog.Error("get comment failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get comment: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("comment %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateCommentAPI handles POST /api/v1/tasks/{id}/comments.
func (s *Server) CreateCommentAPI(w http.ResponseWriter, r *http.Request) {
	if s.issueCommentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	var req service.IssueComment
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Body == "" {
		httpResponse(w, "body is required", http.StatusBadRequest)
		return
	}

	req.TaskID = taskID
	req.AuthorType = "user"
	req.AuthorID = s.getUserEmail(r)

	record, err := s.issueCommentStore.CreateComment(r.Context(), req)
	if err != nil {
		slog.Error("create comment failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create comment: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateCommentAPI handles PUT /api/v1/comments/{id}.
func (s *Server) UpdateCommentAPI(w http.ResponseWriter, r *http.Request) {
	if s.issueCommentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "comment id is required", http.StatusBadRequest)
		return
	}

	var req service.IssueComment
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Body == "" {
		httpResponse(w, "body is required", http.StatusBadRequest)
		return
	}

	record, err := s.issueCommentStore.UpdateComment(r.Context(), id, req)
	if err != nil {
		slog.Error("update comment failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update comment: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("comment %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteCommentAPI handles DELETE /api/v1/comments/{id}.
func (s *Server) DeleteCommentAPI(w http.ResponseWriter, r *http.Request) {
	if s.issueCommentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "comment id is required", http.StatusBadRequest)
		return
	}

	if err := s.issueCommentStore.DeleteComment(r.Context(), id); err != nil {
		slog.Error("delete comment failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete comment: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
