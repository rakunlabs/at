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
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"

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

	// workflowStore is the persistent store for workflow definitions.
	workflowStore service.WorkflowStorer

	// triggerStore is the persistent store for workflow triggers.
	triggerStore service.TriggerStorer

	// scheduler is the cron trigger scheduler (nil if triggerStore is nil).
	scheduler *workflow.Scheduler

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

	// thoughtSigCache caches Gemini thought_signature tokens keyed by tool
	// call ID.  Many OpenAI-compatible clients (e.g. Vercel AI SDK) strip
	// unknown fields from tool calls when echoing them back.  Gemini 2.5+
	// thinking models require thought_signature on every functionCall part,
	// so the gateway caches signatures from outbound responses and restores
	// them on inbound requests when the client omits them.
	// map key: tool-call ID (string), value: thoughtSigEntry
	thoughtSigCache sync.Map
}

// thoughtSigTTL is how long cached thought_signature entries are kept.
// Conversations rarely exceed this duration between tool-call turns.
const thoughtSigTTL = 30 * time.Minute

// thoughtSigEntry is a cache entry for a single thought_signature.
type thoughtSigEntry struct {
	signature string
	expiresAt time.Time
}

// cacheThoughtSignatures stores thought_signature values from outbound tool
// calls so they can be restored on subsequent inbound requests.
func (s *Server) cacheThoughtSignatures(toolCalls []service.ToolCall) {
	now := time.Now()
	for _, tc := range toolCalls {
		if tc.ThoughtSignature != "" && tc.ID != "" {
			s.thoughtSigCache.Store(tc.ID, thoughtSigEntry{
				signature: tc.ThoughtSignature,
				expiresAt: now.Add(thoughtSigTTL),
			})
		}
	}
}

// lookupThoughtSignature returns a cached thought_signature for a tool call ID,
// or "" if not found or expired.
func (s *Server) lookupThoughtSignature(toolCallID string) string {
	v, ok := s.thoughtSigCache.Load(toolCallID)
	if !ok {
		return ""
	}
	entry := v.(thoughtSigEntry)
	if time.Now().After(entry.expiresAt) {
		s.thoughtSigCache.Delete(toolCallID)
		return ""
	}
	return entry.signature
}

// sweepThoughtSigCache removes expired entries from the thought_signature cache.
// Called periodically from a background goroutine.
func (s *Server) sweepThoughtSigCache() {
	now := time.Now()
	s.thoughtSigCache.Range(func(key, value any) bool {
		if entry := value.(thoughtSigEntry); now.After(entry.expiresAt) {
			s.thoughtSigCache.Delete(key)
		}
		return true
	})
}

func New(ctx context.Context, cfg config.Server, gatewayCfg config.Gateway, providers map[string]ProviderInfo, store service.ProviderStorer, tokenStore service.APITokenStorer, workflowStore service.WorkflowStorer, triggerStore service.TriggerStorer, storeType string, factory ProviderFactory, cl *cluster.Cluster) (*Server, error) {
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
		workflowStore:   workflowStore,
		triggerStore:    triggerStore,
		providerFactory: factory,
		storeType:       storeType,
		authTokens:      gatewayCfg.AuthTokens,
		cluster:         cl,
	}

	// Start background sweep for expired thought_signature cache entries.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepThoughtSigCache()
			}
		}
	}()

	// Initialize cron trigger scheduler if trigger store is available.
	if triggerStore != nil {
		providerLookup := func(key string) (service.LLMProvider, string, error) {
			s.providerMu.RLock()
			info, ok := s.providers[key]
			s.providerMu.RUnlock()
			if !ok {
				return nil, "", fmt.Errorf("provider %q not found", key)
			}
			return info.provider, info.defaultModel, nil
		}

		s.scheduler = workflow.NewScheduler(triggerStore, workflowStore, providerLookup)
		if err := s.scheduler.Start(ctx); err != nil {
			slog.Error("failed to start cron scheduler", "error", err)
			// Non-fatal: server can run without cron triggers.
		}
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

	// Workflow management
	apiGroup.GET("/v1/workflows", s.ListWorkflowsAPI)
	apiGroup.POST("/v1/workflows", s.CreateWorkflowAPI)
	apiGroup.POST("/v1/workflows/run/*", s.RunWorkflowAPI)
	apiGroup.GET("/v1/workflows/*", s.GetWorkflowAPI)
	apiGroup.PUT("/v1/workflows/*", s.UpdateWorkflowAPI)
	apiGroup.DELETE("/v1/workflows/*", s.DeleteWorkflowAPI)

	// Trigger management (nested under workflows for list/create)
	apiGroup.GET("/v1/workflows/*/triggers", s.ListTriggersAPI)
	apiGroup.POST("/v1/workflows/*/triggers", s.CreateTriggerAPI)
	apiGroup.GET("/v1/triggers/*", s.GetTriggerAPI)
	apiGroup.PUT("/v1/triggers/*", s.UpdateTriggerAPI)
	apiGroup.DELETE("/v1/triggers/*", s.DeleteTriggerAPI)

	// Admin chat completions (used by workflow editor AI panel)
	apiGroup.POST("/v1/chat/completions", s.AdminChatCompletions)

	// Webhook endpoint (public, no auth middleware needed for external callers)
	apiGroup.POST("/v1/webhooks/*", s.WebhookAPI)

	// Settings API (protected by admin token)
	settingsGroup := apiGroup.Group("/v1/settings")
	settingsGroup.Use(s.adminAuthMiddleware())
	settingsGroup.POST("/rotate-key", s.RotateKeyAPI)

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
func NewProviderInfo(provider service.LLMProvider, cfg config.LLMConfig) ProviderInfo {
	return ProviderInfo{
		provider:     provider,
		providerType: cfg.Type,
		defaultModel: cfg.Model,
		models:       cfg.Models,
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

	info := NewProviderInfo(provider, cfg)

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
