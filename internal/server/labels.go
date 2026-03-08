package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListLabelsAPI handles GET /api/v1/labels?org_id=xxx.
func (s *Server) ListLabelsAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.URL.Query().Get("org_id")

	records, err := s.labelStore.ListLabels(r.Context(), orgID)
	if err != nil {
		slog.Error("list labels failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list labels: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Label{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetLabelAPI handles GET /api/v1/labels/{id}.
func (s *Server) GetLabelAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	record, err := s.labelStore.GetLabel(r.Context(), id)
	if err != nil {
		slog.Error("get label failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get label: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("label %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateLabelAPI handles POST /api/v1/labels.
func (s *Server) CreateLabelAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Label
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Color == "" {
		httpResponse(w, "color is required", http.StatusBadRequest)
		return
	}

	record, err := s.labelStore.CreateLabel(r.Context(), req)
	if err != nil {
		slog.Error("create label failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create label: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateLabelAPI handles PUT /api/v1/labels/{id}.
func (s *Server) UpdateLabelAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	var req service.Label
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Color == "" {
		httpResponse(w, "color is required", http.StatusBadRequest)
		return
	}

	record, err := s.labelStore.UpdateLabel(r.Context(), id, req)
	if err != nil {
		slog.Error("update label failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update label: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("label %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteLabelAPI handles DELETE /api/v1/labels/{id}.
func (s *Server) DeleteLabelAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	if err := s.labelStore.DeleteLabel(r.Context(), id); err != nil {
		slog.Error("delete label failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete label: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// AddLabelToTaskAPI handles POST /api/v1/tasks/{id}/labels/{label_id}.
func (s *Server) AddLabelToTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	labelID := r.PathValue("label_id")
	if labelID == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	if err := s.labelStore.AddLabelToTask(r.Context(), taskID, labelID); err != nil {
		slog.Error("add label to task failed", "task_id", taskID, "label_id", labelID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to add label to task: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "added", http.StatusOK)
}

// RemoveLabelFromTaskAPI handles DELETE /api/v1/tasks/{id}/labels/{label_id}.
func (s *Server) RemoveLabelFromTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	labelID := r.PathValue("label_id")
	if labelID == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	if err := s.labelStore.RemoveLabelFromTask(r.Context(), taskID, labelID); err != nil {
		slog.Error("remove label from task failed", "task_id", taskID, "label_id", labelID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to remove label from task: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "removed", http.StatusOK)
}

// ListLabelsForTaskAPI handles GET /api/v1/tasks/{id}/labels.
func (s *Server) ListLabelsForTaskAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	records, err := s.labelStore.ListLabelsForTask(r.Context(), taskID)
	if err != nil {
		slog.Error("list labels for task failed", "task_id", taskID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list labels for task: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Label{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// ListTasksForLabelAPI handles GET /api/v1/labels/{id}/tasks.
func (s *Server) ListTasksForLabelAPI(w http.ResponseWriter, r *http.Request) {
	if s.labelStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	labelID := r.PathValue("id")
	if labelID == "" {
		httpResponse(w, "label id is required", http.StatusBadRequest)
		return
	}

	records, err := s.labelStore.ListTasksForLabel(r.Context(), labelID)
	if err != nil {
		slog.Error("list tasks for label failed", "label_id", labelID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tasks for label: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []string{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
