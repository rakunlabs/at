package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// mcpServerExportData is the portable representation of an MCP Server (no id/timestamps).
type mcpServerExportData struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Config      service.MCPServerConfig `json:"config"`
	Servers     []string                `json:"servers,omitempty"`
	URLs        []string                `json:"urls,omitempty"`
}

// ─── General MCP Server CRUD API ───

// ListMCPServersAPI handles GET /api/v1/mcp/servers.
func (s *Server) ListMCPServersAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.mcpServerStore.ListMCPServers(r.Context(), q)
	if err != nil {
		slog.Error("list mcp servers failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list mcp servers: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.MCPServer]{Data: []service.MCPServer{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetMCPServerAPI handles GET /api/v1/mcp/servers/{id}.
func (s *Server) GetMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	record, err := s.mcpServerStore.GetMCPServer(r.Context(), id)
	if err != nil {
		slog.Error("get mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("mcp server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateMCPServerAPI handles POST /api/v1/mcp/servers.
func (s *Server) CreateMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.MCPServer
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

	record, err := s.mcpServerStore.CreateMCPServer(r.Context(), req)
	if err != nil {
		slog.Error("create mcp server failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateMCPServerAPI handles PUT /api/v1/mcp/servers/{id}.
func (s *Server) UpdateMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	var req service.MCPServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.mcpServerStore.UpdateMCPServer(r.Context(), id, req)
	if err != nil {
		slog.Error("update mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("mcp server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteMCPServerAPI handles DELETE /api/v1/mcp/servers/{id}.
func (s *Server) DeleteMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	if err := s.mcpServerStore.DeleteMCPServer(r.Context(), id); err != nil {
		slog.Error("delete mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

// ─── MCP Server Import / Export API ───

// ExportMCPServerAPI handles GET /api/v1/mcp/servers/{id}/export.
func (s *Server) ExportMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	record, err := s.mcpServerStore.GetMCPServer(r.Context(), id)
	if err != nil {
		slog.Error("export mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to export mcp server: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("mcp server %q not found", id), http.StatusNotFound)
		return
	}

	export := mcpServerExportData{
		Name:        record.Name,
		Description: record.Description,
		Config:      record.Config,
		Servers:     record.Servers,
		URLs:        record.URLs,
	}

	httpResponseJSON(w, export, http.StatusOK)
}

// ImportMCPServerAPI handles POST /api/v1/mcp/servers/import.
func (s *Server) ImportMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req mcpServerExportData
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	server := service.MCPServer{
		Name:        req.Name,
		Description: req.Description,
		Config:      req.Config,
		Servers:     req.Servers,
		URLs:        req.URLs,
		CreatedBy:   userEmail,
		UpdatedBy:   userEmail,
	}

	record, err := s.mcpServerStore.CreateMCPServer(r.Context(), server)
	if err != nil {
		slog.Error("import mcp server failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// PreviewImportMCPServerAPI handles POST /api/v1/mcp/servers/import/preview.
func (s *Server) PreviewImportMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	var req mcpServerExportData
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	httpResponseJSON(w, req, http.StatusOK)
}
