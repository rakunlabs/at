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
				input := b.Input
				if input == nil {
					input = map[string]any{}
				}
				args, _ := json.Marshal(input)
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

	// Tool results must immediately follow the assistant tool calls. Put any
	// additional user text/media after all results, not between a call and result.
	var msgs []map[string]any
	var content []any
	var text string
	hasMedia := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text += b.Text
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		case "image", "document", "audio", "video":
			if b.Source == nil {
				continue
			}
			url := b.Source.URL
			if url == "" && b.Source.Data != "" {
				url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
			}
			if url == "" {
				continue
			}
			switch b.Type {
			case "image":
				content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			case "document":
				filename := "document"
				if b.Source.MediaType == "application/pdf" {
					filename += ".pdf"
				}
				content = append(content, map[string]any{"type": "file", "file": map[string]any{"file_data": url, "filename": filename}})
			case "video":
				content = append(content, map[string]any{"type": "video_url", "video_url": map[string]any{"url": url}})
			case "audio":
				format := "wav"
				if b.Source.MediaType == "audio/mpeg" || b.Source.MediaType == "audio/mp3" {
					format = "mp3"
				}
				content = append(content, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": b.Source.Data, "format": format}})
			}
			hasMedia = true
		case "tool_result":
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": b.ToolUseID,
				"content":      b.Content,
			})
		}
	}

	if hasMedia {
		msgs = append(msgs, map[string]any{"role": role, "content": content})
	} else if text != "" {
		msgs = append(msgs, map[string]any{"role": role, "content": text})
	}

	return msgs
}
