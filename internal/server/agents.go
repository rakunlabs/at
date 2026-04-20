package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/agentmd"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// agentsResponse wraps a list of agent records for JSON output.
type agentsResponse struct {
	Agents []service.Agent `json:"agents"`
}

// ListAgentsAPI handles GET /api/v1/agents.
func (s *Server) ListAgentsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.agentStore.ListAgents(r.Context(), q)
	if err != nil {
		slog.Error("list agents failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list agents: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Agent]{Data: []service.Agent{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentAPI handles GET /api/v1/agents/:id.
func (s *Server) GetAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentStore.GetAgent(r.Context(), id)
	if err != nil {
		slog.Error("get agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateAgentAPI handles POST /api/v1/agents.
func (s *Server) CreateAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Agent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Config.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}

	// Set defaults if missing
	if req.Config.MaxIterations == 0 {
		req.Config.MaxIterations = 10
	}
	if req.Config.ToolTimeout == 0 {
		req.Config.ToolTimeout = 60
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.agentStore.CreateAgent(r.Context(), req)
	if err != nil {
		slog.Error("create agent failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateAgentAPI handles PUT /api/v1/agents/:id.
func (s *Server) UpdateAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.Agent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Config.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail
	// Ensure UpdateTime is handled by the store, but we set user.

	record, err := s.agentStore.UpdateAgent(r.Context(), id, req)
	if err != nil {
		slog.Error("update agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update agent: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteAgentAPI handles DELETE /api/v1/agents/:id.
func (s *Server) DeleteAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	if err := s.agentStore.DeleteAgent(r.Context(), id); err != nil {
		slog.Error("delete agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Agent Import / Export API ───

// agentToMD converts an Agent to its portable markdown representation.
func agentToMD(agent *service.Agent) *agentmd.AgentMD {
	return &agentmd.AgentMD{
		Name:                      agent.Name,
		Description:               agent.Config.Description,
		Provider:                  agent.Config.Provider,
		Model:                     agent.Config.Model,
		Skills:                    service.StringsFromSkillRefs(agent.Config.Skills),
		MCPSets:                   agent.Config.MCPSets,
		MCPs:                      agent.Config.MCPs,
		Workflows:                 agent.Config.Workflows,
		BuiltinTools:              agent.Config.BuiltinTools,
		MaxIterations:             agent.Config.MaxIterations,
		ToolTimeout:               agent.Config.ToolTimeout,
		ConfirmationRequiredTools: agent.Config.ConfirmationRequiredTools,
		AvatarSeed:                agent.Config.AvatarSeed,
		SystemPrompt:              agent.Config.SystemPrompt,
	}
}

// mdToAgent converts a parsed AgentMD to a service.Agent (no ID/timestamps).
func mdToAgent(a *agentmd.AgentMD) service.Agent {
	return service.Agent{
		Name: a.Name,
		Config: service.AgentConfig{
			Description:               a.Description,
			Provider:                  a.Provider,
			Model:                     a.Model,
			SystemPrompt:              a.SystemPrompt,
			Skills:                    service.SkillRefsFromStrings(a.Skills),
			MCPSets:                   a.MCPSets,
			MCPs:                      a.MCPs,
			Workflows:                 a.Workflows,
			BuiltinTools:              a.BuiltinTools,
			MaxIterations:             a.MaxIterations,
			ToolTimeout:               a.ToolTimeout,
			ConfirmationRequiredTools: a.ConfirmationRequiredTools,
			AvatarSeed:                a.AvatarSeed,
		},
	}
}

// ExportAgentAPI handles GET /api/v1/agents/{id}/export.
// Returns the agent as a downloadable markdown file.
func (s *Server) ExportAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentStore.GetAgent(r.Context(), id)
	if err != nil {
		slog.Error("export agent failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to export agent: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	md := agentToMD(record)
	data, err := agentmd.Generate(md)
	if err != nil {
		slog.Error("generate agent markdown failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to generate agent markdown: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.md"`, record.Name))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ExportAgentJSONAPI handles GET /api/v1/agents/{id}/export-json.
// Returns the agent config as JSON (no ID/timestamps).
func (s *Server) ExportAgentJSONAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentStore.GetAgent(r.Context(), id)
	if err != nil {
		slog.Error("export agent json failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to export agent: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	export := struct {
		Name   string              `json:"name"`
		Config service.AgentConfig `json:"config"`
	}{
		Name:   record.Name,
		Config: record.Config,
	}

	httpResponseJSON(w, export, http.StatusOK)
}

// ImportAgentAPI handles POST /api/v1/agents/import.
// Accepts markdown content (agent .md) in the request body.
func (s *Server) ImportAgentAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2 MB limit
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	parsed, err := agentmd.Parse(body)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to parse agent markdown: %v", err), http.StatusBadRequest)
		return
	}

	if parsed.Name == "" {
		httpResponse(w, "agent name is required in frontmatter", http.StatusBadRequest)
		return
	}

	agent := mdToAgent(parsed)

	// Set defaults if missing.
	if agent.Config.MaxIterations == 0 {
		agent.Config.MaxIterations = 10
	}
	if agent.Config.ToolTimeout == 0 {
		agent.Config.ToolTimeout = 60
	}

	userEmail := s.getUserEmail(r)
	agent.CreatedBy = userEmail
	agent.UpdatedBy = userEmail

	record, err := s.agentStore.CreateAgent(r.Context(), agent)
	if err != nil {
		slog.Error("import agent failed", "name", agent.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import agent: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// PreviewImportAgentAPI handles POST /api/v1/agents/import/preview.
// Parses the markdown without persisting, returns the parsed agent config.
func (s *Server) PreviewImportAgentAPI(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	parsed, err := agentmd.Parse(body)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to parse agent markdown: %v", err), http.StatusBadRequest)
		return
	}

	agent := mdToAgent(parsed)

	export := struct {
		Name   string              `json:"name"`
		Config service.AgentConfig `json:"config"`
	}{
		Name:   agent.Name,
		Config: agent.Config,
	}

	httpResponseJSON(w, export, http.StatusOK)
}
