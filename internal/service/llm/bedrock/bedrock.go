// Package bedrock implements an LLM provider that talks to the AWS Bedrock
// Runtime "Converse" API. The Converse API is a unified shape across the
// model families on Bedrock (Anthropic Claude, Meta Llama, Amazon Titan,
// Cohere Command, Mistral, AI21, …) so this single provider can serve all
// of them without per-family adapters.
//
// Credentials are sourced from standard environment variables:
//
//	AWS_ACCESS_KEY_ID
//	AWS_SECRET_ACCESS_KEY
//	AWS_SESSION_TOKEN  (optional, for assumed roles / temporary creds)
//
// or from the LLMConfig.APIKey field encoded as "ACCESS_KEY:SECRET_KEY" or
// "ACCESS_KEY:SECRET_KEY:SESSION_TOKEN" — useful when the operator wants
// to provision credentials through the AT UI rather than relying on the
// host environment.
//
// Region is read from LLMConfig.BaseURL when it's an HTTP URL
// (e.g. "https://bedrock-runtime.eu-west-1.amazonaws.com"), or from the
// AWS_REGION env var. We do NOT call IMDSv2 / STS — that would couple us
// to running on EC2.
//
// Streaming uses the InvokeWithResponseStream endpoint (AWS event-stream
// binary protocol) — implemented in stream.go. Non-streaming uses the
// /converse endpoint and returns a single LLMResponse.
package bedrock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const (
	defaultRegion = "us-east-1"
	sigv4Service  = "bedrock"
)

// Provider implements service.LLMProvider for AWS Bedrock.
type Provider struct {
	accessKey    string
	secretKey    string
	sessionToken string
	region       string
	model        string
	endpoint     string
	httpClient   *http.Client
	limiter      *ratelimit.Limiter
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithRateLimiter attaches a per-provider rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(p *Provider) { p.limiter = l }
}

// New creates a Bedrock provider.
//
// `apiKey` may be empty (env-only) or a colon-delimited string carrying
// "ACCESS_KEY:SECRET_KEY[:SESSION_TOKEN]".
// `baseURL` may be empty (uses the regional default) or a full Bedrock
// endpoint URL — we derive the region from it.
func New(apiKey, model, baseURL string, proxyURL string, insecureSkipVerify bool, opts ...Option) (*Provider, error) {
	access, secret, session := splitAPIKey(apiKey)
	if access == "" {
		access = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if secret == "" {
		secret = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if session == "" {
		session = os.Getenv("AWS_SESSION_TOKEN")
	}
	if access == "" || secret == "" {
		return nil, fmt.Errorf("bedrock provider requires AWS credentials (set api_key as ACCESS:SECRET[:SESSION] or env AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY)")
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	endpoint := strings.TrimSuffix(baseURL, "/")
	if endpoint != "" {
		if parsed, perr := url.Parse(endpoint); perr == nil && parsed.Host != "" {
			// Endpoints look like bedrock-runtime.<region>.amazonaws.com
			if h := parsed.Host; strings.Contains(h, ".") {
				parts := strings.Split(h, ".")
				if len(parts) >= 3 && region == "" {
					region = parts[1]
				}
			}
		}
	}
	if region == "" {
		region = defaultRegion
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region)
	}

	// Build HTTP client with optional proxy.
	httpClient := http.DefaultClient
	if proxyURL != "" || insecureSkipVerify {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if proxyURL != "" {
			u, perr := url.Parse(proxyURL)
			if perr != nil {
				return nil, fmt.Errorf("parse proxy URL: %w", perr)
			}
			t.Proxy = http.ProxyURL(u)
		}
		if insecureSkipVerify {
			t.TLSClientConfig = nil
		}
		httpClient = &http.Client{Transport: t, Timeout: 5 * time.Minute}
	}

	p := &Provider{
		accessKey:    access,
		secretKey:    secret,
		sessionToken: session,
		region:       region,
		model:        model,
		endpoint:     endpoint,
		httpClient:   httpClient,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// splitAPIKey parses "ACCESS:SECRET[:SESSION]" out of a single string.
// Empty / malformed input returns empties so callers fall through to env.
func splitAPIKey(s string) (access, secret, session string) {
	if s == "" {
		return "", "", ""
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return "", "", ""
	}
	access = parts[0]
	secret = parts[1]
	if len(parts) >= 3 {
		session = parts[2]
	}
	return
}

// ─── Converse API request/response types ───

// converseRequest is the AWS Bedrock Converse API request body.
type converseRequest struct {
	Messages         []converseMessage     `json:"messages"`
	System           []converseSystemBlock `json:"system,omitempty"`
	InferenceConfig  *inferenceConfig      `json:"inferenceConfig,omitempty"`
	ToolConfig       *converseToolConfig   `json:"toolConfig,omitempty"`
	AdditionalFields map[string]any        `json:"additionalModelRequestFields,omitempty"`
}

type converseMessage struct {
	Role    string             `json:"role"` // "user" | "assistant"
	Content []converseContentB `json:"content"`
}

type converseContentB struct {
	Text       string             `json:"text,omitempty"`
	ToolUse    *converseToolUse   `json:"toolUse,omitempty"`
	ToolResult *converseToolResult `json:"toolResult,omitempty"`
	Image      *converseImage     `json:"image,omitempty"`
}

type converseSystemBlock struct {
	Text string `json:"text,omitempty"`
}

type inferenceConfig struct {
	MaxTokens     *int     `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

type converseToolConfig struct {
	Tools      []converseTool      `json:"tools,omitempty"`
	ToolChoice *converseToolChoice `json:"toolChoice,omitempty"`
}

type converseTool struct {
	ToolSpec *converseToolSpec `json:"toolSpec,omitempty"`
}

type converseToolSpec struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	InputSchema map[string]any          `json:"inputSchema,omitempty"`
}

type converseToolChoice struct {
	Auto *map[string]any `json:"auto,omitempty"`
	Any  *map[string]any `json:"any,omitempty"`
	Tool *struct {
		Name string `json:"name"`
	} `json:"tool,omitempty"`
}

type converseToolUse struct {
	ToolUseID string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type converseToolResult struct {
	ToolUseID string                  `json:"toolUseId"`
	Content   []converseToolResultBlk `json:"content"`
}

type converseToolResultBlk struct {
	Text string `json:"text,omitempty"`
	JSON any    `json:"json,omitempty"`
}

type converseImage struct {
	Format string                `json:"format"`
	Source converseImageSourceB  `json:"source"`
}

type converseImageSourceB struct {
	Bytes string `json:"bytes,omitempty"` // base64
}

// converseResponse is the Bedrock Converse API response body.
type converseResponse struct {
	Output struct {
		Message struct {
			Role    string             `json:"role"`
			Content []converseContentB `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		TotalTokens  int `json:"totalTokens"`
	} `json:"usage"`
	Metrics struct {
		LatencyMs int64 `json:"latencyMs"`
	} `json:"metrics"`
	Message string `json:"message,omitempty"` // present on error
}

// ─── LLMProvider implementation ───

// Chat implements service.LLMProvider.
func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("bedrock: model is required")
	}

	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens("", messages, tools))
	if err != nil {
		return nil, err
	}
	defer release()

	reqBody := p.buildConverseRequest(messages, tools, opts)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal bedrock request: %w", err)
	}

	// Path: /model/{modelId}/converse
	endpoint := fmt.Sprintf("%s/model/%s/converse", p.endpoint, url.PathEscape(model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if err := p.signSigV4(req, bodyBytes); err != nil {
		return nil, fmt.Errorf("sign bedrock request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read bedrock response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &service.RateLimitError{
			StatusCode: resp.StatusCode,
			RetryAfter: common.ParseRetryAfter(resp.Header),
			Provider:   "bedrock",
			Message:    string(respBody),
			Underlying: fmt.Errorf("bedrock 429: %s", string(respBody)),
		}
	}
	if resp.StatusCode >= 400 {
		// Bedrock returns {"message":"..."} on error
		var errBody struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(respBody, &errBody)
		msg := errBody.Message
		if msg == "" {
			msg = string(respBody)
		}
		return nil, fmt.Errorf("bedrock API error (status %d): %s", resp.StatusCode, msg)
	}

	var parsed converseResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode bedrock response: %w (body: %s)", err, string(respBody))
	}

	out := &service.LLMResponse{
		Finished:     parsed.StopReason != "tool_use",
		FinishReason: parsed.StopReason,
		Header:       resp.Header,
		Usage: service.Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
	}

	for _, block := range parsed.Output.Message.Content {
		if block.Text != "" {
			out.Content += block.Text
		}
		if block.ToolUse != nil {
			out.ToolCalls = append(out.ToolCalls, service.ToolCall{
				ID:        block.ToolUse.ToolUseID,
				Name:      block.ToolUse.Name,
				Arguments: block.ToolUse.Input,
			})
		}
	}

	return out, nil
}

// buildConverseRequest translates internal Message + Tool slices into the
// Bedrock Converse request shape.
func (p *Provider) buildConverseRequest(messages []service.Message, tools []service.Tool, opts *service.ChatOptions) *converseRequest {
	out := &converseRequest{}

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if s, ok := msg.Content.(string); ok && s != "" {
				out.System = append(out.System, converseSystemBlock{Text: s})
			}
		case "user", "assistant":
			blocks := convertContentToConverse(msg.Content)
			if len(blocks) == 0 {
				continue
			}
			out.Messages = append(out.Messages, converseMessage{
				Role:    msg.Role,
				Content: blocks,
			})
		}
	}

	// Inference config.
	if opts != nil {
		ic := &inferenceConfig{}
		hasIC := false
		if opts.MaxCompletionTokens != nil {
			ic.MaxTokens = opts.MaxCompletionTokens
			hasIC = true
		} else if opts.MaxTokens != nil {
			ic.MaxTokens = opts.MaxTokens
			hasIC = true
		}
		if opts.Temperature != nil {
			ic.Temperature = opts.Temperature
			hasIC = true
		}
		if opts.TopP != nil {
			ic.TopP = opts.TopP
			hasIC = true
		}
		if len(opts.Stop) > 0 {
			ic.StopSequences = opts.Stop
			hasIC = true
		}
		if hasIC {
			out.InferenceConfig = ic
		}
		if len(opts.ExtraBody) > 0 {
			out.AdditionalFields = opts.ExtraBody
		}
	}

	// Tools.
	if len(tools) > 0 {
		tc := &converseToolConfig{}
		for _, t := range tools {
			tc.Tools = append(tc.Tools, converseTool{
				ToolSpec: &converseToolSpec{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: map[string]any{"json": service.SanitizeSchema(t.InputSchema)},
				},
			})
		}
		if opts != nil && opts.ToolChoice != nil {
			tc.ToolChoice = translateBedrockToolChoice(opts.ToolChoice)
		}
		out.ToolConfig = tc
	}

	return out
}

// convertContentToConverse turns a service.Message.Content (any) value
// into a Converse content-block slice.
func convertContentToConverse(content any) []converseContentB {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []converseContentB{{Text: c}}
	case []service.ContentBlock:
		var out []converseContentB
		for _, b := range c {
			switch b.Type {
			case "text":
				if b.Text != "" {
					out = append(out, converseContentB{Text: b.Text})
				}
			case "tool_use":
				out = append(out, converseContentB{ToolUse: &converseToolUse{
					ToolUseID: b.ID,
					Name:      b.Name,
					Input:     b.Input,
				}})
			case "tool_result":
				out = append(out, converseContentB{ToolResult: &converseToolResult{
					ToolUseID: b.ToolUseID,
					Content:   []converseToolResultBlk{{Text: b.Content}},
				}})
			case "image":
				if b.Source != nil && b.Source.Data != "" {
					format := imageFormatFromMime(b.Source.MediaType)
					out = append(out, converseContentB{Image: &converseImage{
						Format: format,
						Source: converseImageSourceB{Bytes: b.Source.Data},
					}})
				}
			}
		}
		return out
	case map[string]any:
		// Gateway passthrough — OpenAI-shape map. Extract text + tool_calls.
		var out []converseContentB
		switch txt := c["content"].(type) {
		case string:
			if txt != "" {
				out = append(out, converseContentB{Text: txt})
			}
		case []any:
			for _, part := range txt {
				m, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := m["type"].(string); t == "text" {
					if s, _ := m["text"].(string); s != "" {
						out = append(out, converseContentB{Text: s})
					}
				}
			}
		}
		if tcs, ok := c["tool_calls"].([]any); ok {
			for _, raw := range tcs {
				tcMap, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				id, _ := tcMap["id"].(string)
				fn, _ := tcMap["function"].(map[string]any)
				if fn == nil {
					continue
				}
				name, _ := fn["name"].(string)
				args, _ := fn["arguments"].(string)
				var input map[string]any
				_ = json.Unmarshal([]byte(args), &input)
				out = append(out, converseContentB{ToolUse: &converseToolUse{
					ToolUseID: id,
					Name:      name,
					Input:     input,
				}})
			}
		}
		if tcID, _ := c["tool_call_id"].(string); tcID != "" {
			text, _ := c["content"].(string)
			out = append(out, converseContentB{ToolResult: &converseToolResult{
				ToolUseID: tcID,
				Content:   []converseToolResultBlk{{Text: text}},
			}})
		}
		return out
	}
	return nil
}

func translateBedrockToolChoice(v any) *converseToolChoice {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "auto":
			empty := map[string]any{}
			return &converseToolChoice{Auto: &empty}
		case "required", "any":
			empty := map[string]any{}
			return &converseToolChoice{Any: &empty}
		}
	case map[string]any:
		t, _ := x["type"].(string)
		switch strings.ToLower(t) {
		case "function":
			fn, _ := x["function"].(map[string]any)
			if fn == nil {
				return nil
			}
			name, _ := fn["name"].(string)
			if name == "" {
				return nil
			}
			return &converseToolChoice{Tool: &struct {
				Name string `json:"name"`
			}{Name: name}}
		case "auto":
			empty := map[string]any{}
			return &converseToolChoice{Auto: &empty}
		case "any", "required":
			empty := map[string]any{}
			return &converseToolChoice{Any: &empty}
		}
	}
	return nil
}

func imageFormatFromMime(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	// Bedrock accepts: png | jpeg | gif | webp
	return "png"
}

// ─── SigV4 signing ───

// signSigV4 attaches AWS Signature Version 4 headers to req for the
// Bedrock service in the configured region.
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4-signed-request-examples.html
func (p *Provider) signSigV4(req *http.Request, body []byte) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)
	if p.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.sessionToken)
	}

	// Body hash
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	req.Header.Set("X-Amz-Content-Sha256", bodyHashHex)

	// Canonical request
	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	canonicalQuery := req.URL.RawQuery // already in canonical form for our use
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		bodyHashHex,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, p.region, sigv4Service)
	crHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hex.EncodeToString(crHash[:]),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+p.secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, p.region)
	kService := hmacSHA256(kRegion, sigv4Service)
	kSigning := hmacSHA256(kService, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.accessKey, credentialScope, signedHeaders, sig)
	req.Header.Set("Authorization", authHeader)
	return nil
}

// canonicalHeaders returns the canonical-headers string and the
// semicolon-joined list of signed header names.
func canonicalHeaders(req *http.Request) (string, string) {
	// Always include host + x-amz-date + x-amz-content-sha256 + x-amz-security-token (if present)
	// plus content-type. We sign every header beginning with "x-amz-" too.
	headerNames := []string{}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "user-agent" {
			continue
		}
		if lk == "host" || lk == "content-type" || strings.HasPrefix(lk, "x-amz-") {
			headerNames = append(headerNames, lk)
		}
	}
	// Host is always implicit.
	hasHost := false
	for _, n := range headerNames {
		if n == "host" {
			hasHost = true
			break
		}
	}
	if !hasHost {
		headerNames = append(headerNames, "host")
	}
	stringSortStrings(headerNames)

	var canonical strings.Builder
	for _, n := range headerNames {
		var v string
		switch n {
		case "host":
			v = req.URL.Host
		default:
			v = strings.TrimSpace(req.Header.Get(n))
		}
		canonical.WriteString(n)
		canonical.WriteString(":")
		canonical.WriteString(v)
		canonical.WriteString("\n")
	}
	return canonical.String(), strings.Join(headerNames, ";")
}

// ─── ChatStream (fallback only) ───
//
// Bedrock has an event-stream binary protocol on /converse-stream. Rather
// than implement that here, we surface no ChatStream method — the gateway's
// streaming layer detects the absence and falls back to one Chat() call +
// fake-streaming, which is functionally complete for SDK clients (they
// just won't get token-by-token granularity).
//
// We do, however, implement Proxy so callers can hit native Bedrock
// endpoints (like /model/.../invoke) through the gateway.

// Proxy forwards a raw HTTP request to the Bedrock API, signing it on the
// way out. This is what /gateway/v1/providers/bedrock/* uses.
func (p *Provider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	target := p.endpoint + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read proxy body: %w", err)
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	// Carry the Content-Type over but strip Authorization/Host so SigV4 wins.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if err := p.signSigV4(req, body); err != nil {
		return err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("bedrock proxy copy failed", "error", err)
	}
	return nil
}
