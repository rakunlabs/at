package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/service/rag"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/tmc/langchaingo/schema"

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
	providerType string // "anthropic", "openai", "vertex", "gemini", "minimax"
	defaultModel string
	models       []string // all supported models; if empty, only defaultModel is advertised

	// retryAfterCap is the maximum time the agent retry loop will sleep
	// when the upstream API returns Retry-After. Resolved from
	// LLMConfig.RateLimit.RetryAfterCap() at provider build time:
	//   - 0 (zero) means "use built-in default 60s" — but we resolve it
	//     to that default here, so a zero value at this level means
	//     "no rate-limit config at all" which we treat the same.
	//   - -1 means "no cap" (sleep whatever upstream says).
	retryAfterCap time.Duration
}

// RetryAfterCap returns the duration to cap an upstream Retry-After at.
// A returned value of -1 means "do not cap".
func (p ProviderInfo) RetryAfterCap() time.Duration {
	return p.retryAfterCap
}

// ProviderFactory is a function that creates an LLMProvider from an LLMConfig.
// This is injected from main.go so the server can hot-reload providers.
type ProviderFactory func(cfg config.LLMConfig) (service.LLMProvider, error)

type Server struct {
	config config.Server

	// ctx is the server-level context used for long-lived goroutines (bots, etc.).
	ctx context.Context

	server *ada.Server

	// Provider registry for the gateway (protected by providerMu).
	providers  map[string]ProviderInfo
	providerMu sync.RWMutex

	// Store is the optional persistent store for provider CRUD.
	store service.ProviderStorer

	// tokenStore is the optional persistent store for API token management.
	tokenStore service.APITokenStorer

	// tokenUsageStore is the optional persistent store for per-token usage tracking.
	tokenUsageStore service.TokenUsageStorer

	// workflowStore is the persistent store for workflow definitions.
	workflowStore service.WorkflowStorer

	// workflowVersionStore is the persistent store for workflow version history.
	workflowVersionStore service.WorkflowVersionStorer

	// triggerStore is the persistent store for workflow triggers.
	triggerStore service.TriggerStorer

	// skillStore is the persistent store for skill definitions.
	skillStore service.SkillStorer

	// variableStore is the persistent store for variables (secret and non-secret).
	variableStore service.VariableStorer

	// nodeConfigStore is the persistent store for node configurations (e.g. SMTP settings).
	nodeConfigStore service.NodeConfigStorer

	// agentStore is the persistent store for agent definitions.
	agentStore service.AgentStorer

	// chatSessionStore is the persistent store for chat sessions and messages.
	chatSessionStore service.ChatSessionStorer

	// ragCollectionStore is the persistent store for RAG collection configs.
	ragCollectionStore service.RAGCollectionStorer

	// ragStateStore is the persistent store for RAG sync states.
	ragStateStore service.RAGStateStorer

	// ragPageStore is the persistent store for original file content (RAG pages).
	ragPageStore service.RAGPageStorer

	// mcpServerStore is the persistent store for general MCP server configurations.
	mcpServerStore service.MCPServerStorer

	// mcpSetStore is the persistent store for MCP set configurations (internal MCPs).
	mcpSetStore service.MCPSetStorer

	// botConfigStore is the persistent store for bot configurations.
	botConfigStore service.BotConfigStorer

	// marketplaceSourceStore is the persistent store for marketplace source configurations.
	marketplaceSourceStore service.MarketplaceSourceStorer

	// userPrefStore is the persistent store for per-user preferences (timezone, location, tokens, etc.).
	userPrefStore service.UserPreferenceStorer

	// organizationStore is the persistent store for organizations (multi-tenant isolation).
	organizationStore service.OrganizationStorer

	// goalStore is the persistent store for goals (mission alignment hierarchy).
	goalStore service.GoalStorer

	// taskStore is the persistent store for tasks (ticket system with atomic checkout).
	taskStore service.TaskStorer

	// agentBudgetStore is the persistent store for agent budgets and cost tracking.
	agentBudgetStore service.AgentBudgetStorer

	// auditStore is the persistent store for the immutable audit log.
	auditStore service.AuditStorer

	// agentHeartbeatStore is the persistent store for agent heartbeat tracking.
	agentHeartbeatStore service.AgentHeartbeatStorer

	// projectStore is the persistent store for projects.
	projectStore service.ProjectStorer

	// issueCommentStore is the persistent store for issue comments.
	issueCommentStore service.IssueCommentStorer

	// labelStore is the persistent store for labels and task-label associations.
	labelStore service.LabelStorer

	// heartbeatRunStore is the persistent store for heartbeat run tracking.
	heartbeatRunStore service.HeartbeatRunStorer

	// wakeupRequestStore is the persistent store for wakeup requests with coalescing.
	wakeupRequestStore service.WakeupRequestStorer

	// agentRuntimeStateStore is the persistent store for agent runtime state.
	agentRuntimeStateStore service.AgentRuntimeStateStorer

	// agentTaskSessionStore is the persistent store for per-task agent sessions.
	agentTaskSessionStore service.AgentTaskSessionStorer

	// approvalStore is the persistent store for governance approvals.
	approvalStore service.ApprovalStorer

	// agentConfigRevisionStore is the persistent store for agent config revisions.
	agentConfigRevisionStore service.AgentConfigRevisionStorer

	// costEventStore is the persistent store for per-call cost tracking.
	costEventStore service.CostEventStorer

	// orgAgentStore is the persistent store for organization-agent memberships (join table).
	orgAgentStore service.OrganizationAgentStorer

	// agentMemoryStore is the persistent store for agent memory (L0/L1 summaries and L2 messages).
	agentMemoryStore service.AgentMemoryStorer

	// marketplaceClient is used for outbound HTTP requests to marketplace APIs.
	marketplaceClient *http.Client

	// ragService is the RAG ingestion and search engine (nil if ragCollectionStore is nil).
	ragService *rag.Service

	// scheduler is the cron trigger scheduler (nil if triggerStore is nil).
	scheduler *workflow.Scheduler

	// providerFactory creates an LLMProvider from config (for hot reload).
	providerFactory ProviderFactory

	storeType string // "postgres", "sqlite", or "none"

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

	// activeRuns tracks currently-running workflow executions.
	// map key: run ID (string), value: *activeRun
	activeRuns sync.Map

	// activeDelegations tracks currently-running task delegation goroutines.
	// map key: task ID (string), value: *activeDelegation
	activeDelegations sync.Map

	version string

	// skillTemplates holds predefined skill templates loaded from embedded JSON.
	skillTemplates []SkillTemplate

	// mcpTemplates holds predefined MCP templates loaded from embedded JSON.
	mcpTemplates []MCPTemplate

	// integrationPacks holds predefined integration packs loaded from embedded JSON.
	integrationPacks []IntegrationPack

	// packSourceStore is the persistent store for Git pack sources.
	packSourceStore service.PackSourceStorer

	// guideStore is the persistent store for user-authored guides.
	guideStore service.GuideStorer

	// connectionStore is the persistent store for named external-service connections
	// (multi-instance OAuth/token credentials referenced by agents).
	connectionStore service.ConnectionStorer

	// runningBots tracks currently-running bot instances.
	// map key: bot config ID (string), value: *runningBot
	runningBots sync.Map

	// stdioManager manages stdio-based MCP subprocess lifecycles.
	stdioManager *service.StdioProcessManager

	// todos holds per-session todo lists for the todo_write/todo_read builtin tools.
	todos *todoStore

	// lspManager manages LSP server processes for the lsp_query builtin tool.
	lspManager *lspManager

	// pendingConfirmations tracks tool calls awaiting human approval.
	// Key: "{sessionID}:{toolCallID}", Value: chan confirmationResult.
	pendingConfirmations sync.Map

	// containerManager manages per-org and per-user Docker containers for isolated execution.
	containerManager *container.Manager

	// loopGov enforces context-window, iteration, and tool-result limits
	// on the three agentic loops (org delegation, chat sessions, the
	// workflow agent_call node). Always non-nil; pass-through mode is
	// entered by configuring loopgov.Config.Disabled = true.
	loopGov *loopgov.Governor
}

func (s *Server) getUserEmail(r *http.Request) string {
	if s.config.UserHeader == "" {
		return ""
	}
	return r.Header.Get(s.config.UserHeader)
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

// New creates a new server instance.
//
// LLM providers, gateway auth tokens, bot adapters, and the loop
// governor are all configured at runtime through the UI / database;
// they are no longer accepted as YAML / env. The loop governor uses
// the defaults baked into `internal/service/loopgov`.
func New(ctx context.Context, cfg config.Server, providers map[string]ProviderInfo, store service.Storer, storeType string, factory ProviderFactory, cl *cluster.Cluster, version string) (*Server, error) {
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
		config:                   cfg,
		ctx:                      ctx,
		server:                   mux,
		providers:                providers,
		store:                    store,
		tokenStore:               store,
		tokenUsageStore:          store,
		workflowStore:            store,
		workflowVersionStore:     store,
		triggerStore:             store,
		skillStore:               store,
		variableStore:            store,
		nodeConfigStore:          store,
		agentStore:               store,
		chatSessionStore:         store,
		ragCollectionStore:       store,
		ragStateStore:            store,
		ragPageStore:             store,
		mcpServerStore:           store,
		mcpSetStore:              store,
		botConfigStore:           store,
		marketplaceSourceStore:   store,
		userPrefStore:            store,
		organizationStore:        store,
		goalStore:                store,
		taskStore:                store,
		agentBudgetStore:         store,
		auditStore:               store,
		agentHeartbeatStore:      store,
		projectStore:             store,
		issueCommentStore:        store,
		labelStore:               store,
		heartbeatRunStore:        store,
		wakeupRequestStore:       store,
		agentRuntimeStateStore:   store,
		agentTaskSessionStore:    store,
		approvalStore:            store,
		agentConfigRevisionStore: store,
		costEventStore:           store,
		orgAgentStore:            store,
		agentMemoryStore:         store,
		packSourceStore:          store,
		guideStore:               store,
		connectionStore:          store,

		marketplaceClient: &http.Client{Timeout: 10 * time.Second},
		providerFactory:   factory,
		storeType:         storeType,
		// Loop governor uses package defaults (loopgov.fillDefaults).
		// Per-deployment tuning, when needed, lives in the database
		// and is applied through the UI in a follow-up change.
		loopGov:          loopgov.New(loopgov.Config{}, nil),
		cluster:          cl,
		version:          version,
		todos:            newTodoStore(),
		lspManager:       newLSPManager(),
		containerManager: container.New(),
	}

	// Load predefined skill templates from embedded JSON files.
	s.loadSkillTemplates()
	// Sync installed skill handlers with current templates (applies handler bug fixes).
	s.syncInstalledSkillHandlers(ctx)
	s.loadMCPTemplates()

	// One-shot migration: rewrite any agent_call.max_iterations == 0
	// (legacy "unlimited" sentinel) to the platform iteration ceiling.
	// Idempotent across restarts. See loop-migration.go.
	s.migrateAgentCallMaxIterations(ctx)

	// Load predefined integration packs from embedded JSON files.
	s.loadIntegrationPacks()

	// Initialize stdio MCP process manager.
	s.stdioManager = service.NewStdioProcessManager(ctx)

	// Close stdio MCP and LSP processes when the server context is cancelled.
	go func() {
		<-ctx.Done()
		s.stdioManager.Close()
		s.lspManager.close()
	}()

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

	// Initialize RAG service if collection store is available.
	{
		providerLookupForRAG := func(ctx context.Context, key string) (*config.LLMConfig, error) {
			if store == nil {
				return nil, fmt.Errorf("provider store not configured")
			}
			rec, err := store.GetProvider(ctx, key)
			if err != nil {
				return nil, err
			}
			if rec == nil {
				return nil, fmt.Errorf("provider %q not found", key)
			}
			return &rec.Config, nil
		}
		s.ragService = rag.NewService(store, providerLookupForRAG)
	}

	// Initialize cron trigger scheduler if trigger store is available.
	{
		providerLookup := func(key string) (service.LLMProvider, string, error) {
			s.providerMu.RLock()
			info, ok := s.providers[key]
			s.providerMu.RUnlock()
			if !ok {
				return nil, "", fmt.Errorf("provider %q not found", key)
			}
			return info.provider, info.defaultModel, nil
		}

		// Build a skill lookup for the scheduler (uses background context).
		var schedulerSkillLookup workflow.SkillLookup
		if store != nil {
			schedulerSkillLookup = func(nameOrID string) (*service.Skill, error) {
				sk, err := store.GetSkill(ctx, nameOrID)
				if err != nil {
					return nil, err
				}
				if sk != nil {
					return sk, nil
				}
				return store.GetSkillByName(ctx, nameOrID)
			}
		}

		// Build a variable lookup for the scheduler.
		var schedulerVarLookup workflow.VarLookup
		var schedulerVarLister workflow.VarLister
		if store != nil {
			schedulerVarLookup = func(key string) (string, error) {
				v, err := store.GetVariableByKey(ctx, key)
				if err != nil {
					return "", err
				}
				if v == nil {
					return "", fmt.Errorf("variable %q not found", key)
				}
				return v.Value, nil
			}
			schedulerVarLister = func() (map[string]string, error) {
				vars, err := store.ListVariables(ctx, nil)
				if err != nil {
					return nil, err
				}
				m := make(map[string]string, len(vars.Data))
				for _, v := range vars.Data {
					m[v.Key] = v.Value
				}
				return m, nil
			}
		}

		// Build a node config lookup for the scheduler.
		var schedulerNodeConfigLookup workflow.NodeConfigLookup
		if store != nil {
			schedulerNodeConfigLookup = func(id string) (*service.NodeConfig, error) {
				return store.GetNodeConfig(ctx, id)
			}
		}

		s.scheduler = workflow.NewScheduler(store, providerLookup, schedulerSkillLookup, schedulerVarLookup, schedulerVarLister, schedulerNodeConfigLookup, s.ragSearchFunc(), s.ragIngestFunc(), s.ragIngestFileFunc(), s.ragDeleteBySourceFunc(), s.varSaveFunc(), s.ragStateLookupFunc(), s.ragStateSaveFunc(), s.dispatchBuiltinTool, builtinToolDefsForWorkflow(), s.chatMessageCreatorFunc(), s.chatSessionLookupFunc(), s.recordUsageFunc(), s.checkBudgetFunc(), s.recordAuditFunc(), s.goalAncestryFunc(), cl)
		s.scheduler.SetRunRegistrar(s.registerRun)
		s.scheduler.SetRAGSync(s.ragSyncFunc())
		s.scheduler.SetRAGPageUpsert(s.ragPageUpsertFunc())
		s.scheduler.SetMemoryRecall(s.memoryRecallFunc())
		s.scheduler.SetConnectionLookup(s.connectionLookupFunc())
		s.scheduler.SetWorkflowByNameLookup(s.workflowByNameLookupFunc())
		s.scheduler.SetWorkflowExecutor(s.workflowExecutorFunc())
		s.scheduler.SetLoopGov(s.loopGov)
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

	// Proxy endpoint
	gatewayGroup.Handle("/proxy/{provider}/*", http.HandlerFunc(s.ProxyRequest))

	// Webhook endpoint (top-level, like gateway — not behind ForwardAuth)
	webhookGroup := mux.Group(cfg.BasePath + "/webhooks")
	webhookGroup.POST("/{id}", s.WebhookAPI)

	// General MCP gateway endpoint (auth-gated, external)
	gatewayGroup.POST("/v1/mcp/{name}", s.GatewayMCPHandler)
	gatewayGroup.POST("/v1/mcp/{name}/mcp", s.GatewayMCPHandler) // MCP clients append /mcp per spec
	gatewayGroup.GET("/v1/mcp/{name}", s.GatewayMCPSSEHandler)
	gatewayGroup.GET("/v1/mcp/{name}/mcp", s.GatewayMCPSSEHandler)

	// Internal MCP endpoint — no auth, for agent-to-server tool resolution.
	// Serves tools from MCP Sets (RAG/skills/HTTP/builtins). Not under /gateway/
	// so it's not exposed through any external reverse proxy.
	internalGroup := mux.Group(cfg.BasePath + "/internal")
	internalGroup.POST("/v1/mcp/{name}", s.InternalMCPHandler)
	internalGroup.POST("/v1/mcp/{name}/mcp", s.InternalMCPHandler)

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
	apiGroup.POST("/v1/providers/claude-auth", s.ClaudeAuthStartAPI)
	apiGroup.POST("/v1/providers/claude-auth/callback", s.ClaudeAuthCallbackAPI)
	apiGroup.POST("/v1/providers/claude-auth/token", s.ClaudeAuthTokenAPI)
	apiGroup.POST("/v1/providers/claude-auth/sync", s.ClaudeAuthSyncAPI)
	apiGroup.GET("/v1/providers/{key}", s.GetProviderAPI)
	apiGroup.PUT("/v1/providers/{key}", s.UpdateProviderAPI)
	apiGroup.DELETE("/v1/providers/{key}", s.DeleteProviderAPI)

	// API Token management
	apiGroup.GET("/v1/api-tokens", s.ListAPITokensAPI)
	apiGroup.POST("/v1/api-tokens", s.CreateAPITokenAPI)
	apiGroup.PUT("/v1/api-tokens/{id}", s.UpdateAPITokenAPI)
	apiGroup.DELETE("/v1/api-tokens/{id}", s.DeleteAPITokenAPI)
	apiGroup.GET("/v1/api-tokens/{id}/usage", s.GetTokenUsageAPI)
	apiGroup.POST("/v1/api-tokens/{id}/usage/reset", s.ResetTokenUsageAPI)

	// Workflow management
	apiGroup.GET("/v1/workflow-node-types", s.ListWorkflowNodeTypesAPI)
	apiGroup.GET("/v1/workflows", s.ListWorkflowsAPI)
	apiGroup.POST("/v1/workflows", s.CreateWorkflowAPI)
	apiGroup.POST("/v1/workflows/run/{id}", s.RunWorkflowAPI)
	apiGroup.POST("/v1/workflows/run-stream/{id}", s.RunWorkflowStreamAPI)
	apiGroup.GET("/v1/workflows/{id}", s.GetWorkflowAPI)
	apiGroup.PUT("/v1/workflows/{id}", s.UpdateWorkflowAPI)
	apiGroup.DELETE("/v1/workflows/{id}", s.DeleteWorkflowAPI)

	// Workflow version management
	apiGroup.GET("/v1/workflows/{id}/versions", s.ListWorkflowVersionsAPI)
	apiGroup.GET("/v1/workflows/{id}/versions/{version}", s.GetWorkflowVersionAPI)
	apiGroup.PUT("/v1/workflows/{id}/active-version", s.SetActiveVersionAPI)

	// Trigger management
	apiGroup.GET("/v1/workflows/{id}/triggers", s.ListTriggersAPI)   // backward compat
	apiGroup.POST("/v1/workflows/{id}/triggers", s.CreateTriggerAPI) // backward compat
	apiGroup.GET("/v1/triggers", s.ListAllTriggersAPI)
	apiGroup.POST("/v1/triggers", s.CreateTriggerGenericAPI) // generic create for any target
	apiGroup.GET("/v1/triggers/{id}", s.GetTriggerAPI)
	apiGroup.PUT("/v1/triggers/{id}", s.UpdateTriggerAPI)
	apiGroup.DELETE("/v1/triggers/{id}", s.DeleteTriggerAPI)

	// Skill management
	apiGroup.GET("/v1/skills", s.ListSkillsAPI)
	apiGroup.POST("/v1/skills", s.CreateSkillAPI)
	apiGroup.POST("/v1/skills/test-handler", s.TestHandlerAPI) // before wildcard
	apiGroup.POST("/v1/skills/import", s.ImportSkillAPI)
	apiGroup.POST("/v1/skills/import-url", s.ImportSkillFromURLAPI)
	apiGroup.POST("/v1/skills/import-url/preview", s.PreviewImportURLAPI)
	apiGroup.POST("/v1/skills/import-skillmd", s.ImportSkillMDAPI)
	apiGroup.GET("/v1/skills/{id}", s.GetSkillAPI)
	apiGroup.PUT("/v1/skills/{id}", s.UpdateSkillAPI)
	apiGroup.DELETE("/v1/skills/{id}", s.DeleteSkillAPI)
	apiGroup.GET("/v1/skills/{id}/export", s.ExportSkillAPI)
	apiGroup.GET("/v1/skills/{id}/export-md", s.ExportSkillMDAPI)

	// Skill templates (predefined / store)
	apiGroup.GET("/v1/skill-templates", s.ListSkillTemplatesAPI)
	apiGroup.GET("/v1/skill-templates/{slug}", s.GetSkillTemplateAPI)
	apiGroup.POST("/v1/skill-templates/{slug}/install", s.InstallSkillTemplateAPI)

	// Integration packs
	apiGroup.GET("/v1/integration-packs", s.ListIntegrationPacksAPI)
	apiGroup.POST("/v1/integration-packs", s.CreatePackAPI)
	apiGroup.GET("/v1/integration-packs/{slug}", s.GetIntegrationPackAPI)
	apiGroup.DELETE("/v1/integration-packs/{slug}", s.DeletePackAPI)
	apiGroup.POST("/v1/integration-packs/{slug}/install", s.InstallIntegrationPackAPI)
	apiGroup.POST("/v1/integration-packs/{slug}/skills", s.AddSkillToPackAPI)
	apiGroup.POST("/v1/integration-packs/{slug}/agents", s.AddAgentToPackAPI)
	apiGroup.POST("/v1/integration-packs/{slug}/mcp-sets", s.AddMCPSetToPackAPI)
	apiGroup.DELETE("/v1/integration-packs/{slug}/{type}/{name}", s.RemoveFromPackAPI)

	// Pack sources (Git repos)
	apiGroup.GET("/v1/pack-sources", s.ListPackSourcesAPI)
	apiGroup.POST("/v1/pack-sources", s.CreatePackSourceAPI)
	apiGroup.DELETE("/v1/pack-sources/{id}", s.DeletePackSourceAPI)
	apiGroup.POST("/v1/pack-sources/{id}/sync", s.SyncPackSourceAPI)

	// Guides (user-authored markdown docs)
	apiGroup.GET("/v1/guides", s.ListGuidesAPI)
	apiGroup.POST("/v1/guides", s.CreateGuideAPI)
	apiGroup.GET("/v1/guides/{id}", s.GetGuideAPI)
	apiGroup.PUT("/v1/guides/{id}", s.UpdateGuideAPI)
	apiGroup.DELETE("/v1/guides/{id}", s.DeleteGuideAPI)

	// Variable management
	apiGroup.GET("/v1/variables", s.ListVariablesAPI)
	apiGroup.POST("/v1/variables", s.CreateVariableAPI)
	apiGroup.GET("/v1/variables/{id}", s.GetVariableAPI)
	apiGroup.PUT("/v1/variables/{id}", s.UpdateVariableAPI)
	apiGroup.DELETE("/v1/variables/{id}", s.DeleteVariableAPI)

	// Node config management
	apiGroup.GET("/v1/node-configs", s.ListNodeConfigsAPI)
	apiGroup.POST("/v1/node-configs", s.CreateNodeConfigAPI)
	apiGroup.GET("/v1/node-configs/{id}", s.GetNodeConfigAPI)
	apiGroup.PUT("/v1/node-configs/{id}", s.UpdateNodeConfigAPI)
	apiGroup.DELETE("/v1/node-configs/{id}", s.DeleteNodeConfigAPI)

	// Agent management
	apiGroup.GET("/v1/agents", s.ListAgentsAPI)
	apiGroup.POST("/v1/agents", s.CreateAgentAPI)
	apiGroup.POST("/v1/agents/import", s.ImportAgentAPI)
	apiGroup.POST("/v1/agents/import/preview", s.PreviewImportAgentAPI)
	apiGroup.GET("/v1/agents/{id}", s.GetAgentAPI)
	apiGroup.PUT("/v1/agents/{id}", s.UpdateAgentAPI)
	apiGroup.DELETE("/v1/agents/{id}", s.DeleteAgentAPI)
	apiGroup.GET("/v1/agents/{id}/export", s.ExportAgentAPI)
	apiGroup.GET("/v1/agents/{id}/export-json", s.ExportAgentJSONAPI)

	apiGroup.GET("/v1/agents/{id}/tasks", s.ListTasksByAgentAPI)
	apiGroup.GET("/v1/agents/{id}/budget", s.GetAgentBudgetAPI)
	apiGroup.PUT("/v1/agents/{id}/budget", s.SetAgentBudgetAPI)
	apiGroup.GET("/v1/agents/{id}/usage", s.GetAgentUsageAPI)
	apiGroup.GET("/v1/agents/{id}/spend", s.GetAgentSpendAPI)
	apiGroup.POST("/v1/agents/{id}/heartbeat", s.RecordHeartbeatAPI)
	apiGroup.GET("/v1/agents/{id}/heartbeat-status", s.GetHeartbeatAPI)
	apiGroup.GET("/v1/heartbeats", s.ListHeartbeatsAPI)

	// Organization management
	apiGroup.GET("/v1/organizations", s.ListOrganizationsAPI)
	apiGroup.POST("/v1/organizations", s.CreateOrganizationAPI)
	apiGroup.POST("/v1/organizations/import", s.ImportOrganizationBundleAPI)
	apiGroup.POST("/v1/organizations/import/preview", s.PreviewImportBundleAPI)
	apiGroup.GET("/v1/organizations/{id}", s.GetOrganizationAPI)
	apiGroup.PUT("/v1/organizations/{id}", s.UpdateOrganizationAPI)
	apiGroup.DELETE("/v1/organizations/{id}", s.DeleteOrganizationAPI)
	apiGroup.GET("/v1/organizations/{id}/export", s.ExportOrganizationBundleAPI)

	// Organization–Agent membership
	apiGroup.GET("/v1/organizations/{id}/agents", s.ListOrganizationAgentsAPI)
	apiGroup.POST("/v1/organizations/{id}/agents", s.AddAgentToOrganizationAPI)
	apiGroup.PUT("/v1/organizations/{id}/agents/{agent_id}", s.UpdateOrganizationAgentAPI)
	apiGroup.DELETE("/v1/organizations/{id}/agents/{agent_id}", s.RemoveAgentFromOrganizationAPI)

	// Agent memories (org-scoped)
	apiGroup.GET("/v1/organizations/{id}/memories", s.ListOrgMemoriesAPI)
	apiGroup.POST("/v1/organizations/{id}/memories/search", s.SearchOrgMemoriesAPI)

	// Agent memories (direct)
	apiGroup.GET("/v1/agent-memories/{id}", s.GetAgentMemoryAPI)
	apiGroup.GET("/v1/agent-memories/{id}/messages", s.GetAgentMemoryMessagesAPI)
	apiGroup.DELETE("/v1/agent-memories/{id}", s.DeleteAgentMemoryAPI)

	// Organization task intake (async)
	apiGroup.POST("/v1/organizations/{id}/tasks", s.IntakeTaskAPI)

	// Goal management
	apiGroup.GET("/v1/goals", s.ListGoalsAPI)
	apiGroup.POST("/v1/goals", s.CreateGoalAPI)
	apiGroup.GET("/v1/goals/{id}", s.GetGoalAPI)
	apiGroup.PUT("/v1/goals/{id}", s.UpdateGoalAPI)
	apiGroup.DELETE("/v1/goals/{id}", s.DeleteGoalAPI)
	apiGroup.GET("/v1/goals/{id}/children", s.ListGoalChildrenAPI)
	apiGroup.GET("/v1/goals/{id}/ancestry", s.GetGoalAncestryAPI)

	// Task management
	apiGroup.GET("/v1/tasks", s.ListTasksAPI)
	apiGroup.POST("/v1/tasks", s.CreateTaskAPI)
	apiGroup.GET("/v1/tasks/{id}", s.GetTaskAPI)
	apiGroup.PUT("/v1/tasks/{id}", s.UpdateTaskAPI)
	apiGroup.DELETE("/v1/tasks/{id}", s.DeleteTaskAPI)
	apiGroup.POST("/v1/tasks/{id}/checkout", s.CheckoutTaskAPI)
	apiGroup.POST("/v1/tasks/{id}/release", s.ReleaseTaskAPI)
	apiGroup.POST("/v1/tasks/{id}/process", s.ProcessTaskAPI)
	apiGroup.POST("/v1/tasks/{id}/cancel", s.CancelTaskDelegationAPI)
	apiGroup.POST("/v1/tasks/{id}/chat", s.CreateTaskChatAPI)
	apiGroup.GET("/v1/active-delegations", s.ListActiveDelegationsAPI)

	// Model pricing
	apiGroup.GET("/v1/model-pricing", s.ListModelPricingAPI)
	apiGroup.POST("/v1/model-pricing", s.SetModelPricingAPI)

	// Audit log
	apiGroup.GET("/v1/audit", s.ListAuditEntriesAPI)
	apiGroup.GET("/v1/audit/{resource_type}/{resource_id}", s.GetAuditTrailAPI)

	// Project management
	apiGroup.GET("/v1/projects", s.ListProjectsAPI)
	apiGroup.POST("/v1/projects", s.CreateProjectAPI)
	apiGroup.GET("/v1/projects/{id}", s.GetProjectAPI)
	apiGroup.PUT("/v1/projects/{id}", s.UpdateProjectAPI)
	apiGroup.DELETE("/v1/projects/{id}", s.DeleteProjectAPI)
	apiGroup.GET("/v1/goals/{id}/projects", s.ListProjectsByGoalAPI)
	apiGroup.GET("/v1/organizations/{id}/projects", s.ListProjectsByOrganizationAPI)

	// Issue comments
	apiGroup.GET("/v1/tasks/{id}/comments", s.ListCommentsByTaskAPI)
	apiGroup.POST("/v1/tasks/{id}/comments", s.CreateCommentAPI)
	apiGroup.GET("/v1/comments/{id}", s.GetCommentAPI)
	apiGroup.PUT("/v1/comments/{id}", s.UpdateCommentAPI)
	apiGroup.DELETE("/v1/comments/{id}", s.DeleteCommentAPI)

	// Label management
	apiGroup.GET("/v1/labels", s.ListLabelsAPI)
	apiGroup.POST("/v1/labels", s.CreateLabelAPI)
	apiGroup.GET("/v1/labels/{id}", s.GetLabelAPI)
	apiGroup.PUT("/v1/labels/{id}", s.UpdateLabelAPI)
	apiGroup.DELETE("/v1/labels/{id}", s.DeleteLabelAPI)
	apiGroup.GET("/v1/labels/{id}/tasks", s.ListTasksForLabelAPI)
	apiGroup.POST("/v1/tasks/{id}/labels/{label_id}", s.AddLabelToTaskAPI)
	apiGroup.DELETE("/v1/tasks/{id}/labels/{label_id}", s.RemoveLabelFromTaskAPI)
	apiGroup.GET("/v1/tasks/{id}/labels", s.ListLabelsForTaskAPI)

	// Heartbeat runs (execution tracking)
	apiGroup.GET("/v1/agents/{id}/runs", s.ListHeartbeatRunsAPI)
	apiGroup.POST("/v1/agents/{id}/runs", s.CreateHeartbeatRunAPI)
	apiGroup.GET("/v1/agents/{id}/active-run", s.GetActiveRunAPI)
	apiGroup.GET("/v1/heartbeat-runs/{id}", s.GetHeartbeatRunAPI)
	apiGroup.PUT("/v1/heartbeat-runs/{id}", s.UpdateHeartbeatRunAPI)

	// Wakeup requests
	apiGroup.POST("/v1/agents/{id}/wakeup", s.CreateWakeupRequestAPI)
	apiGroup.GET("/v1/agents/{id}/wakeup-requests", s.ListPendingWakeupRequestsAPI)
	apiGroup.POST("/v1/agents/{id}/wakeup-requests/promote", s.PromoteDeferredWakeupAPI)
	apiGroup.GET("/v1/wakeup-requests/{id}", s.GetWakeupRequestAPI)
	apiGroup.POST("/v1/wakeup-requests/{id}/dispatch", s.MarkWakeupDispatchedAPI)

	// Agent runtime state
	apiGroup.GET("/v1/agents/{id}/runtime-state", s.GetAgentRuntimeStateAPI)
	apiGroup.PUT("/v1/agents/{id}/runtime-state", s.UpsertAgentRuntimeStateAPI)
	apiGroup.POST("/v1/agents/{id}/runtime-state/accumulate", s.AccumulateUsageAPI)

	// Agent task sessions
	apiGroup.GET("/v1/agents/{id}/task-sessions", s.ListAgentTaskSessionsAPI)
	apiGroup.GET("/v1/agents/{id}/task-sessions/{task_key}", s.GetAgentTaskSessionAPI)
	apiGroup.PUT("/v1/agents/{id}/task-sessions/{task_key}", s.UpsertAgentTaskSessionAPI)
	apiGroup.DELETE("/v1/agents/{id}/task-sessions/{task_key}", s.DeleteAgentTaskSessionAPI)

	// Approvals
	apiGroup.GET("/v1/approvals", s.ListApprovalsAPI)
	apiGroup.POST("/v1/approvals", s.CreateApprovalAPI)
	apiGroup.GET("/v1/approvals/pending", s.ListPendingApprovalsAPI)
	apiGroup.GET("/v1/approvals/{id}", s.GetApprovalAPI)
	apiGroup.PUT("/v1/approvals/{id}", s.UpdateApprovalAPI)

	// Agent config revisions
	apiGroup.GET("/v1/agents/{id}/config-revisions", s.ListAgentConfigRevisionsAPI)
	apiGroup.GET("/v1/agents/{id}/config-revisions/latest", s.GetLatestAgentConfigRevisionAPI)
	apiGroup.GET("/v1/agent-config-revisions/{id}", s.GetAgentConfigRevisionAPI)

	// Cost events
	apiGroup.GET("/v1/cost-events", s.ListCostEventsAPI)
	apiGroup.POST("/v1/cost-events", s.RecordCostEventAPI)
	apiGroup.GET("/v1/cost-events/by-billing-code", s.GetCostByBillingCodeAPI)
	apiGroup.GET("/v1/agents/{id}/cost", s.GetCostByAgentAPI)
	apiGroup.GET("/v1/projects/{id}/cost", s.GetCostByProjectAPI)
	apiGroup.GET("/v1/goals/{id}/cost", s.GetCostByGoalAPI)

	// Usage dashboard (tokens, requests, latency, errors, budgets)
	apiGroup.GET("/v1/usage/summary", s.GetUsageSummaryAPI)
	apiGroup.GET("/v1/usage/grouped", s.GetUsageGroupedAPI)
	apiGroup.GET("/v1/usage/timeseries", s.GetUsageTimeSeriesAPI)
	apiGroup.GET("/v1/usage/budgets", s.GetUsageBudgetsAPI)

	// Chat session management
	apiGroup.GET("/v1/chat/sessions", s.ListChatSessionsAPI)
	apiGroup.POST("/v1/chat/sessions", s.CreateChatSessionAPI)
	apiGroup.GET("/v1/chat/sessions/{id}", s.GetChatSessionAPI)
	apiGroup.PUT("/v1/chat/sessions/{id}", s.UpdateChatSessionAPI)
	apiGroup.DELETE("/v1/chat/sessions/{id}", s.DeleteChatSessionAPI)
	apiGroup.GET("/v1/chat/sessions/{id}/messages", s.ListChatMessagesAPI)
	apiGroup.DELETE("/v1/chat/sessions/{id}/messages", s.DeleteChatMessagesAPI)
	apiGroup.POST("/v1/chat/sessions/{id}/messages", s.SendChatMessageAPI)
	apiGroup.POST("/v1/chat/sessions/{id}/confirm", s.ConfirmToolCallAPI)

	// RAG collection management
	apiGroup.GET("/v1/rag/collections", s.ListRAGCollectionsAPI)
	apiGroup.POST("/v1/rag/collections", s.CreateRAGCollectionAPI)
	apiGroup.GET("/v1/rag/collections/{id}", s.GetRAGCollectionAPI)
	apiGroup.PUT("/v1/rag/collections/{id}", s.UpdateRAGCollectionAPI)
	apiGroup.DELETE("/v1/rag/collections/{id}", s.DeleteRAGCollectionAPI)

	// RAG document ingestion
	apiGroup.POST("/v1/rag/collections/{id}/documents", s.UploadRAGDocumentAPI)
	apiGroup.POST("/v1/rag/collections/{id}/import/url", s.ImportRAGFromURLAPI)

	// RAG git sync
	apiGroup.POST("/v1/rag/collections/{id}/sync", s.SyncRAGCollectionAPI)

	// RAG pages (original file content)
	apiGroup.GET("/v1/rag/collections/{id}/pages", s.ListRAGPagesAPI)
	apiGroup.GET("/v1/rag/pages/{id}", s.GetRAGPageAPI)
	apiGroup.DELETE("/v1/rag/pages/{id}", s.DeleteRAGPageAPI)

	// RAG collection triggers (rag_sync triggers)
	apiGroup.GET("/v1/rag/collections/{id}/triggers", s.ListRAGTriggersAPI)
	apiGroup.POST("/v1/rag/collections/{id}/triggers", s.CreateRAGTriggerAPI)

	// RAG search
	apiGroup.POST("/v1/rag/search", s.SearchRAGAPI)

	// RAG embedding tools
	apiGroup.POST("/v1/rag/discover-embedding-models", s.DiscoverEmbeddingModelsAPI)
	apiGroup.POST("/v1/rag/test-embedding", s.TestEmbeddingAPI)

	// Bot config management
	apiGroup.GET("/v1/bots", s.ListBotConfigsAPI)
	apiGroup.POST("/v1/bots", s.CreateBotConfigAPI)
	apiGroup.GET("/v1/bots/{id}", s.GetBotConfigAPI)
	apiGroup.PUT("/v1/bots/{id}", s.UpdateBotConfigAPI)
	apiGroup.DELETE("/v1/bots/{id}", s.DeleteBotConfigAPI)
	apiGroup.POST("/v1/bots/{id}/start", s.StartBotAPI)
	apiGroup.POST("/v1/bots/{id}/stop", s.StopBotAPI)
	apiGroup.GET("/v1/bots/{id}/status", s.GetBotStatusAPI)

	// Marketplace management
	apiGroup.GET("/v1/marketplace/sources", s.ListMarketplaceSourcesAPI)
	apiGroup.POST("/v1/marketplace/sources", s.CreateMarketplaceSourceAPI)
	apiGroup.PUT("/v1/marketplace/sources/{id}", s.UpdateMarketplaceSourceAPI)
	apiGroup.DELETE("/v1/marketplace/sources/{id}", s.DeleteMarketplaceSourceAPI)
	apiGroup.GET("/v1/marketplace/search", s.MarketplaceSearchAPI)
	apiGroup.GET("/v1/marketplace/top", s.MarketplaceTopAPI)
	apiGroup.POST("/v1/marketplace/preview", s.MarketplacePreviewAPI)
	apiGroup.POST("/v1/marketplace/import", s.MarketplaceImportAPI)

	// User preferences management
	apiGroup.GET("/v1/user-preferences", s.ListUserPreferencesAPI)
	apiGroup.GET("/v1/user-preferences/{user_id}/{key}", s.GetUserPreferenceAPI)
	apiGroup.PUT("/v1/user-preferences", s.SetUserPreferenceAPI)
	apiGroup.DELETE("/v1/user-preferences/{user_id}/{key}", s.DeleteUserPreferenceAPI)

	// OAuth2 flow (generic, provider in query param)
	apiGroup.GET("/v1/oauth/start", s.OAuthStartAPI)
	apiGroup.GET("/v1/oauth/callback", s.OAuthCallbackAPI)
	apiGroup.GET("/v1/oauth/manual-url", s.OAuthManualAuthURLAPI)
	apiGroup.GET("/v1/oauth/code-display", s.OAuthCodeDisplayAPI)
	apiGroup.POST("/v1/oauth/exchange", s.OAuthExchangeAPI)
	apiGroup.GET("/v1/oauth/connections", s.OAuthConnectionsAPI)
	apiGroup.DELETE("/v1/oauth/connections/{provider}", s.OAuthDisconnectAPI)

	// Named connections (multi-instance OAuth / token credential sets).
	apiGroup.GET("/v1/connections", s.ListConnectionsAPI)
	apiGroup.POST("/v1/connections", s.CreateConnectionAPI)
	apiGroup.POST("/v1/connections/import-from-variables", s.ImportConnectionsFromVariablesAPI)
	apiGroup.GET("/v1/connections/{id}", s.GetConnectionAPI)
	apiGroup.PUT("/v1/connections/{id}", s.UpdateConnectionAPI)
	apiGroup.DELETE("/v1/connections/{id}", s.DeleteConnectionAPI)

	// General MCP server management
	apiGroup.GET("/v1/mcp/servers", s.ListMCPServersAPI)
	apiGroup.POST("/v1/mcp/servers", s.CreateMCPServerAPI)
	apiGroup.POST("/v1/mcp/servers/import", s.ImportMCPServerAPI)
	apiGroup.POST("/v1/mcp/servers/import/preview", s.PreviewImportMCPServerAPI)
	apiGroup.GET("/v1/mcp/servers/{id}", s.GetMCPServerAPI)
	apiGroup.PUT("/v1/mcp/servers/{id}", s.UpdateMCPServerAPI)
	apiGroup.DELETE("/v1/mcp/servers/{id}", s.DeleteMCPServerAPI)
	apiGroup.GET("/v1/mcp/servers/{id}/export", s.ExportMCPServerAPI)

	// MCP set management (internal MCPs)
	apiGroup.GET("/v1/mcp/sets", s.ListMCPSetsAPI)
	apiGroup.POST("/v1/mcp/sets", s.CreateMCPSetAPI)
	apiGroup.POST("/v1/mcp/sets/import", s.ImportMCPSetAPI)
	apiGroup.POST("/v1/mcp/sets/import/preview", s.PreviewImportMCPSetAPI)
	apiGroup.GET("/v1/mcp/sets/{id}", s.GetMCPSetAPI)
	apiGroup.PUT("/v1/mcp/sets/{id}", s.UpdateMCPSetAPI)
	apiGroup.DELETE("/v1/mcp/sets/{id}", s.DeleteMCPSetAPI)
	apiGroup.GET("/v1/mcp/sets/{id}/export", s.ExportMCPSetAPI)

	// MCP set tool resolution (for Chat UI)
	apiGroup.GET("/v1/mcp/set-tools/{name}", s.ListMCPSetToolsAPI)
	apiGroup.POST("/v1/mcp/set-tools/{name}/call", s.CallMCPSetToolAPI)

	// MCP templates (store)
	apiGroup.GET("/v1/mcp-templates", s.ListMCPTemplatesAPI)
	apiGroup.GET("/v1/mcp-templates/{slug}", s.GetMCPTemplateAPI)
	apiGroup.POST("/v1/mcp-templates/{slug}/install", s.InstallMCPTemplateAPI)

	// Admin chat completions (used by workflow editor AI panel)
	apiGroup.POST("/v1/chat/completions", s.AdminChatCompletions)

	// MCP proxy endpoints (used by Chat UI for tool-calling loop)
	apiGroup.POST("/v1/mcp/list-tools", s.MCPListToolsAPI)
	apiGroup.POST("/v1/mcp/call-tool", s.MCPCallToolAPI)
	apiGroup.POST("/v1/mcp/call-skill-tool", s.SkillCallToolAPI)

	// Built-in tools (server-side tools for Chat UI: http, bash, js, url_fetch)
	apiGroup.GET("/v1/mcp/builtin-tools", s.BuiltinToolListAPI)
	apiGroup.POST("/v1/mcp/call-builtin-tool", s.BuiltinToolCallAPI)

	// RAG tools (direct access for Chat UI, bypasses MCP protocol)
	apiGroup.GET("/v1/mcp/rag-tools", s.RAGToolListAPI)
	apiGroup.POST("/v1/mcp/call-rag-tool", s.RAGToolCallAPI)

	// Workflow run management
	apiGroup.GET("/v1/runs", s.ListActiveRunsAPI)
	apiGroup.POST("/v1/runs/{id}/cancel", s.CancelRunAPI)

	// File browser
	apiGroup.GET("/v1/files/browse", s.FileBrowseAPI)
	apiGroup.GET("/v1/files/serve", s.FileServeAPI)
	apiGroup.DELETE("/v1/files", s.FileDeleteAPI)

	// Audio transcription
	apiGroup.POST("/v1/audio/transcribe", s.TranscribeAudioAPI)

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

	// Start bot adapters from DB config (managed via the UI).
	s.startBotsFromDB(ctx)

	// Cleanup containers on shutdown.
	go func() {
		<-ctx.Done()
		slog.Info("server: cleaning up containers")
		s.containerManager.StopAll(context.Background())
	}()

	return s, nil
}

// NewProviderInfo creates a ProviderInfo from a provider and its config.
func NewProviderInfo(provider service.LLMProvider, cfg config.LLMConfig) ProviderInfo {
	cap := time.Duration(0)
	if cfg.RateLimit != nil {
		cap = cfg.RateLimit.RetryAfterCap()
	}
	return ProviderInfo{
		provider:      provider,
		providerType:  cfg.Type,
		defaultModel:  cfg.Model,
		models:        cfg.Models,
		retryAfterCap: cap,
	}
}

func (s *Server) Start(ctx context.Context) error {
	// Boot self-check: ensure the task workspace base dir exists and is writable.
	// /tmp is tmpfs on most Linux deployments and is wiped on reboot, so the dir
	// won't exist on a fresh boot. If creation fails (e.g. when running under a
	// non-root systemd User= and the dir was previously created by another user),
	// every subsequent task delegation logs a per-task warning and every
	// bash_execute fails with `chdir: no such file or directory` until the LLM
	// burns its iteration budget. Catch that here, loudly, at startup.
	ensureTaskWorkspaceBase(defaultTaskWorkspaceBase)

	return s.server.StartWithContext(ctx, net.JoinHostPort(s.config.Host, s.config.Port))
}

// ensureTaskWorkspaceBase makes sure the per-task workspace root exists and is
// writable by the current process. Failures are logged at ERROR level but do
// not abort startup — the server can still serve API/UI traffic; only org
// delegation tasks will be degraded.
func ensureTaskWorkspaceBase(base string) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		slog.Error("startup: cannot create task workspace base — org delegation will fail",
			"path", base, "error", err.Error())
		return
	}
	// Best-effort chmod in case the dir already existed with a tighter mode.
	if err := os.Chmod(base, 0o755); err != nil {
		slog.Warn("startup: cannot chmod task workspace base", "path", base, "error", err.Error())
	}
	// Smoke-test write access by creating and removing a sentinel file.
	probe := filepath.Join(base, ".at-startup-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		slog.Error("startup: task workspace base is not writable — org delegation will fail",
			"path", base, "error", err.Error(),
			"hint", "ensure the service user owns this directory; on systemd consider RuntimeDirectory= or chown the path")
		return
	}
	_ = os.Remove(probe)
	slog.Info("startup: task workspace base ready", "path", base)
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

// ragSearchFunc returns a workflow.RAGSearchFunc that delegates to the server's
// ragService. Returns nil when RAG is not configured, which is safe — nodes
// should check for nil before calling.
func (s *Server) ragSearchFunc() workflow.RAGSearchFunc {
	if s.ragService == nil {
		return nil
	}
	return func(ctx context.Context, query string, collectionIDs []string, numResults int, scoreThreshold float32) ([]workflow.RAGSearchResult, error) {
		results, err := s.ragService.Search(ctx, rag.SearchRequest{
			Query:          query,
			CollectionIDs:  collectionIDs,
			NumResults:     numResults,
			ScoreThreshold: scoreThreshold,
		})
		if err != nil {
			return nil, err
		}
		out := make([]workflow.RAGSearchResult, len(results))
		for i, r := range results {
			out[i] = workflow.RAGSearchResult{
				Content:      r.Content,
				Metadata:     r.Metadata,
				Score:        r.Score,
				CollectionID: r.CollectionID,
			}
		}
		return out, nil
	}
}

// ragIngestFunc returns a workflow.RAGIngestFunc that delegates to the server's
// ragService. Returns nil when RAG is not configured.
func (s *Server) ragIngestFunc() workflow.RAGIngestFunc {
	if s.ragService == nil {
		return nil
	}
	return func(ctx context.Context, collectionID string, chunks []workflow.RAGIngestDocument) (int, error) {
		// Convert workflow documents to langchaingo schema documents.
		docs := make([]schema.Document, len(chunks))
		for i, c := range chunks {
			docs[i] = schema.Document{
				PageContent: c.PageContent,
				Metadata:    c.Metadata,
			}
		}
		return s.ragService.IngestChunks(ctx, collectionID, docs)
	}
}

// ragIngestFileFunc returns a workflow.RAGIngestFileFunc that delegates to the
// server's ragService. Returns nil when RAG is not configured.
func (s *Server) ragIngestFileFunc() workflow.RAGIngestFileFunc {
	if s.ragService == nil {
		return nil
	}
	return func(ctx context.Context, collectionID string, content []byte, source string, extraMetadata map[string]any) (int, error) {
		result, err := s.ragService.Ingest(ctx, collectionID, bytes.NewReader(content), "", source, extraMetadata)
		if err != nil {
			return 0, err
		}
		return result.ChunksStored, nil
	}
}

// ragDeleteBySourceFunc returns a workflow.RAGDeleteBySourceFunc that delegates
// to the server's ragService. Returns nil when RAG is not configured.
func (s *Server) ragDeleteBySourceFunc() workflow.RAGDeleteBySourceFunc {
	if s.ragService == nil {
		return nil
	}
	return func(ctx context.Context, collectionID, source string) error {
		return s.ragService.DeleteDocumentsBySource(ctx, collectionID, source)
	}
}

// connectionLookupFunc returns a workflow.ConnectionLookup that resolves a
// named Connection by ID. Returns nil when the connection store is not
// configured (in which case agent_call nodes resolve only against global
// variables, preserving pre-connections behavior).
func (s *Server) connectionLookupFunc() workflow.ConnectionLookup {
	if s.connectionStore == nil {
		return nil
	}
	return func(ctx context.Context, id string) (*service.Connection, error) {
		return s.connectionStore.GetConnection(ctx, id)
	}
}

// varSaveFunc returns a workflow.VarSaveFunc that creates or updates a variable
// by key. Returns nil when variable store is not configured.
func (s *Server) varSaveFunc() workflow.VarSaveFunc {
	if s.variableStore == nil {
		return nil
	}
	return func(ctx context.Context, key, value string) error {
		existing, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil {
			return fmt.Errorf("lookup variable %q: %w", key, err)
		}
		if existing != nil {
			// Update existing variable.
			existing.Value = value
			_, err = s.variableStore.UpdateVariable(ctx, existing.ID, *existing)
			if err != nil {
				return fmt.Errorf("update variable %q: %w", key, err)
			}
			return nil
		}
		// Create new variable.
		_, err = s.variableStore.CreateVariable(ctx, service.Variable{
			Key:   key,
			Value: value,
		})
		if err != nil {
			return fmt.Errorf("create variable %q: %w", key, err)
		}
		return nil
	}
}

// ragStateLookupFunc returns a workflow.RAGStateLookupFunc that delegates to the
// server's ragStateStore. Returns nil when store is not configured.
func (s *Server) ragStateLookupFunc() workflow.RAGStateLookupFunc {
	if s.ragStateStore == nil {
		return nil
	}
	return func(ctx context.Context, key string) (*service.RAGState, error) {
		return s.ragStateStore.GetRAGState(ctx, key)
	}
}

// ragStateSaveFunc returns a workflow.RAGStateSaveFunc that delegates to the
// server's ragStateStore. Returns nil when store is not configured.
func (s *Server) ragStateSaveFunc() workflow.RAGStateSaveFunc {
	if s.ragStateStore == nil {
		return nil
	}
	return func(ctx context.Context, key, value string) error {
		return s.ragStateStore.SetRAGState(ctx, key, value)
	}
}

// recordUsageFunc returns a workflow.RecordUsageFunc that records agent token
// usage with cost estimation. It writes to both agent_usage (legacy) and
// cost_events (dashboard-ready). Returns nil when the budget store is not configured.
func (s *Server) recordUsageFunc() workflow.RecordUsageFunc {
	if s.agentBudgetStore == nil && s.costEventStore == nil {
		return nil
	}
	return func(ctx context.Context, event workflow.UsageEvent) error {
		// Look up model pricing to estimate cost.
		var estimatedCost float64
		if s.agentBudgetStore != nil {
			pricingList, err := s.agentBudgetStore.ListModelPricing(ctx)
			if err == nil {
				for _, p := range pricingList {
					if p.Model == event.Model {
						estimatedCost = (float64(event.Usage.PromptTokens) * p.PromptPricePer1M / 1_000_000) +
							(float64(event.Usage.CompletionTokens) * p.CompletionPricePer1M / 1_000_000)
						break
					}
				}
			}
		}

		// Dollar-denominated for agent_usage (historic), cents for cost_events.
		costCents := estimatedCost * 100

		// 1. Legacy: agent_usage (per-agent totals, budget enforcement).
		if s.agentBudgetStore != nil {
			if err := s.agentBudgetStore.RecordAgentUsage(ctx, service.AgentUsageRecord{
				AgentID:          event.AgentID,
				TaskID:           event.TaskID,
				WorkflowRunID:    event.RunID,
				Model:            event.Model,
				PromptTokens:     int64(event.Usage.PromptTokens),
				CompletionTokens: int64(event.Usage.CompletionTokens),
				TotalTokens:      int64(event.Usage.TotalTokens),
				EstimatedCost:    estimatedCost,
			}); err != nil {
				return err
			}
		}

		// 2. Dashboard: cost_events (per-call with latency/status/attribution).
		if s.costEventStore != nil {
			status := event.Status
			if status == "" {
				status = "ok"
			}
			if err := s.costEventStore.RecordCostEvent(ctx, service.CostEvent{
				OrganizationID: event.OrganizationID,
				AgentID:        event.AgentID,
				TaskID:         event.TaskID,
				ProjectID:      event.ProjectID,
				GoalID:         event.GoalID,
				BillingCode:    event.BillingCode,
				RunID:          event.RunID,
				Provider:       event.Provider,
				Model:          event.Model,
				InputTokens:    int64(event.Usage.PromptTokens),
				OutputTokens:   int64(event.Usage.CompletionTokens),
				CostCents:      costCents,
				LatencyMs:      event.LatencyMs,
				Status:         status,
				ErrorCode:      event.ErrorCode,
				ErrorMessage:   event.ErrorMessage,
			}); err != nil {
				// Non-fatal: budget write already succeeded; log and continue.
				slog.Warn("record cost event failed", "agent_id", event.AgentID, "error", err)
			}
		}

		return nil
	}
}

// checkBudgetFunc returns a workflow.CheckBudgetFunc that checks whether an
// agent has exceeded its spending budget. Returns nil when the budget store
// is not configured.
func (s *Server) checkBudgetFunc() workflow.CheckBudgetFunc {
	if s.agentBudgetStore == nil {
		return nil
	}
	return func(ctx context.Context, agentID string) error {
		budget, err := s.agentBudgetStore.GetAgentBudget(ctx, agentID)
		if err != nil {
			return fmt.Errorf("check budget for agent %s: %w", agentID, err)
		}
		if budget == nil {
			// No budget set — unlimited.
			return nil
		}
		totalSpend, err := s.agentBudgetStore.GetAgentTotalSpend(ctx, agentID)
		if err != nil {
			return fmt.Errorf("get total spend for agent %s: %w", agentID, err)
		}
		if totalSpend >= budget.MonthlyLimit {
			return fmt.Errorf("agent %s has exceeded monthly budget (%.2f / %.2f USD)", agentID, totalSpend, budget.MonthlyLimit)
		}
		return nil
	}
}

// recordAuditFunc returns a workflow.RecordAuditFunc that appends an entry
// to the immutable audit log. Returns nil when the audit store is not configured.
func (s *Server) recordAuditFunc() workflow.RecordAuditFunc {
	if s.auditStore == nil {
		return nil
	}
	return func(ctx context.Context, entry service.AuditEntry) error {
		return s.auditStore.RecordAudit(ctx, entry)
	}
}

// goalAncestryFunc returns a workflow.GoalAncestryFunc that retrieves the
// full goal chain from a given goal up to the root mission. Returns nil
// when the goal store is not configured.
func (s *Server) goalAncestryFunc() workflow.GoalAncestryFunc {
	if s.goalStore == nil {
		return nil
	}
	return func(ctx context.Context, goalID string) ([]service.Goal, error) {
		return s.goalStore.GetGoalAncestry(ctx, goalID)
	}
}

// ragPageUpsertFunc returns a workflow.RAGPageUpsertFunc that stores original
// file content in the rag_pages table. Returns nil when page store is not configured.
func (s *Server) ragPageUpsertFunc() workflow.RAGPageUpsertFunc {
	if s.ragPageStore == nil {
		return nil
	}
	return func(ctx context.Context, collectionID, source, path, content, contentType string, metadata map[string]any) error {
		if contentType == "" {
			contentType = rag.DetectContentType(path)
		}
		h := sha256.Sum256([]byte(content))
		hash := hex.EncodeToString(h[:])
		_, err := s.ragPageStore.UpsertRAGPage(ctx, service.RAGPage{
			CollectionID: collectionID,
			Source:       source,
			Path:         path,
			Content:      content,
			ContentType:  contentType,
			Metadata:     metadata,
			ContentHash:  hash,
		})
		return err
	}
}

// ragSyncFunc returns a workflow.RAGSyncFunc that triggers a git sync for a
// RAG collection. Used by the cron scheduler for rag_sync triggers.
// Returns nil when RAG is not configured.
func (s *Server) ragSyncFunc() workflow.RAGSyncFunc {
	if s.ragService == nil || s.ragCollectionStore == nil {
		return nil
	}
	return func(ctx context.Context, collectionID string) error {
		collection, err := s.ragCollectionStore.GetRAGCollection(ctx, collectionID)
		if err != nil {
			return fmt.Errorf("get collection %s: %w", collectionID, err)
		}
		if collection == nil {
			return fmt.Errorf("collection %s not found", collectionID)
		}
		if collection.Config.GitSource == nil {
			return fmt.Errorf("collection %s has no git source configured", collectionID)
		}

		deps := rag.SyncDeps{
			RAGService: s.ragService,
			PageStore:  s.ragPageStore,
			StateStore: s.ragStateStore,
			VarStore:   s.variableStore,
		}

		_, err = rag.SyncCollection(ctx, deps, collection)
		return err
	}
}

// versionLookupFunc returns a VersionLookupFunc that resolves a workflow's
// active version graph. Returns nil if the required stores are not configured.
func (s *Server) versionLookupFunc() workflow.VersionLookupFunc {
	if s.workflowStore == nil || s.workflowVersionStore == nil {
		return nil
	}
	return func(ctx context.Context, workflowID string) (*service.WorkflowGraph, error) {
		wf, err := s.workflowStore.GetWorkflow(ctx, workflowID)
		if err != nil {
			return nil, fmt.Errorf("get workflow %s: %w", workflowID, err)
		}
		if wf == nil || wf.ActiveVersion == nil {
			return nil, nil
		}
		ver, err := s.workflowVersionStore.GetWorkflowVersion(ctx, workflowID, *wf.ActiveVersion)
		if err != nil {
			return nil, fmt.Errorf("get workflow version %s v%d: %w", workflowID, *wf.ActiveVersion, err)
		}
		if ver == nil {
			return nil, nil
		}
		return &ver.Graph, nil
	}
}

func (s *Server) memoryRecallFunc() workflow.MemoryRecallFunc {
	if s.agentMemoryStore == nil {
		return nil
	}
	return func(ctx context.Context, agentID, orgID string, maxTokens int) (string, error) {
		memories, err := s.agentMemoryStore.ListOrgMemories(ctx, orgID)
		if err != nil {
			return "", fmt.Errorf("list org memories for recall: %w", err)
		}
		if len(memories) == 0 {
			return "", nil
		}

		// Use token budget as char limit (roughly 4 chars per token).
		maxChars := maxTokens * 4
		var buf strings.Builder
		buf.WriteString("## Relevant Past Work\n\n")

		for _, mem := range memories {
			var entry strings.Builder
			agentLabel := agentID
			if mem.AgentID != agentID {
				agentLabel = fmt.Sprintf("agent %s", mem.AgentID)
			} else {
				agentLabel = "you"
			}
			entry.WriteString(fmt.Sprintf("### %s (by %s)\n", mem.TaskIdentifier, agentLabel))
			entry.WriteString(fmt.Sprintf("**Summary**: %s\n", mem.SummaryL0))
			if mem.SummaryL1 != "" {
				entry.WriteString(mem.SummaryL1 + "\n")
			}
			entry.WriteString("\n")

			entryStr := entry.String()
			if buf.Len()+len(entryStr) > maxChars {
				break
			}
			buf.WriteString(entryStr)
		}

		return buf.String(), nil
	}
}
