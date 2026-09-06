package service

import "fmt"

// ReasoningEffortValidator is an optional adapter guard for agent reasoning effort.
// Support here describes the adapter, not model capability: upstream models may
// still reject an effort. Empty effort leaves the provider default unchanged.
type ReasoningEffortValidator interface {
	ValidateReasoningEffort(effort string) error
}

// ValidateReasoningEffort checks the exact, case-sensitive configuration vocabulary.
func ValidateReasoningEffort(effort string) error {
	switch effort {
	case "", "low", "medium", "high", "xhigh":
		return nil
	default:
		return fmt.Errorf("invalid reasoning_effort %q: expected empty, low, medium, high, or xhigh", effort)
	}
}

// ValidateProviderReasoningEffort checks adapter support, not model capability.
// It neither rewrites model IDs nor maps xhigh to another effort or caps output tokens.
func ValidateProviderReasoningEffort(providerType, effort string) error {
	if err := ValidateReasoningEffort(effort); err != nil {
		return err
	}
	if effort == "" {
		return nil
	}
	switch providerType {
	case "openai", "azure", "vertex":
		return nil
	case "anthropic", "gemini", "vertex-gemini", "minimax":
		if effort != "xhigh" {
			return nil
		}
	}
	return fmt.Errorf("reasoning_effort %q is not supported by provider type %q", effort, providerType)
}
