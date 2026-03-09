package server

import (
	"bytes"
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
	"github.com/rakunlabs/at/internal/service/llm/antropic"
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
	providerType string // "anthropic", "openai", "vertex", "gemini"
	defaultModel string
	models       []string // all supported models; if empty, only defaultModel is advertised
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

	// ragMCPServerStore is the persistent store for named RAG MCP server configurations.
	ragMCPServerStore service.RAGMCPServerStorer

	// mcpServerStore is the persistent store for general MCP server configurations.
	mcpServerStore service.MCPServerStorer

	// mcpSetStore is the persistent store for MCP set configurations.
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

	// marketplaceClient is used for outbound HTTP requests to marketplace APIs.
	marketplaceClient *http.Client

	// ragService is the RAG ingestion and search engine (nil if ragCollectionStore is nil).
	ragService *rag.Service

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

	// activeRuns tracks currently-running workflow executions.
	// map key: run ID (string), value: *activeRun
	activeRuns sync.Map

	version string

	// botsCfg holds optional Discord/Telegram bot configuration.
	botsCfg config.Bots

	// skillTemplates holds predefined skill templates loaded from embedded JSON.
	skillTemplates []SkillTemplate

	// mcpTemplates holds predefined MCP templates loaded from embedded JSON.
	mcpTemplates []MCPTemplate

	// stdioManager manages stdio-based MCP subprocess lifecycles.
	stdioManager *service.StdioProcessManager

	// todos holds per-session todo lists for the todo_write/todo_read builtin tools.
	todos *todoStore

	// lspManager manages LSP server processes for the lsp_query builtin tool.
	lspManager *lspManager

	// pendingConfirmations tracks tool calls awaiting human approval.
	// Key: "{sessionID}:{toolCallID}", Value: chan confirmationResult.
	pendingConfirmations sync.Map
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
func New(ctx context.Context, cfg config.Server, gatewayCfg config.Gateway, botsCfg config.Bots, providers map[string]ProviderInfo, store service.Storer, storeType string, factory ProviderFactory, cl *cluster.Cluster, version string) (*Server, error) {
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
		ragMCPServerStore:        store,
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
		marketplaceClient:        &http.Client{Timeout: 10 * time.Second},
		providerFactory:          factory,
		storeType:                storeType,
		authTokens:               gatewayCfg.AuthTokens,
		cluster:                  cl,
		version:                  version,
		botsCfg:                  botsCfg,
		todos:                    newTodoStore(),
		lspManager:               newLSPManager(),
	}

	// Load predefined skill templates from embedded JSON files.
	s.loadSkillTemplates()
	s.loadMCPTemplates()

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

	// Gateway MCP endpoints for named RAG MCP servers (auth-gated)
	gatewayGroup.POST("/v1/mcp/rag/{name}", s.GatewayRAGMCPHandler)

	// General MCP gateway endpoint (auth-gated)
	gatewayGroup.POST("/v1/mcp/{name}", s.GatewayMCPHandler)

	// MCP Set gateway endpoint — serves tools from an MCP Set's own config
	gatewayGroup.POST("/v1/mcp-set/{name}", s.GatewayMCPSetHandler)

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
	apiGroup.GET("/v1/workflows", s.ListWorkflowsAPI)
	apiGroup.POST("/v1/workflows", s.CreateWorkflowAPI)
	apiGroup.POST("/v1/workflows/run/{id}", s.RunWorkflowAPI)
	apiGroup.GET("/v1/workflows/{id}", s.GetWorkflowAPI)
	apiGroup.PUT("/v1/workflows/{id}", s.UpdateWorkflowAPI)
	apiGroup.DELETE("/v1/workflows/{id}", s.DeleteWorkflowAPI)

	// Workflow version management
	apiGroup.GET("/v1/workflows/{id}/versions", s.ListWorkflowVersionsAPI)
	apiGroup.GET("/v1/workflows/{id}/versions/{version}", s.GetWorkflowVersionAPI)
	apiGroup.PUT("/v1/workflows/{id}/active-version", s.SetActiveVersionAPI)

	// Trigger management (nested under workflows for list/create)
	apiGroup.GET("/v1/workflows/{id}/triggers", s.ListTriggersAPI)
	apiGroup.POST("/v1/workflows/{id}/triggers", s.CreateTriggerAPI)
	apiGroup.GET("/v1/triggers", s.ListAllTriggersAPI)
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

	// Skill templates (predefined / store)
	apiGroup.GET("/v1/skill-templates", s.ListSkillTemplatesAPI)
	apiGroup.GET("/v1/skill-templates/{slug}", s.GetSkillTemplateAPI)
	apiGroup.POST("/v1/skill-templates/{slug}/install", s.InstallSkillTemplateAPI)

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
	apiGroup.GET("/v1/agents/{id}", s.GetAgentAPI)
	apiGroup.PUT("/v1/agents/{id}", s.UpdateAgentAPI)
	apiGroup.DELETE("/v1/agents/{id}", s.DeleteAgentAPI)

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
	apiGroup.GET("/v1/organizations/{id}", s.GetOrganizationAPI)
	apiGroup.PUT("/v1/organizations/{id}", s.UpdateOrganizationAPI)
	apiGroup.DELETE("/v1/organizations/{id}", s.DeleteOrganizationAPI)

	// Organization–Agent membership
	apiGroup.GET("/v1/organizations/{id}/agents", s.ListOrganizationAgentsAPI)
	apiGroup.POST("/v1/organizations/{id}/agents", s.AddAgentToOrganizationAPI)
	apiGroup.PUT("/v1/organizations/{id}/agents/{agent_id}", s.UpdateOrganizationAgentAPI)
	apiGroup.DELETE("/v1/organizations/{id}/agents/{agent_id}", s.RemoveAgentFromOrganizationAPI)

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

	// RAG search
	apiGroup.POST("/v1/rag/search", s.SearchRAGAPI)

	// RAG MCP server management
	apiGroup.GET("/v1/rag/mcp-servers", s.ListRAGMCPServersAPI)
	apiGroup.POST("/v1/rag/mcp-servers", s.CreateRAGMCPServerAPI)
	apiGroup.GET("/v1/rag/mcp-servers/{id}", s.GetRAGMCPServerAPI)
	apiGroup.PUT("/v1/rag/mcp-servers/{id}", s.UpdateRAGMCPServerAPI)
	apiGroup.DELETE("/v1/rag/mcp-servers/{id}", s.DeleteRAGMCPServerAPI)

	// RAG embedding tools
	apiGroup.POST("/v1/rag/discover-embedding-models", s.DiscoverEmbeddingModelsAPI)
	apiGroup.POST("/v1/rag/test-embedding", s.TestEmbeddingAPI)

	// Bot config management
	apiGroup.GET("/v1/bots", s.ListBotConfigsAPI)
	apiGroup.POST("/v1/bots", s.CreateBotConfigAPI)
	apiGroup.GET("/v1/bots/{id}", s.GetBotConfigAPI)
	apiGroup.PUT("/v1/bots/{id}", s.UpdateBotConfigAPI)
	apiGroup.DELETE("/v1/bots/{id}", s.DeleteBotConfigAPI)

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

	// General MCP server management
	apiGroup.GET("/v1/mcp/servers", s.ListMCPServersAPI)
	apiGroup.POST("/v1/mcp/servers", s.CreateMCPServerAPI)
	apiGroup.GET("/v1/mcp/servers/{id}", s.GetMCPServerAPI)
	apiGroup.PUT("/v1/mcp/servers/{id}", s.UpdateMCPServerAPI)
	apiGroup.DELETE("/v1/mcp/servers/{id}", s.DeleteMCPServerAPI)

	// MCP templates (store)
	apiGroup.GET("/v1/mcp-templates", s.ListMCPTemplatesAPI)
	apiGroup.GET("/v1/mcp-templates/{slug}", s.GetMCPTemplateAPI)
	apiGroup.POST("/v1/mcp-templates/{slug}/install", s.InstallMCPTemplateAPI)

	// MCP set management
	apiGroup.GET("/v1/mcp/sets", s.ListMCPSetsAPI)
	apiGroup.POST("/v1/mcp/sets", s.CreateMCPSetAPI)
	apiGroup.GET("/v1/mcp/sets/{id}", s.GetMCPSetAPI)
	apiGroup.PUT("/v1/mcp/sets/{id}", s.UpdateMCPSetAPI)
	apiGroup.DELETE("/v1/mcp/sets/{id}", s.DeleteMCPSetAPI)

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

	// Start bot adapters from YAML config (non-fatal on failure).
	if botsCfg.Discord != nil && botsCfg.Discord.Token != "" {
		s.startDiscordBot(ctx, "", botsCfg.Discord)
	}
	if botsCfg.Telegram != nil && botsCfg.Telegram.Token != "" {
		s.startTelegramBot(ctx, "", botsCfg.Telegram)
	}

	// Start bot adapters from DB config.
	s.startBotsFromDB(ctx)

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

	// Wire the token refresh callback for Claude OAuth providers so that
	// refreshed tokens are persisted to the store automatically.
	// This only updates the DB — it does NOT trigger reloadProvider again
	// (which would create an infinite loop).
	if cfg.Type == "anthropic" && cfg.AuthType == "claude-code" {
		if ap, ok := provider.(interface {
			SetTokenRefreshCallback(antropic.TokenRefreshCallback)
		}); ok {
			providerKey := key // capture for closure
			ap.SetTokenRefreshCallback(func(ctx context.Context, accessToken, refreshToken string) {
				if s.store == nil {
					return
				}
				record, err := s.store.GetProvider(ctx, providerKey)
				if err != nil || record == nil {
					slog.Error("claude oauth: failed to read provider for token persist", "key", providerKey, "error", err)
					return
				}
				updCfg := record.Config
				updCfg.APIKey = accessToken
				updCfg.RefreshToken = refreshToken
				if _, err := s.store.UpdateProvider(ctx, providerKey, service.ProviderRecord{
					Key:    providerKey,
					Config: updCfg,
				}); err != nil {
					slog.Error("claude oauth: failed to persist refreshed tokens", "key", providerKey, "error", err)
				} else {
					slog.Debug("claude oauth: persisted refreshed tokens", "key", providerKey)
				}
			})
		}
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
// usage with cost estimation. Returns nil when the budget store is not configured.
func (s *Server) recordUsageFunc() workflow.RecordUsageFunc {
	if s.agentBudgetStore == nil {
		return nil
	}
	return func(ctx context.Context, agentID, model string, usage service.Usage) error {
		// Look up model pricing to estimate cost.
		var estimatedCost float64
		pricingList, err := s.agentBudgetStore.ListModelPricing(ctx)
		if err == nil {
			for _, p := range pricingList {
				if p.Model == model {
					estimatedCost = (float64(usage.PromptTokens) * p.PromptPricePer1M / 1_000_000) +
						(float64(usage.CompletionTokens) * p.CompletionPricePer1M / 1_000_000)
					break
				}
			}
		}

		return s.agentBudgetStore.RecordAgentUsage(ctx, service.AgentUsageRecord{
			AgentID:          agentID,
			Model:            model,
			PromptTokens:     int64(usage.PromptTokens),
			CompletionTokens: int64(usage.CompletionTokens),
			TotalTokens:      int64(usage.TotalTokens),
			EstimatedCost:    estimatedCost,
		})
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
