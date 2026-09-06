package openai

import "github.com/rakunlabs/at/internal/service"

func (*Provider) ValidateReasoningEffort(effort string) error {
	return service.ValidateProviderReasoningEffort("openai", effort)
}

func (*CodexProvider) ValidateReasoningEffort(effort string) error {
	return service.ValidateProviderReasoningEffort("openai", effort)
}
