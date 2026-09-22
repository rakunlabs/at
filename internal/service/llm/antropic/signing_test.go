package antropic

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestComputeVersionSuffix_PadsShortMessages verifies the '0' padding
// when the message is shorter than the highest sampled index (20).
// This was a subtle bug in early plugin versions and is the most
// likely place a port can drift, so we pin it explicitly.
func TestComputeVersionSuffix_PadsShortMessages(t *testing.T) {
	short := "hi" // shorter than index 4 → all three sample slots become '0'
	got := computeVersionSuffix(short, "2.1.112")

	// Recompute manually using the documented algorithm.
	sampled := []byte{'0', '0', '0'}
	input := billingSalt + string(sampled) + "2.1.112"
	sum := sha256.Sum256([]byte(input))
	want := hex.EncodeToString(sum[:])[:3]

	if got != want {
		t.Errorf("computeVersionSuffix(short, ...) = %q, want %q", got, want)
	}
	if len(got) != 3 {
		t.Errorf("suffix length = %d, want 3", len(got))
	}
}

// TestComputeVersionSuffix_SamplesCorrectIndices confirms we pick chars
// at positions 4, 7, 20 — not 0,4,7 or anything else. We construct an
// input where each sampled char is unique so a wrong index produces a
// detectably different hash.
func TestComputeVersionSuffix_SamplesCorrectIndices(t *testing.T) {
	// 21-char string: a b c d E f g H i j k l m n o p q r s t U
	//                 0 1 2 3 4 5 6 7 8 9 ...                   20
	msg := "abcdEfgHijklmnopqrstU"
	got := computeVersionSuffix(msg, "v1")

	// Manual computation using the documented sample positions.
	sampled := string([]byte{msg[4], msg[7], msg[20]}) // "EHU"
	input := billingSalt + sampled + "v1"
	sum := sha256.Sum256([]byte(input))
	want := hex.EncodeToString(sum[:])[:3]

	if got != want {
		t.Errorf("suffix sampling drift: got %q, want %q (sampled=%q)", got, want, sampled)
	}
}

// TestExtractFirstUserMessageText_StringContent covers the simple
// case where content is a plain string.
func TestExtractFirstUserMessageText_StringContent(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "system prompt"},
		{"role": "user", "content": "actual first user message"},
		{"role": "user", "content": "second user message"},
	}
	got := extractFirstUserMessageText(msgs)
	if got != "actual first user message" {
		t.Errorf("got %q, want first user message", got)
	}
}

// TestExtractFirstUserMessageText_ArrayContent covers the content
// blocks form (the canonical Anthropic shape).
func TestExtractFirstUserMessageText_ArrayContent(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{}},
			map[string]any{"type": "text", "text": "hello there"},
			map[string]any{"type": "text", "text": "ignored second block"},
		}},
	}
	got := extractFirstUserMessageText(msgs)
	if got != "hello there" {
		t.Errorf("got %q, want first text block", got)
	}
}

// TestExtractFirstUserMessageText_NoUserReturnsEmpty matches the plugin's
// behaviour when the messages array has no user role at all (rare but
// possible for system-only setups). Returns "" and downstream functions
// degrade gracefully.
func TestExtractFirstUserMessageText_NoUserReturnsEmpty(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "system only"},
		{"role": "assistant", "content": "no user yet"},
	}
	got := extractFirstUserMessageText(msgs)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// TestBuildBillingHeaderValue_Shape confirms the formatted billing
// string matches the upstream layout exactly. Anthropic's billing
// validator parses this string with a strict regex; any deviation in
// spacing or punctuation kicks the request to "external" rate limits.
func TestBuildBillingHeaderValue_Shape(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
	}
	got := buildBillingHeaderValue(msgs, "2.1.112", "sdk-cli")

	// Recompute the deterministic suffix. Claude Code 2.1.280 sends a
	// fixed cch value on direct first-party requests.
	suffix := computeVersionSuffix("hi", "2.1.112")
	want := "x-anthropic-billing-header: cc_version=2.1.112." + suffix +
		"; cc_entrypoint=sdk-cli; cch=00000;"

	if got != want {
		t.Errorf("billing header mismatch:\n got  %q\n want %q", got, want)
	}
}
