package server

import (
	"github.com/rakunlabs/at/internal/service"
)

// The feature catalog is a two-level tree. A child is only reachable when every
// ancestor is enabled (see featureEnabledIn), which is what lets the original
// coarse keys stay in the catalog as parents: an installation that disabled
// `chat_workbench` keeps Playground, Sessions and Bots off without a migration,
// and a fresh installation sees every key enabled by default.
//
// Parents are never implied by their children: disabling every child leaves the
// parent enabled, because the parent also gates surfaces the children do not.

type featureGroupDefinition struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type featureDefinition struct {
	Key         string
	Name        string
	Description string
	Group       string
	// Parent is the key that must also be enabled for this feature to apply.
	// Empty for top-level features.
	Parent string
}

// featurePreset is a named target state for the whole catalog. Applying one
// writes an explicit row for every key: enabled for the listed ones (plus their
// ancestors, which are implied) and disabled for the rest. Presets exist
// because the catalog is deliberately fine-grained, and "gateway plus chat plus
// tokens, nothing else" is otherwise thirty individual toggles.
type featurePreset struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     []string `json:"enabled"`
}

var featureGroupDefinitions = []featureGroupDefinition{
	{
		Key:         "llm_gateway",
		Name:        "LLM Gateway",
		Description: "Provider setup, pricing, usage accounting and gateway credentials.",
	},
	{
		Key:         "workspace",
		Name:        "Workspace",
		Description: "Interactive chat, sessions, bots and reusable agent definitions.",
	},
	{
		Key:         "automation",
		Name:        "Automation",
		Description: "Workflow builder, runs, webhooks and cron schedules.",
	},
	{
		Key:         "tools",
		Name:        "Tools & Execution",
		Description: "MCP servers, tool-calling proxy, built-in tools and host terminals.",
	},
	{
		Key:         "data_integrations",
		Name:        "Data & Integrations",
		Description: "File browser, external connections and integration packs.",
	},
	{
		Key:         "operations",
		Name:        "Operations",
		Description: "Organizations, tasks, approvals, studio and tracing.",
	},
}

var featureDefinitions = []featureDefinition{
	// ─── LLM Gateway ───
	{
		Key:         service.FeatureProviderSetup,
		Name:        "Provider Setup",
		Description: "Create, edit, delete, authorize and discover models for providers. Existing providers remain usable by the gateway; only the management surface is removed. Parent of pricing and usage.",
		Group:       "llm_gateway",
	},
	{
		Key:         service.FeatureRoutingProfiles,
		Name:        "Routing Profiles",
		Description: "Named model chains that a gateway request may use in place of a provider/model pair. Disabling removes the management surface; existing profiles keep routing gateway traffic.",
		Group:       "llm_gateway",
		Parent:      service.FeatureProviderSetup,
	},
	{
		Key:         service.FeatureModelPricing,
		Name:        "Model Pricing",
		Description: "Per-model price records, the pricing catalog and its external sync sources.",
		Group:       "llm_gateway",
		Parent:      service.FeatureProviderSetup,
	},
	{
		Key:         service.FeatureUsageAnalytics,
		Name:        "Usage & Cost",
		Description: "Usage dashboard, grouped/timeseries reporting, budgets and cost event records.",
		Group:       "llm_gateway",
		Parent:      service.FeatureProviderSetup,
	},
	{
		Key:         service.FeatureAPITokens,
		Name:        "API Tokens",
		Description: "Create, rotate, pause and delete gateway API tokens. Disabling stops token management only; tokens that already exist keep authenticating gateway traffic.",
		Group:       "llm_gateway",
	},

	// ─── Workspace ───
	{
		Key:         service.FeatureChatWorkbench,
		Name:        "Chat Workbench",
		Description: "Umbrella for every interactive chat surface. Disabling it removes Chats, Sessions, Bots and audio transcription at once.",
		Group:       "workspace",
	},
	{
		Key:         service.FeaturePlayground,
		Name:        "Chats",
		Description: "The scratch chat surface, admin chat completions, saved playground conversations and their media attachments.",
		Group:       "workspace",
		Parent:      service.FeatureChatWorkbench,
	},
	{
		Key:         service.FeatureChatSessions,
		Name:        "Chat Sessions",
		Description: "Persistent agent chat sessions, their stored messages and tool-call confirmations. Independent of Chats.",
		Group:       "workspace",
		Parent:      service.FeatureChatWorkbench,
	},
	{
		Key:         service.FeatureBots,
		Name:        "Bots",
		Description: "Telegram and other chat bot adapters. Disabling stops every running adapter immediately and keeps them stopped across restarts.",
		Group:       "workspace",
		Parent:      service.FeatureChatWorkbench,
	},
	{
		Key:         service.FeatureAudioTranscription,
		Name:        "Audio Transcription",
		Description: "Voice-note transcription used by the chat composer and bot adapters.",
		Group:       "workspace",
		Parent:      service.FeatureChatWorkbench,
	},
	{
		Key:         service.FeatureAgents,
		Name:        "Agents",
		Description: "Agent definitions, import/export, budgets and config revisions. Parent of the heartbeat scheduler.",
		Group:       "workspace",
	},
	{
		Key:         service.FeatureAgentHeartbeats,
		Name:        "Agent Heartbeats",
		Description: "Autonomous agent runs: heartbeats, wakeup requests, runtime state and heartbeat run history.",
		Group:       "workspace",
		Parent:      service.FeatureAgents,
	},
	{
		Key:         service.FeatureSkills,
		Name:        "Skills",
		Description: "Skill definitions, skill templates, import/export and handler testing.",
		Group:       "workspace",
	},
	{
		Key:         service.FeatureMarketplaces,
		Name:        "Marketplaces",
		Description: "External skill catalogs: sources, search, preview and import.",
		Group:       "workspace",
	},
	{
		// The key stays `guides` on purpose: `feature_settings` stores overrides
		// by key, so renaming it would make every existing "disabled" row
		// unknown — and a missing row means *enabled*, silently switching the
		// surface back on for installations that had turned it off.
		Key:         service.FeatureGuides,
		Name:        "Documentation",
		Description: "The in-product Documentation surface: the API reference, the built-in guides and the user-authored guide library with its guide_* built-in tools. Disabling it hides the whole page and its sidebar link.",
		Group:       "workspace",
	},
	{
		Key:         service.FeatureVariables,
		Name:        "Variables",
		Description: "Workspace variables and secrets management. Existing values stay resolvable by running agents and skills.",
		Group:       "workspace",
	},

	// ─── Automation ───
	{
		Key:         service.FeatureAutomation,
		Name:        "Automation",
		Description: "Umbrella for the workflow engine. Disabling it removes the builder, runs, webhooks and schedules at once.",
		Group:       "automation",
	},
	{
		Key:         service.FeatureWorkflowBuilder,
		Name:        "Workflow Builder",
		Description: "Workflow CRUD, versions, node types, reusable node configs, trigger definitions and the workflow_*/trigger_* built-in tools.",
		Group:       "automation",
		Parent:      service.FeatureAutomation,
	},
	{
		Key:         service.FeatureWorkflowRuns,
		Name:        "Workflow Runs",
		Description: "Executing workflows on demand and listing/cancelling active runs.",
		Group:       "automation",
		Parent:      service.FeatureAutomation,
	},
	{
		Key:         service.FeatureWebhookTriggers,
		Name:        "Webhook Triggers",
		Description: "The public /webhooks/{id} endpoint. Disabling answers 404 without touching the trigger records.",
		Group:       "automation",
		Parent:      service.FeatureAutomation,
	},
	{
		Key:         service.FeatureCronTriggers,
		Name:        "Cron Schedules",
		Description: "The cron scheduler. Disabling stops it immediately; schedules are preserved and resume when re-enabled.",
		Group:       "automation",
		Parent:      service.FeatureAutomation,
	},

	// ─── Tools & Execution ───
	{
		Key:         service.FeatureMCPServers,
		Name:        "MCP Servers & Sets",
		Description: "MCP server definitions, MCP Sets, templates and import/export.",
		Group:       "tools",
	},
	{
		Key:         service.FeatureMCPTools,
		Name:        "MCP Tool Calls",
		Description: "The tool-listing and tool-calling proxy the chat loops use to reach MCP and skill tools.",
		Group:       "tools",
	},
	{
		Key:         service.FeatureBuiltinTools,
		Name:        "Built-in Tools",
		Description: "Server-side built-in tools as a whole. Disabling removes them from every advertised tool list and refuses dispatch.",
		Group:       "tools",
	},
	{
		Key:         service.FeatureBuiltinShell,
		Name:        "Shell Tool",
		Description: "bash_execute. The most privileged built-in tool: it runs host commands under the workspace execution policy.",
		Group:       "tools",
		Parent:      service.FeatureBuiltinTools,
	},
	{
		Key:         service.FeatureBuiltinScript,
		Name:        "Script Tool",
		Description: "js_execute — arbitrary JavaScript in the embedded Goja VM.",
		Group:       "tools",
		Parent:      service.FeatureBuiltinTools,
	},
	{
		Key:         service.FeatureBuiltinHTTP,
		Name:        "HTTP Tools",
		Description: "http_request and url_fetch — outbound HTTP from the server.",
		Group:       "tools",
		Parent:      service.FeatureBuiltinTools,
	},
	{
		Key:         service.FeatureTerminal,
		Name:        "Host Terminals",
		Description: "Browser-attached tmux terminals on the host. Disabling blocks creating and attaching; existing tmux sessions keep running.",
		Group:       "tools",
	},

	// ─── Data & Integrations ───
	{
		Key:         service.FeatureFiles,
		Name:        "Files",
		Description: "Workspace file browser, upload/serve/delete and the file_* built-in tools.",
		Group:       "data_integrations",
	},
	{
		Key:         service.FeatureConnections,
		Name:        "Connections & Integrations",
		Description: "Umbrella for external-service credentials and packaged installs.",
		Group:       "data_integrations",
	},
	{
		Key:         service.FeatureExternalConnections,
		Name:        "Connections",
		Description: "Named external-service credentials, connector definitions and the OAuth authorization flow.",
		Group:       "data_integrations",
		Parent:      service.FeatureConnections,
	},
	{
		Key:         service.FeatureIntegrationPacks,
		Name:        "Integration Packs",
		Description: "Packaged org/agent/skill bundles and their remote pack sources.",
		Group:       "data_integrations",
		Parent:      service.FeatureConnections,
	},

	// ─── Operations ───
	{
		Key:         service.FeatureOrganizationWorkflows,
		Name:        "Organization Workflows",
		Description: "Umbrella for organizations, tasks, delegation and governance.",
		Group:       "operations",
	},
	{
		Key:         service.FeatureOrganizations,
		Name:        "Organizations",
		Description: "Organization records, agent rosters, hierarchy and budgets.",
		Group:       "operations",
		Parent:      service.FeatureOrganizationWorkflows,
	},
	{
		Key:         service.FeatureTasks,
		Name:        "Tasks & Delegation",
		Description: "Task records, the delegation loop, comments, labels and the task board.",
		Group:       "operations",
		Parent:      service.FeatureOrganizationWorkflows,
	},
	{
		Key:         service.FeatureGoalsProjects,
		Name:        "Goals & Projects",
		Description: "Goal trees and projects used to group tasks and attribute cost.",
		Group:       "operations",
		Parent:      service.FeatureOrganizationWorkflows,
	},
	{
		Key:         service.FeatureApprovals,
		Name:        "Approvals",
		Description: "Board approvals for agent hiring and other gated organization actions.",
		Group:       "operations",
		Parent:      service.FeatureOrganizationWorkflows,
	},
	{
		Key:         service.FeatureStudio,
		Name:        "Studio",
		Description: "Avatar and Series Studio screens. The underlying skills and assets stay installed.",
		Group:       "operations",
		Parent:      service.FeatureOrganizationWorkflows,
	},
	{
		Key:         service.FeatureWorkspaceManagement,
		Name:        "Workspaces",
		Description: "Creating, renaming, archiving and deleting workspaces, plus membership and invitations. Disabling it also pins every request to the default workspace: other workspaces keep their records but become unselectable until it is turned back on.",
		Group:       "operations",
	},
	{
		Key:         service.FeatureLLMTraces,
		Name:        "Traces",
		Description: "The trace/observation browser and its API. Observation skeletons are recorded regardless; this controls reading them.",
		Group:       "operations",
	},
	{
		Key:         service.FeatureLLMAudit,
		Name:        "Trace Body Capture",
		Description: "Record full request/response bodies for every LLM call, and tool input/output previews. Bodies are retained for 7 days. Disabling keeps skeletons (tokens, cost, latency, hierarchy) but stops storing content.",
		Group:       "operations",
		Parent:      service.FeatureLLMTraces,
	},
}

// featurePresets are ordered from most restrictive to least.
var featurePresets = []featurePreset{
	{
		Key:         "minimal",
		Name:        "Gateway only",
		Description: "Providers, API tokens and the API reference. Every admin surface beyond configuring the gateway is hidden.",
		Enabled: []string{
			service.FeatureProviderSetup,
			service.FeatureModelPricing,
			service.FeatureUsageAnalytics,
			service.FeatureAPITokens,
			service.FeatureRoutingProfiles,
			// The Documentation page carries the gateway API reference, which is
			// how a client gets configured against this installation — a
			// gateway-only preset is the last one that should hide it.
			service.FeatureGuides,
			// Every preset enables workspace management. A preset writes an
			// explicit row for each key, so omitting it would take membership
			// and invitations away as a side effect of choosing which *product*
			// surfaces to show — and a workspace is what owns the providers and
			// tokens the gateway-only presets are about.
			service.FeatureWorkspaceManagement,
		},
	},
	{
		Key:         "gateway_traced",
		Name:        "Gateway + tracing",
		Description: "Gateway only, plus the trace browser and full body capture for debugging client traffic.",
		Enabled: []string{
			service.FeatureProviderSetup,
			service.FeatureModelPricing,
			service.FeatureUsageAnalytics,
			service.FeatureAPITokens,
			service.FeatureRoutingProfiles,
			service.FeatureGuides,
			service.FeatureWorkspaceManagement,
			service.FeatureLLMTraces,
			service.FeatureLLMAudit,
		},
	},
	{
		Key:         "gateway_chat",
		Name:        "Gateway + chat",
		Description: "Providers, API tokens, Chats and Sessions with tool calling. No agents, workflows, organizations or bots.",
		Enabled: []string{
			service.FeatureProviderSetup,
			service.FeatureModelPricing,
			service.FeatureUsageAnalytics,
			service.FeatureAPITokens,
			service.FeatureRoutingProfiles,
			service.FeatureGuides,
			service.FeatureWorkspaceManagement,
			service.FeatureChatWorkbench,
			service.FeaturePlayground,
			service.FeatureChatSessions,
			service.FeatureMCPServers,
			service.FeatureMCPTools,
			service.FeatureBuiltinTools,
			service.FeatureBuiltinHTTP,
			service.FeatureBuiltinScript,
			service.FeatureLLMTraces,
			service.FeatureLLMAudit,
		},
	},
	{
		Key:         "agent_platform",
		Name:        "Agent platform",
		Description: "Chat, agents, skills, files, connections and tools — without workflows, organizations or host terminals.",
		Enabled: []string{
			service.FeatureProviderSetup,
			service.FeatureModelPricing,
			service.FeatureUsageAnalytics,
			service.FeatureAPITokens,
			service.FeatureRoutingProfiles,
			service.FeatureChatWorkbench,
			service.FeaturePlayground,
			service.FeatureChatSessions,
			service.FeatureBots,
			service.FeatureAudioTranscription,
			service.FeatureAgents,
			service.FeatureAgentHeartbeats,
			service.FeatureSkills,
			service.FeatureMarketplaces,
			service.FeatureGuides,
			service.FeatureVariables,
			service.FeatureMCPServers,
			service.FeatureMCPTools,
			service.FeatureBuiltinTools,
			service.FeatureBuiltinShell,
			service.FeatureBuiltinScript,
			service.FeatureBuiltinHTTP,
			service.FeatureFiles,
			service.FeatureConnections,
			service.FeatureExternalConnections,
			service.FeatureIntegrationPacks,
			service.FeatureWorkspaceManagement,
			service.FeatureLLMTraces,
			service.FeatureLLMAudit,
		},
	},
	{
		Key:         "full",
		Name:        "Everything",
		Description: "Every feature enabled. This is the shipped default for a fresh installation.",
		Enabled:     nil, // filled in by featurePresetTargets
	},
}

func featurePresetForKey(key string) (featurePreset, bool) {
	for _, preset := range featurePresets {
		if preset.Key == key {
			return preset, true
		}
	}

	return featurePreset{}, false
}

// featurePresetTargets expands a preset into an explicit enabled/disabled state
// for every key in the catalog. Ancestors of an enabled key are enabled too: a
// child behind a disabled parent is unreachable, so a preset listing only the
// child would silently do nothing.
func featurePresetTargets(preset featurePreset) map[string]bool {
	targets := make(map[string]bool, len(featureDefinitions))
	if preset.Key == "full" {
		for _, def := range featureDefinitions {
			targets[def.Key] = true
		}

		return targets
	}

	for _, def := range featureDefinitions {
		targets[def.Key] = false
	}
	for _, key := range preset.Enabled {
		for cursor := key; cursor != ""; {
			def, ok := featureDefinitionForKey(cursor)
			if !ok {
				break
			}
			targets[def.Key] = true
			cursor = def.Parent
		}
	}

	return targets
}

func featureDefinitionForKey(key string) (featureDefinition, bool) {
	for _, def := range featureDefinitions {
		if def.Key == key {
			return def, true
		}
	}

	return featureDefinition{}, false
}

func featureGroupDefinitionForKey(key string) featureGroupDefinition {
	for _, group := range featureGroupDefinitions {
		if group.Key == key {
			return group
		}
	}

	return featureGroupDefinition{Key: key, Name: key}
}
