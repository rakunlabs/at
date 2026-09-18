package config

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
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

// Config is the top-level YAML/env configuration loaded at startup.
//
// Bootstrap-only knobs (logging, server bind, store backend, telemetry)
// stay here because they must be available BEFORE the database is
// reachable. Everything else — LLM providers, gateway tokens, bot
// adapters, agentic-loop tuning — is managed at runtime through the UI
// (and persisted to the database). YAML / env do NOT carry those any
// more; defaults baked into `internal/service/loopgov` cover the loop
// governor and the database holds the provider, gateway-token, and
// bot-config rows.
type Config struct {
	LogLevel string `cfg:"log_level,no_prefix" default:"info"`

	Store     Store       `cfg:"store"`
	Server    Server      `cfg:"server"`
	Telemetry tell.Config `cfg:"telemetry,noprefix"`
}

// DiscordBotConfig holds Discord bot settings.
//
// Bot configurations are persisted in the database (`at_bot_configs`)
// and managed through the UI; this struct is the in-memory shape used
// when materialising a row before starting an adapter (see bot.go).
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
	// BasePath is the sub-path prefix the whole application is served under
	// (e.g. "/at"). Empty means the root. It is normalized by
	// NormalizeBasePath at load time to exactly one canonical form — leading
	// slash, no trailing slash — because routes are registered both through
	// ada groups (which normalize) and through raw prefix concatenation
	// (which does not), and the two disagree on any other form.
	BasePath string `cfg:"base_path"`

	Port string `cfg:"port" default:"8080"`
	Host string `cfg:"host"`

	// Name is the display name of the server, shown in the UI.
	Name string `cfg:"name" default:"AT"`

	// ForwardAuth, if set, configures the API to forward auth requests to an external
	// authentication service.
	ForwardAuth *mforwardauth.ForwardAuth `cfg:"forward_auth"`

	// NativeAuth is a migration-only import of legacy product settings. Runtime
	// authentication is enabled by default; settings live in PostgreSQL.
	NativeAuth *NativeAuth `cfg:"native_auth"`

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

	// Alan, if set, enables distributed clustering via QUIC peer discovery.
	// This allows multiple AT instances to coordinate encryption key rotation
	// and other admin operations across the cluster. Host terminal routing also
	// requires Alan security.enabled with a shared admission key.
	Alan *alan.Config `cfg:"alan"`

	// TrustedProxies lists the reverse proxies whose forwarded client-address
	// header this deployment believes. Entries are CIDR blocks, single IP
	// addresses, or the aliases `loopback` / `private`.
	//
	// Empty (the default) means no forwarded header is ever read and the
	// socket peer is the client address. Behind a proxy that peer is the
	// proxy, so login history, the sign-in lockout audit and the per-source
	// admission limiters all collapse onto one address until this is set.
	//
	// It is deliberately a bootstrap knob rather than a runtime setting:
	// whoever sets it decides whether callers can choose their own recorded
	// IP, and that is a property of the network the process is deployed in,
	// not of a workspace. Only list proxies that overwrite (not append to)
	// the configured header for inbound requests — trusting a proxy that
	// forwards a client-supplied value trusts the client.
	TrustedProxies []string `cfg:"trusted_proxies"`

	// TrustedProxyHeader selects which header carries the client address:
	// "X-Forwarded-For" (default), "X-Real-IP", or "Forwarded" (RFC 7239).
	// It has no effect while TrustedProxies is empty.
	TrustedProxyHeader string `cfg:"trusted_proxy_header"`

	// Workspace controls where per-task working directories and
	// truncated tool-output dumps are written, and (with an explicit root)
	// where persistent assets live. Defaults baked into
	// `internal/service/loopgov` (`/tmp/at-tasks`, 24h TTL) apply when
	// the block is omitted.
	//
	// On a small VM with a tiny boot disk, point `root` at a mounted
	// persistent data disk (e.g. `/mnt/at-workspace`) so the video pipeline
	// doesn't fill the root filesystem.
	Workspace *Workspace `cfg:"workspace"`
}

// NativeAuth retains backwards-compatible import fields. New installations use
// /auth/setup and /auth/settings instead of configuring these product knobs.
type NativeAuth struct {
	// SessionTTL is the absolute non-remembered lifetime; zero selects 8h.
	SessionTTL time.Duration `cfg:"session_ttl"`
	// RememberTTL is the absolute remembered lifetime; zero selects 30d.
	RememberTTL time.Duration `cfg:"remember_ttl"`
	Enabled     bool          `cfg:"enabled"`
	// Origin is the exact browser origin, without BasePath or trailing slash.
	Origin         string `cfg:"origin"`
	BootstrapToken string `cfg:"bootstrap_token" log:"-"`
	// InsecureHTTP permits HTTP only on a loopback origin for development.
	InsecureHTTP bool `cfg:"insecure_http"`
}

// Workspace bundles the bootstrap-time workspace knobs. They are passed
// straight through to `loopgov.Config` at server start; runtime
// reconfiguration is not supported (the dump/janitor paths are sampled
// once when the loop governor is constructed).
type Workspace struct {
	// Root is the directory under which `<task-id>/` workspaces and
	// `.at-tool-output/<run-id>/` dump dirs live. Empty = use the
	// loopgov default (`/tmp/at-tasks`).
	// A nonblank explicit root also selects <root>/assets for persistent
	// media; otherwise assets remain at ./data/assets. Relative paths are
	// relative to the process working directory. No assets are moved.
	// Use a persistent volume, not /tmp, for media to survive reboots.
	Root string `cfg:"root"`

	// TTLHours is how many hours a terminal-status task workspace is
	// kept before the janitor sweeps it. Tool-output dumps are swept
	// by mtime under the same TTL. 0 = use loopgov default (24h).
	// The reserved assets/ directory is never swept.
	// A negative value disables the janitor entirely (workspaces and
	// dumps are kept forever; useful for debugging).
	TTLHours int `cfg:"ttl_hours"`
}

type Store struct {
	Postgres *StorePostgres `cfg:"postgres"`

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

	// SharedWithAllWorkspaces publishes a Default-workspace provider for use in
	// every workspace. Only platform administrators may manage shared providers.
	SharedWithAllWorkspaces bool `json:"shared_with_all_workspaces,omitempty"`

	// Disabled keeps the provider configured but unavailable: it is hidden from
	// the workspace model catalog, from /gateway/v1/models and from model
	// discovery, and every request naming it is rejected. It is an availability
	// decision, deliberately separate from the credentials, so a provider can be
	// parked without deleting its keys and model list. Toggled through
	// PUT /api/v1/providers/{key}/disable; ordinary config updates preserve it.
	Disabled bool `json:"disabled,omitempty"`

	// APIKey is the authentication key for the provider.
	// Optional for local providers like Ollama and for "vertex" type (uses ADC).
	// Required for "gemini" type (get one from https://aistudio.google.com/apikey).
	APIKey string `cfg:"api_key" json:"api_key" log:"-"`

	// CredentialsJSON holds a Google Cloud service-account key file, verbatim,
	// for the "vertex" and "vertex-gemini" types. It is encrypted at rest with
	// the rest of the provider config, so unlike Application Default
	// Credentials — which are resolved from the server process and are
	// therefore installation-wide — a pasted key belongs to one provider row
	// and one workspace, and can be rotated from the UI without touching the
	// host. When empty those providers fall back to ADC, which is the previous
	// and still supported behaviour. Ignored by every other provider type.
	//
	// The token exchange honours Proxy and InsecureSkipVerify, because a
	// network that only reaches Google through a proxy does not reach
	// oauth2.googleapis.com either. ADC resolved from the GCE metadata server
	// is deliberately never proxied.
	CredentialsJSON string `cfg:"credentials_json" json:"credentials_json,omitempty" log:"-"`

	// BaseURL is the full endpoint URL for the provider's chat completions API.
	// For "openai" type, defaults to "https://api.openai.com/v1/chat/completions".
	// For "anthropic" type, defaults to "https://api.anthropic.com".
	// For "vertex" type, format:
	//   https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions
	// It is derived from the project and region when left empty.
	// For "gemini" type, defaults to "https://generativelanguage.googleapis.com".
	BaseURL string `cfg:"base_url" json:"base_url"`

	// Model is the default model identifier to use (e.g., "gpt-4o", "claude-haiku-4-5").
	Model string `cfg:"model" json:"model"`

	// Models is the list of all models this provider supports.
	// When set, the gateway will reject requests for models not in this list (404).
	// The /v1/models endpoint will advertise all models in this list.
	// If empty, only the default Model is advertised and no strict validation is applied.
	Models []string `cfg:"models" json:"models"`

	// EmbeddingModels is the list of embedding models this provider serves via
	// POST /gateway/v1/embeddings. They are advertised by /gateway/v1/models
	// alongside chat models. Advisory — requests for models outside this list
	// are still forwarded to the provider.
	EmbeddingModels []string `cfg:"embedding_models" json:"embedding_models"`

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
	//   - "chatgpt":       ChatGPT Plus/Pro OAuth for the Codex Responses backend. Use the
	//                     device-auth API endpoint; access and refresh tokens are persisted
	//                     and rotated automatically. This is not OpenAI Platform API billing.
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
	// token-based authentication with automatic refresh (e.g., "claude-code" or "chatgpt").
	// This field is managed automatically by the OAuth flow and should not be set manually.
	RefreshToken string `cfg:"refresh_token" json:"refresh_token" log:"-"`

	// TokenExpiresAt stores the access-token expiry time as RFC3339.
	// Used by OAuth-based auth (auth_type="claude-code" or "chatgpt") so the provider
	// can refresh proactively before the token expires instead of waiting
	// for an upstream 401. Managed automatically by the OAuth flow; an
	// empty value means "expiry unknown" — the token source will refresh
	// on first use.
	TokenExpiresAt string `cfg:"token_expires_at" json:"token_expires_at"`

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

// NormalizeBasePath canonicalizes a configured base path to the single form
// the rest of the process relies on: either "" (served at the root) or a
// cleaned path with a leading slash and no trailing slash.
//
// This has to happen once, centrally. Routes are registered two different
// ways — ada groups, which run path.Join and therefore tolerate "at" or
// "/at/", and raw string concatenation for the /auth, workspace and runtime
// route tables, which does not. Under "/at/" the latter produced patterns
// like "/at//auth/*" containing an empty segment that no request can ever
// match, so those endpoints silently disappeared while the rest of the
// application kept working.
//
// An input that cannot be made safe (containing a query, fragment, escape or
// backslash) is reported rather than silently mangled.
func NormalizeBasePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsAny(trimmed, "?#%\\") {
		return "", fmt.Errorf("base_path %q must not contain ?, #, %% or \\", raw)
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	// Reject parent segments rather than resolving them. path.Clean would
	// happily turn "/../etc" into "/etc", which hides an operator typo behind
	// a prefix that silently serves somewhere else.
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("base_path %q must not contain '..' segments", raw)
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "/" || cleaned == "." {
		// Serving at the root is expressed as the empty string so that
		// prefix concatenation never produces a doubled slash.
		return "", nil
	}
	return cleaned, nil
}

func Load(ctx context.Context, path string) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, path, &cfg, chu.WithLoaderOption(loaderenv.New(loaderenv.WithPrefix("AT_")))); err != nil {
		return nil, err
	}

	basePath, err := NormalizeBasePath(cfg.Server.BasePath)
	if err != nil {
		return nil, err
	}
	cfg.Server.BasePath = basePath

	// Fail fast: a malformed entry here would otherwise degrade silently into
	// "trust nothing", and the symptom (every login recorded from the proxy)
	// looks identical to not having configured it at all.
	if _, err := ParseTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, err
	}
	proxyHeader, err := NormalizeTrustedProxyHeader(cfg.Server.TrustedProxyHeader)
	if err != nil {
		return nil, err
	}
	cfg.Server.TrustedProxyHeader = proxyHeader

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
