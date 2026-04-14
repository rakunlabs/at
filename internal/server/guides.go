package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Guide CRUD API ───

// ListGuidesAPI handles GET /api/v1/guides.
func (s *Server) ListGuidesAPI(w http.ResponseWriter, r *http.Request) {
	if s.guideStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.guideStore.ListGuides(r.Context(), q)
	if err != nil {
		slog.Error("list guides failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list guides: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Guide]{Data: []service.Guide{}}
	}
	if records.Data == nil {
		records.Data = []service.Guide{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetGuideAPI handles GET /api/v1/guides/{id}.
func (s *Server) GetGuideAPI(w http.ResponseWriter, r *http.Request) {
	if s.guideStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "guide id is required", http.StatusBadRequest)
		return
	}

	record, err := s.guideStore.GetGuide(r.Context(), id)
	if err != nil {
		slog.Error("get guide failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get guide: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("guide %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateGuideAPI handles POST /api/v1/guides.
func (s *Server) CreateGuideAPI(w http.ResponseWriter, r *http.Request) {
	if s.guideStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Guide
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		httpResponse(w, "title is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.guideStore.CreateGuide(r.Context(), req)
	if err != nil {
		slog.Error("create guide failed", "title", req.Title, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create guide: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateGuideAPI handles PUT /api/v1/guides/{id}.
func (s *Server) UpdateGuideAPI(w http.ResponseWriter, r *http.Request) {
	if s.guideStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "guide id is required", http.StatusBadRequest)
		return
	}

	var req service.Guide
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		httpResponse(w, "title is required", http.StatusBadRequest)
		return
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.guideStore.UpdateGuide(r.Context(), id, req)
	if err != nil {
		slog.Error("update guide failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update guide: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("guide %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteGuideAPI handles DELETE /api/v1/guides/{id}.
func (s *Server) DeleteGuideAPI(w http.ResponseWriter, r *http.Request) {
	if s.guideStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "guide id is required", http.StatusBadRequest)
		return
	}

	if err := s.guideStore.DeleteGuide(r.Context(), id); err != nil {
		slog.Error("delete guide failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete guide: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
