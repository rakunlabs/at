package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListApprovalsAPI handles GET /api/v1/approvals.
func (s *Server) ListApprovalsAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.approvalStore.ListApprovals(r.Context(), q)
	if err != nil {
		slog.Error("list approvals failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list approvals: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Approval]{Data: []service.Approval{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetApprovalAPI handles GET /api/v1/approvals/{id}.
func (s *Server) GetApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "approval id is required", http.StatusBadRequest)
		return
	}

	record, err := s.approvalStore.GetApproval(r.Context(), id)
	if err != nil {
		slog.Error("get approval failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get approval: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("approval %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateApprovalAPI handles POST /api/v1/approvals.
func (s *Server) CreateApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Approval
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		httpResponse(w, "type is required", http.StatusBadRequest)
		return
	}

	req.Status = "pending"
	req.RequestedByType = "user"
	req.RequestedByID = s.getUserEmail(r)

	record, err := s.approvalStore.CreateApproval(r.Context(), req)
	if err != nil {
		slog.Error("create approval failed", "type", req.Type, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create approval: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateApprovalAPI handles PUT /api/v1/approvals/{id}.
func (s *Server) UpdateApprovalAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "approval id is required", http.StatusBadRequest)
		return
	}

	var req service.Approval
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Status == "approved" || req.Status == "rejected" {
		req.DecidedByUserID = s.getUserEmail(r)
		req.DecidedAt = time.Now().UTC().Format(time.RFC3339)
	}

	record, err := s.approvalStore.UpdateApproval(r.Context(), id, req)
	if err != nil {
		slog.Error("update approval failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update approval: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("approval %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// ListPendingApprovalsAPI handles GET /api/v1/approvals/pending.
func (s *Server) ListPendingApprovalsAPI(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	orgID := r.URL.Query().Get("org_id")

	records, err := s.approvalStore.ListPendingApprovals(r.Context(), orgID)
	if err != nil {
		slog.Error("list pending approvals failed", "org_id", orgID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list pending approvals: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Approval{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
