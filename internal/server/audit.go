package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListAuditEntriesAPI handles GET /api/v1/audit.
func (s *Server) ListAuditEntriesAPI(w http.ResponseWriter, r *http.Request) {
	if s.auditStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.auditStore.ListAuditEntries(r.Context(), q)
	if err != nil {
		slog.Error("list audit entries failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list audit entries: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.AuditEntry]{Data: []service.AuditEntry{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAuditTrailAPI handles GET /api/v1/audit/{resource_type}/{resource_id}.
func (s *Server) GetAuditTrailAPI(w http.ResponseWriter, r *http.Request) {
	if s.auditStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	resourceType := r.PathValue("resource_type")
	if resourceType == "" {
		httpResponse(w, "resource_type is required", http.StatusBadRequest)
		return
	}

	resourceID := r.PathValue("resource_id")
	if resourceID == "" {
		httpResponse(w, "resource_id is required", http.StatusBadRequest)
		return
	}

	records, err := s.auditStore.GetAuditTrail(r.Context(), resourceType, resourceID)
	if err != nil {
		slog.Error("get audit trail failed", "resource_type", resourceType, "resource_id", resourceID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get audit trail: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.AuditEntry{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}
