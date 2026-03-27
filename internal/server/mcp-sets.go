package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── MCP Set CRUD API ───

// ListMCPSetsAPI handles GET /api/v1/mcp/sets.
func (s *Server) ListMCPSetsAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.mcpSetStore.ListMCPSets(r.Context(), q)
	if err != nil {
		slog.Error("list mcp sets failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list mcp sets: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.MCPSet]{Data: []service.MCPSet{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetMCPSetAPI handles GET /api/v1/mcp/sets/{id}.
func (s *Server) GetMCPSetAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp set id is required", http.StatusBadRequest)
		return
	}

	record, err := s.mcpSetStore.GetMCPSet(r.Context(), id)
	if err != nil {
		slog.Error("get mcp set failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get mcp set: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("mcp set %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateMCPSetAPI handles POST /api/v1/mcp/sets.
func (s *Server) CreateMCPSetAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.MCPSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Servers == nil {
		req.Servers = []string{}
	}
	if req.URLs == nil {
		req.URLs = []string{}
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.mcpSetStore.CreateMCPSet(r.Context(), req)
	if err != nil {
		slog.Error("create mcp set failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create mcp set: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateMCPSetAPI handles PUT /api/v1/mcp/sets/{id}.
func (s *Server) UpdateMCPSetAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp set id is required", http.StatusBadRequest)
		return
	}

	var req service.MCPSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Servers == nil {
		req.Servers = []string{}
	}
	if req.URLs == nil {
		req.URLs = []string{}
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.mcpSetStore.UpdateMCPSet(r.Context(), id, req)
	if err != nil {
		slog.Error("update mcp set failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update mcp set: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("mcp set %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteMCPSetAPI handles DELETE /api/v1/mcp/sets/{id}.
func (s *Server) DeleteMCPSetAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp set id is required", http.StatusBadRequest)
		return
	}

	if err := s.mcpSetStore.DeleteMCPSet(r.Context(), id); err != nil {
		slog.Error("delete mcp set failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete mcp set: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}
