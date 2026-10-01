package agentloop

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// IsToolPairingError reports whether err looks like a provider rejection
// caused by an orphan tool_call / tool_use / tool_result pair in the
// outgoing request. Callers use it to trigger a one-shot sanitize-and-retry.
func IsToolPairingError(err error) bool {
	if err == nil {
		return false
	}
	return IsToolPairingMessage(err.Error())
}

// IsToolPairingMessage applies IsToolPairingError's wording checks to text,
// for loops that surface provider errors as response text rather than errors.
// Each provider phrases the rejection differently:
//
//   - Anthropic: "tool_result block ... does not refer to a preceding
//     tool_use" / "tool call result does not follow"
//   - OpenAI:    "tool id (call_xxxx) not found" /
//     "Invalid parameter: messages with role 'tool' must be a response
//     to a preceding message with 'tool_calls'"
//   - Vertex:    same wording as OpenAI (OpenAI-compat dialect)
func IsToolPairingMessage(s string) bool {
	if s == "" {
		return false
	}
	// Anthropic.
	if strings.Contains(s, "tool call result does not follow") ||
		strings.Contains(s, "tool_use content block") ||
		(strings.Contains(s, "tool_result") && strings.Contains(s, "not follow")) {
		return true
	}
	// OpenAI / Vertex.
	if strings.Contains(s, "tool id") && strings.Contains(s, "not found") {
		return true
	}
	if strings.Contains(s, "tool_call_id") && strings.Contains(s, "not found") {
		return true
	}
	if strings.Contains(s, "messages with role 'tool'") && strings.Contains(s, "tool_calls") {
		return true
	}
	if strings.Contains(s, "tool_calls") && strings.Contains(s, "must be followed by") {
		return true
	}
	return false
}

// toolPairing drops assistant tool calls whose results are incomplete, and
// results that answer no surviving call. It is shared by the stored-row and
// in-memory history shapes, which differ only in where the IDs live.
type toolPairing struct {
	valid  map[string]bool
	broken map[string]bool
}

func newToolPairing(callIDs, resultIDs [][]string) toolPairing {
	p := toolPairing{valid: map[string]bool{}, broken: map[string]bool{}}
	present := map[string]bool{}
	for _, ids := range resultIDs {
		for _, id := range ids {
			present[id] = true
		}
	}
	for _, ids := range callIDs {
		complete := true
		for _, id := range ids {
			p.valid[id] = true
			if !present[id] {
				complete = false
			}
		}
		if !complete {
			for _, id := range ids {
				p.broken[id] = true
			}
		}
	}
	return p
}

func (p toolPairing) anyBroken(ids []string) bool {
	for _, id := range ids {
		if p.broken[id] {
			return true
		}
	}
	return false
}

func (p toolPairing) anyOrphan(ids []string) bool {
	for _, id := range ids {
		if p.broken[id] || !p.valid[id] {
			return true
		}
	}
	return false
}

// SanitizeChatHistory removes corrupted tool call/result sequences from stored
// chat rows: assistant messages with any unanswered call are dropped together
// with all of their results, and tool rows answering no call are dropped.
func SanitizeChatHistory(msgs []service.ChatMessage) []service.ChatMessage {
	if len(msgs) == 0 {
		return msgs
	}

	var callIDs, resultIDs [][]string
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			callIDs = append(callIDs, ChatToolCallIDs(msg.Data.ToolCalls))
		case "tool":
			if msg.Data.ToolCallID != "" {
				resultIDs = append(resultIDs, []string{msg.Data.ToolCallID})
			}
		}
	}
	pairing := newToolPairing(callIDs, resultIDs)

	var result []service.ChatMessage
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			if ids := ChatToolCallIDs(msg.Data.ToolCalls); pairing.anyBroken(ids) {
				slog.Debug("sanitize chat history: dropping broken assistant message", "tool_call_ids", len(ids))
				continue
			}
		case "tool":
			if msg.Data.ToolCallID == "" || pairing.anyOrphan([]string{msg.Data.ToolCallID}) {
				slog.Debug("sanitize chat history: dropping tool message", "tool_call_id", msg.Data.ToolCallID)
				continue
			}
		}
		result = append(result, msg)
	}

	return result
}

// SanitizeMessages is SanitizeChatHistory for in-memory provider messages,
// where calls are assistant tool_use blocks and results are user tool_result
// blocks. The input is returned unchanged when nothing is broken.
func SanitizeMessages(msgs []service.Message) []service.Message {
	if len(msgs) == 0 {
		return msgs
	}

	var callIDs, resultIDs [][]string
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			callIDs = append(callIDs, contentBlockIDs(msg.Content, "tool_use"))
		case "user":
			resultIDs = append(resultIDs, contentBlockIDs(msg.Content, "tool_result"))
		}
	}
	pairing := newToolPairing(callIDs, resultIDs)
	if len(pairing.broken) == 0 {
		return msgs
	}

	var result []service.Message
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			if ids := contentBlockIDs(msg.Content, "tool_use"); pairing.anyBroken(ids) {
				slog.Debug("sanitize messages: dropping broken assistant message", "tool_use_ids", len(ids))
				continue
			}
		case "user":
			if ids := contentBlockIDs(msg.Content, "tool_result"); pairing.anyOrphan(ids) {
				slog.Debug("sanitize messages: dropping orphaned tool_result message", "tool_result_ids", len(ids))
				continue
			}
		}
		result = append(result, msg)
	}

	return result
}

// ChatToolCallIDs extracts tool call IDs from a stored assistant message's
// ToolCalls field, which decodes from JSON as loosely typed maps.
func ChatToolCallIDs(toolCalls any) []string {
	var maps []map[string]any
	switch v := toolCalls.(type) {
	case []any:
		for _, tc := range v {
			if m, ok := tc.(map[string]any); ok {
				maps = append(maps, m)
			}
		}
	case []map[string]any:
		maps = v
	}

	var ids []string
	for _, m := range maps {
		for _, key := range []string{"id", "Id", "ID"} {
			if id, ok := m[key].(string); ok && id != "" {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids
}

func contentBlockIDs(content any, blockType string) []string {
	blocks, ok := content.([]service.ContentBlock)
	if !ok {
		return nil
	}
	var ids []string
	for _, b := range blocks {
		if b.Type != blockType {
			continue
		}
		id := b.ID
		if blockType == "tool_result" {
			id = b.ToolUseID
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// CallMCPTool dispatches a tool call to the first MCP client that answers it.
// A failing client is skipped rather than ending the search.
func CallMCPTool(ctx context.Context, clients []service.MCPClient, name string, args map[string]any) (string, error) {
	for _, c := range clients {
		result, err := c.CallTool(ctx, name, args)
		if err != nil {
			continue
		}
		return result, nil
	}
	return "", fmt.Errorf("MCP tool %q: no server returned a result", name)
}
