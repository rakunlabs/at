-- Trace privacy: rules that keep observations out of the trace store (skip)
-- or strip their content (redact) before they are written. A NULL workspace
-- is an installation rule managed by platform administrators; a workspace
-- rule is managed by that workspace's administrators. Cost accounting is
-- recorded separately and is never affected by these rules.
CREATE TABLE ${TABLE_PREFIX}trace_privacy_rules (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    description TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    token_id TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT 'skip' CHECK (action IN ('skip', 'redact')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}trace_privacy_rules_workspace ON ${TABLE_PREFIX}trace_privacy_rules(workspace_id);

-- Installation-wide settings; a single row keyed 'default'.
CREATE TABLE ${TABLE_PREFIX}trace_privacy_settings (
    id TEXT PRIMARY KEY CHECK (id = 'default'),
    allow_user_opt_out BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
