package antropic

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestOAuthToolNamesAreReversibleAndDoNotMutateHistory(t *testing.T) {
	names := []string{"Read", "read", "mcp_Read", "Bash"}
	var tools []service.Tool
	for _, name := range names {
		tools = append(tools, service.Tool{Name: name, InputSchema: map[string]any{"type": "object"}})
	}
	p := &Provider{MaxTokens: 1024, tokenSource: NewStaticTokenSource("token")}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			msgs := []service.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: []any{map[string]any{"type": "tool_use", "name": name, "id": "call_1", "input": map[string]any{}}}},
				{Role: "user", Content: []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"}}},
			}
			before, _ := json.Marshal(msgs)
			body := p.buildRequestBody("claude-opus-5", msgs, tools, &service.ChatOptions{ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": name}}})
			choice := body["tool_choice"].(map[string]any)["name"].(string)
			if got := restoreOAuthToolName(choice, tools); got != name {
				t.Fatalf("roundtrip %q -> %q -> %q", name, choice, got)
			}
			seen := map[string]bool{}
			for _, raw := range body["tools"].([]any) {
				wire := raw.(map[string]any)["name"].(string)
				if seen[wire] {
					t.Fatalf("wire name collision: %q", wire)
				}
				seen[wire] = true
			}
			if !seen[choice] {
				t.Fatalf("forced tool not defined: %q", choice)
			}
			history := body["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if history["name"] != choice {
				t.Fatalf("tool history mismatch: %+v", history)
			}
			after, _ := json.Marshal(msgs)
			if string(before) != string(after) {
				t.Fatal("caller history mutated by cache/OAuth transform")
			}
		})
	}
}
