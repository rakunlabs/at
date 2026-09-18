package antropic

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// A string tool result must serialize exactly as it did before ContentBlock
// widened, so existing traffic is untouched.
func TestToolResultStringSerializationUnchanged(t *testing.T) {
	got := contentBlockToMap(service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content:   "exit code 0",
	})

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"content":"exit code 0","tool_use_id":"call_1","type":"tool_result"}`
	if string(raw) != want {
		t.Fatalf("marshal = %s\nwant     %s", raw, want)
	}
}

func TestToolResultEmptyContentOmitsField(t *testing.T) {
	got := contentBlockToMap(service.ContentBlock{Type: "tool_result", ToolUseID: "call_1"})
	if _, present := got["content"]; present {
		t.Fatalf("empty content must not emit the field: %+v", got)
	}
}

// A screenshot returned by a tool must reach Anthropic as a native image block.
func TestToolResultStructuredContentEmitsNativeBlocks(t *testing.T) {
	got := contentBlockToMap(service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content: []service.ContentBlock{
			{Type: "text", Text: "here it is"},
			{Type: "image", Source: &service.MediaSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
		},
	})

	parts, ok := got["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v want two native blocks", got["content"])
	}

	text, ok := parts[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "here it is" {
		t.Fatalf("text block = %#v", parts[0])
	}

	image, ok := parts[1].(map[string]any)
	if !ok || image["type"] != "image" {
		t.Fatalf("image block = %#v", parts[1])
	}
	source, ok := image["source"].(*service.MediaSource)
	if !ok || source.MediaType != "image/png" || source.Data != "aGVsbG8=" {
		t.Fatalf("image source = %#v", image["source"])
	}
}
