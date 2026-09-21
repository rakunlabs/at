package service

import "context"

// Feature keys. The catalog (names, descriptions, groups and parent/child
// relations) lives in the server package; these constants exist so check sites
// elsewhere do not spell the key as a literal.
//
// The seven keys in the first block predate the granular catalog. They are kept
// as *parents* rather than renamed: a row disabling one of them keeps every
// child off, so an existing installation's overrides survive without a data
// migration.
const (
	FeatureProviderSetup         = "provider_setup"
	FeatureChatWorkbench         = "chat_workbench"
	FeatureAgents                = "agents"
	FeatureAutomation            = "automation"
	FeatureFiles                 = "files"
	FeatureConnections           = "connections_integrations"
	FeatureOrganizationWorkflows = "organization_workflows"
)

// Granular children and standalone features.
const (
	// LLM gateway.
	FeatureModelPricing   = "model_pricing"
	FeatureUsageAnalytics = "usage_analytics"
	FeatureAPITokens      = "api_tokens"
	// FeatureRoutingProfiles gates the management surface for named model
	// chains. Existing profiles keep routing gateway traffic when it is off.
	FeatureRoutingProfiles = "routing_profiles"

	// Workspace.
	FeaturePlayground         = "playground"
	FeatureChatSessions       = "chat_sessions"
	FeatureBots               = "bots"
	FeatureAudioTranscription = "audio_transcription"
	FeatureAgentHeartbeats    = "agent_heartbeats"
	FeatureSkills             = "skills"
	FeatureMarketplaces       = "marketplaces"
	FeatureGuides             = "guides"
	FeatureVariables          = "variables"

	// Automation.
	FeatureWorkflowBuilder = "workflow_builder"
	FeatureWorkflowRuns    = "workflow_runs"
	FeatureWebhookTriggers = "webhook_triggers"
	FeatureCronTriggers    = "cron_triggers"

	// Tools & execution.
	FeatureMCPServers    = "mcp_servers"
	FeatureMCPTools      = "mcp_tools"
	FeatureBuiltinTools  = "builtin_tools"
	FeatureBuiltinShell  = "builtin_shell"
	FeatureBuiltinScript = "builtin_script"
	FeatureBuiltinHTTP   = "builtin_http"
	FeatureBuiltinOther  = "builtin_other"
	FeatureTerminal      = "terminal"

	// Data & integrations.
	FeatureExternalConnections = "external_connections"
	FeatureIntegrationPacks    = "integration_packs"

	// Operations.
	FeatureOrganizations = "organizations"
	FeatureTasks         = "tasks"
	FeatureGoalsProjects = "goals_projects"
	FeatureApprovals     = "approvals"
	FeatureStudio        = "studio"
	FeatureLLMTraces     = "llm_traces"
	// FeatureWorkspaceManagement gates the multi-workspace surface: creating,
	// renaming, archiving and deleting workspaces, plus membership and
	// invitations. Disabling it also pins every request to DefaultWorkspaceID,
	// because a workspace nobody may administer is one nobody should be
	// working in. Records of other workspaces are left untouched and become
	// reachable again when it is re-enabled.
	FeatureWorkspaceManagement = "workspace_management"
)

// FeatureSetting stores the persisted enabled/disabled state for a built-in
// feature. Feature definitions live in the server catalog; this record only
// stores runtime overrides.
type FeatureSetting struct {
	Key       string `json:"key"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// FeatureSettingStorer defines persistence for runtime feature flags.
type FeatureSettingStorer interface {
	ListFeatureSettings(ctx context.Context) ([]FeatureSetting, error)
	GetFeatureSetting(ctx context.Context, key string) (*FeatureSetting, error)
	UpsertFeatureSetting(ctx context.Context, key string, enabled bool, updatedBy string) (*FeatureSetting, error)
}
