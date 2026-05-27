package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/server"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/bedrock"
	"github.com/rakunlabs/at/internal/service/llm/cohere"
	"github.com/rakunlabs/at/internal/service/llm/gemini"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
	"github.com/rakunlabs/at/internal/service/ratelimit"
	"github.com/rakunlabs/at/internal/store"
)

// googleADCTokenSource resolves Google Application Default Credentials
// for the cloud-platform scope and adapts the resulting oauth2.TokenSource
// to the gemini package's GoogleTokenSource interface.
//
// The underlying oauth2.TokenSource caches and auto-refreshes tokens
// internally, so calling Token() on every request is cheap.
func googleADCTokenSource(ctx context.Context) (gemini.GoogleTokenSource, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	return &googleADCSource{inner: ts}, nil
}

// googleADCSource adapts oauth2.TokenSource → gemini.GoogleTokenSource.
type googleADCSource struct {
	inner oauth2TokenSource
}

// oauth2TokenSource is the minimum interface from golang.org/x/oauth2.
// We avoid importing oauth2 in this struct's type signature to keep the
// dependency surface small.
type oauth2TokenSource interface {
	Token() (*oauth2.Token, error)
}

// Token implements gemini.GoogleTokenSource.
func (g *googleADCSource) Token() (string, error) {
	tok, err := g.inner.Token()
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// buildLimiter constructs a rate limiter from the provider config, or
// returns nil when no limits are configured.
func buildLimiter(rl *config.RateLimitConfig) *ratelimit.Limiter {
	if rl.IsZero() {
		return nil
	}
	return ratelimit.New(ratelimit.Config{
		RequestsPerMinute: rl.RequestsPerMinute,
		InputTokensPerMin: rl.InputTokensPerMinute,
		MaxConcurrent:     rl.MaxConcurrent,
		WaitTimeout:       rl.WaitTimeout(),
	})
}

var (
	name    = "at"
	version = "v0.0.0"
	commit  = "-"
	date    = "-"
)

func main() {
	config.Service = name + "/" + version

	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s [%s] commit=%s date=%s", name, version, commit, date),
	)
}

// ///////////////////////////////////////////////////////////////////

func newProvider(cfg config.LLMConfig) (service.LLMProvider, error) {
	// Build the per-provider rate limiter once. It's safe to share with
	// any of the provider types; nil means no limiting.
	limiter := buildLimiter(cfg.RateLimit)

	switch cfg.Type {
	case "anthropic":
		var opts []antropic.Option

		switch cfg.AuthType {
		case "claude-code":
			if cfg.APIKey != "" {
				// When a refresh token is present, use the auto-refreshing
				// OAuth token source so the access token is rotated before
				// it expires (Claude Pro/Max access tokens are valid for
				// ~8 hours). If only an access token is set (no refresh
				// token), fall back to a static source — the user will
				// have to re-sync from the UI when it expires.
				if cfg.RefreshToken != "" {
					// Build a proxy-aware HTTP client so refresh requests
					// to platform.claude.com go through the same proxy as
					// the inference traffic.
					httpClient, err := openai.ProxyHTTPClient(cfg.Proxy, cfg.InsecureSkipVerify)
					if err != nil {
						return nil, fmt.Errorf("failed to create proxy client for claude oauth: %w", err)
					}

					// Parse the persisted expiry. An empty / unparseable
					// value yields a zero time, which OAuthTokenSource
					// treats as "expired" — it will refresh on first use.
					var expiresAt time.Time
					if cfg.TokenExpiresAt != "" {
						if t, perr := time.Parse(time.RFC3339, cfg.TokenExpiresAt); perr == nil {
							expiresAt = t
						} else {
							slog.Warn("anthropic provider: ignoring unparseable token_expires_at",
								"value", cfg.TokenExpiresAt, "error", perr.Error())
						}
					}

					// Persistence callback is wired by the server after
					// the provider is constructed (see Server.wireClaudeOAuthCallback);
					// passing nil here keeps cmd/at decoupled from the store.
					ts := antropic.NewOAuthTokenSource(cfg.APIKey, cfg.RefreshToken, expiresAt, httpClient, nil)
					opts = append(opts, antropic.WithTokenSource(ts))
				} else {
					ts := antropic.NewStaticTokenSource(cfg.APIKey)
					opts = append(opts, antropic.WithTokenSource(ts))
				}
			}
			// If no APIKey yet, create the provider without a token source.
			// The user will need to complete the OAuth flow via the UI.
		case "":
			if cfg.APIKey == "" {
				return nil, fmt.Errorf("anthropic provider requires an api_key")
			}
		default:
			return nil, fmt.Errorf("unknown auth_type %q for anthropic provider (supported: claude-code)", cfg.AuthType)
		}

		if limiter != nil {
			opts = append(opts, antropic.WithRateLimiter(limiter))
		}

		// Prompt caching is ON by default; operators can disable via
		// ExtraHeaders["at-prompt-caching"]="off" when they need byte-
		// identical wire output for compliance / replay scenarios.
		if v, ok := cfg.ExtraHeaders["at-prompt-caching"]; ok && strings.EqualFold(v, "off") {
			opts = append(opts, antropic.WithPromptCachingDisabled(true))
		}

		return antropic.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
	case "openai":
		var opts []openai.Option

		// Clone extra headers so we don't mutate the original config map.
		headers := make(map[string]string, len(cfg.ExtraHeaders)+4)
		maps.Copy(headers, cfg.ExtraHeaders)

		switch cfg.AuthType {
		case "copilot":
			if cfg.APIKey == "" {
				return nil, fmt.Errorf("openai provider with auth_type=copilot requires an api_key (authorize via device flow)")
			}

			// Build a proxy-aware HTTP client for the Copilot token exchange
			// so it can reach api.github.com through the configured proxy.
			httpClient, err := openai.ProxyHTTPClient(cfg.Proxy, cfg.InsecureSkipVerify)
			if err != nil {
				return nil, fmt.Errorf("failed to create proxy client for copilot token source: %w", err)
			}

			opts = append(opts, openai.WithTokenSource(openai.NewCopilotTokenSource(cfg.APIKey, httpClient)))

			// Copilot API requires editor identification headers on every request.
			if _, ok := headers["Editor-Version"]; !ok {
				headers["Editor-Version"] = "vscode/1.95.0"
			}
			if _, ok := headers["Editor-Plugin-Version"]; !ok {
				headers["Editor-Plugin-Version"] = "copilot/1.0.0"
			}
			if _, ok := headers["User-Agent"]; !ok {
				headers["User-Agent"] = "GithubCopilot/1.0"
			}
			if _, ok := headers["Copilot-Integration-Id"]; !ok {
				headers["Copilot-Integration-Id"] = "vscode-chat"
			}
		case "":
			// Default: use static APIKey as Bearer token (handled by klient).
		default:
			return nil, fmt.Errorf("unknown auth_type %q for openai provider (supported: copilot)", cfg.AuthType)
		}

		if limiter != nil {
			opts = append(opts, openai.WithRateLimiter(limiter))
		}

		return openai.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, headers, opts...)
	case "vertex":
		var opts []vertex.Option
		if limiter != nil {
			opts = append(opts, vertex.WithRateLimiter(limiter))
		}
		return vertex.New(cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
	case "gemini":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("gemini provider requires an api_key (get one from https://aistudio.google.com/apikey)")
		}
		var opts []gemini.Option
		if limiter != nil {
			opts = append(opts, gemini.WithRateLimiter(limiter))
		}
		return gemini.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
	case "minimax":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("minimax provider requires an api_key (get one from https://platform.minimax.io)")
		}
		headers := make(map[string]string, len(cfg.ExtraHeaders))
		maps.Copy(headers, cfg.ExtraHeaders)
		var anthropicOpts []antropic.Option
		if limiter != nil {
			anthropicOpts = append(anthropicOpts, antropic.WithRateLimiter(limiter))
		}
		return minimax.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, headers, anthropicOpts...)
	case "bedrock":
		var opts []bedrock.Option
		if limiter != nil {
			opts = append(opts, bedrock.WithRateLimiter(limiter))
		}
		return bedrock.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
	case "azure":
		// Azure OpenAI uses an OpenAI-compatible wire format with a few
		// differences (api-version query param, api-key header, resource-
		// scoped URLs). We funnel it through the openai adapter with an
		// extra-headers tweak that injects `api-key` rather than Bearer.
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("azure provider requires an api_key (Azure OpenAI resource key)")
		}
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("azure provider requires a base_url like https://<resource>.openai.azure.com/openai/deployments/<deployment>/chat/completions?api-version=2024-10-21")
		}
		headers := make(map[string]string, len(cfg.ExtraHeaders)+1)
		maps.Copy(headers, cfg.ExtraHeaders)
		// Azure auth is `api-key: <key>` rather than `Authorization: Bearer`.
		headers["api-key"] = cfg.APIKey
		var azOpts []openai.Option
		if limiter != nil {
			azOpts = append(azOpts, openai.WithRateLimiter(limiter))
		}
		// Pass apiKey="" so the OpenAI adapter doesn't also set
		// `Authorization: Bearer <key>`, which Azure rejects.
		return openai.New("", cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, headers, azOpts...)
	case "vertex-gemini":
		// Native Gemini API via Vertex AI. Unlike "vertex" (which uses
		// the OpenAI-compatible adapter on Vertex), this routes through
		// the gemini provider so we keep features like thinkingConfig,
		// safetySettings, grounding, and cachedContent.
		//
		// Required cfg.BaseURL format:
		//   https://{REGION}-aiplatform.googleapis.com
		// We auto-append /v1/projects/{PROJECT}/locations/{REGION}/publishers/google
		// when the URL stops at the regional host.
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("vertex-gemini provider requires base_url (e.g. https://us-central1-aiplatform.googleapis.com) and AT_VERTEX_PROJECT env")
		}
		project := cfg.ExtraHeaders["vertex_project"]
		region := cfg.ExtraHeaders["vertex_region"]
		if project == "" {
			return nil, fmt.Errorf("vertex-gemini provider requires extra_headers.vertex_project")
		}
		if region == "" {
			region = "us-central1"
		}
		pathPrefix := fmt.Sprintf("/v1/projects/%s/locations/%s/publishers/google", project, region)

		ts, err := googleADCTokenSource(context.Background())
		if err != nil {
			return nil, fmt.Errorf("vertex-gemini ADC: %w", err)
		}

		var gopts []gemini.Option
		gopts = append(gopts, gemini.WithGoogleTokenSource(ts))
		gopts = append(gopts, gemini.WithPathPrefix(pathPrefix))
		if limiter != nil {
			gopts = append(gopts, gemini.WithRateLimiter(limiter))
		}
		return gemini.New("", cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, gopts...)
	case "cohere":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("cohere provider requires an api_key (get one from https://dashboard.cohere.com)")
		}
		var copts []cohere.Option
		if limiter != nil {
			copts = append(copts, cohere.WithRateLimiter(limiter))
		}
		return cohere.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, copts...)
	default:
		return nil, fmt.Errorf("unknown provider type: %q (supported: anthropic, openai, vertex, gemini, minimax, bedrock, azure, cohere)", cfg.Type)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize store. Falls back to a default on-disk SQLite database
	// (./data/at.db) if no backend is configured. The ./data/ directory
	// is created on startup if missing, so Docker users can bind-mount a
	// host volume to /data in a single step.
	st, err := store.New(ctx, cfg.Store)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}
	defer st.Close()

	// Build LLM providers from the database. Provider definitions are
	// no longer accepted via YAML — add them through the UI / API.
	dbRecords, err := st.ListProviders(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to load providers from DB: %w", err)
	}

	providers := make(map[string]server.ProviderInfo, len(dbRecords.Data))
	for _, rec := range dbRecords.Data {
		provider, err := newProvider(rec.Config)
		if err != nil {
			slog.Warn("failed to create DB provider, skipping", "key", rec.Key, "error", err)
			continue
		}

		providers[rec.Key] = server.NewProviderInfo(provider, rec.Config)
		slog.Debug("provider loaded from DB", "key", rec.Key, "type", rec.Config.Type)
	}

	// Determine store type for info API.
	// The store is always either postgres or sqlite (default when unconfigured).
	storeType := "sqlite"
	if cfg.Store.Postgres != nil {
		storeType = "postgres"
	}

	// Initialize optional cluster (distributed coordination via alan).
	cl, err := cluster.New(cfg.Server.Alan)
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	if cl != nil {
		// The onNewKey callback is invoked when a peer broadcasts a new
		// encryption key. We update the store's in-memory key so future
		// decrypt operations use the rotated key. The in-memory provider
		// configs already contain plaintext, so no reload is needed.
		onNewKey := func(newKey []byte) {
			if updater, ok := st.(service.EncryptionKeyUpdater); ok {
				updater.SetEncryptionKey(newKey)
				slog.Info("encryption key updated from cluster peer broadcast")
			}
		}

		go func() {
			if err := cl.Start(ctx, onNewKey); err != nil {
				slog.Error("cluster stopped with error", "error", err)
			}
		}()
		defer func() {
			if err := cl.Stop(); err != nil {
				slog.Error("cluster shutdown error", "error", err)
			}
		}()

		slog.Info("cluster enabled, waiting for peers", "dns_addr", cfg.Server.Alan.DNSAddr)
	}

	// Create and start HTTP server.
	srv, err := server.New(ctx, cfg.Server, providers, st, storeType, newProvider, cl, version)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return srv.Start(ctx)
}
