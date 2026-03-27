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

	// Parse skills array.
	if raw, ok := args["skills"]; ok {
		data, _ := json.Marshal(raw)
		var skills []string
		if err := json.Unmarshal(data, &skills); err == nil {
			config.Skills = skills
		}
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

	// Replace skills if provided.
	if raw, ok := args["skills"]; ok {
		data, _ := json.Marshal(raw)
		var skills []string
		if err := json.Unmarshal(data, &skills); err == nil {
			existing.Config.Skills = skills
		}
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
