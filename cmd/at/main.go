package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/server"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
	"github.com/rakunlabs/at/internal/store"
)

var (
	name    = "at"
	version = "v0.0.0"
)

func main() {
	config.Service = name + "/" + version

	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s [%s]", name, version),
	)
}

// ///////////////////////////////////////////////////////////////////

func newProvider(cfg config.LLMConfig) (service.LLMProvider, error) {
	switch cfg.Type {
	case "anthropic":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("anthropic provider requires an api_key")
		}

		return antropic.New(cfg.APIKey, cfg.Model, cfg.BaseURL)
	case "openai":
		return openai.New(cfg.APIKey, cfg.Model, cfg.BaseURL, cfg.ExtraHeaders)
	case "vertex":
		return vertex.New(cfg.Model, cfg.BaseURL)
	default:
		return nil, fmt.Errorf("unknown provider type: %q (supported: anthropic, openai, vertex)", cfg.Type)
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

		providers[key] = server.NewProviderInfo(provider, provCfg.Type, provCfg.Model, provCfg.Models)
		slog.Info("provider created from config", "key", key, "type", provCfg.Type, "model", provCfg.Model)
	}

	// Initialize store (optional — only if postgres is configured).
	var providerStore service.ProviderStorer
	if cfg.Store.Postgres != nil {
		st, err := store.New(ctx, cfg.Store)
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer st.Close()

		providerStore = st

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

			providers[rec.Key] = server.NewProviderInfo(provider, rec.Config.Type, rec.Config.Model, rec.Config.Models)
			slog.Info("provider loaded from DB (overrides YAML)", "key", rec.Key, "type", rec.Config.Type)
		}
	}

	if len(providers) == 0 {
		slog.Warn("no providers configured; gateway will have no backends until providers are added via API")
	}

	// Create and start HTTP server.
	srv, err := server.New(ctx, cfg.Server, cfg.Gateway, providers, providerStore, newProvider)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	slog.Info("starting gateway server", "host", cfg.Server.Host, "port", cfg.Server.Port)

	return srv.Start(ctx)
}
