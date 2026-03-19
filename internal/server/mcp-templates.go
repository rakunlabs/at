package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

//go:embed mcp_templates/*.json
var mcpTemplateFS embed.FS

// MCPTemplate is a predefined MCP configuration that ships with AT.
type MCPTemplate struct {
	Slug        string                `json:"slug"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Category    string                `json:"category"`
	Tags        []string              `json:"tags"`
	MCPServer   MCPTemplateServerData `json:"mcp_server"`
}

// MCPTemplateServerData holds the MCP server payload to be installed.
type MCPTemplateServerData struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Config      service.MCPServerConfig `json:"config"`
}

// loadMCPTemplates reads all embedded JSON template files.
func (s *Server) loadMCPTemplates() {
	entries, err := mcpTemplateFS.ReadDir("mcp_templates")
	if err != nil {
		slog.Warn("failed to read mcp_templates dir", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := mcpTemplateFS.ReadFile("mcp_templates/" + entry.Name())
		if err != nil {
			slog.Warn("failed to read mcp template", "file", entry.Name(), "error", err)
			continue
		}

		var tmpl MCPTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			slog.Warn("failed to parse mcp template", "file", entry.Name(), "error", err)
			continue
		}

		s.mcpTemplates = append(s.mcpTemplates, tmpl)
	}

	slog.Info("loaded mcp templates", "count", len(s.mcpTemplates))
}

// ListMCPTemplatesAPI handles GET /api/v1/mcp-templates.
func (s *Server) ListMCPTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	var result []MCPTemplate
	for _, t := range s.mcpTemplates {
		if category != "" && !strings.EqualFold(t.Category, category) {
			continue
		}
		result = append(result, t)
	}

	if result == nil {
		result = []MCPTemplate{}
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// GetMCPTemplateAPI handles GET /api/v1/mcp-templates/{slug}.
func (s *Server) GetMCPTemplateAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	for _, t := range s.mcpTemplates {
		if t.Slug == slug {
			httpResponseJSON(w, t, http.StatusOK)
			return
		}
	}
	httpResponse(w, fmt.Sprintf("template %q not found", slug), http.StatusNotFound)
}

// InstallMCPTemplateAPI handles POST /api/v1/mcp-templates/{slug}/install.
func (s *Server) InstallMCPTemplateAPI(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	slug := r.PathValue("slug")
	var tmpl *MCPTemplate
	for i := range s.mcpTemplates {
		if s.mcpTemplates[i].Slug == slug {
			tmpl = &s.mcpTemplates[i]
			break
		}
	}
	if tmpl == nil {
		httpResponse(w, fmt.Sprintf("template %q not found", slug), http.StatusNotFound)
		return
	}

	userEmail := s.getUserEmail(r)

	srv := service.MCPServer{
		Name:        tmpl.MCPServer.Name,
		Description: tmpl.MCPServer.Description,
		Config:      tmpl.MCPServer.Config,
		CreatedBy:   userEmail,
		UpdatedBy:   userEmail,
	}

	record, err := s.mcpServerStore.CreateMCPServer(r.Context(), srv)
	if err != nil {
		slog.Error("install mcp template failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to install template: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}
