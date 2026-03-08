package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListProjectsAPI handles GET /api/v1/projects.
func (s *Server) ListProjectsAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.projectStore.ListProjects(r.Context(), q)
	if err != nil {
		slog.Error("list projects failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list projects: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Project]{Data: []service.Project{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetProjectAPI handles GET /api/v1/projects/{id}.
func (s *Server) GetProjectAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "project id is required", http.StatusBadRequest)
		return
	}

	record, err := s.projectStore.GetProject(r.Context(), id)
	if err != nil {
		slog.Error("get project failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get project: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("project %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateProjectAPI handles POST /api/v1/projects.
func (s *Server) CreateProjectAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Project
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "active"
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.projectStore.CreateProject(r.Context(), req)
	if err != nil {
		slog.Error("create project failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create project: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateProjectAPI handles PUT /api/v1/projects/{id}.
func (s *Server) UpdateProjectAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "project id is required", http.StatusBadRequest)
		return
	}

	var req service.Project
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.projectStore.UpdateProject(r.Context(), id, req)
	if err != nil {
		slog.Error("update project failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update project: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("project %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteProjectAPI handles DELETE /api/v1/projects/{id}.
func (s *Server) DeleteProjectAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "project id is required", http.StatusBadRequest)
		return
	}

	if err := s.projectStore.DeleteProject(r.Context(), id); err != nil {
		slog.Error("delete project failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete project: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ListProjectsByGoalAPI handles GET /api/v1/goals/{id}/projects.
func (s *Server) ListProjectsByGoalAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "goal id is required", http.StatusBadRequest)
		return
	}

	records, err := s.projectStore.ListProjectsByGoal(r.Context(), id)
	if err != nil {
		slog.Error("list projects by goal failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list projects by goal: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Project{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// ListProjectsByOrganizationAPI handles GET /api/v1/organizations/{id}/projects.
func (s *Server) ListProjectsByOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	records, err := s.projectStore.ListProjectsByOrganization(r.Context(), id)
	if err != nil {
		slog.Error("list projects by organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list projects by organization: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Project{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
