-- History of non-durable workflow runs (cron, webhook, API, workflow_run tool).
-- These ran on in-memory goroutines and left nothing behind but a log line, so
-- a failing scheduled run was invisible from the UI. Durable runs keep their
-- own record in workflow_executions. Retention: completed 7 days, failed and
-- cancelled 30 days (swept by the server).
CREATE TABLE ${TABLE_PREFIX}workflow_runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    workflow_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workflows(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('api', 'cron', 'webhook', 'tool')),
    trigger_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'cancelled', 'interrupted')),
    error TEXT NOT NULL DEFAULT '',
    failed_node_id TEXT NOT NULL DEFAULT '',
    failed_node_type TEXT NOT NULL DEFAULT '',
    handled_errors JSONB NOT NULL DEFAULT '[]',
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- Refreshed by the owning process while the run is live. A running row
    -- whose heartbeat stops (restart, crash) becomes 'interrupted'; replicas
    -- never close each other's live runs because each refreshes its own.
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX ON ${TABLE_PREFIX}workflow_runs(workspace_id, workflow_id, started_at DESC);
CREATE INDEX ON ${TABLE_PREFIX}workflow_runs(status, finished_at);
CREATE INDEX ON ${TABLE_PREFIX}workflow_runs(heartbeat_at) WHERE status = 'running';
