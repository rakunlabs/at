package service

import "context"

const (
	FeatureProviderSetup         = "provider_setup"
	FeatureChatWorkbench         = "chat_workbench"
	FeatureAgents                = "agents"
	FeatureAutomation            = "automation"
	FeatureFiles                 = "files"
	FeatureConnections           = "connections_integrations"
	FeatureOrganizationWorkflows = "organization_workflows"
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
