package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "HIGH", " high", "high ", "none", "auto", "minimal", "max"} {
		t.Run(effort, func(t *testing.T) {
			wantValid := effort == "" || effort == "low" || effort == "medium" || effort == "high" || effort == "xhigh"
			if err := ValidateReasoningEffort(effort); (err == nil) != wantValid {
				t.Fatalf("ValidateReasoningEffort(%q) = %v", effort, err)
			}
		})
	}
}

func TestValidateProviderReasoningEffort(t *testing.T) {
	for _, provider := range []string{"openai", "azure", "vertex", "anthropic", "gemini", "vertex-gemini", "minimax", "bedrock", "cohere", "unknown", "", "OpenAI"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "HIGH", " high", "none"} {
			t.Run(provider+"/"+effort, func(t *testing.T) {
				all := provider == "openai" || provider == "azure" || provider == "vertex"
				limited := provider == "anthropic" || provider == "gemini" || provider == "vertex-gemini" || provider == "minimax"
				valid := ValidateReasoningEffort(effort) == nil
				wantValid := valid && (effort == "" || all || (limited && effort != "xhigh"))
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

func TestAgentConfigReasoningEffortJSON(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh"} {
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
