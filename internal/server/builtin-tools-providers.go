package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
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

// ─── Provider Write Tool Executors (Phase 2) ───
//
// Provider records are keyed by the user-supplied `key` string, NOT
// by ULID. We mirror the HTTP layer:
//   - Create: rejects duplicate keys (409-equivalent)
//   - Update: empty api_key/refresh_token preserve the stored values
//             so the agent can fetch+edit other fields without having
//             to recover the redacted secrets first
//   - Both:   hot-reload the provider into the live registry on
//             success via reloadProvider() / removeProvider() so the
//             change takes effect immediately
//   - All responses redact secrets via redactProviderRecord — same
//             "***" placeholder the UI uses

// decodeLLMConfig coerces an args["config"] value into a config.LLMConfig.
func decodeLLMConfig(raw any) (config.LLMConfig, error) {
	var cfg config.LLMConfig
	if raw == nil {
		return cfg, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("marshal: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid config object: %w", err)
	}
	return cfg, nil
}

func (s *Server) execProviderCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("provider store not configured")
	}
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	cfg, err := decodeLLMConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if cfg.Type == "" {
		return "", fmt.Errorf("config.type is required")
	}
	if msg := validateRateLimitConfig(cfg.RateLimit); msg != "" {
		return "", fmt.Errorf("%s", msg)
	}

	if existing, _ := s.store.GetProvider(ctx, key); existing != nil {
		return "", fmt.Errorf("provider %q already exists", key)
	}

	record, err := s.store.CreateProvider(ctx, service.ProviderRecord{
		Key:       key,
		Config:    cfg,
		CreatedBy: "mcp",
		UpdatedBy: "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("create provider %q: %w", key, err)
	}

	if err := s.reloadProvider(key, cfg); err != nil {
		// Match HTTP behaviour: warn but don't fail — the DB record is
		// authoritative and the next process restart will pick it up.
		slog.Warn("provider created in DB but failed to hot-reload", "key", key, "error", err)
	}

	redactProviderRecord(record)
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider: %w", err)
	}
	return string(out), nil
}

func (s *Server) execProviderUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("provider store not configured")
	}
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	cfg, err := decodeLLMConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if cfg.Type == "" {
		return "", fmt.Errorf("config.type is required")
	}
	if msg := validateRateLimitConfig(cfg.RateLimit); msg != "" {
		return "", fmt.Errorf("%s", msg)
	}

	// Preserve managed OAuth fields omitted by redacted provider reads.
	existing, err := s.store.GetProvider(ctx, key)
	if err != nil {
		return "", fmt.Errorf("read existing provider %q: %w", key, err)
	}
	if existing != nil {
		preserveProviderManagedAuth(&cfg, existing.Config)
	}

	record, err := s.store.UpdateProvider(ctx, key, service.ProviderRecord{
		Key:       key,
		Config:    cfg,
		UpdatedBy: "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("update provider %q: %w", key, err)
	}
	if record == nil {
		return "", fmt.Errorf("provider %q not found", key)
	}
	if err := s.reloadProvider(key, cfg); err != nil {
		slog.Warn("provider updated in DB but failed to hot-reload", "key", key, "error", err)
	}

	redactProviderRecord(record)
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider: %w", err)
	}
	return string(out), nil
}

func (s *Server) execProviderDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("provider store not configured")
	}
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	if err := s.store.DeleteProvider(ctx, key); err != nil {
		return "", fmt.Errorf("delete provider %q: %w", key, err)
	}
	s.removeProvider(key)
	return fmt.Sprintf(`{"status":"deleted","key":%q}`, key), nil
}

// execProviderDiscoverModels mirrors DiscoverModelsAPI: when the
// caller passes an existing key but a redacted (empty) api_key, fall
// back to the stored key so the model-listing call still works. The
// list of supported types is intentionally hardcoded to match the
// HTTP handler — keeping the surfaces in lockstep.
func (s *Server) execProviderDiscoverModels(ctx context.Context, args map[string]any) (string, error) {
	cfg, err := decodeLLMConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if cfg.Type == "" {
		return "", fmt.Errorf("config.type is required")
	}

	if key, _ := args["key"].(string); key != "" && s.store != nil {
		if existing, err := s.store.GetProvider(ctx, key); err == nil && existing != nil {
			if cfg.AuthType == "" {
				cfg.AuthType = existing.Config.AuthType
			}
			preserveProviderManagedAuth(&cfg, existing.Config)
		}
	}

	var models []string
	key, _ := args["key"].(string)
	switch cfg.Type {
	case "openai":
		models, err = s.discoverOpenAIProviderModels(ctx, key, cfg)
	case "anthropic":
		models, err = discoverAnthropicModels(ctx, cfg)
		if err != nil {
			// Some Anthropic-compatible providers don't expose /v1/models;
			// match the HTTP handler's "swallow + return empty" behaviour.
			slog.Warn("anthropic model discovery failed, returning empty list", "error", err)
			models = nil
			err = nil
		}
	case "gemini":
		models, err = discoverGeminiModels(ctx, cfg)
	case "minimax":
		models = []string{
			"MiniMax-M2.7",
			"MiniMax-M2.7-highspeed",
			"MiniMax-M2.5",
			"MiniMax-M2.5-highspeed",
			"MiniMax-M2.1",
			"MiniMax-M2.1-highspeed",
			"MiniMax-M2",
		}
	default:
		return "", fmt.Errorf("model discovery is not supported for provider type %q (supported: openai, anthropic, gemini, minimax)", cfg.Type)
	}
	if err != nil {
		return "", fmt.Errorf("discover models: %w", err)
	}
	if models == nil {
		models = []string{}
	}
	out, _ := json.MarshalIndent(map[string]any{"models": models}, "", "  ")
	return string(out), nil
}
