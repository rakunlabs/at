package wire

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Anthropic Messages inbound wire shape ───

// AnthropicMessagesRequest is the Anthropic Messages API request body. It is the
// shape Claude Code, Cline, Roo and Kilo send natively, so accepting it is what
// lets them reach the gateway's routing, fallback, budgets and tracing instead of
// the opaque single-provider passthrough.
type AnthropicMessagesRequest struct {
	Model         string               `json:"model"`
	Messages      []AnthropicMessage   `json:"messages"`
	System        json.RawMessage      `json:"system,omitempty"` // string or []block
	MaxTokens     *int                 `json:"max_tokens"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Tools         []AnthropicTool      `json:"tools,omitempty"`
	ToolChoice    *AnthropicToolChoice `json:"tool_choice,omitempty"`
	Metadata      map[string]any       `json:"metadata,omitempty"`
	Thinking      map[string]any       `json:"thinking,omitempty"`

	// AT extensions, mirroring the OpenAI-shape endpoint.
	AtFallbacks []string `json:"at_fallbacks,omitempty"`
	TimeoutMs   int      `json:"timeout_ms,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []block
}

type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// AnthropicToolChoice is Anthropic's tool_choice object.
type AnthropicToolChoice struct {
	Type string `json:"type"` // "auto" | "any" | "tool" | "none"
	Name string `json:"name,omitempty"`
}

// AnthropicBlock is one element of a content array. It covers every block type
// an inbound request can carry, including the media that must survive routing.
type AnthropicBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result: Content is a string or a nested block array.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// image / document
	Source *service.MediaSource `json:"source,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// AnthropicSystemPrompt accepts the two forms the field takes: a plain string,
// or an array of text blocks (which is how clients attach cache breakpoints).
func AnthropicSystemPrompt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}

	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}

	return strings.Join(parts, "\n\n")
}

// anthropicMessageBlocks normalizes a message's content to a block list. A bare
// string becomes a single text block, which is what the wire shape means.
func anthropicMessageBlocks(raw json.RawMessage) []AnthropicBlock {
	if len(raw) == 0 {
		return nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}

		return []AnthropicBlock{{Type: "text", Text: s}}
	}

	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}

	return blocks
}

// ─── Cell 4: Anthropic inbound → Anthropic-family provider ───

// TranslateAnthropicMessages is a near-identity decode: service.ContentBlock is
// modelled on Anthropic's block shape, so this is the cell that costs nothing.
// It exists so an Anthropic-in / Anthropic-out request never round-trips through
// the OpenAI DTO, which would drop everything the OpenAI shape cannot express.
func TranslateAnthropicMessages(req *AnthropicMessagesRequest) (systemPrompt string, messages []service.Message) {
	systemPrompt = AnthropicSystemPrompt(req.System)

	for _, msg := range req.Messages {
		blocks := anthropicMessageBlocks(msg.Content)
		if len(blocks) == 0 {
			continue
		}

		converted := make([]service.ContentBlock, 0, len(blocks))
		for _, b := range blocks {
			converted = append(converted, anthropicBlockToService(b))
		}
		messages = append(messages, service.Message{Role: msg.Role, Content: converted})
	}

	return systemPrompt, messages
}

func anthropicBlockToService(b AnthropicBlock) service.ContentBlock {
	switch b.Type {
	case "tool_use":
		input := b.Input
		if input == nil {
			input = map[string]any{}
		}

		return service.ContentBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: input}

	case "tool_result":
		return service.ContentBlock{
			Type:      "tool_result",
			ToolUseID: b.ToolUseID,
			Content:   anthropicToolResultContent(b.Content),
		}

	case "image", "document", "audio", "video":
		return service.ContentBlock{Type: b.Type, Source: b.Source}

	case "thinking":
		return service.ContentBlock{Type: "thinking", Thinking: b.Thinking, Signature: b.Signature}
	}

	return service.ContentBlock{Type: "text", Text: b.Text}
}

// anthropicToolResultContent preserves a structured tool result. A string result
// stays a string; a block array is carried through so an image inside a tool
// result — a browser tool's screenshot — reaches the provider.
func anthropicToolResultContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return string(raw)
	}

	converted := make([]service.ContentBlock, 0, len(blocks))
	textOnly := true
	for _, b := range blocks {
		if b.Type != "text" && b.Type != "" {
			textOnly = false
		}
		converted = append(converted, anthropicBlockToService(b))
	}

	if textOnly {
		// Collapse to the string form so a text-only result is byte-identical
		// to what a plain string would have produced.
		var text strings.Builder
		for _, b := range converted {
			text.WriteString(b.Text)
		}

		return text.String()
	}

	return converted
}

// ─── Cell 3: Anthropic inbound → OpenAI-family provider ───

// TranslateAnthropicToOpenAI converts to the OpenAI inbound DTO so the existing
// OpenAI-family translation can serve the request. This is the only cell where a
// conversion genuinely has to happen; it is never used for an Anthropic target.
func TranslateAnthropicToOpenAI(req *AnthropicMessagesRequest) []OpenAIMessage {
	out := make([]OpenAIMessage, 0, len(req.Messages)+1)

	if system := AnthropicSystemPrompt(req.System); system != "" {
		out = append(out, OpenAIMessage{Role: "system", Content: jsonString(system)})
	}

	for _, msg := range req.Messages {
		blocks := anthropicMessageBlocks(msg.Content)
		if len(blocks) == 0 {
			continue
		}

		// Tool results become their own `tool` messages; OpenAI has no notion of
		// a user message that carries them.
		var (
			textParts []any
			toolCalls []OpenAIToolCall
			pending   []OpenAIMessage
		)
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				args, err := json.Marshal(b.Input)
				if err != nil {
					args = []byte("{}")
				}
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:       b.ID,
					Type:     "function",
					Function: OpenAIFunctionCall{Name: b.Name, Arguments: string(args)},
				})

			case "tool_result":
				pending = append(pending, OpenAIMessage{
					Role:       "tool",
					ToolCallID: b.ToolUseID,
					Content:    anthropicToolResultToOpenAI(b.Content),
				})

			case "image":
				if part := anthropicMediaToOpenAIPart(b.Source); part != nil {
					textParts = append(textParts, part)
				}

			case "document":
				if part := anthropicDocumentToOpenAIPart(b.Source); part != nil {
					textParts = append(textParts, part)
				}

			case "thinking":
				// Signed reasoning state is Anthropic-specific and has no
				// OpenAI equivalent; replaying it as text would corrupt the
				// turn, so it is dropped for this target only.

			default:
				if b.Text != "" {
					textParts = append(textParts, map[string]any{"type": "text", "text": b.Text})
				}
			}
		}

		if len(textParts) > 0 || len(toolCalls) > 0 {
			m := OpenAIMessage{Role: msg.Role, ToolCalls: toolCalls}
			if len(textParts) > 0 {
				m.Content = jsonValue(textParts)
			}
			out = append(out, m)
		}
		out = append(out, pending...)
	}

	return out
}

// anthropicToolResultToOpenAI renders a tool result as OpenAI tool-message
// content. Text-only results become a plain string, which is what every
// OpenAI-compatible server expects; a multimodal result becomes a parts array so
// the media is not discarded on the way.
func anthropicToolResultToOpenAI(raw json.RawMessage) json.RawMessage {
	content := anthropicToolResultContent(raw)

	switch v := content.(type) {
	case string:
		return jsonString(v)
	case []service.ContentBlock:
		parts := make([]any, 0, len(v))
		for _, b := range v {
			switch b.Type {
			case "image":
				if part := anthropicMediaToOpenAIPart(b.Source); part != nil {
					parts = append(parts, part)
				}
			case "document":
				if part := anthropicDocumentToOpenAIPart(b.Source); part != nil {
					parts = append(parts, part)
				}
			default:
				if b.Text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": b.Text})
				}
			}
		}

		return jsonValue(parts)
	}

	return jsonString("")
}

// anthropicMediaToOpenAIPart renders an image as an OpenAI image content part.
// A base64 source becomes a data URL, which is the only inline form the OpenAI
// shape has.
func anthropicMediaToOpenAIPart(source *service.MediaSource) map[string]any {
	url := mediaSourceToURL(source)
	if url == "" {
		return nil
	}

	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

// anthropicDocumentToOpenAIPart renders a document as an OpenAI file part.
func anthropicDocumentToOpenAIPart(source *service.MediaSource) map[string]any {
	url := mediaSourceToURL(source)
	if url == "" {
		return nil
	}

	filename := source.Filename
	if filename == "" {
		filename = "document"
	}

	return map[string]any{"type": "file", "file": map[string]any{"file_data": url, "filename": filename}}
}

// mediaSourceToURL renders a media source as the URL form OpenAI-family
// providers accept.
func mediaSourceToURL(source *service.MediaSource) string {
	if source == nil {
		return ""
	}
	switch {
	case source.Type == "url" && source.URL != "":
		return source.URL
	case source.Data != "":
		mediaType := source.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}

		return "data:" + mediaType + ";base64," + source.Data
	}

	return ""
}

// ─── Shared helpers ───

func jsonString(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}

	return raw
}

func jsonValue(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`""`)
	}

	return raw
}

// TranslateAnthropicTools converts inbound tool declarations.
func TranslateAnthropicTools(tools []AnthropicTool) []service.Tool {
	out := make([]service.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, service.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	return out
}

// AnthropicToolChoiceToOpenAI maps Anthropic's tool_choice to the OpenAI shape
// the internal ChatOptions carries. The adapters translate onwards from there,
// including back to Anthropic's own form for an Anthropic target — a round trip,
// but a lossless one over a four-value enum.
func AnthropicToolChoiceToOpenAI(choice *AnthropicToolChoice) any {
	if choice == nil {
		return nil
	}

	switch choice.Type {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		if choice.Name == "" {
			return "required"
		}

		return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}
	}

	return nil
}

// ValidateAnthropicRequest enforces the parts of the wire contract that are
// required rather than optional.
func ValidateAnthropicRequest(req *AnthropicMessagesRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if req.MaxTokens == nil {
		// Anthropic requires it; accepting the request and inventing a value
		// would silently change the output length the client asked for.
		return fmt.Errorf("max_tokens is required")
	}
	if *req.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be greater than zero")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages must not be empty")
	}

	return nil
}

// anthropicThinkingConfig maps the inbound thinking block. An explicit
// budget_tokens of 0 is preserved rather than erased: it means "no thinking",
// which is different from "unspecified".
func anthropicThinkingConfig(raw map[string]any) *service.ThinkingConfig {
	if len(raw) == 0 {
		return nil
	}

	cfg := &service.ThinkingConfig{Type: "enabled"}
	if t, ok := raw["type"].(string); ok && t != "" {
		cfg.Type = t
	}
	switch v := raw["budget_tokens"].(type) {
	case float64:
		cfg.BudgetTokens = int(v)
	case int:
		cfg.BudgetTokens = v
	}

	return cfg
}

// BuildAnthropicChatOptions maps the inbound generation parameters.
func BuildAnthropicChatOptions(req *AnthropicMessagesRequest) *service.ChatOptions {
	opts := &service.ChatOptions{
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}
	if choice := AnthropicToolChoiceToOpenAI(req.ToolChoice); choice != nil {
		opts.ToolChoice = choice
	}
	if thinking := anthropicThinkingConfig(req.Thinking); thinking != nil {
		opts.Thinking = thinking
	}
	if req.TopK != nil {
		if opts.ExtraBody == nil {
			opts.ExtraBody = map[string]any{}
		}
		// top_k has no field on ChatOptions and is Anthropic/Gemini-specific;
		// extra_body is the documented route for a provider-native field.
		opts.ExtraBody["top_k"] = *req.TopK
	}

	return opts
}
