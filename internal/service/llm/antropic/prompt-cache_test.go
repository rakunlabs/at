package antropic

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Exercise the full request transform with the typed content emitted by the
// gateway, rather than only string messages. Both Chat and ChatStream use it.
func TestBuildRequestBodyCachesToolConversation(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		name := "api-key"
		if oauth {
			name = "oauth"
		}
		t.Run(name, func(t *testing.T) {
			p := &Provider{MaxTokens: 1024}
			if oauth {
				p.tokenSource = NewStaticTokenSource("test-token")
			}
			messages := []service.Message{
				{Role: "system", Content: "stable instructions"},
				{Role: "user", Content: []service.ContentBlock{
					{Type: "text", Text: "inspect this image"},
					{Type: "image", Source: &service.MediaSource{Type: "url", URL: "https://example.com/image.png"}},
				}},
				{Role: "assistant", Content: []service.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: "read_file", Input: map[string]any{}},
				}},
				{Role: "user", Content: []service.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: "file contents"},
				}},
			}
			tools := []service.Tool{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}}
			body := p.buildRequestBody("claude-opus-5", messages, tools, nil)
			// Inspect actual JSON wire shape, independent of Go slice types.
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			msgs := wire["messages"].([]any)
			if len(msgs) != 3 {
				t.Fatalf("message count = %d, want 3: %s", len(msgs), data)
			}
			first := msgs[0].(map[string]any)["content"].([]any)
			foundText, foundImage := false, false
			for _, b := range first {
				block := b.(map[string]any)
				foundText = foundText || block["text"] == "inspect this image"
				foundImage = foundImage || block["type"] == "image"
			}
			if !foundText || !foundImage {
				t.Errorf("first user content lost during transform: %s", data)
			}
			tool := wire["tools"].([]any)[0].(map[string]any)
			call := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if call["name"] != tool["name"] {
				t.Errorf("history tool name %v differs from definition %v", call["name"], tool["name"])
			}
			result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
			if result["tool_use_id"] != call["id"] || result["content"] != "file contents" {
				t.Errorf("tool result corrupted: %v", result)
			}
			mark, _ := result["cache_control"].(map[string]any)
			if mark["type"] != "ephemeral" {
				t.Errorf("tool conversation has no cache breakpoint: %s", data)
			}
		})
	}
}

func TestMarkMessageContentRepresentations(t *testing.T) {
	for _, name := range []string{"string", "any-blocks", "map-blocks"} {
		t.Run(name, func(t *testing.T) {
			var content any = "result"
			block := map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "result"}
			switch name {
			case "any-blocks":
				content = []any{block}
			case "map-blocks":
				content = []map[string]any{block}
			}
			mark := map[string]any{"type": "ephemeral", "ttl": "1h"}
			markMessageContent(content, mark, func(v any) { content = v })
			// Repeated installation must preserve an existing caller TTL.
			markMessageContent(content, map[string]any{"type": "ephemeral"}, func(v any) { content = v })
			blocks := contentToAnySlice(content)
			if len(blocks) != 1 {
				t.Fatalf("content = %#v, want one block", content)
			}
			got, _ := blocks[0].(map[string]any)["cache_control"].(map[string]any)
			if got["type"] != "ephemeral" || got["ttl"] != "1h" {
				t.Fatalf("cache marker = %#v, want ephemeral with preserved 1h TTL", got)
			}
		})
	}
}
