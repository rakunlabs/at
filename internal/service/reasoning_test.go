package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestValidateReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "HIGH", " high", "high ", "auto", "extreme"} {
		t.Run(effort, func(t *testing.T) {
			wantValid := effort == "" || slices.Contains(ReasoningEffortLevels, effort)
			if err := ValidateReasoningEffort(effort); (err == nil) != wantValid {
				t.Fatalf("ValidateReasoningEffort(%q) = %v", effort, err)
			}
		})
	}
}

func TestValidateProviderReasoningEffort(t *testing.T) {
	accepts := map[string][]string{
		"openai":        ReasoningEffortLevels,
		"azure":         ReasoningEffortLevels,
		"vertex":        ReasoningEffortLevels,
		"anthropic":     {"low", "medium", "high", "xhigh", "max"},
		"minimax":       {"low", "medium", "high"},
		"gemini":        {"minimal", "low", "medium", "high"},
		"vertex-gemini": {"minimal", "low", "medium", "high"},
	}
	for _, provider := range []string{"openai", "azure", "vertex", "anthropic", "gemini", "vertex-gemini", "minimax", "bedrock", "cohere", "unknown", "", "OpenAI"} {
		for _, effort := range append([]string{"", "HIGH", " high"}, ReasoningEffortLevels...) {
			t.Run(provider+"/"+effort, func(t *testing.T) {
				valid := ValidateReasoningEffort(effort) == nil
				wantValid := valid && (effort == "" || slices.Contains(accepts[provider], effort))
				err := ValidateProviderReasoningEffort(provider, effort)
				if (err == nil) != wantValid {
					t.Fatalf("ValidateProviderReasoningEffort = %v, want valid %v", err, wantValid)
				}
				if !valid && err.Error() != ValidateReasoningEffort(effort).Error() {
					t.Fatalf("syntax must be validated first: %v", err)
				}
			})
		}
	}
}

// Expected values are the per-model tables published by each provider:
// platform.claude.com effort + thinking-troubleshooting, ai.google.dev
// thinking levels and developers.openai.com model pages.
func TestDetectModelReasoningEfforts(t *testing.T) {
	all5 := []string{"low", "medium", "high", "xhigh", "max"}
	noXHigh := []string{"low", "medium", "high", "max"}
	budget := []string{"low", "medium", "high"}
	tests := []struct {
		provider, model string
		want            []string
		known           bool
	}{
		// Anthropic
		{"anthropic", "claude-opus-5-5", all5, true},
		{"anthropic", "claude-opus-5", all5, true},
		{"anthropic", "claude-opus-4-8", all5, true},
		{"anthropic", "claude-opus-4-7", all5, true},
		{"anthropic", "claude-opus-4-6", noXHigh, true},
		{"anthropic", "claude-sonnet-4-6", noXHigh, true},
		{"anthropic", "claude-sonnet-5-5", all5, true},
		{"anthropic", "claude-sonnet-5", all5, true},
		{"anthropic", "claude-fable-5-1", all5, true},
		{"anthropic", "claude-mythos-5", all5, true},
		{"anthropic", "claude-mythos-preview", noXHigh, true},
		{"anthropic", "claude-opus-4-5-20251101", budget, true},
		{"anthropic", "claude-sonnet-4-5-20250929", budget, true},
		{"anthropic", "claude-haiku-4-5", budget, true},
		{"anthropic", "claude-sonnet-4-20250514", budget, true},
		{"anthropic", "claude-3-7-sonnet-latest", budget, true},
		{"anthropic", "claude-3-5-haiku-latest", []string{}, true},
		{"anthropic", "claude-3-5-sonnet-20241022", []string{}, true},
		{"anthropic", "MiniMax-M2", nil, false},
		// Gemini
		{"gemini", "gemini-3-pro-preview", []string{"low", "high"}, true},
		{"gemini", "gemini-3.1-pro-preview", budget, true},
		{"gemini", "gemini-3-flash-preview", []string{"minimal", "low", "medium", "high"}, true},
		{"gemini", "gemini-3.8-flash", budget, true},
		{"gemini", "gemini-3.1-flash-lite-image", []string{"minimal", "high"}, true},
		{"vertex-gemini", "gemini-2.5-pro", budget, true},
		{"gemini", "gemini-2.0-flash", []string{}, true},
		{"gemini", "gemma-3-27b-it", nil, false},
		{"vertex", "google/gemini-3-flash-preview", budget, true},
		// OpenAI
		{"openai", "gpt-5", []string{"minimal", "low", "medium", "high"}, true},
		{"openai", "gpt-5.1", []string{"none", "low", "medium", "high"}, true},
		{"openai", "gpt-5.2", []string{"none", "low", "medium", "high", "xhigh"}, true},
		{"openai", "gpt-5.5", []string{"none", "low", "medium", "high", "xhigh"}, true},
		{"openai", "gpt-5.6-luna", []string{"none", "low", "medium", "high", "xhigh", "max"}, true},
		{"openai", "gpt-6-astra", all5, true},
		{"openai", "gpt-6-luna", []string{"none", "low", "medium", "high", "xhigh", "max"}, true},
		{"openai", "gpt-6.1-sol", all5, true},
		{"azure", "o4-mini", budget, true},
		{"openai", "o3", budget, true},
		{"openai", "gpt-4o", []string{}, true},
		{"openai", "gpt-5-chat-latest", []string{}, true},
		{"openai", "gpt-5.1-codex", nil, false},
		{"openai", "llama-3.3-70b-versatile", nil, false},
		{"bedrock", "claude-opus-5", nil, false},
		{"anthropic", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			got, known := DetectModelReasoningEfforts(tt.provider, tt.model)
			if known != tt.known || !slices.Equal(got, tt.want) {
				t.Fatalf("got %v (known %v), want %v (known %v)", got, known, tt.want, tt.known)
			}
			for _, effort := range got {
				if err := ValidateProviderReasoningEffort(tt.provider, effort); err != nil {
					t.Fatalf("detected effort the adapter cannot express: %v", err)
				}
			}
		})
	}
}

func TestModelReasoningEffortsOverride(t *testing.T) {
	// An override wins, is sorted, and cannot smuggle in an effort the
	// adapter has no mapping for.
	got, known := ModelReasoningEfforts("anthropic", "my-proxy-model", []string{"high", "low", "minimal"})
	if !known || !slices.Equal(got, []string{"low", "high"}) {
		t.Fatalf("override = %v, %v", got, known)
	}
	// An explicit empty override declares the model non-reasoning.
	got, known = ModelReasoningEfforts("openai", "gpt-6-astra", []string{})
	if !known || len(got) != 0 {
		t.Fatalf("empty override = %v, %v", got, known)
	}
	// nil keeps detection.
	got, _ = ModelReasoningEfforts("openai", "gpt-5", nil)
	if !slices.Equal(got, []string{"minimal", "low", "medium", "high"}) {
		t.Fatalf("detection = %v", got)
	}
}

func TestValidateModelReasoningEffortOverride(t *testing.T) {
	tests := []struct {
		provider string
		efforts  []string
		ok       bool
	}{
		{"anthropic", []string{"low", "max"}, true},
		{"anthropic", []string{}, true},
		{"anthropic", []string{"none"}, false},
		{"gemini", []string{"xhigh"}, false},
		{"openai", []string{"low", "low"}, false},
		{"openai", []string{""}, false},
		{"bedrock", []string{"low"}, false},
	}
	for _, tt := range tests {
		if err := ValidateModelReasoningEffortOverride(tt.provider, tt.efforts); (err == nil) != tt.ok {
			t.Errorf("%s %v: %v", tt.provider, tt.efforts, err)
		}
	}
}

func TestAnthropicThinkingMode(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-4-7":            "adaptive",
		"claude-opus-4-6":            "adaptive",
		"claude-sonnet-4.6":          "adaptive",
		"claude-opus-4-5-20251101":   "extended_effort",
		"claude-sonnet-4-5":          "extended",
		"claude-sonnet-4-20250514":   "extended",
		"claude-opus-4-1-20250805":   "extended",
		"claude-3-7-sonnet-20250219": "extended",
		"claude-3-5-haiku-latest":    "none",
		"anthropic/claude-opus-5":    "adaptive",
		"MiniMax-M2":                 "",
	} {
		if got := AnthropicThinkingMode(model); got != want {
			t.Errorf("%s = %q, want %q", model, got, want)
		}
	}
}

func TestAgentConfigReasoningEffortJSON(t *testing.T) {
	for _, effort := range append([]string{""}, ReasoningEffortLevels...) {
		t.Run(effort, func(t *testing.T) {
			config := AgentConfig{Provider: "openai", Model: "unchanged-model", ReasoningEffort: effort}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "reasoning_effort") != (effort != "") {
				t.Fatalf("unexpected omission behavior: %s", data)
			}
			var restored AgentConfig
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.ReasoningEffort != effort || restored.Model != config.Model {
				t.Fatalf("round trip changed config: %+v", restored)
			}
		})
	}
}
