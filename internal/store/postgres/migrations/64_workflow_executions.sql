CREATE TABLE ${TABLE_PREFIX}workflow_executions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    workflow_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workflows(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('api','cron','webhook')),
    status TEXT NOT NULL CHECK (status IN ('queued','running','waiting','completed','blocked','cancelled','expired')),
    revision BIGINT NOT NULL DEFAULT 0,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    resume_requested BOOLEAN NOT NULL DEFAULT FALSE,
    wait_node_id TEXT NOT NULL DEFAULT '',
    wait_mode TEXT NOT NULL DEFAULT '',
    wait_prompt TEXT NOT NULL DEFAULT '',
    wake_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    decision_by TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    checkpoint JSONB NOT NULL DEFAULT '{"version":1,"nodes":{},"outputs":{}}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}workflow_executions(workspace_id, workflow_id, created_at DESC);
CREATE INDEX ON ${TABLE_PREFIX}workflow_executions(status, wake_at);
CREATE INDEX ON ${TABLE_PREFIX}workflow_executions(status, expires_at);
CREATE INDEX ON ${TABLE_PREFIX}workflow_executions(status, source, created_at);
