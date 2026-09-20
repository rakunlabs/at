package server

import (
	"net/http"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestFeatureKeyForAPIRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		method   string
		basePath string
		want     string
	}{
		{
			name:   "provider list remains available",
			path:   "/api/v1/providers",
			method: http.MethodGet,
		},
		{
			name:   "provider create uses setup feature",
			path:   "/api/v1/providers",
			method: http.MethodPost,
			want:   service.FeatureProviderSetup,
		},
		{
			name:   "provider auth status uses setup feature",
			path:   "/api/v1/providers/device-auth-status",
			method: http.MethodGet,
			want:   service.FeatureProviderSetup,
		},
		{
			name:   "model pricing has its own feature",
			path:   "/api/v1/model-pricing/catalog",
			method: http.MethodGet,
			want:   service.FeatureModelPricing,
		},
		{
			name:   "usage reporting has its own feature",
			path:   "/api/v1/usage/summary",
			method: http.MethodGet,
			want:   service.FeatureUsageAnalytics,
		},
		{
			name:   "api tokens have their own feature",
			path:   "/api/v1/api-tokens/token-id/rotate",
			method: http.MethodPost,
			want:   service.FeatureAPITokens,
		},
		{
			name:   "chat sessions are separate from playground",
			path:   "/api/v1/chat/sessions/abc/messages",
			method: http.MethodPost,
			want:   service.FeatureChatSessions,
		},
		{
			name:   "admin chat belongs to playground",
			path:   "/api/v1/chat/completions",
			method: http.MethodPost,
			want:   service.FeaturePlayground,
		},
		{
			name:   "playground conversations belong to playground",
			path:   "/api/v1/chats/conversations/abc/messages",
			method: http.MethodGet,
			want:   service.FeaturePlayground,
		},
		{
			name:   "media settings stay available",
			path:   "/api/v1/media/settings",
			method: http.MethodPut,
		},
		{
			name:   "agents use agents feature",
			path:   "/api/v1/agents/agent-id/export",
			method: http.MethodGet,
			want:   service.FeatureAgents,
		},
		{
			name:   "agent runtime state belongs to heartbeats",
			path:   "/api/v1/agents/agent-id/runtime-state",
			method: http.MethodPut,
			want:   service.FeatureAgentHeartbeats,
		},
		{
			name:   "heartbeat runs belong to heartbeats",
			path:   "/api/v1/heartbeat-runs/run-id",
			method: http.MethodPut,
			want:   service.FeatureAgentHeartbeats,
		},
		{
			name:   "workflow crud uses the builder feature",
			path:   "/api/v1/workflows/workflow-id",
			method: http.MethodPut,
			want:   service.FeatureWorkflowBuilder,
		},
		{
			name:   "workflow execution uses the runs feature",
			path:   "/api/v1/workflows/run/workflow-id",
			method: http.MethodPost,
			want:   service.FeatureWorkflowRuns,
		},
		{
			name:   "active runs use the runs feature",
			path:   "/api/v1/runs/run-id/cancel",
			method: http.MethodPost,
			want:   service.FeatureWorkflowRuns,
		},
		{
			name:   "triggers use the builder feature",
			path:   "/api/v1/triggers/trigger-id",
			method: http.MethodPut,
			want:   service.FeatureWorkflowBuilder,
		},
		{
			name:   "bots have their own feature",
			path:   "/api/v1/bots/bot-id/start",
			method: http.MethodPost,
			want:   service.FeatureBots,
		},
		{
			name:   "audio transcription has its own feature",
			path:   "/api/v1/audio/transcribe",
			method: http.MethodPost,
			want:   service.FeatureAudioTranscription,
		},
		{
			name:   "files use files feature",
			path:   "/api/v1/files/browse",
			method: http.MethodGet,
			want:   service.FeatureFiles,
		},
		{
			name:   "connections use the connections child feature",
			path:   "/api/v1/connections",
			method: http.MethodGet,
			want:   service.FeatureExternalConnections,
		},
		{
			name:   "integration packs have their own feature",
			path:   "/api/v1/integration-packs/foo/install",
			method: http.MethodPost,
			want:   service.FeatureIntegrationPacks,
		},
		{
			name:   "node configs use the builder feature",
			path:   "/api/v1/node-configs/config-id",
			method: http.MethodDelete,
			want:   service.FeatureWorkflowBuilder,
		},
		{
			name:   "public webhooks use the webhook feature",
			path:   "/webhooks/my-hook",
			method: http.MethodPost,
			want:   service.FeatureWebhookTriggers,
		},
		{
			name:   "organizations have their own feature",
			path:   "/api/v1/organizations/org-id/agents",
			method: http.MethodPost,
			want:   service.FeatureOrganizations,
		},
		{
			name:   "organization task intake belongs to tasks",
			path:   "/api/v1/organizations/org-id/tasks",
			method: http.MethodPost,
			want:   service.FeatureTasks,
		},
		{
			name:   "tasks have their own feature",
			path:   "/api/v1/tasks/task-id/process",
			method: http.MethodPost,
			want:   service.FeatureTasks,
		},
		{
			name:   "goals belong to goals and projects",
			path:   "/api/v1/goals/goal-id/children",
			method: http.MethodGet,
			want:   service.FeatureGoalsProjects,
		},
		{
			name:   "approvals have their own feature",
			path:   "/api/v1/approvals/pending",
			method: http.MethodGet,
			want:   service.FeatureApprovals,
		},
		{
			name:   "traces are separate from body capture",
			path:   "/api/v1/llm-calls/traces",
			method: http.MethodGet,
			want:   service.FeatureLLMTraces,
		},
		{
			name:   "mcp servers have their own feature",
			path:   "/api/v1/mcp/servers/server-id",
			method: http.MethodPut,
			want:   service.FeatureMCPServers,
		},
		{
			name:   "mcp tool calls have their own feature",
			path:   "/api/v1/mcp/call-tool",
			method: http.MethodPost,
			want:   service.FeatureMCPTools,
		},
		{
			name:   "builtin tool calls have their own feature",
			path:   "/api/v1/mcp/call-builtin-tool",
			method: http.MethodPost,
			want:   service.FeatureBuiltinTools,
		},
		{
			name:   "terminals have their own feature",
			path:   "/api/v1/terminals/terminal-id/start",
			method: http.MethodPost,
			want:   service.FeatureTerminal,
		},
		{
			name:   "skills have their own feature",
			path:   "/api/v1/skill-templates/foo/install",
			method: http.MethodPost,
			want:   service.FeatureSkills,
		},
		{
			name:   "variables have their own feature",
			path:   "/api/v1/variables/var-id",
			method: http.MethodDelete,
			want:   service.FeatureVariables,
		},
		{
			name:     "base path is ignored",
			path:     "/at/api/v1/chat/sessions",
			method:   http.MethodGet,
			basePath: "/at",
			want:     service.FeatureChatSessions,
		},
		{
			name:   "creating a workspace belongs to workspace management",
			path:   "/api/v1/workspaces",
			method: http.MethodPost,
			want:   service.FeatureWorkspaceManagement,
		},
		{
			name:   "workspace members belong to workspace management",
			path:   "/api/v1/workspaces/ws-id/members/user-id",
			method: http.MethodPut,
			want:   service.FeatureWorkspaceManagement,
		},
		{
			name:   "listing workspace invitations belongs to workspace management",
			path:   "/api/v1/workspaces/ws-id/invitations",
			method: http.MethodGet,
			want:   service.FeatureWorkspaceManagement,
		},
		{
			name:   "accepting an invitation belongs to workspace management",
			path:   "/auth/invitations/accept",
			method: http.MethodPost,
			want:   service.FeatureWorkspaceManagement,
		},
		{
			name:   "deleting a workspace belongs to workspace management",
			path:   "/api/v1/workspaces/ws-id/delete",
			method: http.MethodPost,
			want:   service.FeatureWorkspaceManagement,
		},
		{
			// Every page resolves its workspace through these, so gating the
			// reads would break the application rather than hide a surface.
			name:   "workspace list stays available",
			path:   "/api/v1/workspaces",
			method: http.MethodGet,
		},
		{
			name:   "selected workspace read stays available",
			path:   "/api/v1/workspaces/ws-id",
			method: http.MethodGet,
		},
		{
			// Provider configuration and runtime policy must keep working in
			// the pinned single-workspace mode.
			name:   "workspace provider grants stay with providers",
			path:   "/api/v1/workspaces/ws-id/provider-grants",
			method: http.MethodPost,
		},
		{
			name:   "workspace execution policy stays available",
			path:   "/api/v1/workspaces/ws-id/execution-policy",
			method: http.MethodPut,
		},
		{
			name:   "workspace sign-in preference stays available",
			path:   "/auth/workspaces/preferences",
			method: http.MethodPut,
		},
		{
			name:   "features endpoint remains available",
			path:   "/api/v1/features",
			method: http.MethodGet,
		},
		{
			name:   "info endpoint remains available",
			path:   "/api/v1/info",
			method: http.MethodGet,
		},
		{
			name:   "options preflight remains available",
			path:   "/api/v1/chat/sessions",
			method: http.MethodOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := featureKeyForAPIRequest(tt.path, tt.method, tt.basePath); got != tt.want {
				t.Fatalf("featureKeyForAPIRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A child is only reachable when its whole ancestor chain is enabled. This is
// what keeps a pre-existing `chat_workbench: false` row meaningful after the
// catalog was split into per-surface keys, without a data migration.
func TestFeatureAncestorChain(t *testing.T) {
	tests := []struct {
		name  string
		flags map[string]bool
		key   string
		want  bool
	}{
		{
			name: "no rows means enabled",
			key:  service.FeatureChatSessions,
			want: true,
		},
		{
			name:  "legacy parent keeps children off",
			flags: map[string]bool{service.FeatureChatWorkbench: false},
			key:   service.FeatureChatSessions,
			want:  false,
		},
		{
			name:  "child off does not affect a sibling",
			flags: map[string]bool{service.FeatureChatSessions: false},
			key:   service.FeaturePlayground,
			want:  true,
		},
		{
			name:  "child off does not turn the parent off",
			flags: map[string]bool{service.FeatureChatSessions: false, service.FeaturePlayground: false},
			key:   service.FeatureChatWorkbench,
			want:  true,
		},
		{
			name:  "grandparent reaches the whole subtree",
			flags: map[string]bool{service.FeatureProviderSetup: false},
			key:   service.FeatureModelPricing,
			want:  false,
		},
		{
			name:  "unknown keys are never gated",
			flags: map[string]bool{service.FeatureChatWorkbench: false},
			key:   "not_a_feature",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := featureEnabledIn(tt.key, tt.flags); got != tt.want {
				t.Fatalf("featureEnabledIn(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}

	blocked := featureBlockedBy(service.FeatureChatSessions, map[string]bool{service.FeatureChatWorkbench: false})
	if blocked != service.FeatureChatWorkbench {
		t.Fatalf("featureBlockedBy() = %q, want %q", blocked, service.FeatureChatWorkbench)
	}
	if blocked := featureBlockedBy(service.FeatureChatWorkbench, map[string]bool{service.FeatureChatWorkbench: false}); blocked != "" {
		t.Fatalf("featureBlockedBy() reported a feature as blocking itself: %q", blocked)
	}
}

func TestFeaturePresetTargets(t *testing.T) {
	for _, preset := range featurePresets {
		targets := featurePresetTargets(preset)
		if len(targets) != len(featureDefinitions) {
			t.Fatalf("preset %q covers %d keys, want %d", preset.Key, len(targets), len(featureDefinitions))
		}
		// An enabled child behind a disabled parent is unreachable, so a preset
		// that listed only the child would silently do nothing.
		for key, enabled := range targets {
			if !enabled {
				continue
			}
			def, ok := featureDefinitionForKey(key)
			if !ok {
				t.Fatalf("preset %q enables unknown key %q", preset.Key, key)
			}
			if def.Parent != "" && !targets[def.Parent] {
				t.Fatalf("preset %q enables %q but leaves parent %q disabled", preset.Key, key, def.Parent)
			}
		}
	}

	full := featurePresetTargets(featurePresets[len(featurePresets)-1])
	for key, enabled := range full {
		if !enabled {
			t.Fatalf("the full preset leaves %q disabled", key)
		}
	}

	minimal, ok := featurePresetForKey("minimal")
	if !ok {
		t.Fatal("minimal preset missing")
	}
	targets := featurePresetTargets(minimal)
	for _, key := range []string{service.FeatureProviderSetup, service.FeatureAPITokens} {
		if !targets[key] {
			t.Fatalf("minimal preset should keep %q enabled", key)
		}
	}
	for _, key := range []string{service.FeatureChatWorkbench, service.FeatureAutomation, service.FeatureOrganizationWorkflows} {
		if targets[key] {
			t.Fatalf("minimal preset should disable %q", key)
		}
	}
}

// Every definition must resolve to a declared group and an existing parent,
// and the catalog must list parents before their children so the UI can render
// it in order without sorting.
func TestFeatureCatalogIntegrity(t *testing.T) {
	seen := make(map[string]int, len(featureDefinitions))
	for i, def := range featureDefinitions {
		if _, dup := seen[def.Key]; dup {
			t.Fatalf("duplicate feature key %q", def.Key)
		}
		seen[def.Key] = i

		group := featureGroupDefinitionForKey(def.Group)
		if group.Description == "" {
			t.Fatalf("feature %q belongs to undeclared group %q", def.Key, def.Group)
		}
		if def.Parent == "" {
			continue
		}
		parentIdx, ok := seen[def.Parent]
		if !ok {
			t.Fatalf("feature %q has parent %q that is missing or declared later", def.Key, def.Parent)
		}
		parent := featureDefinitions[parentIdx]
		if parent.Parent != "" {
			t.Fatalf("feature %q nests more than two levels deep", def.Key)
		}
		if parent.Group != def.Group {
			t.Fatalf("feature %q is grouped apart from its parent %q", def.Key, def.Parent)
		}
	}
}

func TestBuiltinToolFeatureKey(t *testing.T) {
	tests := map[string]string{
		"bash_execute":    service.FeatureBuiltinShell,
		"js_execute":      service.FeatureBuiltinScript,
		"http_request":    service.FeatureBuiltinHTTP,
		"url_fetch":       service.FeatureBuiltinHTTP,
		"file_read":       service.FeatureFiles,
		"workflow_run":    service.FeatureWorkflowBuilder,
		"trigger_create":  service.FeatureWorkflowBuilder,
		"task_complete":   service.FeatureTasks,
		"org_task_intake": service.FeatureOrganizations,
		"agent_create":    service.FeatureAgents,
		"skill_list":      service.FeatureSkills,
		"mcp_set_list":    service.FeatureMCPServers,
		"mcp_server_get":  service.FeatureMCPServers,
		"apitoken_create": service.FeatureAPITokens,
		"provider_list":   service.FeatureProviderSetup,
		"llm_trace_list":  service.FeatureLLMTraces,
		"bot_start":       service.FeatureBots,
		// Bookkeeping tools belong to no feature; the master switch covers them.
		"todo_write":    "",
		"batch_execute": "",
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := builtinToolFeatureKey(name); got != want {
				t.Fatalf("builtinToolFeatureKey(%q) = %q, want %q", name, got, want)
			}
			if want == "" {
				return
			}
			if _, ok := featureDefinitionForKey(want); !ok {
				t.Fatalf("builtinToolFeatureKey(%q) returned unknown feature %q", name, want)
			}
		})
	}
}
