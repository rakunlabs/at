package common

// RepairOpenAIToolPairs drops orphan tool_call / tool_result entries
// from a sequence of OpenAI-wire-format messages so the request body
// satisfies the OpenAI API invariant: every `tool_call.id` advertised
// on an assistant message must have a matching `role:"tool"` message
// with the same `tool_call_id`, and vice versa.
//
// Without this pass, OpenAI rejects the request with
//
//	"tool id (call_xxxx) not found"
//
// — most commonly when message-window truncation upstream drops one
// half of an assistant ↔ tool pair. We have a generic in-memory
// repair pass in `internal/service/loopgov` (RepairToolPairs) that
// covers all providers; this is a defense-in-depth pass at the
// provider's wire layer to catch edge cases (gateway passthrough,
// callers that bypass loopgov, etc.).
//
// Input: a `[]any` slice of `map[string]any` messages already in
// OpenAI request shape (role/content/tool_calls/tool_call_id).
// Output: a new slice with orphans pruned. Non-map elements pass
// through unchanged. The function never mutates entries in place
// except for stripping orphan tool_calls from an assistant map; in
// that case the caller's map is replaced with a shallow copy.
func RepairOpenAIToolPairs(messages []any) []any {
	if len(messages) == 0 {
		return messages
	}

	// Pass 1: collect all tool_call IDs (assistant) and tool_call_ids
	// (tool result messages).
	callIDs := make(map[string]struct{})
	resultIDs := make(map[string]struct{})
	for _, m := range messages {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		switch role {
		case "assistant":
			for _, id := range extractToolCallIDs(mm) {
				callIDs[id] = struct{}{}
			}
		case "tool":
			if id, ok := mm["tool_call_id"].(string); ok && id != "" {
				resultIDs[id] = struct{}{}
			}
		}
	}

	orphanCalls := make(map[string]struct{})
	for id := range callIDs {
		if _, ok := resultIDs[id]; !ok {
			orphanCalls[id] = struct{}{}
		}
	}
	orphanResults := make(map[string]struct{})
	for id := range resultIDs {
		if _, ok := callIDs[id]; !ok {
			orphanResults[id] = struct{}{}
		}
	}
	if len(orphanCalls) == 0 && len(orphanResults) == 0 {
		return messages
	}

	out := make([]any, 0, len(messages))
	for _, m := range messages {
		mm, ok := m.(map[string]any)
		if !ok {
			out = append(out, m)
			continue
		}
		role, _ := mm["role"].(string)
		switch role {
		case "tool":
			if id, ok := mm["tool_call_id"].(string); ok {
				if _, drop := orphanResults[id]; drop {
					continue
				}
			}
			out = append(out, mm)
		case "assistant":
			cleaned, kept := stripOrphanToolCalls(mm, orphanCalls)
			if !kept {
				continue
			}
			out = append(out, cleaned)
		default:
			out = append(out, mm)
		}
	}
	return out
}

// extractToolCallIDs reads the `id` field from each entry in an
// assistant message's `tool_calls` array.
func extractToolCallIDs(m map[string]any) []string {
	tcs, ok := m["tool_calls"].([]any)
	if !ok {
		// Some callers may use []map[string]any directly.
		if alt, ok := m["tool_calls"].([]map[string]any); ok {
			ids := make([]string, 0, len(alt))
			for _, tc := range alt {
				if id, ok := tc["id"].(string); ok && id != "" {
					ids = append(ids, id)
				}
			}
			return ids
		}
		return nil
	}
	ids := make([]string, 0, len(tcs))
	for _, raw := range tcs {
		tc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := tc["id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// stripOrphanToolCalls removes any tool_calls[].id entries listed in
// orphanCalls from the assistant message. Returns the (possibly
// rewritten) map and whether the message should be kept. A message
// with no surviving tool_calls AND no textual content is dropped to
// avoid sending an empty assistant turn.
func stripOrphanToolCalls(m map[string]any, orphanCalls map[string]struct{}) (map[string]any, bool) {
	rawTcs, present := m["tool_calls"]
	if !present {
		// No tool_calls — nothing to strip; keep as-is.
		return m, true
	}

	// Normalise to []any so we have a single code path.
	var tcs []any
	switch v := rawTcs.(type) {
	case []any:
		tcs = v
	case []map[string]any:
		tcs = make([]any, 0, len(v))
		for _, tc := range v {
			tcs = append(tcs, tc)
		}
	default:
		return m, true
	}

	kept := make([]any, 0, len(tcs))
	dropped := false
	for _, raw := range tcs {
		tc, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if id, ok := tc["id"].(string); ok {
			if _, drop := orphanCalls[id]; drop {
				dropped = true
				continue
			}
		}
		kept = append(kept, raw)
	}
	if !dropped {
		return m, true
	}

	// Shallow-copy the map so we don't mutate the caller's input.
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	if len(kept) == 0 {
		delete(cp, "tool_calls")
		// If the assistant has no surviving content either, drop.
		if isEmptyAssistantContent(cp["content"]) {
			return nil, false
		}
		return cp, true
	}
	cp["tool_calls"] = kept
	return cp, true
}

// isEmptyAssistantContent reports whether the assistant message's
// content field is missing or empty (string "", nil, empty array).
func isEmptyAssistantContent(c any) bool {
	switch v := c.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case []map[string]any:
		return len(v) == 0
	}
	return false
}
