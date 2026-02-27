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
	Telemetry tell.Config `cfg:"telemetry,noprefix"`
}

type Server struct {
	BasePath string `cfg:"base_path"`

	Port string `cfg:"port" default:"8080"`
	Host string `cfg:"host"`

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

	// AllowedProviders restricts this token to specific provider keys.
	// If empty/nil, all providers are accessible.
	AllowedProviders []string `cfg:"allowed_providers" json:"allowed_providers"`

	// AllowedModels restricts this token to specific models in
	// "provider/model" format (e.g., "openai/gpt-4o").
	// If empty/nil, all models are accessible.
	AllowedModels []string `cfg:"allowed_models" json:"allowed_models"`

	// AllowedWebhooks restricts this token to specific webhook triggers
	// by trigger ID or alias. If empty/nil, all webhooks are accessible.
	AllowedWebhooks []string `cfg:"allowed_webhooks" json:"allowed_webhooks"`

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
	// Supported values (only applies to "openai" type):
	//   - "" (empty):  Use APIKey directly as a static Bearer token (default).
	//   - "copilot":   GitHub Copilot authentication. Use the device-auth API endpoint
	//                  to authorize via the GitHub OAuth device flow. The resulting OAuth
	//                  token is stored in APIKey and exchanged for short-lived Copilot
	//                  JWTs that are cached and automatically refreshed before expiry.
	AuthType string `cfg:"auth_type" json:"auth_type"`

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
