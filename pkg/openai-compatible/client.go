// Package openaicompatible is a small, dependency-light Go client for any
// server that speaks the OpenAI Chat Completions wire format. It is designed
// to talk to:
//
//   - the official OpenAI API (https://api.openai.com/v1)
//   - the AT gateway (http://<host>/gateway/v1)
//   - any other OpenAI-compatible endpoint (Ollama, vLLM, LiteLLM, Together,
//     Groq, GitHub Models, Azure OpenAI, ...)
//
// The package surface is intentionally narrow: a single Client with methods
// for chat, streaming, embeddings, and listing models. Everything that goes
// on the wire is exposed as a plain Go struct that mirrors the OpenAI shape,
// so users can set any field a particular server supports — including fields
// this library does not know about — via the [ChatRequest.Extra] map.
//
// Basic usage:
//
//	client, err := openaicompatible.New(
//	    openaicompatible.WithBaseURL("https://api.openai.com/v1"),
//	    openaicompatible.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
//	)
//	if err != nil { ... }
//
//	resp, err := client.Chat(ctx, &openaicompatible.ChatRequest{
//	    Model: "gpt-4o-mini",
//	    Messages: []openaicompatible.Message{
//	        openaicompatible.SystemMessage("You are a helpful assistant."),
//	        openaicompatible.UserMessage("Hello!"),
//	    },
//	})
package openaicompatible

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rakunlabs/ok"
)

// DefaultBaseURL is the default OpenAI v1 root used when no base URL is
// provided. Note: it is the API root, not the chat endpoint — the client
// appends "/chat/completions", "/embeddings", "/models" itself.
const DefaultBaseURL = "https://api.openai.com/v1"

// Client talks to an OpenAI-compatible server.
//
// A Client is safe for concurrent use by multiple goroutines. Create one
// per (base URL, credentials) pair and reuse it for the lifetime of the
// process.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	headers    http.Header
}

// HTTPClient returns the underlying *http.Client. Callers can use this for
// advanced cases (e.g. issuing a raw request to a non-standard endpoint on
// the same server). Modifying the returned client's Transport will affect
// all subsequent requests.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// BaseURL returns the API root the client was configured with.
func (c *Client) BaseURL() string { return c.baseURL }

// DefaultModel returns the default model id, if one was configured via
// [WithModel]. Empty string if not set.
func (c *Client) DefaultModel() string { return c.model }

// config is the internal options bag built up by [Option] functions.
type config struct {
	baseURL            string
	apiKey             string
	model              string
	headers            http.Header
	userAgent          string
	proxy              string
	insecureSkipVerify bool
	timeout            time.Duration
	httpClient         *http.Client
	disableRetry       bool
	retryMax           int
	okOptions          []ok.OptionClientFn
}

// Option configures a [Client].
type Option func(*config)

// WithBaseURL sets the API root URL (without trailing /chat/completions).
//
// Examples:
//   - "https://api.openai.com/v1"
//   - "http://localhost:8080/gateway/v1" (AT gateway)
//   - "http://localhost:11434/v1"        (Ollama)
//
// If the configured URL ends with "/chat/completions", that suffix is
// stripped automatically so users can paste the chat URL by mistake without
// breaking other endpoints.
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// WithAPIKey sets the bearer token used in the Authorization header.
// Leave unset (or empty) for servers that do not require authentication.
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// WithModel sets a default model id used by methods when [ChatRequest.Model]
// or [EmbeddingRequest.Model] is empty.
func WithModel(model string) Option {
	return func(c *config) { c.model = model }
}

// WithHeader sets a single extra HTTP header sent on every request.
// Calling it again with the same key replaces the previous value.
func WithHeader(key, value string) Option {
	return func(c *config) {
		if c.headers == nil {
			c.headers = http.Header{}
		}
		c.headers.Set(key, value)
	}
}

// WithHeaders merges a header map into the per-request headers.
// Existing values for the same key are overwritten.
func WithHeaders(h http.Header) Option {
	return func(c *config) {
		if c.headers == nil {
			c.headers = http.Header{}
		}
		for k, vs := range h {
			c.headers[k] = append([]string(nil), vs...)
		}
	}
}

// WithUserAgent overrides the default User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *config) { c.userAgent = ua }
}

// WithProxy routes requests through the given HTTP/HTTPS/SOCKS5 proxy URL.
// Empty string disables the proxy.
func WithProxy(proxy string) Option {
	return func(c *config) { c.proxy = proxy }
}

// WithInsecureSkipVerify disables TLS certificate verification.
// Use with care — only for self-signed development servers.
func WithInsecureSkipVerify(skip bool) Option {
	return func(c *config) { c.insecureSkipVerify = skip }
}

// WithTimeout sets the overall HTTP client timeout. This applies to the
// total time of a single request including all retries. For streaming
// requests the read of the SSE body is bounded by the request context,
// not by this timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithHTTPClient lets callers supply their own *http.Client. When provided,
// proxy / TLS / retry / timeout options are still honoured by wrapping the
// supplied client's Transport.
func WithHTTPClient(h *http.Client) Option {
	return func(c *config) { c.httpClient = h }
}

// WithDisableRetry disables the built-in retry-with-backoff behaviour.
// By default the client retries on connection errors and 5xx responses.
func WithDisableRetry(disable bool) Option {
	return func(c *config) { c.disableRetry = disable }
}

// WithRetryMax overrides the default retry attempt count (4).
// Set to 0 to issue a single attempt with no retries.
func WithRetryMax(n int) Option {
	return func(c *config) { c.retryMax = n }
}

// WithOKOptions is an escape hatch that forwards arbitrary
// [github.com/rakunlabs/ok.OptionClientFn] values to the underlying HTTP
// client builder. Use it for advanced configuration (custom retry policy,
// round-tripper wrappers, telemetry injection, etc).
func WithOKOptions(opts ...ok.OptionClientFn) Option {
	return func(c *config) { c.okOptions = append(c.okOptions, opts...) }
}

// New constructs a Client. At minimum a base URL is required, either via
// [WithBaseURL] or by leaving it blank to use [DefaultBaseURL].
func New(opts ...Option) (*Client, error) {
	cfg := &config{
		baseURL:  DefaultBaseURL,
		retryMax: 4,
	}
	for _, o := range opts {
		o(cfg)
	}

	// Normalise base URL: strip trailing "/chat/completions" and any trailing slash.
	cfg.baseURL = trimChatSuffix(cfg.baseURL)

	headers := http.Header{
		"Content-Type": []string{"application/json"},
		"Accept":       []string{"application/json"},
	}
	if cfg.apiKey != "" {
		headers.Set("Authorization", "Bearer "+cfg.apiKey)
	}
	for k, vs := range cfg.headers {
		headers[k] = append([]string(nil), vs...)
	}

	okOpts := []ok.OptionClientFn{
		ok.WithBaseURL(cfg.baseURL + "/"), // trailing slash so relative paths resolve correctly
		ok.WithHeader(headers),
		ok.WithDisableRetry(cfg.disableRetry),
		ok.WithRetryMax(cfg.retryMax),
		ok.WithRetryLog(false),
	}
	if cfg.userAgent != "" {
		okOpts = append(okOpts, ok.WithUserAgent(cfg.userAgent))
	}
	if cfg.proxy != "" {
		okOpts = append(okOpts, ok.WithProxy(cfg.proxy))
	}
	if cfg.insecureSkipVerify {
		okOpts = append(okOpts, ok.WithInsecureSkipVerify(true))
	}
	if cfg.timeout > 0 {
		okOpts = append(okOpts, ok.WithTimeout(cfg.timeout))
	}
	if cfg.httpClient != nil {
		okOpts = append(okOpts, ok.WithHTTPClient(cfg.httpClient))
	}
	okOpts = append(okOpts, cfg.okOptions...)

	okClient, err := ok.New(okOpts...)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: build http client: %w", err)
	}

	return &Client{
		httpClient: okClient.HTTP,
		baseURL:    cfg.baseURL,
		apiKey:     cfg.apiKey,
		model:      cfg.model,
		headers:    headers,
	}, nil
}

// trimChatSuffix removes "/chat/completions" and any trailing slash so the
// base URL is always the API root.
func trimChatSuffix(u string) string {
	for {
		switch {
		case len(u) > 0 && u[len(u)-1] == '/':
			u = u[:len(u)-1]
		case hasSuffix(u, "/chat/completions"):
			u = u[:len(u)-len("/chat/completions")]
		default:
			return u
		}
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
