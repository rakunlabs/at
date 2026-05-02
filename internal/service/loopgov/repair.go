package loopgov

import (
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
)

// RepairToolPairs drops orphan tool_use and tool_result content blocks
// from a message slice so the result satisfies every provider's pairing
// invariant: every assistant tool_use ID must have a matching user
// tool_result, and vice versa.
//
// Why this exists: the loop governor windows the conversation by
// keeping only a tail suffix that fits the budget. The cut can fall
// between an assistant message that carries tool_use blocks and the
// subsequent user message carrying matching tool_result blocks
// (or after a tool_result whose parent assistant got dropped). When
// that happens, OpenAI rejects the request with
//
//	"tool id (call_xxxx) not found"
//
// and Anthropic rejects with
//
//	"tool_result block ... does not refer to a preceding tool_use".
//
// Anthropic's adapter has its own wire-level repair pass; OpenAI /
// Vertex / Gemini do not. Running the repair here covers all
// providers regardless of which path they take to the wire.
//
// Operates on the []service.Message + []ContentBlock representation
// (the in-memory format used by all agentic loops in this codebase).
// String-content messages and other roles pass through unchanged. A
// message whose content collapses to zero blocks after pruning is
// dropped entirely so we do not send an empty assistant turn.
//
// The function is a no-op (returns the input slice as-is) when no
// orphans are found; otherwise it allocates a new slice.
func RepairToolPairs(messages []service.Message) []service.Message {
	if len(messages) == 0 {
		return messages
	}

	// Pass 1: collect all tool_use IDs (from assistant content blocks)
	// and all tool_result IDs (from user content blocks).
	useIDs := make(map[string]struct{})
	resultIDs := make(map[string]struct{})
	for _, m := range messages {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if b.ID != "" {
					useIDs[b.ID] = struct{}{}
				}
			case "tool_result":
				if b.ToolUseID != "" {
					resultIDs[b.ToolUseID] = struct{}{}
				}
			}
		}
	}

	// Identify orphans on each side.
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
		return messages
	}

	// Pass 2: rebuild the slice, pruning orphan blocks and dropping
	// messages whose content collapses to empty.
	out := make([]service.Message, 0, len(messages))
	for _, m := range messages {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			out = append(out, m)
			continue
		}
		filtered := make([]service.ContentBlock, 0, len(blocks))
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if _, drop := orphanedUses[b.ID]; drop {
					continue
				}
			case "tool_result":
				if _, drop := orphanedResults[b.ToolUseID]; drop {
					continue
				}
			}
			filtered = append(filtered, b)
		}
		if len(filtered) == 0 {
			// Whole message collapses — drop it. Sending an
			// assistant message with empty content (or a user
			// message that was nothing but tool_results) would be
			// rejected by every provider.
			continue
		}
		// If the surviving blocks are identical to the originals,
		// reuse the original message; otherwise emit a copy with the
		// new content so we don't mutate the caller's slice.
		if len(filtered) == len(blocks) {
			out = append(out, m)
		} else {
			cp := m
			cp.Content = filtered
			out = append(out, cp)
		}
	}

	slog.Debug("loopgov.repair_tool_pairs",
		"orphan_uses", len(orphanedUses),
		"orphan_results", len(orphanedResults),
		"in", len(messages),
		"out", len(out))

	return out
}
