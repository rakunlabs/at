package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"

	mfolder "github.com/rakunlabs/ada/handler/folder"
	mcors "github.com/rakunlabs/ada/middleware/cors"
	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"
)

//go:embed dist/*
var uiFS embed.FS

// ProviderInfo holds a provider instance along with its metadata.
type ProviderInfo struct {
	provider     service.LLMProvider
	providerType string // "anthropic", "openai", "vertex", "gemini"
	defaultModel string
	models       []string // all supported models; if empty, only defaultModel is advertised
}

// ProviderFactory is a function that creates an LLMProvider from an LLMConfig.
// This is injected from main.go so the server can hot-reload providers.
type ProviderFactory func(cfg config.LLMConfig) (service.LLMProvider, error)

type Server struct {
	config config.Server

	server *ada.Server

	// Provider registry for the gateway (protected by providerMu).
	providers  map[string]ProviderInfo
	providerMu sync.RWMutex

	// Store is the optional persistent store for provider CRUD.
	store service.ProviderStorer

	// tokenStore is the optional persistent store for API token management.
	tokenStore service.APITokenStorer

	// providerFactory creates an LLMProvider from config (for hot reload).
	providerFactory ProviderFactory

	storeType  string // "postgres", "sqlite", or "none"
	authTokens []config.AuthTokenConfig

	// cluster is the optional distributed coordination layer (alan).
	// nil when clustering is not configured (single-instance mode).
	cluster *cluster.Cluster

	// tokenLastUsed tracks when each token's last_used_at was last written to
	// the DB, so we can throttle updates to at most once per 5 minutes.
	tokenLastUsed sync.Map // map[string]time.Time

	// tokenLastUsedMu holds a per-token mutex so concurrent requests for the
	// same token don't fire redundant DB writes.
	tokenLastUsedMu sync.Map // map[string]*sync.Mutex
}

func New(ctx context.Context, cfg config.Server, gatewayCfg config.Gateway, providers map[string]ProviderInfo, store service.ProviderStorer, tokenStore service.APITokenStorer, storeType string, factory ProviderFactory, cl *cluster.Cluster) (*Server, error) {
	mux := ada.New()
	mux.Use(
		mrecover.Middleware(),
		mserver.Middleware(config.Service),
		mcors.Middleware(),
		mrequestid.Middleware(),
		mlog.Middleware(),
		mtelemetry.Middleware(),
	)

	s := &Server{
		config:          cfg,
		server:          mux,
		providers:       providers,
		store:           store,
		tokenStore:      tokenStore,
		providerFactory: factory,
		storeType:       storeType,
		authTokens:      gatewayCfg.AuthTokens,
		cluster:         cl,
	}

	// ////////////////////////////////////////////

	if cfg.BasePath != "" {
		slog.Info("configuring server with base path", "base_path", cfg.BasePath)
	}

	baseGroup := mux.Group(cfg.BasePath)

	// OpenAI-compatible gateway API (separate prefix so clients use /gateway/v1/ as base URL)
	gatewayGroup := mux.Group(cfg.BasePath + "/gateway")
	gatewayGroup.POST("/v1/chat/completions", s.ChatCompletions)
	gatewayGroup.GET("/v1/models", s.ListModels)

	// ////////////////////////////////////////////
	if cfg.ForwardAuth != nil {
		slog.Info("forward auth enabled", "url", cfg.ForwardAuth.Address)
		baseGroup.Use(mforwardauth.Middleware(mforwardauth.WithConfig(*cfg.ForwardAuth)))
	} else {
		slog.Info("forward auth disabled (no forward_auth config)")
	}

	apiGroup := baseGroup.Group("/api")

	// Gateway info API
	apiGroup.GET("/v1/info", s.InfoAPI)

	// Provider management API
	apiGroup.GET("/v1/providers", s.ListProvidersAPI)
	apiGroup.POST("/v1/providers", s.CreateProviderAPI)
	apiGroup.POST("/v1/providers/discover-models", s.DiscoverModelsAPI)
	apiGroup.POST("/v1/providers/device-auth", s.DeviceAuthAPI)
	apiGroup.GET("/v1/providers/device-auth-status", s.DeviceAuthStatusAPI)
	apiGroup.GET("/v1/providers/*", s.GetProviderAPI)
	apiGroup.PUT("/v1/providers/*", s.UpdateProviderAPI)
	apiGroup.DELETE("/v1/providers/*", s.DeleteProviderAPI)

	// API Token management
	apiGroup.GET("/v1/api-tokens", s.ListAPITokensAPI)
	apiGroup.POST("/v1/api-tokens", s.CreateAPITokenAPI)
	apiGroup.PUT("/v1/api-tokens/*", s.UpdateAPITokenAPI)
	apiGroup.DELETE("/v1/api-tokens/*", s.DeleteAPITokenAPI)

	// Admin API (protected by admin token)
	adminGroup := apiGroup.Group("/v1/admin")
	adminGroup.Use(s.adminAuthMiddleware())
	adminGroup.POST("/rotate-key", s.RotateKeyAPI)

	// ////////////////////////////////////////////

	f, err := fs.Sub(uiFS, "dist")
	if err != nil {
		return nil, err
	}

	folderM, err := mfolder.New(&mfolder.Config{
		BasePath:       cfg.BasePath,
		Index:          true,
		StripIndexName: true,
		SPA:            true,
		PrefixPath:     cfg.BasePath,
		CacheRegex: []*mfolder.RegexCacheStore{
			{
				Regex:        `index\.html$`,
				CacheControl: "no-store",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	folderM.SetFs(http.FS(f))

	baseGroup.Handle("/*", folderM)

	return s, nil
}

// NewProviderInfo creates a ProviderInfo from a provider and its config.
func NewProviderInfo(provider service.LLMProvider, providerType, defaultModel string, models []string) ProviderInfo {
	return ProviderInfo{
		provider:     provider,
		providerType: providerType,
		defaultModel: defaultModel,
		models:       models,
	}
}

func (s *Server) Start(ctx context.Context) error {
	return s.server.StartWithContext(ctx, net.JoinHostPort(s.config.Host, s.config.Port))
}

// ─── Hot Reload ───

// reloadProvider creates a new LLMProvider from config and updates the
// in-memory provider registry. Called after DB create/update operations.
func (s *Server) reloadProvider(key string, cfg config.LLMConfig) error {
	if s.providerFactory == nil {
		return fmt.Errorf("no provider factory configured")
	}

	provider, err := s.providerFactory(cfg)
	if err != nil {
		return fmt.Errorf("create provider %q: %w", key, err)
	}

	info := NewProviderInfo(provider, cfg.Type, cfg.Model, cfg.Models)

	s.providerMu.Lock()
	s.providers[key] = info
	s.providerMu.Unlock()

	slog.Info("provider hot-reloaded", "key", key, "type", cfg.Type)

	return nil
}

// removeProvider removes a provider from the in-memory registry.
// Called after DB delete operations.
func (s *Server) removeProvider(key string) {
	s.providerMu.Lock()
	delete(s.providers, key)
	s.providerMu.Unlock()

	slog.Info("provider removed from registry", "key", key)
}

// adminAuthMiddleware returns middleware that protects admin endpoints.
// If no admin_token is configured, all admin requests are rejected with 403.
// If configured, requests must provide a matching Authorization: Bearer <token> header.
func (s *Server) adminAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.config.AdminToken == "" {
				httpResponse(w, "admin token not configured", http.StatusForbidden)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				httpResponse(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth || token != s.config.AdminToken {
				httpResponse(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
