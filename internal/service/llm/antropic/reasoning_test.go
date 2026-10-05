package antropic

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestReasoningEffortBudget(t *testing.T) {
	p := &Provider{MaxTokens: 32768}
	body := p.buildRequestBody("claude-sonnet-4-5", nil, nil, &service.ChatOptions{ReasoningEffort: "medium"})
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != 8192 {
		t.Fatalf("thinking = %+v", body["thinking"])
	}
	if _, ok := body["output_config"]; ok {
		t.Fatalf("extended-thinking model got output_config: %+v", body)
	}
	if body["max_tokens"] != 32768 {
		t.Fatalf("provider token budget changed: %+v", body)
	}
}

// Claude 4.6+ steers thinking with output_config.effort, and 4.7+ answers
// {type: enabled} with a 400, so adaptive models must never get a budget.
func TestReasoningEffortAdaptive(t *testing.T) {
	for _, tt := range []struct{ model, effort string }{
		{"claude-opus-4-7", "xhigh"},
		{"claude-opus-5-5", "max"},
		{"claude-sonnet-4-6", "low"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			p := &Provider{MaxTokens: 32768}
			body := p.buildRequestBody(tt.model, nil, nil, &service.ChatOptions{ReasoningEffort: tt.effort})
			thinking, _ := body["thinking"].(map[string]any)
			if thinking["type"] != "adaptive" || thinking["budget_tokens"] != nil {
				t.Fatalf("thinking = %+v", body["thinking"])
			}
			cfg, _ := body["output_config"].(map[string]any)
			if cfg["effort"] != tt.effort {
				t.Fatalf("output_config = %+v", body["output_config"])
			}
			if body["max_tokens"] != 32768 {
				t.Fatalf("max_tokens changed: %v", body["max_tokens"])
			}
		})
	}
}

// An explicit thinking block still wins over effort mapping.
func TestExplicitThinkingWinsOverEffort(t *testing.T) {
	p := &Provider{MaxTokens: 32768}
	body := p.buildRequestBody("claude-opus-4-6", nil, nil, &service.ChatOptions{
		ReasoningEffort: "high",
		Thinking:        &service.ThinkingConfig{Type: "enabled", BudgetTokens: 4000},
	})
	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != 4000 {
		t.Fatalf("thinking = %+v", body["thinking"])
	}
	if _, ok := body["output_config"]; ok {
		t.Fatalf("output_config added alongside explicit thinking: %+v", body)
	}
}

func TestExplicitAdaptiveAndDisabledThinking(t *testing.T) {
	for _, mode := range []string{"adaptive", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			p := &Provider{MaxTokens: 32768, tokenSource: NewStaticTokenSource("test-token")}
			body := p.buildRequestBody("claude-opus-4-7", nil, nil, &service.ChatOptions{
				Thinking: &service.ThinkingConfig{Type: mode, BudgetTokens: 4000}, ReasoningEffort: "high",
			})
			thinking, _ := body["thinking"].(map[string]any)
			if thinking["type"] != mode || thinking["budget_tokens"] != nil {
				t.Fatalf("thinking = %+v", thinking)
			}
			cfg, _ := body["output_config"].(map[string]any)
			if mode == "adaptive" && cfg["effort"] != "high" || mode == "disabled" && cfg != nil {
				t.Fatalf("output_config = %+v", cfg)
			}
		})
	}
}

func TestOAuthDefaultThinkingUsesModelMode(t *testing.T) {
	for _, tt := range []struct{ model, mode string }{
		{"claude-opus-4-7", "adaptive"},
		{"claude-sonnet-4-6", "adaptive"},
		{"claude-sonnet-4-5", "enabled"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			p := &Provider{MaxTokens: 32768, tokenSource: NewStaticTokenSource("test-token")}
			body := p.buildRequestBody(tt.model, nil, nil, nil)
			thinking, _ := body["thinking"].(map[string]any)
			if thinking["type"] != tt.mode {
				t.Fatalf("thinking = %+v", thinking)
			}
		})
	}
}
