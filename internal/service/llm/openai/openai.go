package openai

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
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const DefaultBaseURL = "https://api.openai.com/v1/chat/completions"

type Provider struct {
	APIKey  string
	Model   string
	BaseURL string

	client      *klient.Client
	tokenSource TokenSource

	// limiter is shared by all callers of this provider; nil means no
	// rate limiting.
	limiter *ratelimit.Limiter
}

// Option configures the Provider.
type Option func(*Provider)

// WithTokenSource sets a token source for per-request authentication.
// When set, the token source is called before each request and the returned
// token is used as the Bearer token, overriding the static APIKey.
func WithTokenSource(ts TokenSource) Option {
	return func(p *Provider) {
		p.tokenSource = ts
	}
}

// WithRateLimiter attaches a per-provider rate limiter. All Chat and
// ChatStream calls will Acquire before issuing the upstream request.
// Pass nil (or omit the option) to disable limiting.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(p *Provider) {
		p.limiter = l
	}
}

// New creates an OpenAI-compatible provider.
//
// extraHeaders allows setting additional HTTP headers for providers that
// require them (e.g., GitHub Models recommends Accept and X-GitHub-Api-Version).
// proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL (e.g., "http://proxy:8080").
func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, extraHeaders map[string]string, opts ...Option) (*Provider, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	headers := http.Header{
		"Content-Type": []string{"application/json"},
	}
	if apiKey != "" {
		headers["Authorization"] = []string{"Bearer " + apiKey}
	}
	for k, v := range extraHeaders {
		headers[k] = []string{v}
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(baseURL),
		klient.WithLogger(slog.Default()),
		klient.WithHeaderSet(headers),
		klient.WithDisableRetry(true),
		klient.WithDisableEnvValues(true),
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
		APIKey:  apiKey,
		Model:   model,
		BaseURL: baseURL,
		client:  client,
	}

	for _, o := range opts {
		o(p)
	}

	return p, nil
}

type OpenAIResponse struct {
	Error   *OpenAIError `json:"error,omitempty"`
	Choices []Choice     `json:"choices"`
	Usage   *OpenAIUsage `json:"usage,omitempty"`
}

type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Choice struct {
	Message      ChoiceMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type ChoiceMessage struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
}

type ToolCall struct {
	// Index is set on streaming deltas so fragments of the same tool call
	// (which OpenAI splits across many SSE events) can be reassembled.
	// It is nil on non-streaming responses.
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	// Rate limit before issuing the request.
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens("", messages, tools))
	if err != nil {
		return nil, err
	}
	defer release()

	reqBody := p.buildRequestBody(model, messages, tools, opts)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// If a token source is configured, get a fresh token and set it on the
	// request. klient's TransportKlient only applies default headers when they
	// are not already present, so this overrides the static APIKey header.
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	var result OpenAIResponse
	var headers http.Header
	var statusCode int
	if err := p.client.Do(req, func(r *http.Response) error {
		headers = r.Header
		statusCode = r.StatusCode
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

	// Surface 429 as a typed error so the agent retry loop can honour
	// Retry-After. OpenAI-compatible providers return 429 with an error
	// body; we detect via status or error.type.
	if statusCode == http.StatusTooManyRequests ||
		(result.Error != nil && (result.Error.Type == "rate_limit_error" || result.Error.Type == "tokens" || result.Error.Type == "requests")) {
		msg := "rate limited"
		if result.Error != nil {
			msg = result.Error.Message
		}
		return nil, &service.RateLimitError{
			StatusCode: statusCode,
			RetryAfter: common.ParseRetryAfter(headers),
			Provider:   "openai",
			Message:    msg,
			Underlying: fmt.Errorf("openai-compatible API error (status %d): %s", statusCode, msg),
		}
	}

	if result.Error != nil {
		return &service.LLMResponse{
			Content:  fmt.Sprintf("Error from provider: %s", result.Error.Message),
			Finished: true,
		}, nil
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response choices from provider")
	}

	choice := result.Choices[0]
	llmResp := &service.LLMResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		Finished:         choice.FinishReason != "tool_calls",
		Header:           headers,
	}

	if result.Usage != nil {
		llmResp.Usage = service.Usage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	for _, tc := range choice.Message.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("failed to parse tool call arguments: %w", err)
		}

		llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return llmResp, nil
}

// ─── Streaming ───

// streamChoice is the SSE chunk format returned by OpenAI-compatible APIs.
type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type streamResponse struct {
	Error   *OpenAIError   `json:"error,omitempty"`
	Choices []streamChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

// ChatStream implements service.LLMStreamProvider for true SSE streaming.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}

	// Rate limit before issuing the request.
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens("", messages, tools))
	if err != nil {
		return nil, nil, err
	}
	releaseOnce := func() {
		if release != nil {
			release()
			release = nil
		}
	}

	reqBody := p.buildRequestBody(model, messages, tools, opts)
	reqBody["stream"] = true
	reqBody["stream_options"] = map[string]any{"include_usage": true}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		releaseOnce()
		return nil, nil, err
	}

	// Override auth header when a token source is configured (see Chat() comment).
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			releaseOnce()
			return nil, nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Use the klient's HTTP client which has transport with headers and base URL.
	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		defer releaseOnce()
		bodyData, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, nil, &service.RateLimitError{
				StatusCode: resp.StatusCode,
				RetryAfter: common.ParseRetryAfter(resp.Header),
				Provider:   "openai",
				Message:    string(bodyData),
				Underlying: fmt.Errorf("provider returned status %d: %s", resp.StatusCode, string(bodyData)),
			}
		}
		return nil, nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()
		defer releaseOnce()

		// OpenAI streams tool calls as many small deltas: the first
		// carries id+name with empty arguments; subsequent deltas carry
		// partial argument string fragments keyed by the same "index".
		// We must accumulate per-index fragments and emit only the
		// completed tool call(s) when the stream signals finish — emitting
		// each fragment as its own ToolCall (the old behaviour) produces
		// garbage downstream (nil Arguments, duplicate IDs, etc.) which
		// in turn causes Anthropic/MiniMax to reject a re-translated
		// request with "invalid function arguments json string".
		type toolAccum struct {
			id        string
			name      string
			arguments strings.Builder
		}
		var toolOrder []int
		toolsByIndex := map[int]*toolAccum{}

		flushToolCalls := func() []service.ToolCall {
			if len(toolOrder) == 0 {
				return nil
			}
			out := make([]service.ToolCall, 0, len(toolOrder))
			for _, idx := range toolOrder {
				t := toolsByIndex[idx]
				args := map[string]any{}
				if s := t.arguments.String(); s != "" {
					_ = json.Unmarshal([]byte(s), &args)
				}
				out = append(out, service.ToolCall{
					ID:        t.id,
					Name:      t.name,
					Arguments: args,
				})
			}
			// Reset so a second tool-call burst in the same stream starts clean.
			toolOrder = toolOrder[:0]
			toolsByIndex = map[int]*toolAccum{}
			return out
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line size (images can produce large SSE events)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// SSE data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// End of stream
			if data == "[DONE]" {
				if tcs := flushToolCalls(); len(tcs) > 0 {
					ch <- service.StreamChunk{ToolCalls: tcs}
				}
				return
			}

			var sr streamResponse
			if err := json.Unmarshal([]byte(data), &sr); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE chunk: %w", err)}
				return
			}

			if sr.Error != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("provider error: %s", sr.Error.Message)}
				return
			}

			// OpenAI sends a final chunk with empty choices but populated
			// usage when stream_options.include_usage is set. Capture it.
			if len(sr.Choices) == 0 {
				if sr.Usage != nil {
					ch <- service.StreamChunk{
						Usage: &service.Usage{
							PromptTokens:     sr.Usage.PromptTokens,
							CompletionTokens: sr.Usage.CompletionTokens,
							TotalTokens:      sr.Usage.TotalTokens,
						},
					}
				}
				continue
			}

			choice := sr.Choices[0]

			// Accumulate tool-call fragments by index.
			for i, tc := range choice.Delta.ToolCalls {
				// Some upstreams omit the index field on single-tool
				// responses; fall back to positional index.
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc, ok := toolsByIndex[idx]
				if !ok {
					acc = &toolAccum{}
					toolsByIndex[idx] = acc
					toolOrder = append(toolOrder, idx)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.arguments.WriteString(tc.Function.Arguments)
				}
			}

			chunk := service.StreamChunk{
				Content:          choice.Delta.Content,
				ReasoningContent: choice.Delta.ReasoningContent,
			}

			if choice.FinishReason != nil {
				chunk.FinishReason = *choice.FinishReason
				// Flush accumulated tool calls alongside the finish signal
				// so downstream code sees them before/with the terminator.
				if tcs := flushToolCalls(); len(tcs) > 0 {
					chunk.ToolCalls = tcs
				}
			}

			// Don't forward "empty" fragment chunks. Pure tool-call
			// deltas are accumulated silently and emitted at finish.
			if chunk.Content == "" && chunk.ReasoningContent == "" && len(chunk.ToolCalls) == 0 && chunk.FinishReason == "" {
				continue
			}

			ch <- chunk
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
			return
		}

		// Scanner exited cleanly without a [DONE] marker — flush any
		// tool calls we accumulated so they aren't silently dropped.
		if tcs := flushToolCalls(); len(tcs) > 0 {
			ch <- service.StreamChunk{ToolCalls: tcs}
		}
	}()

	return ch, resp.Header, nil
}

func (p *Provider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	// Clean up path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// For OpenAI, BaseURL is typically "https://api.openai.com/v1/chat/completions".
	// We want to proxy to other endpoints like "/v1/files".
	// So we need to intelligently strip the suffix.
	baseURL := p.BaseURL
	if strings.HasSuffix(baseURL, "/chat/completions") {
		baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	} else if strings.HasSuffix(baseURL, "/v1") {
		// Keep /v1 as root for most calls
	} else {
		// Just append path if it's a generic base
	}

	// Handle case where path starts with /v1/ and base ends with /v1
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(path, "/v1/") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
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
				}
			} else if p.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+p.APIKey)
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
func (p *Provider) buildRequestBody(model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) map[string]any {
	openaiTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		openaiTools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  service.SanitizeSchema(tool.InputSchema),
			},
		}
	}

	var reqMessages []any
	for _, msg := range messages {
		switch c := msg.Content.(type) {
		case map[string]any:
			// Gateway passthrough — already in OpenAI wire format.
			reqMessages = append(reqMessages, c)
		case []service.ContentBlock:
			// ContentBlock messages from agent_call / Agent.Run().
			// Convert from Anthropic-style content blocks to OpenAI format.
			for _, m := range common.ConvertContentBlocksToOpenAI(msg.Role, c) {
				reqMessages = append(reqMessages, m)
			}
		default:
			// Plain string or other content.
			reqMessages = append(reqMessages, map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": reqMessages,
	}
	if len(tools) > 0 {
		reqBody["tools"] = openaiTools
	}

	// Apply per-request generation options.
	if opts != nil {
		if opts.MaxCompletionTokens != nil {
			reqBody["max_completion_tokens"] = *opts.MaxCompletionTokens
		} else if opts.MaxTokens != nil {
			reqBody["max_tokens"] = *opts.MaxTokens
		}
		if opts.Temperature != nil {
			reqBody["temperature"] = *opts.Temperature
		}
		if opts.TopP != nil {
			reqBody["top_p"] = *opts.TopP
		}
		if len(opts.Stop) > 0 {
			if len(opts.Stop) == 1 {
				reqBody["stop"] = opts.Stop[0]
			} else {
				reqBody["stop"] = opts.Stop
			}
		}
		if opts.Seed != nil {
			reqBody["seed"] = *opts.Seed
		}
		if len(opts.ResponseFormat) > 0 {
			reqBody["response_format"] = opts.ResponseFormat
		}
		if opts.ReasoningEffort != "" {
			reqBody["reasoning_effort"] = opts.ReasoningEffort
		}
		if len(opts.WebSearchOptions) > 0 {
			reqBody["web_search_options"] = opts.WebSearchOptions
		}
	}

	return reqBody
}
