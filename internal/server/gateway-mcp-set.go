package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// GatewayMCPSetHandler handles MCP protocol requests at /gateway/v1/mcp-set/{name}.
// It serves tools from an MCP Set's own Config (RAG/HTTP/External/Skills).
func (s *Server) GatewayMCPSetHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpSetStore == nil {
		httpResponse(w, "mcp set store not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate.
	auth, errMsg := s.authenticateRequest(r)
	if auth == nil {
		httpResponse(w, errMsg, http.StatusUnauthorized)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "mcp set name is required", http.StatusBadRequest)
		return
	}

	mcpSet, err := s.mcpSetStore.GetMCPSetByName(r.Context(), name)
	if err != nil {
		slog.Error("get mcp set failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up MCP set", http.StatusInternalServerError)
		return
	}
	if mcpSet == nil {
		httpResponse(w, fmt.Sprintf("MCP set %q not found", name), http.StatusNotFound)
		return
	}

	// Build a temporary MCPServer from the set's config to reuse existing handlers.
	virtualSrv := &service.MCPServer{
		Name:   mcpSet.Name,
		Config: mcpSet.Config,
	}

	// Parse the JSON-RPC request.
	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Route by method — reuse the existing MCP server handlers.
	switch req.Method {
	case "initialize":
		s.gwGenMCPInitialize(w, req, virtualSrv)
	case "notifications/initialized":
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.gwGenMCPListTools(w, req, virtualSrv)
	case "tools/call":
		s.gwGenMCPCallTool(w, r, req, virtualSrv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// mcpSetHasOwnTools returns true if the MCP set has its own tool config.
func mcpSetHasOwnTools(cfg service.MCPServerConfig) bool {
	return len(cfg.EnabledRAGTools) > 0 ||
		len(cfg.HTTPTools) > 0 ||
		len(cfg.MCPUpstreams) > 0 ||
		len(cfg.EnabledSkills) > 0 ||
		len(cfg.EnabledBuiltinTools) > 0
}
