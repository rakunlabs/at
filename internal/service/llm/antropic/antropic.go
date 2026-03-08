package antropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
)

const DefaultBaseURL = "https://api.anthropic.com"

// DefaultMaxTokens is the default max_tokens value sent to the Anthropic API.
// Anthropic requires max_tokens on every request, unlike other providers.
const DefaultMaxTokens = 4096

type Provider struct {
	APIKey    string
	Model     string
	MaxTokens int

	client      *klient.Client
	tokenSource TokenSource
}

// Option configures the Provider.
type Option func(*Provider)

// WithTokenSource sets a token source for per-request authentication.
// When set, the token source is called before each request and the returned
// token is used as Authorization: Bearer, overriding the static X-Api-Key.
func WithTokenSource(ts TokenSource) Option {
	return func(p *Provider) {
		p.tokenSource = ts
	}
}

// WithMaxTokens sets the default max_tokens value for requests.
// If not set, DefaultMaxTokens (4096) is used.
func WithMaxTokens(n int) Option {
	return func(p *Provider) {
		p.MaxTokens = n
	}
}

// SetTokenRefreshCallback wires a callback on the provider's OAuthTokenSource
// (if present) so that refreshed tokens can be persisted to the store.
// This is a no-op if the provider does not use an OAuthTokenSource.
func (p *Provider) SetTokenRefreshCallback(fn TokenRefreshCallback) {
	if ts, ok := p.tokenSource.(*OAuthTokenSource); ok {
		ts.SetRefreshCallback(fn)
	}
}

type AnthropicResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Error      Error          `json:"error"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Response structures
type ContentBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	Thinking string         `json:"thinking"`
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Input    map[string]any `json:"input"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, opts ...Option) (*Provider, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	headers := http.Header{
		"Anthropic-Version": []string{"2023-06-01"},
		"Content-Type":      []string{"application/json"},
	}
	if apiKey != "" {
		headers["X-Api-Key"] = []string{apiKey}
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(baseURL),
		klient.WithLogger(slog.Default()),
		klient.WithDisableRetry(true),
		klient.WithDisableEnvValues(true),
		klient.WithHeaderSet(headers),
	}
	if proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(proxy))
	}
	if insecureSkipVerify {
		klientOpts = append(klientOpts, klient.WithInsecureSkipVerify(true))
	}

	client, err := klient.New(klientOpts...)
	if err != nil {
		return nil, err
	}

	p := &Provider{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: DefaultMaxTokens,
		client:    client,
	}

	for _, o := range opts {
		o(p)
	}

	// Ensure max_tokens has a sane minimum.
	if p.MaxTokens <= 0 {
		p.MaxTokens = DefaultMaxTokens
	}

	return p, nil
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)

	jsonData, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// If a token source is configured, get a fresh token and use Bearer auth
	// instead of the static X-Api-Key header.
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		req.Header.Del("X-Api-Key")
	}

	var result AnthropicResponse
	var headers http.Header
	if err := p.client.Do(req, func(r *http.Response) error {
		headers = r.Header
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(bodyData, &result); err != nil {
			return fmt.Errorf("failed to decode response: %w (body: %s)", err, string(bodyData))
		}

		return nil
	}); err != nil {
		return nil, err
	}

	llmResp := &service.LLMResponse{
		Finished: result.StopReason != "tool_use",
		Header:   headers,
	}

	if result.Type == "error" {
		llmResp.Content = fmt.Sprintf("Error from Anthropic: %s", result.Error.Message)

		return llmResp, nil
	}

	// Map upstream usage to the internal Usage struct.
	llmResp.Usage = service.Usage{
		PromptTokens:     result.Usage.InputTokens,
		CompletionTokens: result.Usage.OutputTokens,
		TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
	}

	for _, block := range result.Content {
		switch block.Type {
		case "thinking":
			llmResp.ReasoningContent += block.Thinking
		case "text":
			llmResp.Content += block.Text
		case "tool_use":
			input := block.Input
			if input == nil {
				input = map[string]any{}
			}
			llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: input,
			})
		}
	}

	return llmResp, nil
}

// ─── Streaming ───

// Anthropic SSE event types for streaming.
type streamEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`

	// For content_block_start
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type thinkingDelta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type toolInputDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type messageDelta struct {
	StopReason string `json:"stop_reason"`
	Usage      *Usage `json:"usage,omitempty"` // output_tokens on message_delta
}

// messageStartBody is the top-level structure of an Anthropic message_start event.
type messageStartBody struct {
	Type    string               `json:"type"`
	Message *messageStartMessage `json:"message,omitempty"`
}

type messageStartMessage struct {
	Usage *Usage `json:"usage,omitempty"` // input_tokens on message_start
}

// ChatStream implements service.LLMStreamProvider for Anthropic's SSE format.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)
	reqBody["stream"] = true

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, err
	}

	// Override auth header when a token source is configured (see Chat() comment).
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		req.Header.Del("X-Api-Key")
	}

	// Use the klient's HTTP client directly for streaming.
	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyData, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Track the current content block for tool_use streaming.
		// Anthropic streams tool input as partial JSON fragments that
		// need to be accumulated and parsed at the end.
		var currentToolID string
		var currentToolName string
		var toolInputBuf strings.Builder

		// Track whether the current content block is a thinking block.
		var inThinkingBlock bool

		// Accumulate token usage from message_start and message_delta events.
		var usageInputTokens int
		var usageOutputTokens int

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line size (images can produce large SSE events)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// We only care about data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			var event streamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE event: %w", err)}
				return
			}

			switch event.Type {
			case "message_start":
				// message_start contains initial usage (input_tokens).
				var msb messageStartBody
				if err := json.Unmarshal([]byte(data), &msb); err == nil && msb.Message != nil && msb.Message.Usage != nil {
					usageInputTokens = msb.Message.Usage.InputTokens
				}

			case "content_block_start":
				// A new content block is starting. Track its type so deltas
				// can be routed correctly.
				if event.ContentBlock != nil {
					switch event.ContentBlock.Type {
					case "tool_use":
						currentToolID = event.ContentBlock.ID
						currentToolName = event.ContentBlock.Name
						toolInputBuf.Reset()
						inThinkingBlock = false
					case "thinking":
						inThinkingBlock = true
					default:
						inThinkingBlock = false
					}
				}

			case "content_block_delta":
				if len(event.Delta) == 0 {
					continue
				}

				// Try thinking delta first (when inside a thinking block).
				if inThinkingBlock {
					var tkd thinkingDelta
					if err := json.Unmarshal(event.Delta, &tkd); err == nil && tkd.Type == "thinking_delta" {
						ch <- service.StreamChunk{ReasoningContent: tkd.Thinking}
						continue
					}
				}

				// Try text delta first
				var td textDelta
				if err := json.Unmarshal(event.Delta, &td); err == nil && td.Type == "text_delta" {
					ch <- service.StreamChunk{Content: td.Text}
					continue
				}

				// Try tool input delta
				var tid toolInputDelta
				if err := json.Unmarshal(event.Delta, &tid); err == nil && tid.Type == "input_json_delta" {
					toolInputBuf.WriteString(tid.PartialJSON)
				}

			case "content_block_stop":
				// If we were accumulating tool input, parse and emit it now.
				if currentToolID != "" {
					args := map[string]any{}
					if toolInputBuf.Len() > 0 {
						json.Unmarshal([]byte(toolInputBuf.String()), &args)
					}
					ch <- service.StreamChunk{
						ToolCalls: []service.ToolCall{{
							ID:        currentToolID,
							Name:      currentToolName,
							Arguments: args,
						}},
					}
					currentToolID = ""
					currentToolName = ""
					toolInputBuf.Reset()
				}

			case "message_delta":
				if len(event.Delta) == 0 {
					continue
				}
				var md messageDelta
				if err := json.Unmarshal(event.Delta, &md); err == nil {
					if md.Usage != nil {
						usageOutputTokens = md.Usage.OutputTokens
					}
					if md.StopReason != "" {
						finishReason := "stop"
						if md.StopReason == "tool_use" {
							finishReason = "tool_calls"
						}
						ch <- service.StreamChunk{FinishReason: finishReason}
					}
				}

			case "message_stop":
				// Emit accumulated usage on the final event.
				total := usageInputTokens + usageOutputTokens
				ch <- service.StreamChunk{
					Usage: &service.Usage{
						PromptTokens:     usageInputTokens,
						CompletionTokens: usageOutputTokens,
						TotalTokens:      total,
					},
				}
				return

			case "error":
				var errMsg struct {
					Error Error `json:"error"`
				}
				if err := json.Unmarshal([]byte(data), &errMsg); err == nil {
					ch <- service.StreamChunk{Error: fmt.Errorf("anthropic error: %s", errMsg.Error.Message)}
				} else {
					ch <- service.StreamChunk{Error: fmt.Errorf("anthropic stream error: %s", data)}
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
		}
	}()

	return ch, resp.Header, nil
}

func (p *Provider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	// Anthropic base URL is default "https://api.anthropic.com".
	baseURL := DefaultBaseURL

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	targetURL, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL = targetURL
			req.Host = targetURL.Host

			// Auth
			if p.tokenSource != nil {
				token, err := p.tokenSource.Token(req.Context())
				if err != nil {
					slog.Error("failed to get auth token in proxy", "error", err)
				} else {
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("anthropic-beta", "oauth-2025-04-20")
					req.Header.Del("x-api-key")
				}
				req.Header.Set("anthropic-version", "2023-06-01")
			} else if p.APIKey != "" {
				req.Header.Set("x-api-key", p.APIKey)
				req.Header.Set("anthropic-version", "2023-06-01")
			}
		},
		Transport: p.client.HTTP.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == context.Canceled {
				// Client disconnected
				return
			}
			slog.Error("proxy error", "error", err)
			http.Error(w, fmt.Sprintf("proxy error: %v", err), http.StatusBadGateway)
		},
	}

	// Disable retries for proxy requests
	ctx := klient.CtxWithRetryPolicy(r.Context(), klient.OptionRetry.WithRetryDisable())
	r = r.WithContext(ctx)

	proxy.ServeHTTP(w, r)
	return nil
}

// buildRequestBody creates the common request body for Chat and ChatStream.
func (p *Provider) buildRequestBody(model string, messages []service.Message, tools []service.Tool) map[string]any {
	anthropicTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		anthropicTools[i] = map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		}
	}

	// Extract system messages — Anthropic uses a top-level "system" parameter
	// rather than including system messages in the messages array.
	var systemPrompt string
	var filteredMessages []service.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			if s, ok := msg.Content.(string); ok {
				if systemPrompt != "" {
					systemPrompt += "\n"
				}
				systemPrompt += s
			}
		} else {
			filteredMessages = append(filteredMessages, msg)
		}
	}

	// Convert content blocks to raw maps so we control exactly which fields
	// are present.  Anthropic requires "input" on tool_use blocks, but Go's
	// omitempty drops empty maps, so struct serialization can't guarantee it.
	for i := range filteredMessages {
		filteredMessages[i].Content = convertContent(filteredMessages[i].Content)
	}

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": p.MaxTokens,
		"messages":   filteredMessages,
	}
	if systemPrompt != "" {
		reqBody["system"] = systemPrompt
	}
	if len(tools) > 0 {
		reqBody["tools"] = anthropicTools
	}

	return reqBody
}

// convertContent ensures tool_use content blocks always have the "input"
// field.  It converts []service.ContentBlock to []map[string]any so that
// json.Marshal cannot drop the field via omitempty.
func convertContent(content any) any {
	switch blocks := content.(type) {
	case []service.ContentBlock:
		out := make([]map[string]any, 0, len(blocks))
		for _, b := range blocks {
			out = append(out, contentBlockToMap(b))
		}
		return out
	case []any:
		for _, b := range blocks {
			if m, ok := b.(map[string]any); ok {
				if m["type"] == "tool_use" {
					if _, has := m["input"]; !has {
						m["input"] = map[string]any{}
					}
				}
			}
		}
		return blocks
	default:
		return content
	}
}

func contentBlockToMap(b service.ContentBlock) map[string]any {
	switch b.Type {
	case "tool_use":
		input := b.Input
		if input == nil {
			input = map[string]any{}
		}
		m := map[string]any{
			"type":  "tool_use",
			"id":    b.ID,
			"name":  b.Name,
			"input": input,
		}
		return m
	case "tool_result":
		m := map[string]any{
			"type":        "tool_result",
			"tool_use_id": b.ToolUseID,
		}
		if b.Content != "" {
			m["content"] = b.Content
		}
		return m
	case "image":
		m := map[string]any{"type": "image"}
		if b.Source != nil {
			m["source"] = b.Source
		}
		return m
	default: // "text", etc.
		m := map[string]any{"type": b.Type}
		if b.Text != "" {
			m["text"] = b.Text
		}
		return m
	}
}
