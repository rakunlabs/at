CREATE TABLE ${TABLE_PREFIX}provider_use_policy (
    workspace_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    config JSONB NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1
);
