package antropic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// billingSalt is the secret salt Anthropic uses to bind the cc_version
// suffix to the upstream billing pipeline. Reverse-engineered from the
// Claude Code CLI; mirrored verbatim by the opencode-claude-auth
// plugin (src/signing.ts). Without the correct suffix the request is
// classed as "external (no billing)" and Anthropic rate-limits the
// caller far more aggressively than legitimate Claude Code traffic.
const billingSalt = "59cf53e54c78"

// extractFirstUserMessageText returns the text of the first user
// message's first text block. Mirrors Claude Code's K19() function and
// the plugin's extractFirstUserMessageText. Returns "" when there is
// no user message or no text content — the rest of the billing header
// math then operates on an empty string, which is the correct degraded
// behaviour (the upstream still accepts the request but with a slightly
// less specific cch/suffix, matching the plugin).
func extractFirstUserMessageText(messages []map[string]any) string {
	for _, m := range messages {
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			return c
		case []any:
			for _, b := range c {
				blk, ok := b.(map[string]any)
				if !ok {
					continue
				}
				if blk["type"] == "text" {
					if t, ok := blk["text"].(string); ok && t != "" {
						return t
					}
				}
			}
		}
	}
	return ""
}

// computeCch is the first 5 hex characters of SHA-256(messageText).
func computeCch(messageText string) string {
	sum := sha256.Sum256([]byte(messageText))
	return hex.EncodeToString(sum[:])[:5]
}

// computeVersionSuffix returns the 3-char version suffix that goes after
// the CLI version in the cc_version field. Sampling: characters at
// indices 4, 7, 20 from the message text (padded with '0' when the
// message is shorter), concatenated with the billing salt and CLI
// version, then SHA-256 → first 3 hex chars.
//
// Algorithm and constants mirrored verbatim from the upstream Claude
// Code CLI (via opencode-claude-auth's src/signing.ts:computeVersionSuffix).
// Diverging here will cause Anthropic to reject the request with a
// 401 or down-throttle it as untrusted traffic.
func computeVersionSuffix(messageText, version string) string {
	indices := []int{4, 7, 20}
	sampled := make([]byte, 0, len(indices))
	for _, i := range indices {
		if i < len(messageText) {
			sampled = append(sampled, messageText[i])
		} else {
			sampled = append(sampled, '0')
		}
	}
	input := billingSalt + string(sampled) + version
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:3]
}

// buildBillingHeaderValue produces the literal text that goes into the
// system[0] block of OAuth-billed Anthropic requests. Format matches
// the upstream Claude Code CLI:
//
//	x-anthropic-billing-header: cc_version=<version>.<suffix>; cc_entrypoint=<entrypoint>; cch=<hash5>;
//
// IMPORTANT: this string lives inside the request body's `system` field
// as a text block, NOT as an HTTP header. The legacy code in this
// repo set it as an `X-Anthropic-Billing-Header` HTTP header, which
// Anthropic ignores — the body-side billing block is what actually
// gates the request through Claude Code's billing pipeline. That's
// the root cause of the OAuth rate-limit issue users were hitting:
// without this body-side block, requests were classed as "external"
// and throttled aggressively.
func buildBillingHeaderValue(
	messages []map[string]any,
	version, entrypoint string,
) string {
	text := extractFirstUserMessageText(messages)
	suffix := computeVersionSuffix(text, version)
	cch := computeCch(text)
	return fmt.Sprintf(
		"x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=%s; cch=%s;",
		version, suffix, entrypoint, cch,
	)
}
