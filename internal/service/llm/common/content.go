// Package common provides shared utilities for LLM provider adapters.
package common

import (
	"encoding/json"

	"github.com/rakunlabs/at/internal/service"
)

// ConvertContentBlocksToOpenAI converts Anthropic-style []ContentBlock into
// OpenAI-compatible message maps. Used by providers that speak the OpenAI
// format (openai, vertex) to normalize messages containing tool calls/results.
func ConvertContentBlocksToOpenAI(role string, blocks []service.ContentBlock) []map[string]any {
	if role == "assistant" {
		// Collect text and tool_calls from the assistant message.
		var text string
		var toolCalls []map[string]any
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text += b.Text
			case "tool_use":
				args, _ := json.Marshal(b.Input)
				tc := map[string]any{
					"id":   b.ID,
					"type": "function",
					"function": map[string]any{
						"name":      b.Name,
						"arguments": string(args),
					},
				}
				if b.ThoughtSignature != "" {
					tc["thought_signature"] = b.ThoughtSignature
				}
				toolCalls = append(toolCalls, tc)
			}
		}

		m := map[string]any{"role": "assistant"}
		if text != "" {
			m["content"] = text
		}
		if len(toolCalls) > 0 {
			m["tool_calls"] = toolCalls
		}
		return []map[string]any{m}
	}

	// For user messages: split tool_result blocks into individual role:"tool"
	// messages. Any text blocks become a separate user message.
	var msgs []map[string]any
	var text string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool_result":
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": b.ToolUseID,
				"content":      b.Content,
			})
		}
	}

	// Prepend text message before tool results if there was any text.
	if text != "" {
		msgs = append([]map[string]any{{"role": role, "content": text}}, msgs...)
	}

	return msgs
}
