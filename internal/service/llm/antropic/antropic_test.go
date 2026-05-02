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

// TestBuildRequestBodyDropsAdjacencyOrphanToolUse guards against the
// regression where mergeConsecutiveMessages collapses two adjacent
// assistant messages — leaving an assistant tool_use block whose
// matching tool_result is no longer in the immediately next user
// message. Anthropic rejects this with
//
//	"tool_use ids were found without tool_result blocks immediately
//	 after... tool_use block must have a corresponding tool_result
//	 block in the next message"
//
// The fix runs loopgov.RepairToolPairs *after* the merge so adjacency
// orphans created by the merge are pruned before the request goes out.
//
// We exercise the static-API-key path (tokenSource = nil) because the
// OAuth-only wire repair (transformAnthropicSystem) does NOT run here
// — proving the post-merge in-memory pass is what catches the bug.
func TestBuildRequestBodyDropsAdjacencyOrphanToolUse(t *testing.T) {
	p := &Provider{
		MaxTokens: 1024,
	}

	// Conversation shape: user → assistant(tool_use) → assistant(text)
	// → user(tool_result). After mergeConsecutiveMessages collapses
	// the two adjacent assistants, the merged assistant message
	// contains a tool_use whose matching tool_result is in the NEXT
	// message — that part looks fine globally. But the ordering of
	// the merge can interleave a non-tool_use block between the
	// tool_use and the user tool_result. More importantly, this test
	// constructs the case that mimics the production failure: the
	// loop governor dropped a user tool_result that used to sit
	// between the two assistants.
	msgs := []service.Message{
		{Role: "user", Content: "do things"},
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "tool_use", ID: "call_dropped", Name: "x", Input: map[string]any{}},
			},
		},
		// A second adjacent assistant message — triggers the merge
		// path. The matching tool_result for call_dropped never
		// arrives (was dropped by upstream windowing).
		{Role: "assistant", Content: "I gave up"},
	}

	body := p.buildRequestBody("claude-3-5-sonnet-latest", msgs, nil, nil)

	// Verify the outgoing payload contains NO tool_use blocks at all
	// (the only one was an adjacency orphan and must be dropped).
	bodyJSON, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytesContainsString(bodyJSON, `"type":"tool_use"`) {
		t.Fatalf("orphan tool_use survived: %s", string(bodyJSON))
	}
}

// TestBuildRequestBodyDropsTrailingToolUse covers the simplest
// failure mode: the LAST message in the conversation is an assistant
// tool_use with no following user tool_result. Anthropic rejects
// this with the same "tool_use without tool_result" error.
func TestBuildRequestBodyDropsTrailingToolUse(t *testing.T) {
	p := &Provider{MaxTokens: 1024}

	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "text", Text: "let me try..."},
				{Type: "tool_use", ID: "call_dangling", Name: "x", Input: map[string]any{}},
			},
		},
	}

	body := p.buildRequestBody("claude-3-5-sonnet-latest", msgs, nil, nil)

	bodyJSON, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytesContainsString(bodyJSON, `"type":"tool_use"`) {
		t.Fatalf("trailing orphan tool_use survived: %s", string(bodyJSON))
	}
	// The text block should still be there.
	if !bytesContainsString(bodyJSON, `let me try`) {
		t.Fatalf("expected text block to survive: %s", string(bodyJSON))
	}
}

// bytesContainsString is a small helper to keep the test asserts
// readable without pulling in another dep.
func bytesContainsString(b []byte, sub string) bool {
	return len(b) >= len(sub) && stringIndex(string(b), sub) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
