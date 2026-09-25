package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"golang.org/x/oauth2"

	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/server"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/bedrock"
	"github.com/rakunlabs/at/internal/service/llm/cohere"
	"github.com/rakunlabs/at/internal/service/llm/gcp"
	"github.com/rakunlabs/at/internal/service/llm/gemini"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
	"github.com/rakunlabs/at/internal/service/ratelimit"
	"github.com/rakunlabs/at/internal/store"
)

// vertexTokenSource resolves the credentials for a Vertex provider: the
// service-account key stored on the provider row when it carries one, and
// Application Default Credentials from this process otherwise.
//
// The provider's proxy and TLS settings are applied to the token exchange as
// well as to inference. A network that can only reach Google through a proxy
// cannot reach oauth2.googleapis.com directly either, and without this the
// provider fails while fetching the token rather than while calling the model.
//
// The returned source caches and auto-refreshes internally, so calling Token()
// on every request is cheap. context.Background() is deliberate: oauth2 keeps
// the context for the lifetime of the provider, so a request context would
// cancel every later refresh.
func vertexTokenSource(cfg config.LLMConfig) (oauth2.TokenSource, error) {
	httpClient, err := openai.ProxyHTTPClient(cfg.Proxy, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("proxy client for the Google token exchange: %w", err)
	}

	return gcp.TokenSource(context.Background(), cfg.CredentialsJSON, httpClient)
}

// vertexProjectRegion resolves the GCP project and region for a Vertex
// provider. The project falls back to the one named by the stored
// service-account key, which is the project the key can actually reach — so a
// pasted key is a complete configuration on its own.
func vertexProjectRegion(cfg config.LLMConfig) (project, region string) {
	project = strings.TrimSpace(cfg.ExtraHeaders["vertex_project"])
	region = gcp.Region(cfg.ExtraHeaders["vertex_region"])

	if project == "" && cfg.CredentialsJSON != "" {
		if creds, err := gcp.ParseCredentials(cfg.CredentialsJSON); err == nil {
			project = creds.ProjectID
		}
	}

	return project, region
}

// googleAccessTokenSource adapts oauth2.TokenSource → gemini.GoogleTokenSource,
// which carries the bare access token because that is all the Gemini adapter
// puts on the wire.
type googleAccessTokenSource struct {
	inner oauth2TokenSource
}

// oauth2TokenSource is the minimum interface from golang.org/x/oauth2.
// We avoid importing oauth2 in this struct's type signature to keep the
// dependency surface small.
type oauth2TokenSource interface {
	Token() (*oauth2.Token, error)
}

// Token implements gemini.GoogleTokenSource.
func (g *googleAccessTokenSource) Token() (string, error) {
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

		opts = append(opts, antropic.WithExtraHeaders(cfg.ExtraHeaders))
		return antropic.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
	case "openai":
		var opts []openai.Option

		// Clone extra headers so we don't mutate the original config map.
		headers := make(map[string]string, len(cfg.ExtraHeaders)+4)
		maps.Copy(headers, cfg.ExtraHeaders)

		switch cfg.AuthType {
		case "chatgpt":
			httpClient, err := openai.ProxyHTTPClient(cfg.Proxy, cfg.InsecureSkipVerify)
			if err != nil {
				return nil, fmt.Errorf("failed to create proxy client for ChatGPT OAuth: %w", err)
			}

			var expiresAt time.Time
			if cfg.TokenExpiresAt != "" {
				if t, parseErr := time.Parse(time.RFC3339, cfg.TokenExpiresAt); parseErr == nil {
					expiresAt = t
				} else {
					slog.Warn("ChatGPT provider: ignoring unparseable token_expires_at",
						"value", cfg.TokenExpiresAt, "error", parseErr.Error())
				}
			}

			accountID := headers["ChatGPT-Account-ID"]
			var tokenSource openai.TokenSource
			if cfg.APIKey != "" {
				tokenSource = openai.NewCodexTokenSource(cfg.APIKey, cfg.RefreshToken, accountID, expiresAt, httpClient)
			}

			codexOpts := []openai.CodexProviderOption{
				openai.WithCodexBaseURL(cfg.BaseURL),
				openai.WithCodexHTTPClient(httpClient),
				openai.WithCodexClientVersion(openai.CodexClientVersion),
			}
			if limiter != nil {
				codexOpts = append(codexOpts, openai.WithCodexRateLimiter(limiter))
			}
			return openai.NewCodexProvider(cfg.Model, accountID, tokenSource, codexOpts...), nil
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
			headers = openai.ApplyCopilotHeaders(headers)
		case "":
			// Default: use static APIKey as Bearer token (handled by the HTTP client).
		default:
			return nil, fmt.Errorf("unknown auth_type %q for openai provider (supported: copilot, chatgpt)", cfg.AuthType)
		}

		if limiter != nil {
			opts = append(opts, openai.WithRateLimiter(limiter))
		}

		return openai.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, headers, opts...)
	case "vertex":
		ts, err := vertexTokenSource(cfg)
		if err != nil {
			return nil, fmt.Errorf("vertex credentials: %w", err)
		}

		// The endpoint shape is fixed by Google, so a provider that names its
		// project and region does not also have to paste a 130-character URL
		// and get every segment right. An explicit base_url still wins.
		endpoint := cfg.BaseURL
		if endpoint == "" {
			project, region := vertexProjectRegion(cfg)
			if project == "" {
				return nil, fmt.Errorf("vertex provider requires a base_url, or a project to derive one from " +
					"(extra_headers.vertex_project, or a credentials_json carrying its project_id)")
			}
			endpoint = gcp.ChatCompletionsEndpoint(project, region)
		}

		opts := []vertex.Option{vertex.WithTokenSource(ts)}
		if limiter != nil {
			opts = append(opts, vertex.WithRateLimiter(limiter))
		}
		return vertex.New(cfg.Model, endpoint, cfg.Proxy, cfg.InsecureSkipVerify, opts...)
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
		// cfg.BaseURL format:
		//   https://{REGION}-aiplatform.googleapis.com
		// We auto-append /v1/projects/{PROJECT}/locations/{REGION}/publishers/google
		// when the URL stops at the regional host, and derive the host itself
		// from the region when no base_url is given — the preset has always
		// told operators to leave it empty, and rejecting that was a bug.
		project, region := vertexProjectRegion(cfg)
		if project == "" {
			return nil, fmt.Errorf("vertex-gemini provider requires extra_headers.vertex_project, " +
				"or a credentials_json carrying its project_id")
		}

		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = gcp.RegionalHost(region)
		}
		pathPrefix := fmt.Sprintf("/v1/projects/%s/locations/%s/publishers/google", project, region)

		ts, err := vertexTokenSource(cfg)
		if err != nil {
			return nil, fmt.Errorf("vertex-gemini credentials: %w", err)
		}

		var gopts []gemini.Option
		gopts = append(gopts, gemini.WithGoogleTokenSource(&googleAccessTokenSource{inner: ts}))
		gopts = append(gopts, gemini.WithPathPrefix(pathPrefix))
		if limiter != nil {
			gopts = append(gopts, gemini.WithRateLimiter(limiter))
		}
		return gemini.New("", cfg.Model, baseURL, cfg.Proxy, cfg.InsecureSkipVerify, gopts...)
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
		return nil, fmt.Errorf("unknown provider type: %q (supported: %s)", cfg.Type, strings.Join(service.SupportedProviderTypes, ", "))
	}
}

func run(ctx context.Context) error {
	// Operator recovery runs before the server boots so an installation with
	// no usable administrator credential can still be recovered.
	if handled, err := runAuthCommand(ctx, os.Args[1:]); handled {
		return err
	}
	cfg, err := config.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize store. Postgres is the only supported backend; startup
	// fails with a descriptive error when store.postgres is not configured.
	st, err := store.New(ctx, cfg.Store)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}
	defer st.Close()

	// Build LLM providers from the database. Provider definitions are
	// no longer accepted via YAML — add them through the UI / API.
	// The boot registry holds installation-owned providers. Workspace-owned
	// providers are never cached globally: scoped execution resolves those
	// per request through the workspace provider grants.
	dbRecords, err := st.ListProviders(service.WithLegacyWorkspaceAccess(ctx), nil)
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

		providers[rec.Key] = server.NewProviderInfo(provider, rec.Config).WithProviderID(rec.ID)
		slog.Debug("provider loaded from DB", "key", rec.Key, "type", rec.Config.Type)
	}

	// Store type for the info API — postgres is the only backend.
	storeType := "postgres"

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
	srv, err := server.New(ctx, cfg.Server, providers, st, storeType, newProvider, cl, version, commit, date)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return srv.Start(ctx)
}
