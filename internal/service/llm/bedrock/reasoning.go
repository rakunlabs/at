package bedrock

import "github.com/rakunlabs/at/internal/service"

func (*Provider) ValidateReasoningEffort(effort string) error {
	return service.ValidateProviderReasoningEffort("bedrock", effort)
}
