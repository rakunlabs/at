package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
)

// Google Generative Language API (generativelanguage.googleapis.com)
// Native Gemini API with API key authentication.
//
// Non-streaming:  POST /v1beta/models/{model}:generateContent
// Streaming:      POST /v1beta/models/{model}:streamGenerateContent?alt=sse

const defaultBaseURL = "https://generativelanguage.googleapis.com"

// Provider implements service.LLMProvider and service.LLMStreamProvider
// for the Google Generative Language API (generativelanguage.googleapis.com).
type Provider struct {
	Model   string
	BaseURL string
	APIKey  string
	client  *klient.Client
}

// New creates a Google AI (Gemini) provider.
//
// apiKey is the API key from Google AI Studio (aistudio.google.com).
// model is the default model (e.g., "gemini-2.5-flash").
// baseURL optionally overrides the default "https://generativelanguage.googleapis.com".
// proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL. If empty, no proxy is used.
func New(apiKey, model, baseURL, proxy string) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini provider requires an api_key (get one from https://aistudio.google.com/apikey)")
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(baseURL),
		klient.WithDisableBaseURLCheck(true),
		klient.WithLogger(slog.Default()),
		klient.WithHeaderSet(http.Header{
			"Content-Type":   []string{"application/json"},
			"x-goog-api-key": []string{apiKey},
		}),
	}
	if proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(proxy))
	}

	client, err := klient.New(klientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	return &Provider{
		Model:   model,
		BaseURL: baseURL,
		APIKey:  apiKey,
		client:  client,
	}, nil
}

// ─── Google API types ───

// generateContentRequest is the native Google Generative Language API request.
type generateContentRequest struct {
	Contents          []content         `json:"contents"`
	Tools             []googleTool      `json:"tools,omitempty"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

type functionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type googleTool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
}

type functionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// generateContentResponse is the native Google Generative Language API response.
type generateContentResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
	Error         *googleError   `json:"error,omitempty"`
}

type candidate struct {
	Content       *content `json:"content,omitempty"`
	FinishReason  string   `json:"finishReason,omitempty"`
	SafetyRatings []any    `json:"safetyRatings,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type googleError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ─── Chat (non-streaming) ───

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequest(messages, tools)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("/v1beta/models/%s:generateContent", model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	var result generateContentResponse
	if err := p.client.Do(req, func(r *http.Response) error {
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		if r.StatusCode != http.StatusOK {
			var errResp generateContentResponse
			if json.Unmarshal(bodyData, &errResp) == nil && errResp.Error != nil {
				result = errResp
				return nil
			}
			return fmt.Errorf("gemini returned status %d: %s", r.StatusCode, string(bodyData))
		}

		if err := json.Unmarshal(bodyData, &result); err != nil {
			return fmt.Errorf("failed to decode response: %w (body: %s)", err, string(bodyData))
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if result.Error != nil {
		return &service.LLMResponse{
			Content:  fmt.Sprintf("Error from Gemini API: %s (code: %d, status: %s)", result.Error.Message, result.Error.Code, result.Error.Status),
			Finished: true,
		}, nil
	}

	return parseResponse(&result)
}

// ─── Streaming ───

// ChatStream implements service.LLMStreamProvider using Google's
// streamGenerateContent endpoint with alt=sse for server-sent events.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (<-chan service.StreamChunk, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequest(messages, tools)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("/v1beta/models/%s:streamGenerateContent?alt=sse", model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// Use the klient's HTTP client directly for streaming.
	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyData, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line size (images can produce large SSE events)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and SSE comments.
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			var sr generateContentResponse
			if err := json.Unmarshal([]byte(data), &sr); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE chunk: %w", err)}
				return
			}

			if sr.Error != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("gemini error: %s (code: %d)", sr.Error.Message, sr.Error.Code)}
				return
			}

			if len(sr.Candidates) == 0 {
				continue
			}

			cand := sr.Candidates[0]
			chunk := service.StreamChunk{}

			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					if p.Text != "" {
						chunk.Content += p.Text
					}
					if p.FunctionCall != nil {
						chunk.ToolCalls = append(chunk.ToolCalls, service.ToolCall{
							ID:        generateToolCallID(p.FunctionCall.Name),
							Name:      p.FunctionCall.Name,
							Arguments: p.FunctionCall.Args,
						})
					}
				}
			}

			if cand.FinishReason != "" {
				switch cand.FinishReason {
				case "STOP", "MAX_TOKENS":
					chunk.FinishReason = "stop"
				default:
					// Other reasons like SAFETY, RECITATION, etc.
					chunk.FinishReason = "stop"
				}
				// If there are tool calls, mark it as tool_calls finish reason.
				if len(chunk.ToolCalls) > 0 {
					chunk.FinishReason = "tool_calls"
				}
			}

			ch <- chunk
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
		}
	}()

	return ch, nil
}

// ─── Request building ───

// buildRequest translates internal service types to Google's native API format.
func (p *Provider) buildRequest(messages []service.Message, tools []service.Tool) *generateContentRequest {
	req := &generateContentRequest{}

	// Convert tools to Google's functionDeclarations format.
	if len(tools) > 0 {
		decls := make([]functionDeclaration, len(tools))
		for i, tool := range tools {
			decls[i] = functionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			}
		}
		req.Tools = []googleTool{{FunctionDeclarations: decls}}
	}

	// Convert messages to Google's contents format.
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// System messages become systemInstruction.
			text := extractText(msg.Content)
			if text != "" {
				req.SystemInstruction = &content{
					Parts: []part{{Text: text}},
				}
			}

		case "user":
			parts := convertToParts(msg)
			if len(parts) > 0 {
				req.Contents = append(req.Contents, content{
					Role:  "user",
					Parts: parts,
				})
			}

		case "assistant":
			parts := convertToParts(msg)
			if len(parts) > 0 {
				req.Contents = append(req.Contents, content{
					Role:  "model",
					Parts: parts,
				})
			}

		case "tool":
			// Tool results: in the OpenAI format these come as role=tool messages.
			// In Google's format, they become user messages with functionResponse parts.
			parts := convertToolResultToParts(msg)
			if len(parts) > 0 {
				req.Contents = append(req.Contents, content{
					Role:  "user",
					Parts: parts,
				})
			}
		}
	}

	return req
}

// convertToParts converts a service.Message's content to Google API parts.
func convertToParts(msg service.Message) []part {
	switch c := msg.Content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []part{{Text: c}}

	case []any:
		// Array of content blocks (OpenAI multi-part format).
		var parts []part
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}

			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				if text, ok := block["text"].(string); ok && text != "" {
					parts = append(parts, part{Text: text})
				}
			case "image_url":
				// OpenAI-format image block: {type:"image_url", image_url:{url:"data:..."}}
				imageURL, _ := block["image_url"].(map[string]any)
				if imageURL == nil {
					continue
				}
				url, _ := imageURL["url"].(string)
				mimeType, data := parseGeminiDataURL(url)
				if data != "" {
					parts = append(parts, part{
						InlineData: &inlineData{MimeType: mimeType, Data: data},
					})
				}
			case "tool_use":
				// Assistant's tool call -> functionCall part.
				name, _ := block["name"].(string)
				args, _ := block["input"].(map[string]any)
				if name != "" {
					parts = append(parts, part{
						FunctionCall: &functionCall{
							Name: name,
							Args: args,
						},
					})
				}
			case "tool_result":
				// Tool result -> functionResponse part.
				name, _ := block["name"].(string)
				toolContent, _ := block["content"].(string)
				toolUseID, _ := block["tool_use_id"].(string)
				if name == "" {
					name = toolUseID
				}
				parts = append(parts, part{
					FunctionResponse: &functionResponse{
						Name: name,
						Response: map[string]any{
							"result": toolContent,
						},
					},
				})
			}
		}
		return parts

	case []service.ContentBlock:
		var parts []part
		for _, block := range c {
			switch block.Type {
			case "text":
				if block.Text != "" {
					parts = append(parts, part{Text: block.Text})
				}
			case "image":
				// Anthropic-format image block with Source field
				if block.Source != nil && block.Source.Data != "" {
					parts = append(parts, part{
						InlineData: &inlineData{
							MimeType: block.Source.MediaType,
							Data:     block.Source.Data,
						},
					})
				}
			case "tool_use":
				if block.Name != "" {
					parts = append(parts, part{
						FunctionCall: &functionCall{
							Name: block.Name,
							Args: block.Input,
						},
					})
				}
			case "tool_result":
				name := block.Name
				if name == "" {
					name = block.ToolUseID
				}
				parts = append(parts, part{
					FunctionResponse: &functionResponse{
						Name: name,
						Response: map[string]any{
							"result": block.Content,
						},
					},
				})
			}
		}
		return parts

	case map[string]any:
		// Single pre-built message (passthrough from gateway).
		// Extract role/content if present.
		if contentArr, ok := c["content"].([]any); ok {
			// Content may be an array of content blocks (multi-part with images).
			var parts []part
			hasNonText := false
			for _, item := range contentArr {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := block["type"].(string)
				if blockType != "text" {
					hasNonText = true
					break
				}
			}
			if hasNonText {
				for _, item := range contentArr {
					block, ok := item.(map[string]any)
					if !ok {
						continue
					}
					blockType, _ := block["type"].(string)
					switch blockType {
					case "text":
						if text, ok := block["text"].(string); ok && text != "" {
							parts = append(parts, part{Text: text})
						}
					case "image_url":
						imageURL, _ := block["image_url"].(map[string]any)
						if imageURL == nil {
							continue
						}
						url, _ := imageURL["url"].(string)
						mimeType, data := parseGeminiDataURL(url)
						if data != "" {
							parts = append(parts, part{
								InlineData: &inlineData{MimeType: mimeType, Data: data},
							})
						}
					}
				}
				return parts
			}
		}
		if text, ok := c["content"].(string); ok && text != "" {
			return []part{{Text: text}}
		}
		// Handle OpenAI-style tool_calls in assistant messages.
		if toolCalls, ok := c["tool_calls"].([]any); ok {
			var parts []part
			// Add text content if present.
			if text, ok := c["content"].(string); ok && text != "" {
				parts = append(parts, part{Text: text})
			}
			for _, tc := range toolCalls {
				tcMap, ok := tc.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := tcMap["function"].(map[string]any)
				if !ok {
					continue
				}
				name, _ := fn["name"].(string)
				argsStr, _ := fn["arguments"].(string)
				var args map[string]any
				if argsStr != "" {
					json.Unmarshal([]byte(argsStr), &args)
				}
				parts = append(parts, part{
					FunctionCall: &functionCall{
						Name: name,
						Args: args,
					},
				})
			}
			return parts
		}
		return nil

	default:
		return nil
	}
}

// convertToolResultToParts converts a role=tool message to functionResponse parts.
func convertToolResultToParts(msg service.Message) []part {
	switch c := msg.Content.(type) {
	case string:
		// OpenAI style: role=tool with string content. We need the tool name
		// but it's typically encoded in the message. Use a generic name.
		return []part{{
			FunctionResponse: &functionResponse{
				Name:     "tool",
				Response: map[string]any{"result": c},
			},
		}}

	case map[string]any:
		// Passthrough message that includes tool_call_id, content, etc.
		name, _ := c["name"].(string)
		if name == "" {
			name, _ = c["tool_call_id"].(string)
		}
		if name == "" {
			name = "tool"
		}
		content, _ := c["content"].(string)
		return []part{{
			FunctionResponse: &functionResponse{
				Name:     name,
				Response: map[string]any{"result": content},
			},
		}}

	default:
		return nil
	}
}

// ─── Response parsing ───

// parseResponse converts a Google API response to the internal LLMResponse.
func parseResponse(resp *generateContentResponse) (*service.LLMResponse, error) {
	if resp.Error != nil {
		return &service.LLMResponse{
			Content:  fmt.Sprintf("Error from Gemini API: %s (code: %d, status: %s)", resp.Error.Message, resp.Error.Code, resp.Error.Status),
			Finished: true,
		}, nil
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no response candidates from Gemini API")
	}

	cand := resp.Candidates[0]
	llmResp := &service.LLMResponse{
		Finished: true,
	}

	if cand.Content != nil {
		for _, p := range cand.Content.Parts {
			if p.Text != "" {
				llmResp.Content += p.Text
			}
			if p.FunctionCall != nil {
				llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
					ID:        generateToolCallID(p.FunctionCall.Name),
					Name:      p.FunctionCall.Name,
					Arguments: p.FunctionCall.Args,
				})
			}
		}
	}

	// If there are tool calls, the response is not finished (needs tool execution).
	if len(llmResp.ToolCalls) > 0 {
		llmResp.Finished = false
	}

	return llmResp, nil
}

// ─── Helpers ───

// extractText gets a plain text string from a message content that may be
// a string or a structured type.
func extractText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case map[string]any:
		if text, ok := c["content"].(string); ok {
			return text
		}
	}
	return ""
}

// generateToolCallID creates a deterministic tool call ID from the function name.
// Google's API doesn't provide tool call IDs like OpenAI does, so we generate one.
func generateToolCallID(name string) string {
	return "call_" + name
}

// parseGeminiDataURL splits a data URI (e.g. "data:image/png;base64,iVBOR...")
// into its MIME type and base64-encoded data.
func parseGeminiDataURL(url string) (mimeType, data string) {
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
