package service

import (
	"context"
	"slices"

	"github.com/rakunlabs/at/internal/config"
)

type ProviderCatalogEntry struct {
	Key          string   `json:"key"`
	Reference    string   `json:"reference,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	Type         string   `json:"type"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
	Shared       bool     `json:"shared"`
	// ReasoningEfforts lists the efforts each model accepts. Only models with
	// a known answer are present; an empty list means non-reasoning. Pickers
	// fall back to ProviderReasoningEfforts for absent models.
	ReasoningEfforts map[string][]string `json:"reasoning_efforts,omitempty"`
	// ModelCapabilities is the resolved per-model view (detection plus
	// overrides). Models with nothing known are absent.
	ModelCapabilities map[string]ModelCapabilities `json:"model_capabilities,omitempty"`
}

// CatalogReasoningEfforts resolves ReasoningEfforts for a provider's models.
func CatalogReasoningEfforts(providerType string, models []string, overrides func(model string) []string) map[string][]string {
	out := map[string][]string{}
	for _, model := range models {
		if efforts, known := ModelReasoningEfforts(providerType, model, overrides(model)); known {
			out[model] = efforts
		}
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// CatalogModelCapabilities resolves every advertised chat model of a stored
// provider configuration.
func CatalogModelCapabilities(cfg config.LLMConfig, models []string) map[string]ModelCapabilities {
	out := map[string]ModelCapabilities{}
	for _, model := range models {
		if caps := ProviderModelCapabilities(cfg, model); caps.Known() {
			out[model] = caps
		}
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// ProviderChatModels is the advertised chat model list: models plus the
// default model when it is not already listed.
func ProviderChatModels(cfg config.LLMConfig) []string {
	models := slices.Clone(cfg.Models)
	if cfg.Model != "" && !slices.Contains(models, cfg.Model) {
		models = append(models, cfg.Model)
	}

	return models
}

type WorkspaceProviderCatalogStorer interface {
	ListWorkspaceProviderCatalog(context.Context) ([]ProviderCatalogEntry, error)
}
