package service

import (
	"context"
	"net/http"
)

// ─── LLM Provider Interfaces ───

// LLMProvider is the core interface for all LLM providers.
type LLMProvider interface {
	// Chat sends messages to the LLM and returns a response.
	// The model parameter allows per-request model override;
	// if empty, the provider's default model is used.
	// opts may be nil, in which case provider defaults are used.
	Chat(ctx context.Context, model string, messages []Message, tools []Tool, opts *ChatOptions) (*LLMResponse, error)
}

// LLMStreamProvider is optionally implemented by providers that support
// true server-sent event (SSE) streaming. The gateway checks for this
// interface via type assertion; if a provider doesn't implement it,
// the gateway falls back to calling Chat() and fake-streaming the result.
type LLMStreamProvider interface {
	ChatStream(ctx context.Context, model string, messages []Message, tools []Tool, opts *ChatOptions) (<-chan StreamChunk, http.Header, error)

	// Proxy forwards a raw HTTP request to the provider's API.
	// The path is relative to the provider's base URL.
	Proxy(w http.ResponseWriter, r *http.Request, path string) error
}

// ─── LLM Request Options ───

// ChatOptions contains optional per-request parameters that control
// generation behaviour. All pointer fields use nil to mean "use provider
// default". Providers ignore fields they don't support.
type ChatOptions struct {
	// MaxTokens limits the maximum number of output tokens.
	// Used by non-reasoning models (maps to max_tokens for OpenAI/Anthropic,
	// maxOutputTokens for Gemini).
	MaxTokens *int

	// MaxCompletionTokens is used by OpenAI reasoning models (o-series).
	// When set, it is sent as max_completion_tokens instead of max_tokens.
	MaxCompletionTokens *int

	// Temperature controls randomness (0.0–2.0).
	Temperature *float64

	// TopP controls nucleus sampling.
	TopP *float64

	// Stop sequences that cause the model to stop generating.
	Stop []string

	// Seed for deterministic generation (provider-dependent support).
	Seed *int

	// ResponseFormat requests a specific output format (e.g. {"type":"json_object"}).
	ResponseFormat map[string]any

	// ReasoningEffort controls thinking depth for reasoning models.
	// Values: "low", "medium", "high".
	// For OpenAI o-series: forwarded directly as reasoning_effort.
	// For Anthropic: mapped to thinking budget (low=2048, medium=8192, high=24576).
	// For Gemini: mapped to thinkingBudget.
	ReasoningEffort string

	// Thinking enables extended thinking / chain-of-thought mode.
	// When non-nil, providers activate their native thinking mechanism.
	// This takes precedence over ReasoningEffort for Anthropic and Gemini.
	Thinking *ThinkingConfig

	// WebSearchOptions enables web search for models that support it.
	// For OpenAI: forwarded as web_search_options in the request body.
	// Currently supported by gpt-4o-search-preview, gpt-4o-mini-search-preview.
	WebSearchOptions map[string]any
}

// ThinkingConfig enables extended thinking / chain-of-thought.
type ThinkingConfig struct {
	// Type is typically "enabled" (following Anthropic's convention).
	Type string
	// BudgetTokens is the token budget for the thinking phase.
	// 0 means the provider should use its own default.
	BudgetTokens int
}

// ─── LLM Message & Response Types ───

// Message represents a chat message with role and content.
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or array of content blocks
}

// ContentBlock represents a structured content block within a message.
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	Source    *MediaSource   `json:"source,omitempty"` // For media content blocks (images, documents, audio, video — Anthropic format)
	// ThoughtSignature is an opaque token from Gemini thinking models (2.5+)
	// that preserves the model's reasoning state across function-calling turns.
	// It must be echoed back on the corresponding tool_use content block.
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// MediaSource represents a media source for content blocks (images, documents, audio, video).
// Used by Anthropic-format content blocks where the source contains base64-encoded data
// or a URL reference.
type MediaSource struct {
	Type      string `json:"type"`                 // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // e.g. "image/png", "application/pdf", "audio/wav"
	Data      string `json:"data,omitempty"`       // base64-encoded data (when type="base64")
	URL       string `json:"url,omitempty"`        // URL reference (when type="url")
}

// Usage contains token usage statistics from the upstream provider.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// InlineImage represents a base64-encoded image returned by a provider (e.g. Gemini).
type InlineImage struct {
	MimeType string // e.g. "image/png"
	Data     string // base64-encoded
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	// Content is the text delta for this chunk (may be empty).
	Content string

	// ReasoningContent is the reasoning/thinking text delta for this chunk.
	// Populated by providers that support thinking tokens (e.g. Gemini 2.5+
	// thinking models, Anthropic extended thinking).
	ReasoningContent string

	// InlineImages contains any base64-encoded images in this chunk (e.g. from Gemini image generation).
	InlineImages []InlineImage

	// ToolCalls contains tool call deltas for this chunk.
	ToolCalls []ToolCall

	// FinishReason is set on the final chunk: "stop" or "tool_calls".
	// Empty string means this is not the final chunk.
	FinishReason string

	// Usage, when non-nil, contains the final token usage statistics for
	// the entire streamed response. Providers set this on the last chunk.
	Usage *Usage

	// Error, if non-nil, indicates the stream encountered an error.
	Error error
}

// LLMResponse is the full response from an LLM provider call.
type LLMResponse struct {
	Content          string
	ReasoningContent string
	InlineImages     []InlineImage
	ToolCalls        []ToolCall
	Finished         bool
	Usage            Usage
	Header           http.Header
}

// ToolCall represents a single tool invocation within an LLM response.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	// ThoughtSignature is an opaque token from Gemini thinking models that
	// preserves the model's reasoning state across function-calling turns.
	// It must be echoed back in the subsequent request for the model to
	// maintain context continuity.
	ThoughtSignature string
}
