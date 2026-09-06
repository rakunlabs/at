package vertex

import "github.com/rakunlabs/at/internal/service"

func (*Provider) ValidateReasoningEffort(effort string) error {
	return service.ValidateProviderReasoningEffort("vertex", effort)
}
