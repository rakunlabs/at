-- Guardrail policies: input filters, prompt-injection detection, sensitive
-- value masking and output filters applied to model calls at the provider
-- boundary. A NULL workspace is an installation policy managed by platform
-- administrators; a workspace policy is managed by that workspace's
-- administrators. Matching fields mirror trace privacy rules.
CREATE TABLE ${TABLE_PREFIX}guardrail_policies (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    mode TEXT NOT NULL DEFAULT 'enforce' CHECK (mode IN ('enforce', 'monitor')),
    user_id TEXT NOT NULL DEFAULT '',
    token_id TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}guardrail_policies_workspace ON ${TABLE_PREFIX}guardrail_policies(workspace_id);
