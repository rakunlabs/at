package service

import (
	"encoding/json"
	"testing"
)

// The widening from string to any must be invisible on the wire: existing rows
// and existing producers all carry strings.
func TestContentBlockStringMarshalsIdentically(t *testing.T) {
	block := ContentBlock{Type: "tool_result", ToolUseID: "call_1", Content: "plain result"}

	got, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"tool_result","tool_use_id":"call_1","content":"plain result"}`
	if string(got) != want {
		t.Fatalf("marshal = %s\nwant     %s", got, want)
	}
}

func TestContentBlockEmptyContentIsOmitted(t *testing.T) {
	got, err := json.Marshal(ContentBlock{Type: "text", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"type":"text","text":"hi"}` {
		t.Fatalf("marshal = %s", got)
	}
}

// Message history persisted before the widening must still load.
func TestContentBlockUnmarshalsLegacyStringContent(t *testing.T) {
	var block ContentBlock
	if err := json.Unmarshal([]byte(`{"type":"tool_result","tool_use_id":"call_1","content":"legacy"}`), &block); err != nil {
		t.Fatal(err)
	}
	if block.ContentText() != "legacy" {
		t.Fatalf("ContentText = %q want legacy", block.ContentText())
	}
	if _, structured := block.ContentBlocks(); structured {
		t.Fatal("a string result must not read as structured")
	}
	if !block.HasContent() {
		t.Fatal("HasContent should be true")
	}
}

func TestContentBlockStructuredContent(t *testing.T) {
	block := ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content: []ContentBlock{
			{Type: "text", Text: "screenshot attached"},
			{Type: "image", Source: &MediaSource{Type: "base64", MediaType: "image/png", Data: "aGk="}},
		},
	}

	blocks, structured := block.ContentBlocks()
	if !structured || len(blocks) != 2 {
		t.Fatalf("ContentBlocks = %v, %v", blocks, structured)
	}
	if !block.HasContent() {
		t.Fatal("HasContent should be true")
	}

	// Flattening keeps the text and marks what could not be represented, rather
	// than silently returning a shorter string.
	if got := block.ContentText(); got != "screenshot attached[image]" {
		t.Fatalf("ContentText = %q", got)
	}
}

func TestContentBlockContentAccessorsOnEmpty(t *testing.T) {
	var block ContentBlock

	if block.ContentText() != "" {
		t.Fatalf("ContentText = %q want empty", block.ContentText())
	}
	if _, structured := block.ContentBlocks(); structured {
		t.Fatal("an absent Content must not read as structured")
	}
	if block.HasContent() {
		t.Fatal("HasContent should be false")
	}
}

// Round-tripping through JSON yields []any, which the accessors must still
// handle — this is the shape a stored history takes on reload.
func TestContentBlockAccessorsAfterJSONRoundTrip(t *testing.T) {
	original := ContentBlock{
		Type:    "tool_result",
		Content: []ContentBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var reloaded ContentBlock
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}

	// encoding/json decodes an array into []any of map[string]any, so the typed
	// accessor correctly reports "not structured" rather than fabricating
	// blocks. Callers that need structure decode into the typed shape.
	if _, structured := reloaded.ContentBlocks(); structured {
		t.Fatal("a generic []any of maps must not masquerade as []ContentBlock")
	}
	if !reloaded.HasContent() {
		t.Fatal("HasContent should still be true after a round trip")
	}
}
