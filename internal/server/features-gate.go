package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// featureCacheTTL bounds how long a replica may serve a decision another
// replica has already changed. Writes on this replica invalidate immediately.
const featureCacheTTL = 10 * time.Second

type featureSnapshot struct {
	flags    map[string]bool
	loadedAt time.Time
}

type featureCache struct {
	data    atomic.Pointer[featureSnapshot]
	refresh sync.Mutex
}

func (s *Server) invalidateFeatureCache() {
	s.features.data.Store(nil)
	// The audit hot path keeps its own longer-lived copy; a toggle that did not
	// reset it took up to its TTL to be observed.
	s.llmAudit.inited.Store(false)
}

// featureFlags returns the persisted overrides for every key. A missing key
// means enabled: the table stores overrides only, so a fresh installation has
// no rows and every feature is on.
func (s *Server) featureFlags(ctx context.Context) (map[string]bool, error) {
	if s.featureStore == nil {
		return map[string]bool{}, nil
	}

	if snap := s.features.data.Load(); snap != nil && time.Since(snap.loadedAt) < featureCacheTTL {
		return snap.flags, nil
	}

	s.features.refresh.Lock()
	defer s.features.refresh.Unlock()

	// Another goroutine may have refreshed while we waited for the lock.
	if snap := s.features.data.Load(); snap != nil && time.Since(snap.loadedAt) < featureCacheTTL {
		return snap.flags, nil
	}

	items, err := s.featureStore.ListFeatureSettings(ctx)
	if err != nil {
		// A stale answer is better than failing every gated request on a
		// transient database error; a first load has nothing to fall back to.
		if snap := s.features.data.Load(); snap != nil {
			slog.Warn("feature settings refresh failed, serving cached state", "error", err)
			return snap.flags, nil
		}

		return nil, err
	}

	flags := make(map[string]bool, len(items))
	for _, item := range items {
		flags[item.Key] = item.Enabled
	}
	s.features.data.Store(&featureSnapshot{flags: flags, loadedAt: time.Now()})

	return flags, nil
}

// featureEnabledIn reports whether key and its whole ancestor chain are
// enabled. Keys outside the catalog are enabled: an unknown key is a caller
// bug, not an administrator's decision to switch something off.
func featureEnabledIn(key string, flags map[string]bool) bool {
	for cursor := key; cursor != ""; {
		def, ok := featureDefinitionForKey(cursor)
		if !ok {
			return true
		}
		if enabled, ok := flags[def.Key]; ok && !enabled {
			return false
		}
		cursor = def.Parent
	}

	return true
}

// featureBlockedBy names the closest disabled ancestor of key, or "" when the
// chain above it is clear. The key's own state is not considered.
func featureBlockedBy(key string, flags map[string]bool) string {
	def, ok := featureDefinitionForKey(key)
	if !ok {
		return ""
	}
	for cursor := def.Parent; cursor != ""; {
		parent, ok := featureDefinitionForKey(cursor)
		if !ok {
			return ""
		}
		if enabled, ok := flags[parent.Key]; ok && !enabled {
			return parent.Key
		}
		cursor = parent.Parent
	}

	return ""
}

func (s *Server) isFeatureEnabled(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return true, nil
	}
	if _, ok := featureDefinitionForKey(key); !ok {
		return true, nil
	}
	if s.featureStore == nil {
		return true, nil
	}

	flags, err := s.featureFlags(ctx)
	if err != nil {
		return false, err
	}

	return featureEnabledIn(key, flags), nil
}

func (s *Server) featureGateMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			featureKeys := featureKeysForAPIRequest(r.URL.Path, r.Method, s.config.BasePath)
			if len(featureKeys) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			for _, featureKey := range featureKeys {
				enabled, err := s.isFeatureEnabled(r.Context(), featureKey)
				if err != nil {
					slog.Error("check feature setting failed", "feature", featureKey, "error", err)
					httpResponse(w, fmt.Sprintf("failed to check feature %q: %v", featureKey, err), http.StatusInternalServerError)
					return
				}
				if !enabled {
					httpResponseJSON(w, map[string]string{
						"message": fmt.Sprintf("feature %q is disabled", featureKey),
						"code":    "feature_disabled",
						"feature": featureKey,
					}, http.StatusNotFound)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func featureKeyForAPIRequest(path, method, basePath string) string {
	keys := featureKeysForAPIRequest(path, method, basePath)
	if len(keys) == 0 {
		return ""
	}

	return keys[0]
}

func featureKeysForAPIRequest(path, method, basePath string) []string {
	key := featureKeyForRoute(path, method, basePath)
	if key == "" {
		return nil
	}

	// One key is enough: isFeatureEnabled walks the ancestor chain, so a route
	// names the most specific feature that owns it and inherits the rest.
	return []string{key}
}

// featureKeyForRoute maps a request to the single feature that owns it.
//
// Matching is on path segments rather than prefixes: with a catalog this size,
// `strings.HasPrefix(apiPath, "/runs")` also claims a future `/runs-export`,
// and `/agents/{id}/runs` has to reach a different feature than `/agents`.
func featureKeyForRoute(path, method, basePath string) string {
	if method == http.MethodOptions {
		return ""
	}

	if basePath != "" {
		path = strings.TrimPrefix(path, strings.TrimRight(basePath, "/"))
	}
	if strings.HasPrefix(path, "/webhooks/") {
		return service.FeatureWebhookTriggers
	}
	// Accepting an invitation is how an account reaches a second workspace, so
	// it belongs to workspace management even though it sits under /auth rather
	// than /api/v1. The rest of /auth/workspaces (list, capabilities, sign-in
	// preference) stays open: it is how the application resolves the workspace
	// it is already in.
	if strings.HasPrefix(path, "/auth/invitations/") {
		return service.FeatureWorkspaceManagement
	}
	if !strings.HasPrefix(path, "/api/v1") {
		return ""
	}

	segs := strings.FieldsFunc(strings.TrimPrefix(path, "/api/v1"), func(r rune) bool { return r == '/' })
	if len(segs) == 0 {
		return ""
	}
	seg := func(i int) string {
		if i < len(segs) {
			return segs[i]
		}

		return ""
	}

	switch segs[0] {
	// ─── LLM gateway ───
	case "providers":
		// The list stays readable: model pickers across the UI need it even
		// when provider management is closed.
		if len(segs) == 1 && method == http.MethodGet {
			return ""
		}

		return service.FeatureProviderSetup
	case "personal-providers":
		return service.FeatureProviderSetup
	case "model-pricing":
		return service.FeatureModelPricing
	case "usage", "cost-events":
		return service.FeatureUsageAnalytics
	case "api-tokens":
		return service.FeatureAPITokens
	case "routing-profiles":
		return service.FeatureRoutingProfiles

	// ─── Automation ───
	case "workflows":
		if seg(1) == "run" || seg(1) == "run-stream" {
			return service.FeatureWorkflowRuns
		}

		return service.FeatureWorkflowBuilder
	case "workflow-node-types", "node-configs", "triggers":
		return service.FeatureWorkflowBuilder
	case "runs":
		return service.FeatureWorkflowRuns

	// ─── Chat ───
	case "chat":
		switch seg(1) {
		case "completions":
			return service.FeaturePlayground
		case "sessions":
			return service.FeatureChatSessions
		}

		return service.FeatureChatWorkbench
	case "chats":
		// The local-MCP registry is its own switch: an installation may want
		// Chats without browsers reaching machines on the user's network.
		if seg(1) == "local-mcp-servers" {
			return service.FeatureChatLocalMCP
		}
		if seg(1) == "shares" || seg(2) == "shares" || seg(2) == "share" {
			return service.FeatureChatSharing
		}

		return service.FeaturePlayground
	case "media":
		// Media storage configuration is an installation setting, not a chat
		// surface; only the objects themselves belong to Playground.
		if seg(1) == "settings" {
			return ""
		}

		return service.FeaturePlayground
	case "bots":
		return service.FeatureBots
	case "audio":
		return service.FeatureAudioTranscription

	// ─── Agents ───
	case "agents":
		switch seg(2) {
		case "heartbeat", "heartbeat-status", "runs", "active-run", "wakeup", "wakeup-requests", "runtime-state":
			return service.FeatureAgentHeartbeats
		case "tasks":
			return service.FeatureTasks
		case "cost", "spend", "usage":
			return service.FeatureUsageAnalytics
		}

		return service.FeatureAgents
	case "heartbeats", "heartbeat-runs", "wakeup-requests":
		return service.FeatureAgentHeartbeats
	case "agent-config-revisions":
		return service.FeatureAgents
	case "skills", "skill-templates":
		return service.FeatureSkills
	case "marketplaces", "marketplace":
		return service.FeatureMarketplaces
	case "guides":
		return service.FeatureGuides
	case "variables":
		return service.FeatureVariables

	// ─── Tools ───
	case "mcp":
		switch seg(1) {
		case "servers", "sets", "binaries", "stdio-processes":
			return service.FeatureMCPServers
		case "list-tools", "call-tool", "call-skill-tool", "set-tools":
			return service.FeatureMCPTools
		case "builtin-tools", "call-builtin-tool":
			return service.FeatureBuiltinTools
		}

		return ""
	case "mcp-templates":
		return service.FeatureMCPServers
	case "terminals":
		return service.FeatureTerminal

	// ─── Data & integrations ───
	case "files":
		return service.FeatureFiles
	case "connections", "connectors", "oauth":
		return service.FeatureExternalConnections
	case "integration-packs", "pack-sources":
		return service.FeatureIntegrationPacks

	// ─── Operations ───
	case "organizations":
		switch seg(2) {
		case "tasks":
			return service.FeatureTasks
		case "projects":
			return service.FeatureGoalsProjects
		}

		return service.FeatureOrganizations
	case "tasks", "active-delegations", "comments", "labels", "task-board":
		if segs[0] == "tasks" && seg(2) == "cost" {
			return service.FeatureUsageAnalytics
		}

		return service.FeatureTasks
	case "goals", "projects":
		if seg(2) == "cost" {
			return service.FeatureUsageAnalytics
		}

		return service.FeatureGoalsProjects
	case "approvals":
		return service.FeatureApprovals
	case "workspaces":
		// Sub-resources are owned by the feature they configure, not by
		// workspace administration: provider grants are provider configuration
		// and the execution policy is a runtime setting, and both must keep
		// working in the pinned single-workspace mode.
		switch seg(2) {
		case "provider-grants", "execution-policy":
			return ""
		}
		// Listing workspaces and reading the selected one stay open. Every page
		// resolves its workspace through them, so gating the reads would break
		// the application instead of hiding a surface.
		if method == http.MethodGet && len(segs) <= 2 {
			return ""
		}

		return service.FeatureWorkspaceManagement
	case "llm-calls":
		return service.FeatureLLMTraces
	default:
		return ""
	}
}

// builtinToolFeatureKeys includes both the tool family and its resource owner.
// Turning Files on must not implicitly enable file tools when Other is off.
func builtinToolFeatureKeys(name string) []string {
	keys := []string{service.FeatureBuiltinTools, builtinToolFamily(name)}
	if owner := builtinToolFeatureKey(name); owner != "" && owner != keys[1] {
		keys = append(keys, owner)
	}
	return keys
}

func builtinToolFamily(name string) string {
	switch name {
	case "bash_execute":
		return service.FeatureBuiltinShell
	case "js_execute":
		return service.FeatureBuiltinScript
	case "http_request", "url_fetch":
		return service.FeatureBuiltinHTTP
	default:
		return service.FeatureBuiltinOther
	}
}

func (s *Server) checkBuiltinToolFeatures(ctx context.Context, name string) error {
	flags, err := s.featureFlags(ctx)
	if err != nil {
		return fmt.Errorf("check built-in tool features: %w", err)
	}
	for _, key := range builtinToolFeatureKeys(name) {
		if !featureEnabledIn(key, flags) {
			return fmt.Errorf("feature %q is disabled", key)
		}
	}
	return nil
}

// builtinToolFeatureKey names the resource feature that owns a built-in tool.
// Family and master-switch dependencies are supplied by builtinToolFeatureKeys.
func builtinToolFeatureKey(name string) string {
	switch name {
	case "bash_execute":
		return service.FeatureBuiltinShell
	case "js_execute":
		return service.FeatureBuiltinScript
	case "http_request", "url_fetch":
		return service.FeatureBuiltinHTTP
	case "active_delegation_list":
		return service.FeatureTasks
	case "lsp_query":
		return service.FeatureFiles
	}

	// Longest prefix first: mcp_server_/mcp_set_ must be tested before any
	// shorter mcp_ rule is ever added, and llm_observation_ before llm_.
	for _, rule := range builtinToolFeaturePrefixes {
		if strings.HasPrefix(name, rule.prefix) {
			return rule.key
		}
	}

	return ""
}

var builtinToolFeaturePrefixes = []struct {
	prefix string
	key    string
}{
	{"file_", service.FeatureFiles},
	{"workflow_", service.FeatureWorkflowBuilder},
	{"trigger_", service.FeatureWorkflowBuilder},
	{"node_config_", service.FeatureWorkflowBuilder},
	{"task_", service.FeatureTasks},
	{"org_", service.FeatureOrganizations},
	{"approval_", service.FeatureApprovals},
	{"agent_", service.FeatureAgents},
	{"skill_", service.FeatureSkills},
	{"mcp_server_", service.FeatureMCPServers},
	{"mcp_set_", service.FeatureMCPServers},
	{"provider_", service.FeatureProviderSetup},
	{"apitoken_", service.FeatureAPITokens},
	{"variable_", service.FeatureVariables},
	{"connection_", service.FeatureExternalConnections},
	{"guide_", service.FeatureGuides},
	{"bot_", service.FeatureBots},
	{"llm_trace_", service.FeatureLLMTraces},
	{"llm_observation_", service.FeatureLLMTraces},
}
