package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListOrganizationsAPI handles GET /api/v1/organizations.
func (s *Server) ListOrganizationsAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.organizationStore.ListOrganizations(r.Context(), q)
	if err != nil {
		slog.Error("list organizations failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list organizations: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Organization]{Data: []service.Organization{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetOrganizationAPI handles GET /api/v1/organizations/{id}.
func (s *Server) GetOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	record, err := s.organizationStore.GetOrganization(r.Context(), id)
	if err != nil {
		slog.Error("get organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateOrganizationAPI handles POST /api/v1/organizations.
func (s *Server) CreateOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Organization
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.organizationStore.CreateOrganization(r.Context(), req)
	if err != nil {
		slog.Error("create organization failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateOrganizationAPI handles PUT /api/v1/organizations/{id}.
func (s *Server) UpdateOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	// Fetch existing so partial updates preserve existing fields.
	existing, err := s.organizationStore.GetOrganization(r.Context(), id)
	if err != nil {
		slog.Error("get organization for update failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	var req service.Organization
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Allow partial updates: fall back to existing values when not provided.
	if req.Name == "" {
		req.Name = existing.Name
	}
	if len(req.CanvasLayout) == 0 {
		req.CanvasLayout = existing.CanvasLayout
	}
	if req.HeadAgentID == "" {
		req.HeadAgentID = existing.HeadAgentID
	}
	if req.MaxDelegationDepth == 0 {
		req.MaxDelegationDepth = existing.MaxDelegationDepth
	}

	// Validate head_agent_id if being changed.
	if req.HeadAgentID != "" && req.HeadAgentID != existing.HeadAgentID {
		if s.orgAgentStore != nil {
			member, err := s.orgAgentStore.GetOrganizationAgentByPair(r.Context(), id, req.HeadAgentID)
			if err != nil {
				slog.Error("validate head agent failed", "org_id", id, "agent_id", req.HeadAgentID, "error", err)
				httpResponse(w, fmt.Sprintf("failed to validate head agent: %v", err), http.StatusInternalServerError)
				return
			}
			if member == nil {
				httpResponse(w, "head agent is not a member of this organization", http.StatusBadRequest)
				return
			}
			if member.Status != "active" {
				httpResponse(w, "head agent is not active", http.StatusBadRequest)
				return
			}
		}
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.organizationStore.UpdateOrganization(r.Context(), id, req)
	if err != nil {
		slog.Error("update organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update organization: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteOrganizationAPI handles DELETE /api/v1/organizations/{id}.
func (s *Server) DeleteOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	if err := s.organizationStore.DeleteOrganization(r.Context(), id); err != nil {
		slog.Error("delete organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
