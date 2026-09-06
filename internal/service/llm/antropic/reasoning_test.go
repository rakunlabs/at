package antropic

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestReasoningEffortBudget(t *testing.T) {
	p := &Provider{MaxTokens: 32768}
	body := p.buildRequestBody("model", nil, nil, &service.ChatOptions{ReasoningEffort: "medium"})
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != 8192 {
		t.Fatalf("thinking = %+v", body["thinking"])
	}
	if body["max_tokens"] != 32768 {
		t.Fatalf("provider token budget changed: %+v", body)
	}
}
