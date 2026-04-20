package antropic

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// findToolUseBlocks walks a marshalled messages payload and returns every
// tool_use content block as a map.
func findToolUseBlocks(t *testing.T, messages []service.Message) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	var back []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	var out []map[string]any
	for _, m := range back {
		// Content can be a string or a []any
		var asSlice []map[string]any
		if err := json.Unmarshal(m.Content, &asSlice); err != nil {
			continue
		}
		for _, b := range asSlice {
			if b["type"] == "tool_use" {
				out = append(out, b)
			}
		}
	}
	return out
}

// TestToolUseBlocksAfterMergeKeepInput guards against the regression where
// two consecutive assistant messages with tool_use content blocks got merged
// via mergeConsecutiveMessages. The merge path stored raw service.ContentBlock
// structs into a []any; convertContent's []any branch only normalized
// map[string]any entries; and Go's json.Marshal dropped the empty
// Input map via omitempty — producing a wire payload whose tool_use
// block lacked "input", which MiniMax/Anthropic reject with
//
//	"invalid function arguments json string" (error 2013).
func TestToolUseBlocksAfterMergeKeepInput(t *testing.T) {
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "text", Text: "calling tool"},
				{Type: "tool_use", ID: "call_a", Name: "do_thing", Input: nil},
			},
		},
		// Adjacent second assistant message — triggers the merge path.
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "tool_use", ID: "call_b", Name: "other_thing", Input: map[string]any{}},
			},
		},
	}

	merged := mergeConsecutiveMessages(msgs)
	for i := range merged {
		merged[i].Content = convertContent(merged[i].Content)
	}

	blocks := findToolUseBlocks(t, merged)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %d (payload: %+v)", len(blocks), merged)
	}
	for _, b := range blocks {
		if _, has := b["input"]; !has {
			t.Errorf("tool_use block %v is missing required \"input\" field", b)
		}
	}
}

// TestToolUseBlocksNoMergeKeepInput covers the non-merge path where
// convertContent's []service.ContentBlock branch runs directly.
func TestToolUseBlocksNoMergeKeepInput(t *testing.T) {
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "tool_use", ID: "call_a", Name: "do_thing", Input: nil},
			},
		},
	}

	merged := mergeConsecutiveMessages(msgs)
	for i := range merged {
		merged[i].Content = convertContent(merged[i].Content)
	}

	blocks := findToolUseBlocks(t, merged)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 tool_use block, got %d", len(blocks))
	}
	if _, has := blocks[0]["input"]; !has {
		t.Errorf("tool_use block %v is missing required \"input\" field", blocks[0])
	}
}

// TestConvertContentHandlesRawAnySlice ensures a []any containing raw
// service.ContentBlock structs (e.g. produced by a future code path) is
// normalized so tool_use blocks still carry an "input" field.
func TestConvertContentHandlesRawAnySlice(t *testing.T) {
	content := []any{
		service.ContentBlock{Type: "tool_use", ID: "call_a", Name: "do_thing", Input: nil},
		map[string]any{"type": "tool_use", "id": "call_b", "name": "other"}, // missing input
	}

	got := convertContent(content)
	slice, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(slice) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(slice))
	}
	for i, b := range slice {
		m, ok := b.(map[string]any)
		if !ok {
			t.Fatalf("element %d: expected map[string]any, got %T", i, b)
		}
		if _, has := m["input"]; !has {
			t.Errorf("element %d: tool_use block missing \"input\": %+v", i, m)
		}
	}
}
