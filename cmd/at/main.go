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
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
	"github.com/rakunlabs/at/internal/store"
)

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
	switch cfg.Type {
	case "anthropic":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("anthropic provider requires an api_key")
		}

		return antropic.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify)
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
			opts = append(opts, openai.WithTokenSource(openai.NewCopilotTokenSource(cfg.APIKey)))

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

		return openai.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify, headers, opts...)
	case "vertex":
		return vertex.New(cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify)
	case "gemini":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("gemini provider requires an api_key (get one from https://aistudio.google.com/apikey)")
		}
		return gemini.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.Proxy, cfg.InsecureSkipVerify)
	default:
		return nil, fmt.Errorf("unknown provider type: %q (supported: anthropic, openai, vertex, gemini)", cfg.Type)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Build all providers from YAML config.
	providers := make(map[string]server.ProviderInfo, len(cfg.Providers))
	for key, provCfg := range cfg.Providers {
		provider, err := newProvider(provCfg)
		if err != nil {
			slog.Warn("failed to create provider, skipping", "key", key, "error", err)
			continue
		}

		providers[key] = server.NewProviderInfo(provider, provCfg)
		slog.Debug("provider created from config", "key", key, "type", provCfg.Type, "model", provCfg.Model)
	}

	// Initialize store (falls back to in-memory if no backend is configured).
	st, err := store.New(ctx, cfg.Store)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}
	defer st.Close()

	// Load DB providers on top of YAML providers (DB overrides YAML).
	dbRecords, err := st.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("failed to load providers from DB: %w", err)
	}

	for _, rec := range dbRecords {
		provider, err := newProvider(rec.Config)
		if err != nil {
			slog.Warn("failed to create DB provider, skipping", "key", rec.Key, "error", err)
			continue
		}

		providers[rec.Key] = server.NewProviderInfo(provider, rec.Config)
		slog.Debug("provider loaded from DB (overrides YAML)", "key", rec.Key, "type", rec.Config.Type)
	}

	// Determine store type for info API.
	storeType := "memory"
	switch {
	case cfg.Store.Postgres != nil:
		storeType = "postgres"
	case cfg.Store.SQLite != nil:
		storeType = "sqlite"
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
	srv, err := server.New(ctx, cfg.Server, cfg.Gateway, providers, st, st, st, st, st, st, st, st, storeType, newProvider, cl, version)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	return srv.Start(ctx)
}
