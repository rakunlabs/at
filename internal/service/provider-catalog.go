package service

import "context"

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

type WorkspaceProviderCatalogStorer interface {
	ListWorkspaceProviderCatalog(context.Context) ([]ProviderCatalogEntry, error)
}
