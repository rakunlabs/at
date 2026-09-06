package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Agent Management Tool Executors ───

// execAgentCreate creates a new agent.
func (s *Server) execAgentCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	config := service.AgentConfig{}
	if v, ok := args["provider"].(string); ok {
		config.Provider = strings.TrimSpace(v)
	}
	if v, ok := args["model"].(string); ok {
		config.Model = strings.TrimSpace(v)
	}
	if v, ok := args["system_prompt"].(string); ok {
		config.SystemPrompt = v
	}
	if v, ok := args["description"].(string); ok {
		config.Description = v
	}
	if v, ok := args["max_iterations"].(float64); ok {
		config.MaxIterations = int(v)
	}
	if v, ok := args["tool_timeout"].(float64); ok {
		config.ToolTimeout = int(v)
	}

	if err := applyAgentToolBindings(&config, args); err != nil {
		return "", err
	}

	// Parse mcp_sets array (internal MCPs).
	if raw, ok := args["mcp_sets"]; ok {
		data, _ := json.Marshal(raw)
		var mcpSets []string
		if err := json.Unmarshal(data, &mcpSets); err == nil {
			config.MCPSets = mcpSets
		}
	}

	// Parse builtin_tools array.
	if raw, ok := args["builtin_tools"]; ok {
		data, _ := json.Marshal(raw)
		var builtins []string
		if err := json.Unmarshal(data, &builtins); err == nil {
			config.BuiltinTools = builtins
		}
	}

	// Validate provider exists and model is available if both are specified.
	if config.Provider != "" && s.store != nil {
		provider, err := s.store.GetProvider(ctx, config.Provider)
		if err != nil {
			return "", fmt.Errorf("failed to validate provider: %w", err)
		}
		if provider == nil {
			return "", fmt.Errorf("provider %q not found. Use provider_list to see available providers", config.Provider)
		}
		if config.Model != "" {
			validModels := provider.Config.Models
			if len(validModels) == 0 && provider.Config.Model != "" {
				validModels = []string{provider.Config.Model}
			}
			if len(validModels) > 0 {
				found := false
				for _, m := range validModels {
					if strings.TrimSpace(m) == config.Model {
						found = true
						break
					}
				}
				if !found {
					return "", fmt.Errorf("model %q not available for provider %q. Available models: %v", config.Model, config.Provider, validModels)
				}
			}
		}
	}

	record, err := s.agentStore.CreateAgent(ctx, service.Agent{
		Name:   name,
		Config: config,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create agent: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execAgentList lists all agents.
func (s *Server) execAgentList(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}

	result, err := s.agentStore.ListAgents(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list agents: %w", err)
	}

	// Return a summary view.
	type agentSummary struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Provider    string `json:"provider,omitempty"`
		Model       string `json:"model,omitempty"`
		Skills      int    `json:"skills_count"`
		CreatedAt   string `json:"created_at"`
	}

	summaries := make([]agentSummary, len(result.Data))
	for i, a := range result.Data {
		summaries[i] = agentSummary{
			ID:          a.ID,
			Name:        a.Name,
			Description: a.Config.Description,
			Provider:    a.Config.Provider,
			Model:       a.Config.Model,
			Skills:      len(a.Config.Skills),
			CreatedAt:   a.CreatedAt,
		}
	}

	out := map[string]any{
		"agents": summaries,
		"total":  result.Meta.Total,
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// execAgentGet gets a single agent by ID.
func (s *Server) execAgentGet(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	record, err := s.agentStore.GetAgent(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get agent: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("agent %q not found", id)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// execAgentUpdate updates an existing agent.
func (s *Server) execAgentUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	// Fetch current agent for merge.
	existing, err := s.agentStore.GetAgent(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get agent: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("agent %q not found", id)
	}
	updated := *existing
	existing = &updated
	if err := applyAgentToolBindings(&existing.Config, args); err != nil {
		return "", err
	}

	// Merge provided fields.
	if v, ok := args["name"].(string); ok && v != "" {
		existing.Name = v
	}
	if v, ok := args["provider"].(string); ok {
		existing.Config.Provider = strings.TrimSpace(v)
	}
	if v, ok := args["model"].(string); ok {
		existing.Config.Model = strings.TrimSpace(v)
	}
	if v, ok := args["system_prompt"].(string); ok {
		existing.Config.SystemPrompt = v
	}
	if v, ok := args["description"].(string); ok {
		existing.Config.Description = v
	}
	if v, ok := args["max_iterations"].(float64); ok {
		existing.Config.MaxIterations = int(v)
	}
	if v, ok := args["tool_timeout"].(float64); ok {
		existing.Config.ToolTimeout = int(v)
	}

	// Replace mcp_sets if provided.
	if raw, ok := args["mcp_sets"]; ok {
		data, _ := json.Marshal(raw)
		var mcpSets []string
		if err := json.Unmarshal(data, &mcpSets); err == nil {
			existing.Config.MCPSets = mcpSets
		}
	}
	// Replace builtin_tools if provided.
	if raw, ok := args["builtin_tools"]; ok {
		data, _ := json.Marshal(raw)
		var builtins []string
		if err := json.Unmarshal(data, &builtins); err == nil {
			existing.Config.BuiltinTools = builtins
		}
	}

	record, err := s.agentStore.UpdateAgent(ctx, id, *existing)
	if err != nil {
		return "", fmt.Errorf("failed to update agent: %w", err)
	}

	data, _ := json.MarshalIndent(record, "", "  ")
	return string(data), nil
}

// Decode both binding levels before applying either so invalid patches are atomic.
func applyAgentToolBindings(config *service.AgentConfig, args map[string]any) error {
	bindings := make(map[string]any)
	for _, key := range []string{"skills", "connections"} {
		if value, ok := args[key]; ok {
			if value == nil {
				return fmt.Errorf("%s must not be null; use an empty array or object to clear it", key)
			}
			bindings[key] = value
		}
	}
	data, err := json.Marshal(bindings)
	if err != nil {
		return fmt.Errorf("failed to encode agent bindings: %w", err)
	}
	var patch struct {
		Skills      []service.SkillRef `json:"skills"`
		Connections map[string]string  `json:"connections"`
	}
	if err := json.Unmarshal(data, &patch); err != nil {
		return fmt.Errorf("invalid agent bindings: %w", err)
	}
	var rawPatch struct {
		Skills []json.RawMessage `json:"skills"`
	}
	if err := json.Unmarshal(data, &rawPatch); err != nil {
		return fmt.Errorf("invalid skill bindings: %w", err)
	}
	validateConnections := func(connections map[string]string) error {
		for provider, id := range connections {
			if strings.TrimSpace(provider) == "" || strings.TrimSpace(id) == "" {
				return fmt.Errorf("connection provider and ID must be non-empty strings")
			}
		}
		return nil
	}
	for i, skill := range patch.Skills {
		if strings.TrimSpace(skill.ID) == "" {
			return fmt.Errorf("skills[%d] must have a non-empty skill ID or name", i)
		}
		if err := validateConnections(skill.Connections); err != nil {
			return fmt.Errorf("invalid skills[%d].connections: %w", i, err)
		}
		if rawPatch.Skills[i][0] == '{' {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rawPatch.Skills[i], &fields); err != nil {
				return fmt.Errorf("invalid skills[%d]: %w", i, err)
			}
			if string(fields["connections"]) == "null" {
				return fmt.Errorf("skills[%d].connections must be an object, not null", i)
			}
		}
	}
	if err := validateConnections(patch.Connections); err != nil {
		return fmt.Errorf("invalid connections: %w", err)
	}
	if _, ok := bindings["skills"]; ok && patch.Skills == nil {
		return fmt.Errorf("skills must be an array, not null")
	}
	if _, ok := bindings["connections"]; ok && patch.Connections == nil {
		return fmt.Errorf("connections must be an object, not null")
	}
	if _, ok := bindings["skills"]; ok {
		config.Skills = patch.Skills
	}
	if _, ok := bindings["connections"]; ok {
		config.Connections = patch.Connections
	}
	return nil
}

// ─── Agent Destructive Tool Executors (Phase 2) ───

// execAgentDelete deletes an agent. The HTTP handler does not perform
// any cascade either — referencing org memberships and connections
// are left as-is. Callers are expected to call org_remove_agent
// first if the agent is in any organization.
func (s *Server) execAgentDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.agentStore.DeleteAgent(ctx, id); err != nil {
		return "", fmt.Errorf("delete agent %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}
