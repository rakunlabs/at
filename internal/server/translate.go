package server

import (
	"encoding/json"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// OpenAI request/response types used by the gateway.

// ChatCompletionRequest is the OpenAI-compatible request body.
type ChatCompletionRequest struct {
	Model     string          `json:"model"`
	Messages  []OpenAIMessage `json:"messages"`
	Tools     []OpenAITool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
	MaxTokens *int            `json:"max_tokens,omitempty"`
}

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"` // string or array
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
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
	ID      string        `json:"id"`
	Object  string        `json:"object"` // "chat.completion.chunk"
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
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
	Content   string           `json:"content,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

// ─── Translation: OpenAI request → service.Message (for OpenAI-compat backends) ───

// translateOpenAIMessages converts OpenAI-format messages to internal service.Message
// for providers that use the OpenAI-compatible format (openai, vertex).
// Since these providers serialize messages directly via json.Marshal, we need to
// preserve the full OpenAI message structure as map[string]any.
func translateOpenAIMessages(msgs []OpenAIMessage) []service.Message {
	result := make([]service.Message, 0, len(msgs))
	for _, msg := range msgs {
		// Rebuild each message as a map to preserve the full OpenAI structure
		// when the provider serializes it in the request body.
		m := map[string]any{
			"role": msg.Role,
		}

		content := extractContentString(msg.Content)
		if content != "" {
			m["content"] = content
		} else if msg.Role != "assistant" {
			// For non-assistant roles, always include content even if empty
			m["content"] = ""
		}

		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}

		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
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
			messages = append(messages, service.Message{
				Role:    "user",
				Content: extractContentString(msg.Content),
			})

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

	for _, tc := range resp.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
			ID:   tc.ID,
			Type: "function",
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
		Usage: ChatCompletionUsage{},
	}
}

// ─── Helpers ───

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
