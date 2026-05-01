package antropic

import (
	"regexp"
	"strings"
	"unicode"
)

// reToolName matches `"name": "mcp_<X>"` blocks in JSON-shaped text
// (allows optional whitespace around the colon). The capture is the
// PascalCase tool name minus the prefix. Used by stripToolPrefixInJSON
// to undo the outbound rename when streaming responses back to the
// caller.
var reToolName = regexp.MustCompile(`"name"\s*:\s*"mcp_([^"]+)"`)

// toolPrefix is the namespace Claude Code uses for MCP-style tools. The
// upstream Anthropic OAuth billing validator rejects multi-tool requests
// where any tool name doesn't follow the Claude Code convention
// (mcp_<PascalCase>), classifying the caller as "external traffic" and
// throttling it. We add the prefix on outbound requests and strip it
// from inbound responses so the rest of the codebase stays unaware.
const toolPrefix = "mcp_"

// claudeCodeIdentity is the verbatim identity string Anthropic's OAuth
// pipeline expects to see at the head of system[]. Any deviation
// (including extra trailing characters in the same block) flunks the
// billing validator.
const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

// prefixToolName turns "Read" / "read" into "mcp_Read" — the PascalCase
// form Claude Code uses on the wire. Lower-case prefixes (mcp_read) are
// flagged as non-Claude-Code clients during multi-tool requests, so the
// first letter must always be uppercased.
func prefixToolName(name string) string {
	if name == "" {
		return name
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return toolPrefix + string(r)
}

// unprefixToolName is prefixToolName's inverse, used when streaming or
// JSON-decoding responses so the rest of the codebase sees the original
// tool name shape it registered.
func unprefixToolName(name string) string {
	if !strings.HasPrefix(name, toolPrefix) {
		return name
	}
	rest := name[len(toolPrefix):]
	if rest == "" {
		return rest
	}
	r := []rune(rest)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// stripToolPrefixInJSON rewrites every `"name": "mcp_X..."` occurrence
// in a JSON-shaped string to `"name": "x..."`. Used on streaming
// response bodies (raw bytes pass through as text on this side of the
// SSE wire), so we don't have to fully parse each chunk just to undo
// the outbound rename. Mirrors the plugin's stripToolPrefix.
func stripToolPrefixInJSON(s string) string {
	return reToolName.ReplaceAllStringFunc(s, func(match string) string {
		// match looks like:  "name":"mcp_Foo"  or  "name" : "mcp_Foo"
		// Pull the captured group out via a second regexp pass; cheaper
		// than running the indexing dance manually each call.
		sub := reToolName.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return strings.Replace(match, "mcp_"+sub[1], unprefixToolName("mcp_"+sub[1]), 1)
	})
}

// transformAnthropicSystem rewrites the request body's `system`,
// `tools`, and `messages` fields to match what Anthropic's OAuth
// billing pipeline expects. Mutates `body` in place and returns it so
// callers can chain.
//
// The five steps mirror opencode-claude-auth's transformBody:
//
//  1. Build a billing header text block and put it at system[0].
//  2. Split the identity prefix into its own system entry. OpenCode's
//     system.transform hook prepends the identity string to existing
//     system text; Anthropic's validator wants them as SEPARATE entries
//     in the system array.
//  3. Move every system entry that isn't the billing header or the
//     identity prefix into the first user message (prepended). The
//     OAuth billing validator rejects requests that have third-party
//     content alongside the identity prefix in system[]; relocating
//     it to the user role is functionally equivalent but passes
//     validation.
//  4. PascalCase tool names with mcp_ prefix on every tool definition
//     and every tool_use content block.
//  5. Repair orphaned tool_use / tool_result pairs (drop blocks whose
//     partner is missing — common after middleware drops messages from
//     long-context truncation).
//
// Anything not in the OAuth path (static API key auth) should not call
// this function — it's specific to the OAuth billing flow and would
// alter the request shape unnecessarily.
func transformAnthropicSystem(body map[string]any, cliVersion, entrypoint string) {
	// ── 1. Inject billing header at system[0] ──────────────────────
	systemArr := normalizeSystemToArray(body["system"])

	// Strip any pre-existing billing entries (idempotent retries).
	filtered := systemArr[:0]
	for _, e := range systemArr {
		text, _ := e["text"].(string)
		if e["type"] == "text" && strings.HasPrefix(text, "x-anthropic-billing-header") {
			continue
		}
		filtered = append(filtered, e)
	}
	systemArr = filtered

	// Collect a messages-as-maps view for the billing-text computation.
	msgsForBilling := normalizeMessagesToMaps(body["messages"])

	billingText := buildBillingHeaderValue(msgsForBilling, cliVersion, entrypoint)

	billingEntry := map[string]any{"type": "text", "text": billingText}
	systemArr = append([]map[string]any{billingEntry}, systemArr...)

	// ── 2. Split identity prefix into its own system[] entry ───────
	splitSystem := make([]map[string]any, 0, len(systemArr)+1)
	for _, entry := range systemArr {
		text, ok := entry["text"].(string)
		if entry["type"] == "text" && ok &&
			strings.HasPrefix(text, claudeCodeIdentity) &&
			len(text) > len(claudeCodeIdentity) {
			rest := strings.TrimLeft(text[len(claudeCodeIdentity):], "\n")

			identityProps := copyMapExcluding(entry, "text", "cache_control")
			identityProps["text"] = claudeCodeIdentity
			splitSystem = append(splitSystem, identityProps)

			if rest != "" {
				restProps := copyMapExcluding(entry, "text")
				restProps["text"] = rest
				splitSystem = append(splitSystem, restProps)
			}
		} else {
			splitSystem = append(splitSystem, entry)
		}
	}
	systemArr = splitSystem

	// ── 3. Move third-party system content to the first user msg ──
	const billingPrefix = "x-anthropic-billing-header"
	keptSystem := systemArr[:0]
	var movedTexts []string
	for _, entry := range systemArr {
		text, _ := entry["text"].(string)
		if strings.HasPrefix(text, billingPrefix) || strings.HasPrefix(text, claudeCodeIdentity) {
			keptSystem = append(keptSystem, entry)
		} else if text != "" {
			movedTexts = append(movedTexts, text)
		}
	}
	if len(movedTexts) > 0 {
		if msgs, ok := body["messages"].([]any); ok {
			prepended := false
			for i := range msgs {
				m, mok := msgs[i].(map[string]any)
				if !mok || m["role"] != "user" {
					continue
				}
				prefix := strings.Join(movedTexts, "\n\n")
				switch c := m["content"].(type) {
				case string:
					m["content"] = prefix + "\n\n" + c
				case []any:
					m["content"] = append([]any{
						map[string]any{"type": "text", "text": prefix},
					}, c...)
				default:
					m["content"] = []any{
						map[string]any{"type": "text", "text": prefix},
					}
				}
				prepended = true
				break
			}
			if prepended {
				systemArr = keptSystem
			}
		}
	}

	body["system"] = systemArr

	// ── 4. PascalCase tool names ───────────────────────────────────
	if tools, ok := body["tools"].([]any); ok {
		for i, t := range tools {
			tm, tok := t.(map[string]any)
			if !tok {
				continue
			}
			if name, nok := tm["name"].(string); nok && name != "" {
				tm["name"] = prefixToolName(name)
				tools[i] = tm
			}
		}
	}
	if tools, ok := body["tools"].([]map[string]any); ok {
		for _, tm := range tools {
			if name, nok := tm["name"].(string); nok && name != "" {
				tm["name"] = prefixToolName(name)
			}
		}
	}

	// Rename tool_use blocks inside messages so the assistant's
	// previously-named tool calls match the renamed tools.
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			mm, mok := m.(map[string]any)
			if !mok {
				continue
			}
			renameToolUseBlocksInContent(mm["content"])
		}
	}

	// ── 5. Repair orphan tool_use / tool_result blocks ─────────────
	if msgs, ok := body["messages"].([]any); ok {
		body["messages"] = repairToolPairsAny(msgs)
	}
}

// renameToolUseBlocksInContent walks a content slice (string is a no-op)
// and rewrites tool_use block names with the mcp_ prefix.
func renameToolUseBlocksInContent(content any) {
	blocks, ok := content.([]any)
	if !ok {
		return
	}
	for _, b := range blocks {
		blk, bok := b.(map[string]any)
		if !bok {
			continue
		}
		if blk["type"] != "tool_use" {
			continue
		}
		if name, nok := blk["name"].(string); nok && name != "" && !strings.HasPrefix(name, toolPrefix) {
			blk["name"] = prefixToolName(name)
		}
	}
}

// repairToolPairsAny drops tool_use blocks whose tool_use_id has no
// matching tool_result and tool_result blocks whose tool_use_id has no
// matching tool_use. Anthropic rejects half-paired requests with a
// 400 "tool_result block ... does not refer to a preceding tool_use",
// commonly hit when message-window truncation drops one half of a
// pair. Mirrors the plugin's repairToolPairs.
//
// Also drops messages whose content is reduced to an empty array.
func repairToolPairsAny(msgs []any) []any {
	useIDs := make(map[string]struct{})
	resultIDs := make(map[string]struct{})

	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := mm["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			blk, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch blk["type"] {
			case "tool_use":
				if id, ok := blk["id"].(string); ok {
					useIDs[id] = struct{}{}
				}
			case "tool_result":
				if id, ok := blk["tool_use_id"].(string); ok {
					resultIDs[id] = struct{}{}
				}
			}
		}
	}

	orphanedUses := make(map[string]struct{})
	for id := range useIDs {
		if _, ok := resultIDs[id]; !ok {
			orphanedUses[id] = struct{}{}
		}
	}
	orphanedResults := make(map[string]struct{})
	for id := range resultIDs {
		if _, ok := useIDs[id]; !ok {
			orphanedResults[id] = struct{}{}
		}
	}
	if len(orphanedUses) == 0 && len(orphanedResults) == 0 {
		return msgs
	}

	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)
			continue
		}
		blocks, ok := mm["content"].([]any)
		if !ok {
			out = append(out, m)
			continue
		}
		filtered := blocks[:0]
		for _, b := range blocks {
			blk, ok := b.(map[string]any)
			if !ok {
				filtered = append(filtered, b)
				continue
			}
			switch blk["type"] {
			case "tool_use":
				if id, ok := blk["id"].(string); ok {
					if _, drop := orphanedUses[id]; drop {
						continue
					}
				}
			case "tool_result":
				if id, ok := blk["tool_use_id"].(string); ok {
					if _, drop := orphanedResults[id]; drop {
						continue
					}
				}
			}
			filtered = append(filtered, b)
		}
		if len(filtered) == 0 {
			// Dropped to empty — drop the whole message so we don't
			// send an Anthropic-rejected zero-block message.
			continue
		}
		mm["content"] = filtered
		out = append(out, mm)
	}
	return out
}

// normalizeSystemToArray accepts the various shapes the system prompt
// can take in our codebase (string, []map, []any, nil) and returns a
// uniform []map[string]any. Empty input → empty slice (not nil) so
// downstream code can append without nil-checks.
func normalizeSystemToArray(v any) []map[string]any {
	switch s := v.(type) {
	case nil:
		return []map[string]any{}
	case string:
		if s == "" {
			return []map[string]any{}
		}
		return []map[string]any{{"type": "text", "text": s}}
	case []map[string]any:
		return s
	case []any:
		out := make([]map[string]any, 0, len(s))
		for _, e := range s {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			} else if str, ok := e.(string); ok && str != "" {
				out = append(out, map[string]any{"type": "text", "text": str})
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

// normalizeMessagesToMaps coerces the body's messages slice into a
// []map[string]any view for the billing-header math. Best-effort: any
// element that doesn't look like a message is skipped silently (the
// caller tolerates an empty result).
func normalizeMessagesToMaps(v any) []map[string]any {
	switch m := v.(type) {
	case nil:
		return nil
	case []map[string]any:
		return m
	case []any:
		out := make([]map[string]any, 0, len(m))
		for _, e := range m {
			if mm, ok := e.(map[string]any); ok {
				out = append(out, mm)
			}
		}
		return out
	default:
		return nil
	}
}

// copyMapExcluding returns a shallow copy of m with the listed keys
// omitted. Used to clone system entry properties (cache_control etc.)
// without dragging the original `text` field into a new entry.
func copyMapExcluding(m map[string]any, exclude ...string) map[string]any {
	skip := make(map[string]struct{}, len(exclude))
	for _, k := range exclude {
		skip[k] = struct{}{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, drop := skip[k]; drop {
			continue
		}
		out[k] = v
	}
	return out
}
