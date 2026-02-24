package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// OpenAI request/response types used by the gateway.

// ChatCompletionRequest is the OpenAI-compatible request body.
type ChatCompletionRequest struct {
	Model         string          `json:"model"`
	Messages      []OpenAIMessage `json:"messages"`
	Tools         []OpenAITool    `json:"tools,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
}

// StreamOptions controls optional streaming behaviour.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"` // string or array
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type OpenAIToolCall struct {
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
	// ThoughtSignature is a Gemini-specific extension: an opaque token that
	// preserves the model's reasoning state across function-calling turns.
	// Clients must echo it back on assistant messages so the gateway can
	// restore it when rebuilding the Gemini request.
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

type OpenAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatCompletionResponse is the OpenAI-compatible response body.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

type ChatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      ChatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

type ChatCompletionMessage struct {
	Role      string           `json:"role"`
	Content   *string          `json:"content"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAI /v1/models response types.

type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelData `json:"data"`
}

type ModelData struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// ─── Streaming response types (SSE / chat.completion.chunk format) ───

// ChatCompletionChunk is the OpenAI-compatible streaming chunk response.
type ChatCompletionChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"` // "chat.completion.chunk"
	Model   string               `json:"model"`
	Choices []ChunkChoice        `json:"choices"`
	Usage   *ChatCompletionUsage `json:"usage,omitempty"`
}

// ChunkChoice represents a single choice in a streaming chunk.
type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// ChunkDelta represents the incremental content in a streaming chunk.
type ChunkDelta struct {
	Role      string           `json:"role,omitempty"`
	Content   any              `json:"content,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

// ─── Translation: OpenAI request → service.Message (for OpenAI-compat backends) ───

// translateOpenAIMessages converts OpenAI-format messages to internal service.Message
// for providers that use the OpenAI-compatible format (openai, vertex).
// Since these providers serialize messages directly via json.Marshal, we need to
// preserve the full OpenAI message structure as map[string]any.
//
// thoughtSigLookup, when non-nil, is called for tool calls that are missing a
// thought_signature. It returns the cached signature (if any) for the given
// tool call ID.  This is needed because many OpenAI-compatible clients strip
// unknown fields like thought_signature when echoing back assistant messages.
func translateOpenAIMessages(msgs []OpenAIMessage, thoughtSigLookup func(string) string) []service.Message {
	result := make([]service.Message, 0, len(msgs))
	for _, msg := range msgs {
		// Rebuild each message as a map to preserve the full OpenAI structure
		// when the provider serializes it in the request body.
		m := map[string]any{
			"role": msg.Role,
		}

		// If the content contains non-text blocks (images, audio, files, video),
		// pass the full content array through as-is. OpenAI and Vertex both
		// natively accept the multi-part content format.
		if hasMultiPartContent(msg.Content) {
			m["content"] = parseContentParts(msg.Content)
		} else {
			content := extractContentString(msg.Content)
			if content != "" {
				m["content"] = content
			} else if msg.Role != "assistant" {
				// For non-assistant roles, always include content even if empty
				m["content"] = ""
			}
		}

		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}

		if len(msg.ToolCalls) > 0 {
			// Convert []OpenAIToolCall to []any so that downstream providers
			// (e.g. Gemini) can type-assert with .([]any) on the stored value.
			// Go does not allow asserting []ConcreteType to []any directly.
			tcs := make([]any, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				tcMap := map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				}
				sig := tc.ThoughtSignature
				// If the client omitted thought_signature, try to restore it
				// from the server-side cache.
				if sig == "" && thoughtSigLookup != nil {
					sig = thoughtSigLookup(tc.ID)
				}
				if sig != "" {
					tcMap["thought_signature"] = sig
				}
				tcs[i] = tcMap
			}
			m["tool_calls"] = tcs
		}

		if msg.Name != "" {
			m["name"] = msg.Name
		}

		result = append(result, service.Message{
			Role:    msg.Role,
			Content: m,
		})
	}
	return result
}

// ─── Translation: OpenAI request → service.Message (for Anthropic backend) ───

// translateOpenAIToAnthropic converts OpenAI-format messages to Anthropic-compatible
// service.Message format. This handles the structural differences:
//   - OpenAI role="tool" → Anthropic role="user" with tool_result content block
//   - OpenAI tool_calls → Anthropic tool_use content blocks
//   - OpenAI role="system" → extracted separately (Anthropic uses system parameter)
func translateOpenAIToAnthropic(msgs []OpenAIMessage) (systemPrompt string, messages []service.Message) {
	messages = make([]service.Message, 0, len(msgs))

	for _, msg := range msgs {
		switch msg.Role {
		case "system", "developer":
			// Anthropic handles system messages separately, but since we're
			// passing through the service.Message interface, we include it
			// as a user message or extract it. For simplicity, prepend to
			// first user message or pass as-is (Anthropic API accepts system param).
			systemPrompt = extractContentString(msg.Content)

		case "user":
			if hasMultiPartContent(msg.Content) {
				// Convert OpenAI multi-part content blocks to Anthropic format
				blocks := convertOpenAIContentToAnthropic(msg.Content)
				messages = append(messages, service.Message{
					Role:    "user",
					Content: blocks,
				})
			} else {
				messages = append(messages, service.Message{
					Role:    "user",
					Content: extractContentString(msg.Content),
				})
			}

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				// Convert to Anthropic tool_use content blocks
				var blocks []service.ContentBlock
				content := extractContentString(msg.Content)
				if content != "" {
					blocks = append(blocks, service.ContentBlock{
						Type: "text",
						Text: content,
					})
				}
				for _, tc := range msg.ToolCalls {
					var args map[string]any
					if tc.Function.Arguments != "" {
						json.Unmarshal([]byte(tc.Function.Arguments), &args)
					}
					blocks = append(blocks, service.ContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: args,
					})
				}
				messages = append(messages, service.Message{
					Role:    "assistant",
					Content: blocks,
				})
			} else {
				messages = append(messages, service.Message{
					Role:    "assistant",
					Content: extractContentString(msg.Content),
				})
			}

		case "tool":
			// OpenAI tool results → Anthropic tool_result content blocks.
			// In Anthropic format, tool results are sent as role="user" with
			// content blocks of type "tool_result".
			content := extractContentString(msg.Content)
			block := service.ContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   content,
			}
			// Check if the last message is already a user message with tool results
			// (Anthropic expects all tool results for a single turn in one message).
			if len(messages) > 0 {
				last := &messages[len(messages)-1]
				if last.Role == "user" {
					if blocks, ok := last.Content.([]service.ContentBlock); ok {
						last.Content = append(blocks, block)
						continue
					}
				}
			}
			messages = append(messages, service.Message{
				Role:    "user",
				Content: []service.ContentBlock{block},
			})
		}
	}

	return systemPrompt, messages
}

// ─── Translation: OpenAI tools → service.Tool ───

func translateOpenAITools(tools []OpenAITool) []service.Tool {
	result := make([]service.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		result = append(result, service.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return result
}

// ─── Translation: service.LLMResponse → OpenAI response ───

func buildOpenAIResponse(id, model string, resp *service.LLMResponse) *ChatCompletionResponse {
	finishReason := "stop"
	if !resp.Finished {
		finishReason = "tool_calls"
	}

	msg := ChatCompletionMessage{
		Role: "assistant",
	}

	if resp.Content != "" {
		content := resp.Content
		msg.Content = &content
	}

	for i, tc := range resp.ToolCalls {
		idx := i
		argsJSON, _ := json.Marshal(tc.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
			Index:            &idx,
			ID:               tc.ID,
			Type:             "function",
			ThoughtSignature: tc.ThoughtSignature,
			Function: OpenAIFunctionCall{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	return &ChatCompletionResponse{
		ID:     id,
		Object: "chat.completion",
		Model:  model,
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: ChatCompletionUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
}

// ─── Helpers ───

// parseDataURL splits a data URI (e.g. "data:image/png;base64,iVBOR...") into
// its MIME type and base64-encoded data. Returns empty strings if not a data URI.
func parseDataURL(url string) (mimeType, data string) {
	if !strings.HasPrefix(url, "data:") {
		return "", ""
	}
	rest := strings.TrimPrefix(url, "data:")
	parts := strings.SplitN(rest, ",", 2)
	if len(parts) != 2 {
		return "", ""
	}
	meta := strings.TrimSuffix(parts[0], ";base64")
	return meta, parts[1]
}

// hasMultiPartContent checks whether a raw OpenAI message content contains any
// non-text content blocks (image_url, input_audio, file, video_url, etc.).
func hasMultiPartContent(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return false
	}
	for _, p := range parts {
		if p.Type != "" && p.Type != "text" {
			return true
		}
	}
	return false
}

// parseContentParts parses a raw OpenAI content array into []any, preserving
// all content part types (text, image_url, input_audio, file, video_url, etc.) as maps.
func parseContentParts(raw json.RawMessage) []any {
	var parts []any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	return parts
}

// convertOpenAIContentToAnthropic converts an OpenAI content array (containing
// text, image_url, input_audio, file, and video_url blocks) to Anthropic-format
// content blocks.
//
// Supported conversions:
//
//	OpenAI image_url  → Anthropic image    (base64 source)
//	OpenAI file       → Anthropic document (base64 source)
//	OpenAI input_audio → passed through as-is (let Anthropic decide)
//	OpenAI video_url   → passed through as-is (let Anthropic decide)
func convertOpenAIContentToAnthropic(raw json.RawMessage) []service.ContentBlock {
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return []service.ContentBlock{{Type: "text", Text: string(raw)}}
	}

	var blocks []service.ContentBlock
	for _, p := range parts {
		partType, _ := p["type"].(string)
		switch partType {
		case "text":
			text, _ := p["text"].(string)
			blocks = append(blocks, service.ContentBlock{
				Type: "text",
				Text: text,
			})

		case "image_url":
			// OpenAI: {type:"image_url", image_url:{url:"data:image/png;base64,..."}}
			// Anthropic: {type:"image", source:{type:"base64", media_type:"image/png", data:"..."}}
			imageURL, _ := p["image_url"].(map[string]any)
			if imageURL == nil {
				continue
			}
			url, _ := imageURL["url"].(string)
			mimeType, data := parseDataURL(url)
			if data == "" {
				// Non-data URI (e.g. https:// URL) — pass through via url source type
				if url != "" {
					blocks = append(blocks, service.ContentBlock{
						Type: "image",
						Source: &service.MediaSource{
							Type: "url",
							URL:  url,
						},
					})
				}
				continue
			}
			blocks = append(blocks, service.ContentBlock{
				Type: "image",
				Source: &service.MediaSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				},
			})

		case "input_audio":
			// OpenAI: {type:"input_audio", input_audio:{data:"<base64>", format:"wav"|"mp3"}}
			// Anthropic: pass through as content block — let the provider decide.
			audio, _ := p["input_audio"].(map[string]any)
			if audio == nil {
				continue
			}
			data, _ := audio["data"].(string)
			format, _ := audio["format"].(string)
			if data == "" {
				continue
			}
			mimeType := "audio/" + format
			if format == "" {
				mimeType = "audio/wav"
			}
			blocks = append(blocks, service.ContentBlock{
				Type: "audio",
				Source: &service.MediaSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				},
			})

		case "file":
			// OpenAI: {type:"file", file:{filename:"doc.pdf", file_data:{mime_type:"application/pdf", data:"<base64>"}}}
			// Anthropic: {type:"document", source:{type:"base64", media_type:"application/pdf", data:"..."}}
			file, _ := p["file"].(map[string]any)
			if file == nil {
				continue
			}
			fileData, _ := file["file_data"].(map[string]any)
			if fileData == nil {
				continue
			}
			mimeType, _ := fileData["mime_type"].(string)
			data, _ := fileData["data"].(string)
			if data == "" {
				continue
			}
			blocks = append(blocks, service.ContentBlock{
				Type: "document",
				Source: &service.MediaSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				},
			})

		case "video_url":
			// OpenAI: {type:"video_url", video_url:{url:"data:video/mp4;base64,..."}}
			// Pass through as video content block — let the provider decide.
			videoURL, _ := p["video_url"].(map[string]any)
			if videoURL == nil {
				continue
			}
			url, _ := videoURL["url"].(string)
			mimeType, data := parseDataURL(url)
			if data == "" {
				continue
			}
			blocks = append(blocks, service.ContentBlock{
				Type: "video",
				Source: &service.MediaSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				},
			})
		}
	}

	if len(blocks) == 0 {
		return []service.ContentBlock{{Type: "text", Text: string(raw)}}
	}
	return blocks
}

// extractContentString extracts a plain string from OpenAI message content.
// Content can be a JSON string or an array of content parts.
func extractContentString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try as array of content parts
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var result string
		for _, p := range parts {
			if p.Type == "text" {
				result += p.Text
			}
		}
		return result
	}

	// Fallback: return raw string
	return string(raw)
}

// generateChatID creates a simple unique ID for chat completion responses.
func generateChatID() string {
	return fmt.Sprintf("chatcmpl-%s", ulid.Make().String())
}
