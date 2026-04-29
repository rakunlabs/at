package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/server"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/gemini"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
	"github.com/rakunlabs/at/internal/service/ratelimit"
	"github.com/rakunlabs/at/internal/store"
)

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
				// Use a static token source — no auto-refresh.
				// Anthropic's token refresh endpoint behaves differently
				// depending on Content-Type and may produce tokens that
				// don't work with tools+thinking. The synced CLI token
				// is valid for 8 hours. Users can re-sync when it expires.
				ts := antropic.NewStaticTokenSource(cfg.APIKey)
				opts = append(opts, antropic.WithTokenSource(ts))
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
	default:
		return nil, fmt.Errorf("unknown provider type: %q (supported: anthropic, openai, vertex, gemini, minimax)", cfg.Type)
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
