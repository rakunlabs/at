package antropic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const DefaultBaseURL = "https://api.anthropic.com"

// DefaultMaxTokens is the default max_tokens value sent to the Anthropic API.
// Anthropic requires max_tokens on every request, unlike other providers.
const DefaultMaxTokens = 4096

type Provider struct {
	APIKey    string
	Model     string
	MaxTokens int

	Client      *klient.Client
	tokenSource TokenSource

	// limiter is shared by all callers of this provider; nil means no
	// rate limiting. When set, every Chat/ChatStream call must Acquire
	// before issuing the upstream request.
	limiter *ratelimit.Limiter

	// systemForEstimate is an optional default system prompt used by the
	// rate limiter to weight ITPM. Providers that build their own system
	// message internally don't need to set this.
	systemForEstimate string

	// session* fields back the X-Claude-Code-Session-Id header. Lazily
	// initialised the first time setOAuthHeaders runs so static-key
	// callers don't pay the UUID cost. The session ID is stable for
	// the lifetime of the Provider — Anthropic's billing pipeline uses
	// it to apply the correct per-session quota window instead of
	// treating each call as fresh untrusted traffic.
	sessionMu   sync.Mutex
	sessionUUID string
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

// WithRateLimiter attaches a per-provider rate limiter. All Chat and
// ChatStream calls will Acquire before issuing the upstream request.
// Pass nil (or omit the option) to disable limiting.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(p *Provider) {
		p.limiter = l
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

	// Apply options early so we know whether a tokenSource is configured
	// before building the klient default headers.
	p := &Provider{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: DefaultMaxTokens,
	}

	for _, o := range opts {
		o(p)
	}

	// Ensure max_tokens has a sane minimum.
	if p.MaxTokens <= 0 {
		p.MaxTokens = DefaultMaxTokens
	}

	headers := http.Header{
		"Anthropic-Version": []string{"2023-06-01"},
		"Content-Type":      []string{"application/json"},
	}
	// Only set X-Api-Key as a default header when using static key auth.
	// When a tokenSource is configured (e.g. OAuth), Bearer auth is used
	// per-request instead. We must NOT add X-Api-Key to the klient defaults
	// here, because klient only injects defaults when the header key is
	// absent from the request — so leaving it out ensures the OAuth paths
	// (which never set X-Api-Key) won't accidentally send an empty one.
	if apiKey != "" && p.tokenSource == nil {
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

	p.Client = client

	return p, nil
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	// Rate limit before issuing the request. Acquire returns immediately
	// when no limiter is configured (nil receiver is a no-op).
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens(p.systemForEstimate, messages, tools))
	if err != nil {
		return nil, err
	}
	defer release()

	reqBody := p.buildRequestBody(model, messages, tools, opts)
	jsonData, _ := json.Marshal(reqBody)

	// On the OAuth path Anthropic expects /v1/messages?beta=true. The
	// `?beta=true` query string activates the experimental message
	// envelope Claude Code uses; without it the request is rejected
	// before billing validation even runs. Static-API-key callers
	// don't get the query param (it changes accounting on that path).
	requestPath := "/v1/messages"
	if p.tokenSource != nil {
		requestPath = "/v1/messages?beta=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestPath, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// If a token source is configured, get a fresh token and use Bearer auth.
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", oauthBetaHeader(model, reqBody))
		p.setOAuthHeaders(req, model)
	}

	// Debug-log minimal request info for OAuth troubleshooting.
	if p.tokenSource != nil {
		slog.Debug("anthropic oauth request",
			"url", "/v1/messages",
			"model", model,
			"body_length", len(jsonData),
		)
	}

	var result AnthropicResponse
	var headers http.Header
	var statusCode int
	var rawBody string
	if err := p.Client.Do(req, func(r *http.Response) error {
		headers = r.Header
		statusCode = r.StatusCode
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		rawBody = string(bodyData)

		if err := json.Unmarshal(bodyData, &result); err != nil {
			return fmt.Errorf("failed to decode response (status %d): %w (body: %s)", r.StatusCode, err, rawBody)
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
		errMsg := result.Error.Message
		if errMsg == "" {
			errMsg = "unknown error"
		}
		slog.Error("anthropic API error",
			"error_type", result.Error.Type,
			"status", statusCode,
			"message", errMsg,
			"model", model,
		)
		// Surface 429 (rate_limit) and 529 (overloaded) as typed
		// *RateLimitError so the agent retry loop and the gateway
		// retry path can honour Retry-After. 529 is Anthropic's
		// non-standard "overloaded" status — empirically transient
		// and worth retrying just like a 429. The opencode-claude-auth
		// reference plugin does the same (retries 429+529 with
		// exponential backoff).
		if isAnthropicTransientStatus(statusCode) ||
			result.Error.Type == "rate_limit_error" ||
			result.Error.Type == "overloaded_error" {
			return nil, &service.RateLimitError{
				StatusCode: statusCode,
				RetryAfter: common.ParseRetryAfter(headers),
				Provider:   "anthropic",
				Message:    errMsg,
				Underlying: fmt.Errorf("anthropic API error [%s] (status %d): %s", result.Error.Type, statusCode, errMsg),
			}
		}
		return nil, fmt.Errorf("anthropic API error [%s] (status %d): %s", result.Error.Type, statusCode, errMsg)
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
			// On the OAuth path we PascalCased + mcp_-prefixed every
			// tool name on the way out (see transformAnthropicSystem)
			// so the assistant returns the same shape. Strip the
			// prefix here so AT's tool dispatcher (which registered
			// the original name) sees the call.
			name := block.Name
			if p.tokenSource != nil {
				name = unprefixToolName(name)
			}
			llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
				ID:        block.ID,
				Name:      name,
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
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}

	// Rate limit before issuing the request. The release runs after the
	// stream goroutine finishes; for synchronous error paths we release
	// immediately via the helper below.
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens(p.systemForEstimate, messages, tools))
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

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	slog.Debug("anthropic stream request",
		"model", model,
		"messages_count", len(messages),
		"tools_count", len(tools),
		"body_size", len(jsonData),
	)

	// Same OAuth-only ?beta=true switch as the Chat path. See the
	// comment in Chat() for why this matters.
	streamPath := "/v1/messages"
	if p.tokenSource != nil {
		streamPath = "/v1/messages?beta=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, streamPath, bytes.NewBuffer(jsonData))
	if err != nil {
		releaseOnce()
		return nil, nil, err
	}

	// If a token source is configured, get a fresh token and use Bearer auth.
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			releaseOnce()
			return nil, nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", oauthBetaHeader(model, reqBody))
		p.setOAuthHeaders(req, model)
	}

	// Log outgoing headers for debugging auth issues.
	slog.Debug("anthropic stream request headers",
		"headers", fmt.Sprintf("%v", req.Header),
		"has_auth", req.Header.Get("Authorization") != "",
		"has_x_api_key", req.Header.Get("X-Api-Key") != "",
		"has_beta", req.Header.Get("anthropic-beta") != "",
	)

	// Use the klient's HTTP client directly for streaming.
	resp, err := p.Client.HTTP.Do(req)
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		defer releaseOnce()
		bodyData, _ := io.ReadAll(resp.Body)
		slog.Error("anthropic stream error",
			"status", resp.StatusCode,
			"response", string(bodyData),
			"model", model,
		)
		// Surface 429 (rate_limit) and 529 (overloaded) as typed
		// *RateLimitError so the gateway and agent retry loops can
		// honour Retry-After.
		if isAnthropicTransientStatus(resp.StatusCode) {
			return nil, nil, &service.RateLimitError{
				StatusCode: resp.StatusCode,
				RetryAfter: common.ParseRetryAfter(resp.Header),
				Provider:   "anthropic",
				Message:    string(bodyData),
				Underlying: fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(bodyData)),
			}
		}
		return nil, nil, fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()
		defer releaseOnce()

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
						// Strip mcp_ prefix on OAuth path (we added it
						// going out — see transformAnthropicSystem).
						name := event.ContentBlock.Name
						if p.tokenSource != nil {
							name = unprefixToolName(name)
						}
						currentToolName = name
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
					msg := errMsg.Error.Message
					if msg == "" {
						msg = "unknown error"
					}
					ch <- service.StreamChunk{Error: fmt.Errorf("anthropic API error [%s]: %s", errMsg.Error.Type, msg)}
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
					// Read the body so we can pass the model name to the
					// beta-flag computer (Haiku excludes
					// interleaved-thinking, etc.) and so the request can
					// be re-sent downstream.
					var bodyMap map[string]any
					var bodyModel string
					if req.Body != nil {
						if bodyBytes, readErr := io.ReadAll(req.Body); readErr == nil && len(bodyBytes) > 0 {
							req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
							if json.Unmarshal(bodyBytes, &bodyMap) == nil {
								if m, ok := bodyMap["model"].(string); ok {
									bodyModel = m
								}
							}
						}
					}
					oauthFlags := oauthBetaHeader(bodyModel, bodyMap)
					// Merge beta flags for OAuth compatibility.
					beta := req.Header.Get("anthropic-beta")
					if beta != "" {
						// Merge unique flags.
						existing := strings.Split(beta, ",")
						required := strings.Split(oauthFlags, ",")
						seen := make(map[string]bool)
						var merged []string
						for _, f := range append(required, existing...) {
							f = strings.TrimSpace(f)
							if f != "" && !seen[f] {
								seen[f] = true
								merged = append(merged, f)
							}
						}
						beta = strings.Join(merged, ",")
					} else {
						beta = oauthFlags
					}
					req.Header.Set("anthropic-beta", beta)
					// Set OAuth-required headers (Stainless, session id,
					// request id, x-app, dangerous-direct-browser, etc.).
					// setOAuthHeaders also clears x-api-key for us.
					p.setOAuthHeaders(req, bodyModel)
				}
				req.Header.Set("anthropic-version", "2023-06-01")
			} else if p.APIKey != "" {
				req.Header.Set("x-api-key", p.APIKey)
				req.Header.Set("anthropic-version", "2023-06-01")
			}
		},
		Transport: p.Client.HTTP.Transport,
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
	anthropicTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		// First apply the generic schema sanitization (strips $ref, $defs,
		// additionalProperties, etc.) that all other providers also use, then
		// clean Anthropic-specific issues like stray "title" fields from MCP
		// tool providers.
		sanitized := service.SanitizeSchema(tool.InputSchema)
		cleanedSchema := cleanToolSchema(sanitized)
		anthropicTools[i] = map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": cleanedSchema,
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

	// OAuth via Claude Code beta requires a system identity prefix.
	// Inject it if not already present.
	if p.tokenSource != nil {
		if !strings.Contains(systemPrompt, claudeCodeSystemIdentity) {
			if systemPrompt != "" {
				systemPrompt = claudeCodeSystemIdentity + "\n" + systemPrompt
			} else {
				systemPrompt = claudeCodeSystemIdentity
			}
		}
	}

	// Anthropic requires messages to strictly alternate between user and assistant.
	// Merge consecutive messages with the same role into a single message.
	filteredMessages = mergeConsecutiveMessages(filteredMessages)

	// Convert content blocks to raw maps so we control exactly which fields
	// are present.  Anthropic requires "input" on tool_use blocks, but Go's
	// omitempty drops empty maps, so struct serialization can't guarantee it.
	for i := range filteredMessages {
		filteredMessages[i].Content = convertContent(filteredMessages[i].Content)
	}

	// Re-run the orphan + adjacency repair AFTER the merge+convert
	// step. The loop governor already calls loopgov.RepairToolPairs
	// on the windowed slice, but mergeConsecutiveMessages can
	// re-introduce adjacency orphans: if loopgov dropped a user
	// tool_result that sat between two assistant messages, those
	// assistants now collapse into one — leaving the kept tool_use
	// blocks no longer immediately followed by their matching
	// tool_result. Anthropic rejects the request with "tool_use ids
	// were found without tool_result blocks immediately after... must
	// have a corresponding tool_result block in the next message".
	// Trailing assistant tool_use messages with no following user
	// message at all (e.g. interrupted resumed conversations) hit the
	// same error. We coerce the post-merge slice into the []any shape
	// repairToolPairsAny operates on so this protection runs on every
	// path, not just OAuth (transformAnthropicSystem also calls the
	// same function later for OAuth-specific reasons; the second pass
	// is idempotent).
	msgsAnyForRepair := make([]any, 0, len(filteredMessages))
	for _, m := range filteredMessages {
		msgsAnyForRepair = append(msgsAnyForRepair, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	repaired := repairToolPairsAny(msgsAnyForRepair)
	// Rebuild filteredMessages from the repaired any-shape so
	// downstream logic (system relocation for OAuth, tool name
	// prefixing) sees the cleaned slice. The rebuild is unconditional
	// because repairToolPairsAny may mutate content blocks in place
	// even when len() is unchanged.
	filteredMessages = filteredMessages[:0]
	for _, m := range repaired {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		filteredMessages = append(filteredMessages, service.Message{
			Role:    role,
			Content: mm["content"],
		})
	}

	// Determine max_tokens: client override > provider default.
	maxTokens := p.MaxTokens
	if opts != nil {
		if opts.MaxCompletionTokens != nil {
			maxTokens = *opts.MaxCompletionTokens
		} else if opts.MaxTokens != nil {
			maxTokens = *opts.MaxTokens
		}
	}

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   filteredMessages,
	}
	if systemPrompt != "" {
		// Always use plain string format for system prompt.
		// The array format [{"type":"text","text":"..."}] causes 400 errors
		// when combined with tools in Claude Code OAuth requests.
		reqBody["system"] = systemPrompt
	}
	if len(tools) > 0 {
		reqBody["tools"] = anthropicTools
	}

	// Apply per-request generation options.
	if opts != nil {
		if opts.Temperature != nil {
			reqBody["temperature"] = *opts.Temperature
		}
		if opts.TopP != nil {
			reqBody["top_p"] = *opts.TopP
		}
		if len(opts.Stop) > 0 {
			reqBody["stop_sequences"] = opts.Stop
		}

		// Thinking / extended thinking support.
		// Direct thinking config takes precedence over reasoning_effort mapping.
		if opts.Thinking != nil && opts.Thinking.Type == "enabled" {
			budget := opts.Thinking.BudgetTokens
			if budget <= 0 {
				budget = 10000 // sensible default
			}
			reqBody["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": budget,
			}
			// Anthropic requires max_tokens >= budget_tokens for thinking models.
			// Ensure we have enough headroom for the actual response.
			if maxTokens < budget+1024 {
				reqBody["max_tokens"] = budget + 1024
			}
		} else if opts.ReasoningEffort != "" {
			// Map OpenAI-style reasoning_effort to Anthropic thinking budget.
			var budget int
			switch opts.ReasoningEffort {
			case "low":
				budget = 2048
			case "medium":
				budget = 8192
			case "high":
				budget = 24576
			}
			if budget > 0 {
				reqBody["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				}
				// Ensure max_tokens accommodates the thinking budget.
				if maxTokens < budget+1024 {
					reqBody["max_tokens"] = budget + 1024
				}
			}
		}
	}

	// OAuth user:inference scope requires extended thinking for Claude 4.x
	// Sonnet/Opus models. If thinking was not already configured above,
	// auto-enable it with a sensible default budget.
	if p.tokenSource != nil {
		if _, hasThinking := reqBody["thinking"]; !hasThinking && modelRequiresThinking(model) {
			budget := 10000
			reqBody["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": budget,
			}
			if maxTokens < budget+1024 {
				reqBody["max_tokens"] = budget + 1024
			}
		}
	}

	// OAuth path: rewrite the body to match Claude Code's wire format
	// exactly (billing-text system block, identity-prefix-as-its-own-
	// entry, third-party system relocated to first user message,
	// PascalCase mcp_ tool names, repaired tool_use/tool_result pairs).
	// Without this, Anthropic's OAuth billing pipeline classifies the
	// caller as "external" traffic and applies aggressive rate limits
	// regardless of the user's Claude Pro/Max plan — that's the root
	// cause of the empty-body 429s users were hitting on this path.
	//
	// We have to coerce messages from []service.Message to []any first
	// so transformAnthropicSystem can mutate map entries in place; the
	// service.Message struct doesn't carry the cache_control / billing
	// fields we need to manipulate.
	if p.tokenSource != nil {
		// Convert messages slice to []any so the transform can mutate.
		msgsAny := make([]any, 0, len(filteredMessages))
		for _, m := range filteredMessages {
			msgsAny = append(msgsAny, map[string]any{
				"role":    m.Role,
				"content": m.Content,
			})
		}
		reqBody["messages"] = msgsAny

		// Convert tools to []any too so the transform's tools loop can
		// mutate the names in place.
		if t, ok := reqBody["tools"].([]map[string]any); ok {
			toolsAny := make([]any, len(t))
			for i, x := range t {
				toolsAny[i] = x
			}
			reqBody["tools"] = toolsAny
		}

		entrypoint := claudeCodeEntrypoint
		transformAnthropicSystem(reqBody, claudeCodeCLIVersion, entrypoint)
	}

	return reqBody
}

// mergeConsecutiveMessages merges adjacent messages that share the same role.
// Anthropic requires strict user/assistant alternation. When two or more
// consecutive messages have the same role, their content is concatenated
// (strings joined with newlines, content-block arrays appended).
func mergeConsecutiveMessages(msgs []service.Message) []service.Message {
	if len(msgs) <= 1 {
		return msgs
	}

	merged := make([]service.Message, 0, len(msgs))
	merged = append(merged, msgs[0])

	for i := 1; i < len(msgs); i++ {
		last := &merged[len(merged)-1]
		if msgs[i].Role == last.Role {
			// Same role — merge content.
			last.Content = mergeContent(last.Content, msgs[i].Content)
		} else {
			merged = append(merged, msgs[i])
		}
	}

	return merged
}

// mergeContent combines two message Content values.
// Content can be a string or []ContentBlock (or []any after conversion).
func mergeContent(a, b any) any {
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)

	if aIsStr && bIsStr {
		return aStr + "\n" + bStr
	}

	// Convert both to slices and concatenate.
	aSlice := contentToSlice(a)
	bSlice := contentToSlice(b)
	return append(aSlice, bSlice...)
}

// contentToSlice normalizes message content to a []any slice.
func contentToSlice(c any) []any {
	switch v := c.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		return v
	case []service.ContentBlock:
		// Convert each block to its final map form so that subsequent
		// merging/marshalling never sees a raw ContentBlock struct (whose
		// Input map[string]any `json:"input,omitempty"` tag would drop an
		// empty-but-required "input" field on tool_use blocks, causing
		// Anthropic/MiniMax to reject the request with
		// "invalid function arguments json string").
		out := make([]any, len(v))
		for i, b := range v {
			out[i] = contentBlockToMap(b)
		}
		return out
	default:
		return []any{c}
	}
}

// cleanToolSchema removes the JSON Schema metadata "title" field that some
// MCP providers include (e.g., "title": "text_to_audioArguments") but which
// Anthropic's API may not accept. It only removes "title" from schema
// definition objects (those that have a "type" key), NOT from the "properties"
// map where "title" is a legitimate property name.
func cleanToolSchema(schema any) any {
	switch v := schema.(type) {
	case map[string]any:
		// Only remove "title" if this looks like a JSON Schema definition
		// (has "type" or "properties" key), NOT if we're inside a "properties" map.
		if _, hasType := v["type"]; hasType {
			delete(v, "title")
		} else if _, hasProps := v["properties"]; hasProps {
			delete(v, "title")
		}
		// Recursively clean nested schemas, but skip the "properties" map's
		// direct children keys (those are field names, not schema metadata).
		for key, val := range v {
			if key == "properties" {
				// Inside "properties", each value is a schema definition — clean those.
				if propsMap, ok := val.(map[string]any); ok {
					for propName, propSchema := range propsMap {
						propsMap[propName] = cleanToolSchema(propSchema)
					}
				}
			} else {
				v[key] = cleanToolSchema(val)
			}
		}
		return v
	case []any:
		for i, val := range v {
			v[i] = cleanToolSchema(val)
		}
		return v
	default:
		return schema
	}
}

// setOAuthHeaders adds the HTTP headers Anthropic's OAuth billing
// pipeline requires to recognise the caller as legitimate Claude Code
// traffic. Without this set Anthropic falls back to the much tighter
// "external" rate limits and 429s the user even when their plan has
// plenty of headroom.
//
// Header set mirrors opencode-claude-auth's buildRequestHeaders +
// getStainlessHeaders. The big departure from the legacy code in
// this file is that we no longer emit the `x-anthropic-billing-header`
// HTTP header — the billing string belongs in the request body's
// system[0] block (see signing.go and transformAnthropicSystem). The
// HTTP-header form was a misread of the upstream protocol that left
// the request unbilled and rate-limited.
func (p *Provider) setOAuthHeaders(req *http.Request, model string) {
	// User-Agent: matches the SDK shape Anthropic's billing pipeline
	// expects. Note the "(external, sdk-cli)" trailer — the older
	// "(external, cli)" form is treated as a different client surface
	// and gets stricter throttling.
	req.Header.Set("User-Agent", "claude-cli/"+claudeCodeCLIVersion+" (external, sdk-cli)")
	req.Header.Set("x-app", "cli")
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")

	// Per-session and per-request UUIDs let Anthropic correlate
	// rate-limit windows to the same caller and apply the right
	// per-session quota rather than a default per-request bucket.
	// Session ID is stable across all requests from the same Provider
	// instance (see Provider.sessionID, lazily initialised).
	req.Header.Set("X-Claude-Code-Session-Id", p.sessionID())
	req.Header.Set("x-client-request-id", newRequestUUID())

	// Stainless SDK headers. The Anthropic SDK ships with these on
	// every request; the billing pipeline uses them to identify the
	// SDK family. Plugin sets them all unconditionally.
	for k, v := range stainlessHeaders() {
		// Don't overwrite anything an upstream caller (e.g. the
		// gateway proxy) explicitly set.
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}

	// Defensive: ensure no stale x-api-key from a static-key code path
	// is still in the request when we're using OAuth.
	req.Header.Del("x-api-key")
}

// claudeCodeSystemIdentity is the required system identity prefix for
// Claude Code OAuth requests. Anthropic's API expects this when using
// the claude-code beta flag.
const claudeCodeSystemIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

// oauthBetaHeader returns the anthropic-beta header value for OAuth requests.
//
// Tracked against the upstream Claude Code CLI (kept in sync with
// opencode-claude-auth's src/model-config.ts:baseBetas):
//   - claude-code-20250219: enables the OAuth user:inference scope
//   - oauth-2025-04-20: bearer-token request format
//   - interleaved-thinking-2025-05-14: extended thinking interleaving
//   - prompt-caching-scope-2026-01-05: prompt caching with OAuth tokens
//   - context-management-2025-06-27: server-side context management
//   - advisor-tool-2026-03-01: Claude Code advisor tooling
//
// Haiku models reject `interleaved-thinking-2025-05-14`, so we strip it
// for those — matching the plugin's per-model exclude list.
//
// We always include the `interleaved-thinking` flag for non-haiku
// models (not just when the body has `thinking`) because the plugin
// does — Anthropic's billing pipeline expects it on the baseline
// Claude Code wire shape.
func oauthBetaHeader(model string, _ map[string]any) string {
	flags := []string{
		"claude-code-20250219",
		"oauth-2025-04-20",
		"interleaved-thinking-2025-05-14",
		"prompt-caching-scope-2026-01-05",
		"context-management-2025-06-27",
		"advisor-tool-2026-03-01",
	}
	if strings.Contains(strings.ToLower(model), "haiku") {
		// Drop interleaved-thinking — haiku doesn't support it.
		filtered := flags[:0]
		for _, f := range flags {
			if f != "interleaved-thinking-2025-05-14" {
				filtered = append(filtered, f)
			}
		}
		flags = filtered
	}
	return strings.Join(flags, ",")
}

// claudeCodeCLIVersion is the Claude CLI version we advertise on
// OAuth requests. Tracked against the upstream Claude Code CLI;
// kept in sync with the opencode-claude-auth plugin's
// src/model-config.ts:ccVersion. Anthropic's OAuth billing pipeline
// uses this value (combined with the message-text-derived suffix —
// see signing.go) to identify legitimate Claude Code traffic.
//
// Override at runtime via the ANTHROPIC_CLI_VERSION env var if
// Anthropic bumps the CLI before we publish a new release.
const claudeCodeCLIVersion = "2.1.112"

// claudeCodeEntrypoint is the cc_entrypoint value embedded in the
// billing system text block. Plugin uses "sdk-cli" (not "cli") so we
// match — the value identifies the SDK/library shape of the caller
// and Anthropic's billing pipeline treats them differently.
const claudeCodeEntrypoint = "sdk-cli"

// isAnthropicTransientStatus reports whether an upstream HTTP status
// from Anthropic warrants treatment as a transient *RateLimitError.
// 429 is the standard rate limit. 529 is Anthropic's non-standard
// "overloaded" status — empirically transient and worth retrying
// (the upstream Claude CLI and opencode-claude-auth plugin both do).
func isAnthropicTransientStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, // 429
		529: // anthropic-specific overloaded
		return true
	}
	return false
}

// sessionID returns the per-Provider stable UUID used for the
// X-Claude-Code-Session-Id header. Created on first call and reused
// for every subsequent request from the same Provider instance,
// matching opencode-claude-auth's per-process sessionId.
func (p *Provider) sessionID() string {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	if p.sessionUUID == "" {
		p.sessionUUID = newRequestUUID()
	}
	return p.sessionUUID
}

// newRequestUUID returns a new RFC 4122 v4 UUID. Used for both the
// per-session ID and the per-request `x-client-request-id` header.
// We don't pull in google/uuid for one helper — 16 random bytes with
// the right v4 marker bits is six lines.
func newRequestUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: a deterministic placeholder is still better than
		// crashing the request — Anthropic accepts any non-empty UUID.
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // v4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// stainlessHeaders returns the static set of `x-stainless-*` headers
// the Anthropic SDK emits. Anthropic's billing pipeline reads these to
// identify the SDK family — they're advisory but their absence is one
// of the signals the pipeline uses to flag callers as untrusted.
//
// `x-stainless-os` and `x-stainless-arch` are derived from runtime so
// the values look authentic on each host. `x-stainless-runtime` is
// always "go" rather than the JS SDK's "node" — Anthropic accepts both.
func stainlessHeaders() map[string]string {
	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "MacOS"
	}
	arch := runtime.GOARCH
	return map[string]string{
		"x-stainless-arch":            arch,
		"x-stainless-lang":            "go",
		"x-stainless-os":              osName,
		"x-stainless-package-version": "0.81.0",
		"x-stainless-retry-count":     "0",
		"x-stainless-runtime":         "go",
		"x-stainless-runtime-version": runtime.Version(),
		"x-stainless-timeout":         "600",
	}
}

// modelRequiresThinking returns true for Claude 4.x Sonnet/Opus models
// that require extended thinking when used via OAuth user:inference scope.
func modelRequiresThinking(model string) bool {
	m := strings.ToLower(model)
	// Claude 4 Sonnet, Opus, and their variants (e.g. claude-sonnet-4-6,
	// claude-opus-4-6, claude-sonnet-4-20250514, etc.)
	// Haiku models and Claude 3.x do NOT require thinking.
	if strings.Contains(m, "sonnet-4") || strings.Contains(m, "opus-4") {
		return true
	}
	return false
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
		for i, b := range blocks {
			switch elem := b.(type) {
			case service.ContentBlock:
				// A raw struct slipped through (e.g. via mergeContent).
				// Normalize via contentBlockToMap so tool_use blocks
				// always carry an "input" object.
				blocks[i] = contentBlockToMap(elem)
			case map[string]any:
				if elem["type"] == "tool_use" {
					if _, has := elem["input"]; !has {
						elem["input"] = map[string]any{}
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
	case "image", "document", "audio", "video":
		// Media content blocks carry their data in a Source field
		// (base64-encoded or URL reference).
		m := map[string]any{"type": b.Type}
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
