package common

import "testing"

func TestRepairOpenAIToolPairs_NoOpWhenAllPaired(t *testing.T) {
	in := []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "x"}},
			},
		},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
	}
	out := RepairOpenAIToolPairs(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
}

func TestRepairOpenAIToolPairs_DropsOrphanToolMessage(t *testing.T) {
	// Tool message references a call_id that is not advertised by any
	// assistant — must be dropped.
	in := []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "tool", "tool_call_id": "call_orphan", "content": "leftover"},
		map[string]any{"role": "user", "content": "now what"},
	}
	out := RepairOpenAIToolPairs(in)
	if len(out) != 2 {
		t.Fatalf("expected orphan tool message dropped, got %d", len(out))
	}
}

func TestRepairOpenAIToolPairs_StripsOrphanToolCallFromAssistant(t *testing.T) {
	// Assistant has two tool_calls; only one has a tool result.
	// Orphan call must be stripped while the paired one stays.
	in := []any{
		map[string]any{
			"role":    "assistant",
			"content": "thinking",
			"tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "x"}},
				map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": "y"}},
			},
		},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
	}
	out := RepairOpenAIToolPairs(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	asst, ok := out[0].(map[string]any)
	if !ok {
		t.Fatalf("assistant not a map: %T", out[0])
	}
	tcs, ok := asst["tool_calls"].([]any)
	if !ok {
		t.Fatalf("expected tool_calls []any, got %T", asst["tool_calls"])
	}
	if len(tcs) != 1 {
		t.Fatalf("expected 1 surviving tool_call, got %d", len(tcs))
	}
	id, _ := tcs[0].(map[string]any)["id"].(string)
	if id != "call_1" {
		t.Fatalf("expected call_1 to survive, got %q", id)
	}
}

func TestRepairOpenAIToolPairs_DropsAssistantWhenAllToolCallsOrphan(t *testing.T) {
	// Assistant has only orphan tool_calls and no text content — drop.
	in := []any{
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{"id": "call_orphan", "type": "function", "function": map[string]any{"name": "x"}},
			},
		},
		map[string]any{"role": "user", "content": "next"},
	}
	out := RepairOpenAIToolPairs(in)
	if len(out) != 1 {
		t.Fatalf("expected orphan-only assistant dropped, got %d", len(out))
	}
}

func TestRepairOpenAIToolPairs_KeepsAssistantTextWhenAllToolCallsOrphan(t *testing.T) {
	// Assistant has orphan tool_calls but ALSO text — keep, with calls stripped.
	in := []any{
		map[string]any{
			"role":    "assistant",
			"content": "I tried to call a tool but...",
			"tool_calls": []any{
				map[string]any{"id": "call_orphan", "type": "function", "function": map[string]any{"name": "x"}},
			},
		},
	}
	out := RepairOpenAIToolPairs(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(out))
	}
	asst := out[0].(map[string]any)
	if _, has := asst["tool_calls"]; has {
		t.Fatalf("expected tool_calls removed entirely")
	}
	if asst["content"] != "I tried to call a tool but..." {
		t.Fatalf("text content lost: %v", asst["content"])
	}
}

func TestRepairOpenAIToolPairs_DoesNotMutateInput(t *testing.T) {
	asst := map[string]any{
		"role": "assistant",
		"tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "x"}},
			map[string]any{"id": "call_orphan", "type": "function", "function": map[string]any{"name": "y"}},
		},
	}
	in := []any{
		asst,
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
	}
	_ = RepairOpenAIToolPairs(in)
	// Original assistant map must still have both tool_calls.
	tcs := asst["tool_calls"].([]any)
	if len(tcs) != 2 {
		t.Fatalf("input was mutated: tool_calls len=%d", len(tcs))
	}
}

func TestRepairOpenAIToolPairs_EmptyInput(t *testing.T) {
	if got := RepairOpenAIToolPairs(nil); got != nil {
		t.Fatalf("nil in → nil out, got %v", got)
	}
	if got := RepairOpenAIToolPairs([]any{}); len(got) != 0 {
		t.Fatalf("empty → empty, got %d", len(got))
	}
}
