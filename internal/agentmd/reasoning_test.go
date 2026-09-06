package agentmd

import (
	"strings"
	"testing"
)

func TestReasoningEffortRoundTrip(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			a := &AgentMD{Name: "agent", Provider: "provider-key", Model: "original-model", ReasoningEffort: effort, SystemPrompt: "Prompt\n"}
			data, err := Generate(a)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "reasoning_effort:") != (effort != "") {
				t.Fatalf("unexpected field omission: %s", data)
			}
			parsed, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.ReasoningEffort != effort || parsed.Model != a.Model || parsed.SystemPrompt != a.SystemPrompt {
				t.Fatalf("round trip changed agent: %+v", parsed)
			}
		})
	}
	parsed, err := Parse([]byte("---\nname: legacy\nprovider: openai\n---\nPrompt"))
	if err != nil || parsed.ReasoningEffort != "" {
		t.Fatalf("legacy parse = %+v, %v", parsed, err)
	}
}
