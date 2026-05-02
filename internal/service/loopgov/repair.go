package loopgov

import (
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
)

// RepairToolPairs drops orphan tool_use and tool_result content blocks
// from a message slice so the result satisfies every provider's pairing
// invariant. Two invariants are enforced:
//
//  1. Global ID pairing: every assistant tool_use ID must have a
//     matching tool_result somewhere in the slice, and vice versa.
//
//  2. Adjacency: every assistant tool_use block must be in a message
//     whose IMMEDIATE successor is a user message carrying a matching
//     tool_result block. Symmetrically, every user tool_result block's
//     IMMEDIATE predecessor must be an assistant message carrying the
//     matching tool_use. This is Anthropic's strict requirement
//     ("tool_use block must have a corresponding tool_result block in
//     the next message"). OpenAI / Vertex enforce a similar rule
//     (assistant tool_calls must be immediately followed by role:"tool"
//     messages).
//
// Why this exists: the loop governor windows the conversation by
// keeping only a tail suffix that fits the budget, and downstream
// provider adapters may merge consecutive same-role messages
// (Anthropic does this to satisfy its strict alternation rule). Both
// transforms can break the adjacency invariant — leaving an assistant
// tool_use block whose matching tool_result lives in some later (not
// immediately next) message, which Anthropic rejects with
//
//	"tool_use ids were found without tool_result blocks immediately
//	 after... tool_use block must have a corresponding tool_result
//	 block in the next message"
//
// and OpenAI rejects with
//
//	"tool id (call_xxxx) not found"
//
// Operates on the []service.Message + []ContentBlock representation
// (the in-memory format used by all agentic loops in this codebase).
// String-content messages and other roles pass through unchanged. A
// message whose content collapses to zero blocks after pruning is
// dropped entirely so we do not send an empty assistant turn.
//
// The function is a no-op (returns the input slice as-is) when nothing
// needs pruning; otherwise it allocates a new slice. The pass is
// idempotent — running it twice yields the same result.
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

	// Pass 2: identify adjacency-orphans. A tool_use ID is
	// adjacency-orphan if its message's immediate successor is not a
	// user message carrying a tool_result with the same ID.
	// Symmetrically, a tool_result ID is adjacency-orphan if its
	// message's immediate predecessor is not an assistant message
	// carrying a tool_use with the same ID.
	adjacentResultIDs := make(map[string]struct{}) // tool_use IDs whose matching tool_result is in the NEXT message
	adjacentUseIDs := make(map[string]struct{})    // tool_result IDs whose matching tool_use is in the PREVIOUS message
	for i, m := range messages {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			continue
		}
		switch m.Role {
		case "assistant":
			// Look at the next message for matching tool_results.
			if i+1 >= len(messages) {
				continue
			}
			next := messages[i+1]
			if next.Role != "user" {
				continue
			}
			nextBlocks, ok := next.Content.([]service.ContentBlock)
			if !ok {
				continue
			}
			nextResultIDs := make(map[string]struct{})
			for _, nb := range nextBlocks {
				if nb.Type == "tool_result" && nb.ToolUseID != "" {
					nextResultIDs[nb.ToolUseID] = struct{}{}
				}
			}
			for _, b := range blocks {
				if b.Type == "tool_use" && b.ID != "" {
					if _, ok := nextResultIDs[b.ID]; ok {
						adjacentResultIDs[b.ID] = struct{}{}
					}
				}
			}
		case "user":
			// Look at the previous message for matching tool_uses.
			if i == 0 {
				continue
			}
			prev := messages[i-1]
			if prev.Role != "assistant" {
				continue
			}
			prevBlocks, ok := prev.Content.([]service.ContentBlock)
			if !ok {
				continue
			}
			prevUseIDs := make(map[string]struct{})
			for _, pb := range prevBlocks {
				if pb.Type == "tool_use" && pb.ID != "" {
					prevUseIDs[pb.ID] = struct{}{}
				}
			}
			for _, b := range blocks {
				if b.Type == "tool_result" && b.ToolUseID != "" {
					if _, ok := prevUseIDs[b.ToolUseID]; ok {
						adjacentUseIDs[b.ToolUseID] = struct{}{}
					}
				}
			}
		}
	}

	// Identify orphans: a tool_use is orphan if it has no matching
	// tool_result in the entire slice (global) OR the matching
	// tool_result is not in the immediately next message (adjacency).
	orphanedUses := make(map[string]struct{})
	for id := range useIDs {
		_, globalMatch := resultIDs[id]
		_, adjacentMatch := adjacentResultIDs[id]
		if !globalMatch || !adjacentMatch {
			orphanedUses[id] = struct{}{}
		}
	}
	orphanedResults := make(map[string]struct{})
	for id := range resultIDs {
		_, globalMatch := useIDs[id]
		_, adjacentMatch := adjacentUseIDs[id]
		if !globalMatch || !adjacentMatch {
			orphanedResults[id] = struct{}{}
		}
	}
	if len(orphanedUses) == 0 && len(orphanedResults) == 0 {
		return messages
	}

	// Pass 3: rebuild the slice, pruning orphan blocks and dropping
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
