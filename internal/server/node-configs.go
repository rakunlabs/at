package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Node Config CRUD API ───

// nodeConfigsResponse wraps a list of node config records for JSON output.
type nodeConfigsResponse struct {
	NodeConfigs []service.NodeConfig `json:"node_configs"`
}

// sensitiveNodeConfigFields lists fields that should be redacted in list responses, keyed by config type.
var sensitiveNodeConfigFields = map[string][]string{
	"email": {"password"},
}

// ListNodeConfigsAPI handles GET /api/v1/node-configs.
// Supports optional ?type=email query parameter for filtered listing.
// Sensitive fields (like password) are redacted in list responses.
func (s *Server) ListNodeConfigsAPI(w http.ResponseWriter, r *http.Request) {
	if s.nodeConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var records []service.NodeConfig
	var err error

	configType := r.URL.Query().Get("type")
	if configType != "" {
		records, err = s.nodeConfigStore.ListNodeConfigsByType(r.Context(), configType)
	} else {
		records, err = s.nodeConfigStore.ListNodeConfigs(r.Context())
	}
	if err != nil {
		slog.Error("list node configs failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list node configs: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.NodeConfig{}
	}

	// Redact sensitive fields in list responses.
	for i := range records {
		records[i].Data = redactNodeConfigData(records[i].Type, records[i].Data)
	}

	httpResponseJSON(w, nodeConfigsResponse{NodeConfigs: records}, http.StatusOK)
}

// GetNodeConfigAPI handles GET /api/v1/node-configs/:id.
// Returns full data including sensitive fields (for editing).
func (s *Server) GetNodeConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.nodeConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "node config id is required", http.StatusBadRequest)
		return
	}

	record, err := s.nodeConfigStore.GetNodeConfig(r.Context(), id)
	if err != nil {
		slog.Error("get node config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get node config: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("node config %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateNodeConfigAPI handles POST /api/v1/node-configs.
func (s *Server) CreateNodeConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.nodeConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		httpResponse(w, "type is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.nodeConfigStore.CreateNodeConfig(r.Context(), req)
	if err != nil {
		slog.Error("create node config failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create node config: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateNodeConfigAPI handles PUT /api/v1/node-configs/:id.
func (s *Server) UpdateNodeConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.nodeConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "node config id is required", http.StatusBadRequest)
		return
	}

	var req service.NodeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		httpResponse(w, "type is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.nodeConfigStore.UpdateNodeConfig(r.Context(), id, req)
	if err != nil {
		slog.Error("update node config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update node config: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("node config %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteNodeConfigAPI handles DELETE /api/v1/node-configs/:id.
func (s *Server) DeleteNodeConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.nodeConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "node config id is required", http.StatusBadRequest)
		return
	}

	if err := s.nodeConfigStore.DeleteNodeConfig(r.Context(), id); err != nil {
		slog.Error("delete node config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete node config: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// redactNodeConfigData replaces sensitive fields with "***" for list responses.
func redactNodeConfigData(configType, data string) string {
	fields, ok := sensitiveNodeConfigFields[configType]
	if !ok || data == "" {
		return data
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return data
	}

	for _, field := range fields {
		if _, ok := m[field]; ok {
			m[field] = "***"
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return string(out)
}
