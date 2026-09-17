package gemini

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestUsesThinkingLevel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-2.0-flash", false},
		{"gemini-2.5-flash", false},
		{"gemini-2.5-pro", false},
		{"gemini-1.5-pro", false},
		{"gemini-3-pro-preview", true},
		{"gemini-3-flash", true},
		{"gemini-3.1-pro", true},
		{"models/gemini-3-pro-preview", true},
		{"publishers/google/models/gemini-2.5-pro", false},
		{"GEMINI-3-PRO", true},
		{"", false},
		{"not-a-gemini-model", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := usesThinkingLevel(tt.model); got != tt.want {
				t.Errorf("usesThinkingLevel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// Gemini 3 rejects thinkingBudget and Gemini 2.5 does not know thinkingLevel,
// so the two generations must never receive the other's field.
func TestThinkingConfigIsGenerationSpecific(t *testing.T) {
	opts := &service.ChatOptions{ReasoningEffort: "high"}

	legacy := geminiThinkingConfig("gemini-2.5-pro", opts)
	if legacy == nil {
		t.Fatal("gemini-2.5 lost its thinking config")
	}
	if legacy.ThinkingBudget == nil || *legacy.ThinkingBudget != thinkingBudgetHigh {
		t.Errorf("gemini-2.5 budget = %v, want %d", legacy.ThinkingBudget, thinkingBudgetHigh)
	}
	if legacy.ThinkingLevel != "" {
		t.Errorf("gemini-2.5 must not receive thinkingLevel, got %q", legacy.ThinkingLevel)
	}

	modern := geminiThinkingConfig("gemini-3-pro-preview", opts)
	if modern == nil {
		t.Fatal("gemini-3 lost its thinking config")
	}
	if modern.ThinkingLevel != "HIGH" {
		t.Errorf("gemini-3 level = %q, want HIGH", modern.ThinkingLevel)
	}
	if modern.ThinkingBudget != nil {
		t.Errorf("gemini-3 must not receive thinkingBudget, got %d", *modern.ThinkingBudget)
	}
}

// Gemini only emits `thought` parts when includeThoughts is set, so every
// enabled thinking config must carry it or ReasoningContent is always empty.
func TestThinkingConfigRequestsThoughts(t *testing.T) {
	for _, model := range []string{"gemini-2.5-pro", "gemini-3-pro-preview"} {
		t.Run(model, func(t *testing.T) {
			for _, effort := range []string{"low", "medium", "high"} {
				cfg := geminiThinkingConfig(model, &service.ChatOptions{ReasoningEffort: effort})
				if cfg == nil || !cfg.IncludeThoughts {
					t.Fatalf("%s: includeThoughts not set for effort %q: %+v", model, effort, cfg)
				}
			}
		})
	}
}

// A zero budget is how thinking is switched off on Gemini 2.5. It has to
// survive JSON encoding, which an `int` with omitempty silently erased.
func TestThinkingDisabledSerializesZeroBudget(t *testing.T) {
	cfg := geminiThinkingConfig("gemini-2.5-flash", &service.ChatOptions{
		Thinking: &service.ThinkingConfig{Type: "disabled"},
	})
	if cfg == nil || cfg.ThinkingBudget == nil || *cfg.ThinkingBudget != 0 {
		t.Fatalf("disabled thinking lost: %+v", cfg)
	}
	if cfg.IncludeThoughts {
		t.Error("includeThoughts should stay off when thinking is disabled")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"thinkingBudget":0`) {
		t.Fatalf("thinkingBudget:0 dropped from the wire body: %s", data)
	}

	// The same intent on Gemini 3 is the MINIMAL level.
	modern := geminiThinkingConfig("gemini-3-pro-preview", &service.ChatOptions{
		Thinking: &service.ThinkingConfig{Type: "disabled"},
	})
	if modern == nil || modern.ThinkingLevel != "MINIMAL" {
		t.Fatalf("gemini-3 disabled thinking = %+v, want MINIMAL", modern)
	}
}

// An explicit Thinking block with no budget means "provider default", which
// for Gemini 2.5 is the dynamic budget rather than thinking switched off.
func TestThinkingEnabledWithoutBudgetIsDynamic(t *testing.T) {
	cfg := geminiThinkingConfig("gemini-2.5-pro", &service.ChatOptions{
		Thinking: &service.ThinkingConfig{Type: "enabled"},
	})
	if cfg == nil || cfg.ThinkingBudget == nil || *cfg.ThinkingBudget != thinkingBudgetDynamic {
		t.Fatalf("enabled-without-budget = %+v, want dynamic budget", cfg)
	}
	modern := geminiThinkingConfig("gemini-3-pro-preview", &service.ChatOptions{
		Thinking: &service.ThinkingConfig{Type: "enabled"},
	})
	if modern == nil || modern.ThinkingLevel != "" || modern.ThinkingBudget != nil {
		t.Fatalf("gemini-3 dynamic thinking should keep the model default: %+v", modern)
	}
}

// Requests that never asked for thinking must be byte-identical to before:
// no thinkingConfig, and no generationConfig created just to hold it.
func TestNoThinkingOptionLeavesGenerationConfigUnset(t *testing.T) {
	if cfg := geminiThinkingConfig("gemini-3-pro-preview", nil); cfg != nil {
		t.Fatalf("nil options produced %+v", cfg)
	}
	if cfg := geminiThinkingConfig("gemini-3-pro-preview", &service.ChatOptions{}); cfg != nil {
		t.Fatalf("empty options produced %+v", cfg)
	}

	p := &Provider{}
	body := p.buildRequest(context.Background(), "gemini-3-pro-preview",
		[]service.Message{{Role: "user", Content: "hi"}}, nil, &service.ChatOptions{})
	if body.GenerationConfig != nil {
		t.Fatalf("generationConfig invented for an optionless request: %+v", body.GenerationConfig)
	}
}

// An effort this adapter does not map must leave the model default rather
// than falling through to a zero budget (which would disable thinking).
func TestUnsupportedReasoningEffortLeavesDefault(t *testing.T) {
	if cfg := geminiThinkingConfig("gemini-2.5-pro", &service.ChatOptions{ReasoningEffort: "xhigh"}); cfg != nil {
		t.Fatalf("xhigh should not be mapped for gemini, got %+v", cfg)
	}
}
