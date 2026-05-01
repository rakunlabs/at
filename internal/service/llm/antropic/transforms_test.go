package antropic

import (
	"strings"
	"testing"
)

// TestPrefixToolName_PascalCase pins the rule that the first char
// after "mcp_" must be uppercase. Anthropic's OAuth billing validator
// flags lowercase tool names (e.g. "mcp_bash") as non-Claude-Code
// traffic when multi-tool requests are present, so this is the most
// important rename rule.
func TestPrefixToolName_PascalCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"bash", "mcp_Bash"},
		{"Bash", "mcp_Bash"},
		{"read", "mcp_Read"},
		{"task_create", "mcp_Task_create"}, // only the first letter is forced
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := prefixToolName(tt.in); got != tt.want {
				t.Errorf("prefixToolName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestUnprefixToolName_RoundTrip confirms the inverse function lower-cases
// the first char and strips the prefix. Used on inbound responses so
// the rest of AT sees the original tool names.
func TestUnprefixToolName_RoundTrip(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"mcp_Bash", "bash"},
		{"mcp_Read", "read"},
		{"mcp_Task_create", "task_create"},
		{"plain_no_prefix", "plain_no_prefix"}, // pass-through
		{"mcp_", ""},                           // edge: prefix-only
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := unprefixToolName(tt.in); got != tt.want {
				t.Errorf("unprefixToolName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestStripToolPrefixInJSON_ReplacesAllOccurrences confirms the regex
// catches both spaced-colon and tight-colon shapes in JSON-on-the-wire,
// because Anthropic's SSE stream is not normalised.
func TestStripToolPrefixInJSON_ReplacesAllOccurrences(t *testing.T) {
	in := `{"name":"mcp_Bash","other":1} ... {"name" : "mcp_Read"}`
	got := stripToolPrefixInJSON(in)
	if strings.Contains(got, "mcp_") {
		t.Errorf("expected mcp_ prefix stripped; got: %s", got)
	}
	if !strings.Contains(got, `"bash"`) || !strings.Contains(got, `"read"`) {
		t.Errorf("expected lowercase tool names in output; got: %s", got)
	}
}

// TestTransformAnthropicSystem_InjectsBillingHeader confirms that the
// billing text block lands at system[0] (the position Anthropic's
// validator looks at) and that its shape matches the canonical
// header. This is the core fix for the OAuth-rate-limit issue.
func TestTransformAnthropicSystem_InjectsBillingHeader(t *testing.T) {
	body := map[string]any{
		"system":   "You are Claude Code, Anthropic's official CLI for Claude.",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	transformAnthropicSystem(body, "2.1.112", "sdk-cli")

	sys, ok := body["system"].([]map[string]any)
	if !ok || len(sys) == 0 {
		t.Fatalf("system is not a non-empty []map after transform: %T %v", body["system"], body["system"])
	}
	first := sys[0]
	text, _ := first["text"].(string)
	if !strings.HasPrefix(text, "x-anthropic-billing-header:") {
		t.Errorf("system[0] is not the billing header; got %q", text)
	}
	if !strings.Contains(text, "cc_version=2.1.112.") {
		t.Errorf("billing header missing cc_version=2.1.112: %q", text)
	}
	if !strings.Contains(text, "cc_entrypoint=sdk-cli;") {
		t.Errorf("billing header missing cc_entrypoint=sdk-cli: %q", text)
	}
}

// TestTransformAnthropicSystem_SplitsIdentityPrefix confirms that when
// the identity string is glued to other text (e.g. "...for Claude.\nAdditional system content"),
// the transform splits it into two system entries. Anthropic's OAuth
// validator requires the identity string to be a STANDALONE entry.
func TestTransformAnthropicSystem_SplitsIdentityPrefix(t *testing.T) {
	body := map[string]any{
		"system":   claudeCodeIdentity + "\nYou are also a helpful coding assistant.",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	transformAnthropicSystem(body, "2.1.112", "sdk-cli")

	sys, _ := body["system"].([]map[string]any)
	// After step 3, third-party content moves to the user message,
	// so we expect only the billing entry + identity entry left.
	if len(sys) != 2 {
		t.Fatalf("expected 2 system entries (billing + identity), got %d: %v", len(sys), sys)
	}
	billingText, _ := sys[0]["text"].(string)
	if !strings.HasPrefix(billingText, "x-anthropic-billing-header:") {
		t.Errorf("system[0] should be billing header; got %q", billingText)
	}
	identityText, _ := sys[1]["text"].(string)
	if identityText != claudeCodeIdentity {
		t.Errorf("system[1] should be ONLY the identity string; got %q", identityText)
	}
}

// TestTransformAnthropicSystem_RelocatesThirdPartyContent confirms the
// third-party system content moves to the first user message (step 3).
// This is what bypasses Anthropic's "out of extra usage" rejection on
// OpenCode-style multi-system setups.
func TestTransformAnthropicSystem_RelocatesThirdPartyContent(t *testing.T) {
	body := map[string]any{
		"system":   claudeCodeIdentity + "\nThird-party agent context goes here.",
		"messages": []any{map[string]any{"role": "user", "content": "user query"}},
	}
	transformAnthropicSystem(body, "2.1.112", "sdk-cli")

	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("messages array is empty after transform")
	}
	first, _ := msgs[0].(map[string]any)
	c, _ := first["content"].(string)
	if !strings.HasPrefix(c, "Third-party agent context goes here.") {
		t.Errorf("first user message should start with relocated system text; got %q", c)
	}
	if !strings.HasSuffix(c, "user query") {
		t.Errorf("first user message should preserve original content suffix; got %q", c)
	}
}

// TestTransformAnthropicSystem_RenamesTools confirms tool definitions
// are PascalCased + mcp_ prefixed.
func TestTransformAnthropicSystem_RenamesTools(t *testing.T) {
	body := map[string]any{
		"system":   claudeCodeIdentity,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{
			map[string]any{"name": "bash", "description": "shell"},
			map[string]any{"name": "read", "description": "read"},
		},
	}
	transformAnthropicSystem(body, "2.1.112", "sdk-cli")

	tools, _ := body["tools"].([]any)
	wantNames := []string{"mcp_Bash", "mcp_Read"}
	for i, w := range wantNames {
		tm := tools[i].(map[string]any)
		if tm["name"] != w {
			t.Errorf("tool[%d].name = %q, want %q", i, tm["name"], w)
		}
	}
}

// TestRepairToolPairs_DropsOrphanedUse confirms a tool_use without a
// matching tool_result is filtered out — Anthropic rejects half-paired
// requests with a 400.
func TestRepairToolPairs_DropsOrphanedUse(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "use_1", "name": "bash"},
		}},
		// No matching tool_result → use_1 is orphaned.
	}
	out := repairToolPairsAny(msgs)
	if len(out) != 0 {
		t.Errorf("expected the orphan-only message to be dropped; got %d messages: %v", len(out), out)
	}
}

// TestRepairToolPairs_KeepsPairedBlocks confirms paired tool_use /
// tool_result blocks survive the cleanup unchanged.
func TestRepairToolPairs_KeepsPairedBlocks(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "use_1", "name": "bash"},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "use_1", "content": "ok"},
		}},
	}
	out := repairToolPairsAny(msgs)
	if len(out) != 2 {
		t.Errorf("expected 2 messages preserved; got %d: %v", len(out), out)
	}
}

// TestRepairToolPairs_DropsOrphanedResult confirms a tool_result whose
// tool_use_id has no matching tool_use is dropped.
func TestRepairToolPairs_DropsOrphanedResult(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "use_phantom", "content": "?"},
			map[string]any{"type": "text", "text": "real text"},
		}},
	}
	out := repairToolPairsAny(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message after dropping orphaned result; got %d", len(out))
	}
	first := out[0].(map[string]any)
	blocks := first["content"].([]any)
	// Only the text block should remain.
	if len(blocks) != 1 {
		t.Errorf("expected 1 block remaining (the text); got %d", len(blocks))
	}
}

// TestNormalizeSystemToArray_AcceptsAllShapes covers the four input
// forms we see in this codebase: nil, string, []map, []any.
func TestNormalizeSystemToArray_AcceptsAllShapes(t *testing.T) {
	if got := normalizeSystemToArray(nil); len(got) != 0 {
		t.Errorf("nil input should produce empty slice; got %v", got)
	}
	if got := normalizeSystemToArray(""); len(got) != 0 {
		t.Errorf("empty string should produce empty slice; got %v", got)
	}
	got := normalizeSystemToArray("hello")
	if len(got) != 1 || got[0]["text"] != "hello" {
		t.Errorf("string input should wrap as text block; got %v", got)
	}
	got = normalizeSystemToArray([]any{
		map[string]any{"type": "text", "text": "a"},
		"b", // bare string
	})
	if len(got) != 2 || got[0]["text"] != "a" || got[1]["text"] != "b" {
		t.Errorf("[]any input should normalise; got %v", got)
	}
}
