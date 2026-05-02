package loopgov

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// helper: quickly build an assistant message with one tool_use block.
func asstToolUse(id, name string) service.Message {
	return service.Message{
		Role: "assistant",
		Content: []service.ContentBlock{
			{Type: "tool_use", ID: id, Name: name, Input: map[string]any{}},
		},
	}
}

// helper: quickly build a user message with one tool_result block.
func userToolResult(id, content string) service.Message {
	return service.Message{
		Role: "user",
		Content: []service.ContentBlock{
			{Type: "tool_result", ToolUseID: id, Content: content},
		},
	}
}

func TestRepairToolPairs_NoOpWhenAllPaired(t *testing.T) {
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		asstToolUse("call_1", "tool_a"),
		userToolResult("call_1", "ok"),
		{Role: "assistant", Content: "done"},
	}
	got := RepairToolPairs(msgs)
	if len(got) != len(msgs) {
		t.Fatalf("expected pass-through, got %d want %d", len(got), len(msgs))
	}
}

func TestRepairToolPairs_DropsOrphanToolUse(t *testing.T) {
	// Assistant emits two tool_use blocks; only one has a result.
	// The orphan tool_use must be dropped; the paired one stays.
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "text", Text: "thinking"},
				{Type: "tool_use", ID: "call_1", Name: "tool_a", Input: map[string]any{}},
				{Type: "tool_use", ID: "call_2", Name: "tool_b", Input: map[string]any{}},
			},
		},
		userToolResult("call_1", "ok"),
	}
	got := RepairToolPairs(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	asst := got[1]
	blocks, ok := asst.Content.([]service.ContentBlock)
	if !ok {
		t.Fatalf("assistant content is not []ContentBlock")
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && b.ID == "call_2" {
			t.Fatalf("orphan tool_use (call_2) was not dropped")
		}
	}
}

func TestRepairToolPairs_DropsOrphanToolResult(t *testing.T) {
	// User has a tool_result whose tool_use was dropped (e.g. by
	// upstream windowing). The orphan tool_result must be dropped.
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		userToolResult("call_missing", "leftover"),
		{Role: "user", Content: "now what"},
	}
	got := RepairToolPairs(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (orphan tool_result dropped), got %d", len(got))
	}
}

func TestRepairToolPairs_DropsMessageWhenAllBlocksOrphan(t *testing.T) {
	// User message contains ONLY orphan tool_results — must be removed
	// entirely (otherwise providers reject the empty content block).
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
		userToolResult("call_orphan", "leftover"),
	}
	got := RepairToolPairs(msgs)
	if len(got) != 1 {
		t.Fatalf("expected message to collapse to 1, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hi" {
		t.Fatalf("wrong message survived: %+v", got[0])
	}
}

func TestRepairToolPairs_KeepsTextWhenSomeBlocksOrphan(t *testing.T) {
	// Assistant has text + orphan tool_use. Text survives, tool_use
	// drops, message remains.
	msgs := []service.Message{
		{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "text", Text: "I tried..."},
				{Type: "tool_use", ID: "call_orphan", Name: "x", Input: map[string]any{}},
			},
		},
	}
	got := RepairToolPairs(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	blocks := got[0].Content.([]service.ContentBlock)
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("expected only text block, got %+v", blocks)
	}
}

func TestRepairToolPairs_StringContentPassThrough(t *testing.T) {
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	got := RepairToolPairs(msgs)
	if len(got) != 3 {
		t.Fatalf("expected pass-through")
	}
}

func TestRepairToolPairs_ReturnsInputWhenNoBlocks(t *testing.T) {
	msgs := []service.Message{
		{Role: "user", Content: "hi"},
	}
	got := RepairToolPairs(msgs)
	// Same backing slice when no orphans.
	if len(got) != 1 || got[0].Content != "hi" {
		t.Fatalf("unexpected output %+v", got)
	}
}

// Integration: simulate the real failure mode — windowing slices the
// conversation between an assistant tool_use and its tool_result. The
// governor must not emit an orphan.
func TestLimit_RepairsOrphanAfterWindowing(t *testing.T) {
	// Build a long conversation with several tool-call rounds. Use big
	// padding text so windowing definitely drops middle messages.
	pad := strings.Repeat("x", 4000)

	msgs := []service.Message{
		{Role: "system", Content: "sys"},
	}
	for i := 0; i < 6; i++ {
		// user query
		msgs = append(msgs, service.Message{Role: "user", Content: pad + " round " + string(rune('A'+i))})
		// assistant tool_use
		msgs = append(msgs, service.Message{
			Role: "assistant",
			Content: []service.ContentBlock{
				{Type: "text", Text: pad},
				{Type: "tool_use", ID: "call_" + string(rune('A'+i)), Name: "x", Input: map[string]any{}},
			},
		})
		// user tool_result
		msgs = append(msgs, service.Message{
			Role: "user",
			Content: []service.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_" + string(rune('A'+i)), Content: pad},
			},
		})
		// assistant text reply
		msgs = append(msgs, service.Message{Role: "assistant", Content: pad})
	}

	g := New(Config{
		WindowTokens:  3000,
		SummaryTokens: 200,
	}, nil)

	got, err := g.Limit(context.Background(), "a", "t", msgs)
	if err != nil {
		t.Fatal(err)
	}

	// Verify pairing invariant in the kept slice: every tool_use ID
	// has a tool_result, and every tool_result ID has a tool_use.
	uses, results := collectIDs(got)
	for id := range uses {
		if _, ok := results[id]; !ok {
			t.Fatalf("orphan tool_use %q in windowed output: %d msgs", id, len(got))
		}
	}
	for id := range results {
		if _, ok := uses[id]; !ok {
			t.Fatalf("orphan tool_result %q in windowed output: %d msgs", id, len(got))
		}
	}
}

func collectIDs(msgs []service.Message) (uses, results map[string]struct{}) {
	uses = map[string]struct{}{}
	results = map[string]struct{}{}
	for _, m := range msgs {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				uses[b.ID] = struct{}{}
			case "tool_result":
				results[b.ToolUseID] = struct{}{}
			}
		}
	}
	return
}
