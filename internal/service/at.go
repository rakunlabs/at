package service

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/config"
	"github.com/worldline-go/types"
)

// Generic LLM Interface
type LLMProvider interface {
	// Chat sends messages to the LLM and returns a response.
	// The model parameter allows per-request model override;
	// if empty, the provider's default model is used.
	Chat(ctx context.Context, model string, messages []Message, tools []Tool) (*LLMResponse, error)
}

// LLMStreamProvider is optionally implemented by providers that support
// true server-sent event (SSE) streaming. The gateway checks for this
// interface via type assertion; if a provider doesn't implement it,
// the gateway falls back to calling Chat() and fake-streaming the result.
type LLMStreamProvider interface {
	ChatStream(ctx context.Context, model string, messages []Message, tools []Tool) (<-chan StreamChunk, error)
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

	// InlineImages contains any base64-encoded images in this chunk (e.g. from Gemini image generation).
	InlineImages []InlineImage

	// ToolCalls contains tool call deltas for this chunk.
	ToolCalls []ToolCall

	// FinishReason is set on the final chunk: "stop" or "tool_calls".
	// Empty string means this is not the final chunk.
	FinishReason string

	// Error, if non-nil, indicates the stream encountered an error.
	Error error
}

// ProviderRecord represents a provider configuration stored in the database.
type ProviderRecord struct {
	ID        string           `json:"id"`
	Key       string           `json:"key"`
	Config    config.LLMConfig `json:"config"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

// ProviderStorer defines CRUD operations for provider configurations
// stored in a persistent backend (e.g., PostgreSQL).
type ProviderStorer interface {
	ListProviders(ctx context.Context) ([]ProviderRecord, error)
	GetProvider(ctx context.Context, key string) (*ProviderRecord, error)
	CreateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*ProviderRecord, error)
	UpdateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*ProviderRecord, error)
	DeleteProvider(ctx context.Context, key string) error
}

// ─── API Token Management ───

// APIToken represents a bearer token stored in the database for gateway auth.
type APIToken struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	TokenPrefix      string                 `json:"token_prefix"`      // first 8 chars for display (e.g. "at_xxxx…")
	AllowedProviders types.Slice[string]    `json:"allowed_providers"` // nil = all providers allowed
	AllowedModels    types.Slice[string]    `json:"allowed_models"`    // nil = all models allowed ("provider/model" format)
	ExpiresAt        types.Null[types.Time] `json:"expires_at"`        // zero value = no expiry
	CreatedAt        types.Time             `json:"created_at"`
	LastUsedAt       types.Null[types.Time] `json:"last_used_at"`
}

// APITokenStorer defines CRUD operations for API tokens.
type APITokenStorer interface {
	ListAPITokens(ctx context.Context) ([]APIToken, error)
	GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error)
	CreateAPIToken(ctx context.Context, token APIToken, tokenHash string) (*APIToken, error)
	UpdateAPIToken(ctx context.Context, id string, token APIToken) (*APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error
	UpdateLastUsed(ctx context.Context, id string) error
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or array of content blocks
}

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	Source    *MediaSource   `json:"source,omitempty"` // For media content blocks (images, documents, audio, video — Anthropic format)
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

type LLMResponse struct {
	Content      string
	InlineImages []InlineImage
	ToolCalls    []ToolCall
	Finished     bool
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// Agent orchestrates MCP and LLM
type Agent struct {
	mcp      *HTTPMCPClient
	provider LLMProvider
	messages []Message

	Tools []Tool
}

func NewAgent(mcp *HTTPMCPClient, provider LLMProvider) *Agent {
	return &Agent{
		mcp:      mcp,
		provider: provider,
		messages: []Message{},
		Tools:    []Tool{},
	}
}

func (a *Agent) SetTools(ctx context.Context) error {
	tools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nAvailable tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}

	a.Tools = tools

	return nil
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.messages = append(a.messages, Message{
		Role:    "user",
		Content: userMessage,
	})

	for {
		resp, err := a.provider.Chat(ctx, "", a.messages, a.Tools)
		if err != nil {
			return err
		}

		if resp.Content != "" {
			fmt.Printf("\n🤖 Assistant: %s\n", resp.Content)
		}

		// Build assistant message content
		var assistantContent []ContentBlock
		if resp.Content != "" {
			assistantContent = append(assistantContent, ContentBlock{
				Type: "text",
				Text: resp.Content,
			})
		}
		for _, tc := range resp.ToolCalls {
			assistantContent = append(assistantContent, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Arguments,
			})
		}

		a.messages = append(a.messages, Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		if resp.Finished {
			break
		}

		// Execute tool calls
		if len(resp.ToolCalls) > 0 {
			var toolResults []ContentBlock
			for _, tc := range resp.ToolCalls {
				fmt.Printf("\n🔧 [Tool Call: %s]\n", tc.Name)
				fmt.Printf("   Arguments: %v\n", tc.Arguments)

				result, err := a.mcp.CallTool(ctx, tc.Name, tc.Arguments)
				if err != nil {
					result = fmt.Sprintf("Error: %v", err)
				}
				fmt.Printf("   ✅ Result: %s\n", result)

				toolResults = append(toolResults, ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				})
			}

			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: toolResults,
			})
		}
	}

	return nil
}
