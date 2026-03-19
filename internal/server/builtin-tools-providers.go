package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ─── Provider Management Tool Executors ───

// execProviderList lists all configured LLM providers with their available models.
// API keys are redacted for security.
func (s *Server) execProviderList(ctx context.Context, args map[string]any) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("provider store not configured")
	}

	result, err := s.store.ListProviders(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list providers: %w", err)
	}

	type providerSummary struct {
		Key          string   `json:"key"`
		Type         string   `json:"type"`
		DefaultModel string   `json:"default_model"`
		Models       []string `json:"models,omitempty"`
		BaseURL      string   `json:"base_url,omitempty"`
		AuthType     string   `json:"auth_type,omitempty"`
		CreatedAt    string   `json:"created_at"`
	}

	summaries := make([]providerSummary, len(result.Data))
	for i, p := range result.Data {
		models := p.Config.Models
		// If no models list, show default model as the only option.
		if len(models) == 0 && p.Config.Model != "" {
			models = []string{strings.TrimSpace(p.Config.Model)}
		}
		// Trim whitespace from all model names.
		for j := range models {
			models[j] = strings.TrimSpace(models[j])
		}

		summaries[i] = providerSummary{
			Key:          p.Key,
			Type:         p.Config.Type,
			DefaultModel: strings.TrimSpace(p.Config.Model),
			Models:       models,
			BaseURL:      p.Config.BaseURL,
			AuthType:     p.Config.AuthType,
			CreatedAt:    p.CreatedAt,
		}
	}

	out := map[string]any{
		"providers": summaries,
		"total":     result.Meta.Total,
		"note":      "Use the 'key' as the provider value and pick a model from the 'models' list when creating agents.",
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// execProviderGet gets details for a single provider by key.
// API keys are redacted for security.
func (s *Server) execProviderGet(ctx context.Context, args map[string]any) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("provider store not configured")
	}

	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}

	record, err := s.store.GetProvider(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to get provider: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("provider %q not found", key)
	}

	// Redact secrets.
	redactProviderRecord(record)

	// Build a clean response.
	models := record.Config.Models
	if len(models) == 0 && record.Config.Model != "" {
		models = []string{strings.TrimSpace(record.Config.Model)}
	}
	// Trim whitespace from all model names.
	for j := range models {
		models[j] = strings.TrimSpace(models[j])
	}

	out := map[string]any{
		"key":           record.Key,
		"type":          record.Config.Type,
		"default_model": strings.TrimSpace(record.Config.Model),
		"models":        models,
		"base_url":      record.Config.BaseURL,
		"auth_type":     record.Config.AuthType,
		"extra_headers": record.Config.ExtraHeaders,
		"created_at":    record.CreatedAt,
		"updated_at":    record.UpdatedAt,
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}
