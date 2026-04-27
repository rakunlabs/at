package config

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rakunlabs/alan"
	_ "github.com/rakunlabs/chu/loader/external/loaderconsul"
	_ "github.com/rakunlabs/chu/loader/external/loadervault"
	"github.com/rakunlabs/chu/loader/loaderenv"
	"github.com/rakunlabs/logi"

	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/tell"
)

var Service = ""

type Config struct {
	LogLevel string `cfg:"log_level,no_prefix" default:"info"`

	// Providers is a map of named provider configurations.
	// Each provider has a type ("anthropic", "openai", "vertex", or "gemini"), along with
	// api_key, base_url, model, and extra_headers fields.
	//
	// Supported types:
	//   - "openai":     OpenAI and all OpenAI-compatible APIs (Groq, DeepSeek,
	//                   Mistral, Together AI, Fireworks, Perplexity, xAI/Grok,
	//                   OpenRouter, Ollama, LM Studio, vLLM, GitHub Models, etc.)
	//   - "anthropic":  Anthropic Claude API
	//   - "vertex":     Google Vertex AI (Gemini) via OpenAI-compatible endpoint
	//                   with automatic Google ADC authentication
	//   - "gemini":     Google AI (Gemini) via generativelanguage.googleapis.com
	//                   with API key authentication (from AI Studio)
	//
	// Example YAML:
	//
	//   providers:
	//     anthropic:
	//       type: anthropic
	//       api_key: "sk-ant-..."
	//       model: "claude-haiku-4-5"
	//     openai:
	//       type: openai
	//       api_key: "sk-..."
	//       model: "gpt-4o"
	//     groq:
	//       type: openai
	//       api_key: "gsk_..."
	//       base_url: "https://api.groq.com/openai/v1/chat/completions"
	//       model: "llama-3.3-70b-versatile"
	//     ollama:
	//       type: openai
	//       base_url: "http://localhost:11434/v1/chat/completions"
	//       model: "llama3.2"
	//     github:
	//       type: openai
	//       api_key: "ghp_..."
	//       base_url: "https://models.github.ai/inference/chat/completions"
	//       model: "openai/gpt-4.1"
	//       extra_headers:
	//         Accept: "application/vnd.github+json"
	//         X-GitHub-Api-Version: "2022-11-28"
	//     vertex:
	//       type: vertex
	//       base_url: "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/endpoints/openapi/chat/completions"
	//       model: "google/gemini-2.5-flash"
	//     gemini:
	//       type: gemini
	//       api_key: "AIzaSy..."
	//       model: "gemini-2.5-flash"
	Providers map[string]LLMConfig `cfg:"providers"`

	// Gateway configures the OpenAI-compatible gateway server.
	Gateway Gateway `cfg:"gateway"`

	Store     Store       `cfg:"store"`
	Server    Server      `cfg:"server"`
	Bots      Bots        `cfg:"bots"`
	Telemetry tell.Config `cfg:"telemetry,noprefix"`
}

// Bots holds configuration for chat bot integrations.
type Bots struct {
	Discord  *DiscordBotConfig  `cfg:"discord"`
	Telegram *TelegramBotConfig `cfg:"telegram"`
}

// DiscordBotConfig holds Discord bot settings.
type DiscordBotConfig struct {
	Token           string            `cfg:"token" log:"-"`
	DefaultAgentID  string            `cfg:"default_agent_id"`
	ChannelAgents   map[string]string `cfg:"channel_agents"`
	AllowedAgentIDs []string          `cfg:"allowed_agent_ids"` // agent IDs users may !switch to; empty = switching disabled
	AccessMode      string            `cfg:"access_mode"`
	PendingApproval bool              `cfg:"pending_approval"`
	AllowedUsers    []string          `cfg:"allowed_users"`
}

// TelegramBotConfig holds Telegram bot settings.
type TelegramBotConfig struct {
	Token           string            `cfg:"token" log:"-"`
	DefaultAgentID  string            `cfg:"default_agent_id"`
	ChatAgents      map[string]string `cfg:"chat_agents"`
	AllowedAgentIDs []string          `cfg:"allowed_agent_ids"` // agent IDs users may /switch to; empty = switching disabled
	AccessMode      string            `cfg:"access_mode"`
	PendingApproval bool              `cfg:"pending_approval"`
	AllowedUsers    []string          `cfg:"allowed_users"`
}

type Server struct {
	BasePath string `cfg:"base_path"`

	Port string `cfg:"port" default:"8080"`
	Host string `cfg:"host"`

	// Name is the display name of the server, shown in the UI.
	Name string `cfg:"name" default:"AT"`

	// ForwardAuth, if set, configures the API to forward auth requests to an external
	// authentication service.
	ForwardAuth *mforwardauth.ForwardAuth `cfg:"forward_auth"`

	// AdminToken, if set, protects the /api/v1/settings/* endpoints with bearer
	// token authentication. Requests must include "Authorization: Bearer <token>".
	// If not set, all settings endpoints are disabled (403 Forbidden).
	AdminToken string `cfg:"admin_token" log:"-"`

	// UserHeader is the HTTP header name that contains the authenticated user's
	// email address (populated by the forward auth middleware).
	UserHeader string `cfg:"user_header" default:"X-User"`

	// ExternalURL is the public URL of this server (e.g. "https://at.example.com").
	// Used to generate OAuth callback links sent to bot users.
	// If empty, OAuth login commands in bots will not be available.
	ExternalURL string `cfg:"external_url"`

	// PacksDir is the directory where user-created integration packs are stored.
	// Defaults to ~/.config/at/packs/ if not set.
	PacksDir string `cfg:"packs_dir"`

	// Alan, if set, enables distributed clustering via UDP peer discovery.
	// This allows multiple AT instances to coordinate encryption key rotation
	// and other admin operations across the cluster.
	Alan *alan.Config `cfg:"alan"`
}

// Gateway configures the OpenAI-compatible gateway server endpoints.
//
// Example YAML:
//
//	gateway:
//	  auth_tokens:
//	    - token: "sk-master-key"
//	      name: "Master Key"
//	      # no restrictions = full access
//	    - token: "sk-ci-token"
//	      name: "CI Pipeline"
//	      allowed_providers:
//	        - openai
//	      allowed_models:
//	        - openai/gpt-4o
//	      expires_at: "2026-12-31T23:59:59Z"
type Gateway struct {
	// AuthTokens is a list of bearer tokens for gateway authentication.
	// Each token can optionally be scoped to specific providers/models and
	// can have an expiration date. If the list is empty, tokens can still
	// be managed via the UI/API (stored in the database).
	// If no auth tokens are configured at all (neither here nor in DB),
	// the gateway allows unauthenticated access.
	AuthTokens []AuthTokenConfig `cfg:"auth_tokens"`
}

// AuthTokenConfig describes a single bearer token for gateway authentication,
// with optional scoping and expiration.
type AuthTokenConfig struct {
	// Token is the bearer token value that clients send in the
	// "Authorization: Bearer <token>" header.
	Token string `cfg:"token" json:"token" log:"-"`

	// Name is an optional human-readable label for this token
	// (e.g., "CI Pipeline", "Dev Team").
	Name string `cfg:"name" json:"name"`

	// AllowedProvidersMode controls provider restriction: "all" (default/""), "none", or "list".
	AllowedProvidersMode string `cfg:"allowed_providers_mode" json:"allowed_providers_mode"`

	// AllowedProviders restricts this token to specific provider keys.
	// Only used when AllowedProvidersMode is "list".
	AllowedProviders []string `cfg:"allowed_providers" json:"allowed_providers"`

	// AllowedModelsMode controls model restriction: "all" (default/""), "none", or "list".
	AllowedModelsMode string `cfg:"allowed_models_mode" json:"allowed_models_mode"`

	// AllowedModels restricts this token to specific models in
	// "provider/model" format (e.g., "openai/gpt-4o").
	// Only used when AllowedModelsMode is "list".
	AllowedModels []string `cfg:"allowed_models" json:"allowed_models"`

	// AllowedWebhooksMode controls webhook restriction: "all" (default/""), "none", or "list".
	AllowedWebhooksMode string `cfg:"allowed_webhooks_mode" json:"allowed_webhooks_mode"`

	// AllowedWebhooks restricts this token to specific webhook triggers
	// by trigger ID or alias. Only used when AllowedWebhooksMode is "list".
	AllowedWebhooks []string `cfg:"allowed_webhooks" json:"allowed_webhooks"`

	// AllowedRAGMCPsMode controls RAG MCP restriction: "all" (default/""), "none", or "list".
	AllowedRAGMCPsMode string `cfg:"allowed_rag_mcps_mode" json:"allowed_rag_mcps_mode"`

	// AllowedRAGMCPs restricts this token to specific RAG MCP server
	// names. Only used when AllowedRAGMCPsMode is "list".
	AllowedRAGMCPs []string `cfg:"allowed_rag_mcps" json:"allowed_rag_mcps"`

	// ExpiresAt is an optional RFC3339 expiration timestamp.
	// After this time the token is rejected. If empty, the token never expires.
	ExpiresAt string `cfg:"expires_at" json:"expires_at"`
}

type Store struct {
	Postgres *StorePostgres `cfg:"postgres"`
	SQLite   *StoreSQLite   `cfg:"sqlite"`

	// EncryptionKey, if set, enables AES-256-GCM encryption for sensitive
	// provider fields (api_key, extra_headers values) stored in the database.
	// The key can be any non-empty string; it is zero-padded or truncated to
	// 32 bytes internally. When empty, no encryption is applied.
	EncryptionKey string `cfg:"encryption_key" log:"-"`
}

type StorePostgres struct {
	TablePrefix     *string        `cfg:"table_prefix"`
	Datasource      string         `cfg:"datasource" log:"-"`
	Schema          string         `cfg:"schema"`
	ConnMaxLifetime *time.Duration `cfg:"conn_max_lifetime"`
	MaxIdleConns    *int           `cfg:"max_idle_conns"`
	MaxOpenConns    *int           `cfg:"max_open_conns"`

	Migrate Migrate `cfg:"migrate"`
}

type StoreSQLite struct {
	TablePrefix *string `cfg:"table_prefix"`
	Datasource  string  `cfg:"datasource"`

	Migrate Migrate `cfg:"migrate"`
}

type Migrate struct {
	Datasource string            `cfg:"datasource" log:"-"`
	Schema     string            `cfg:"schema"`
	Table      string            `cfg:"table"`
	Values     map[string]string `cfg:"values"`
}

// LLMConfig describes a single LLM provider configuration.
type LLMConfig struct {
	// Type is the provider type: "anthropic", "openai", "vertex", or "gemini".
	// The "openai" type works with any OpenAI-compatible API.
	// The "vertex" type uses Google Application Default Credentials (ADC).
	// The "gemini" type uses API key authentication with generativelanguage.googleapis.com.
	Type string `cfg:"type" json:"type"`

	// APIKey is the authentication key for the provider.
	// Optional for local providers like Ollama and for "vertex" type (uses ADC).
	// Required for "gemini" type (get one from https://aistudio.google.com/apikey).
	APIKey string `cfg:"api_key" json:"api_key" log:"-"`

	// BaseURL is the full endpoint URL for the provider's chat completions API.
	// For "openai" type, defaults to "https://api.openai.com/v1/chat/completions".
	// For "anthropic" type, defaults to "https://api.anthropic.com".
	// For "vertex" type, required. Format:
	//   https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions
	// For "gemini" type, defaults to "https://generativelanguage.googleapis.com".
	BaseURL string `cfg:"base_url" json:"base_url"`

	// Model is the default model identifier to use (e.g., "gpt-4o", "claude-haiku-4-5").
	Model string `cfg:"model" json:"model"`

	// Models is the list of all models this provider supports.
	// When set, the gateway will reject requests for models not in this list (404).
	// The /v1/models endpoint will advertise all models in this list.
	// If empty, only the default Model is advertised and no strict validation is applied.
	Models []string `cfg:"models" json:"models"`

	// ExtraHeaders allows setting additional HTTP headers sent with each request.
	// Useful for providers that require custom headers (e.g., GitHub Models).
	ExtraHeaders map[string]string `cfg:"extra_headers" json:"extra_headers"`

	// AuthType selects the authentication mechanism for the provider.
	//
	// For "openai" type:
	//   - "" (empty):     Use APIKey directly as a static Bearer token (default).
	//   - "copilot":      GitHub Copilot authentication. Use the device-auth API endpoint
	//                     to authorize via the GitHub OAuth device flow. The resulting OAuth
	//                     token is stored in APIKey and exchanged for short-lived Copilot
	//                     JWTs that are cached and automatically refreshed before expiry.
	//
	// For "anthropic" type:
	//   - "" (empty):     Use APIKey directly as a static X-Api-Key header (default).
	//   - "claude-code":  Claude Code OAuth. Use the claude-auth API endpoints to
	//                     authorize via the Anthropic OAuth flow (open link, paste code).
	//                     The access token is stored in APIKey and the refresh token in
	//                     RefreshToken. Tokens are automatically refreshed before expiry.
	//                     Requires a Claude Pro or Max subscription.
	AuthType string `cfg:"auth_type" json:"auth_type"`

	// RefreshToken stores the OAuth refresh token for providers that use
	// token-based authentication with automatic refresh (e.g., auth_type="claude-code").
	// This field is managed automatically by the OAuth flow and should not be set manually.
	RefreshToken string `cfg:"refresh_token" json:"refresh_token" log:"-"`

	// Proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL to route all requests
	// through before reaching the provider. For example:
	//   - "http://proxy.example.com:8080"
	//   - "socks5://127.0.0.1:1080"
	// If empty, no proxy is used (requests go directly to the provider).
	Proxy string `cfg:"proxy" json:"proxy"`

	// InsecureSkipVerify disables TLS certificate verification when
	// connecting to the provider. Use this for self-signed certificates
	// or internal endpoints that don't have valid TLS certs.
	InsecureSkipVerify bool `cfg:"insecure_skip_verify" json:"insecure_skip_verify"`

	// RateLimit, if set, applies a per-provider rate limit to ALL traffic
	// going through this provider (agent calls AND gateway proxy calls).
	// Use it to keep within upstream per-account quotas like Anthropic
	// Pro/Max RPM and TPM. Leave nil for unlimited (default).
	//
	// Example for an Anthropic Pro/Max provider:
	//
	//   rate_limit:
	//     max_concurrent: 1
	//     requests_per_minute: 5
	//     input_tokens_per_minute: 30000
	//     wait_timeout_ms: 60000
	//     retry_after_cap_ms: 60000
	RateLimit *RateLimitConfig `cfg:"rate_limit" json:"rate_limit,omitempty"`
}

// RateLimitConfig describes the per-provider rate-limit policy. All fields
// are optional; a nil RateLimitConfig (or one with all zero fields) means
// no limiting.
type RateLimitConfig struct {
	// RequestsPerMinute caps the request rate (token-bucket).
	// 0 = unlimited.
	RequestsPerMinute int `cfg:"requests_per_minute" json:"requests_per_minute,omitempty"`

	// InputTokensPerMinute caps the weighted input-token rate.
	// 0 = unlimited.
	InputTokensPerMinute int `cfg:"input_tokens_per_minute" json:"input_tokens_per_minute,omitempty"`

	// MaxConcurrent caps the number of in-flight requests.
	// 0 = unlimited.
	MaxConcurrent int `cfg:"max_concurrent" json:"max_concurrent,omitempty"`

	// WaitTimeoutMs bounds how long a call will block waiting for the
	// limiter to permit it. 0 = use default (60s).
	WaitTimeoutMs int `cfg:"wait_timeout_ms" json:"wait_timeout_ms,omitempty"`

	// RetryAfterCapMs caps how long the agent retry loop will sleep when
	// the upstream API returns Retry-After. Special values:
	//   0  = use default cap (60s)
	//   -1 = no cap (honour whatever upstream says)
	//   >0 = cap in milliseconds
	RetryAfterCapMs int `cfg:"retry_after_cap_ms" json:"retry_after_cap_ms,omitempty"`
}

// IsZero reports whether the config disables all limiting.
func (c *RateLimitConfig) IsZero() bool {
	if c == nil {
		return true
	}
	return c.RequestsPerMinute <= 0 && c.InputTokensPerMinute <= 0 && c.MaxConcurrent <= 0
}

// WaitTimeout returns the configured wait timeout, or the default (60s)
// when WaitTimeoutMs is zero. Negative values are treated as zero.
func (c *RateLimitConfig) WaitTimeout() time.Duration {
	if c == nil || c.WaitTimeoutMs <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.WaitTimeoutMs) * time.Millisecond
}

// RetryAfterCap returns the configured Retry-After cap.
//   - returns 60s when RetryAfterCapMs == 0 (default)
//   - returns -1 when RetryAfterCapMs < 0 (no cap)
//   - returns RetryAfterCapMs as a duration otherwise
//
// Callers should treat the returned -1 sentinel as "do not cap".
func (c *RateLimitConfig) RetryAfterCap() time.Duration {
	if c == nil || c.RetryAfterCapMs == 0 {
		return 60 * time.Second
	}
	if c.RetryAfterCapMs < 0 {
		return -1
	}
	return time.Duration(c.RetryAfterCapMs) * time.Millisecond
}

func Load(ctx context.Context, path string) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, path, &cfg, chu.WithLoaderOption(loaderenv.New(loaderenv.WithPrefix("AT_")))); err != nil {
		return nil, err
	}

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
