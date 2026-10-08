-- Cron run history: one row per firing of a cron trigger (scheduled or
-- started by hand) plus a bounded log that the system and the agents working
-- on the run write to. Organization runs stay open until their task finishes.
CREATE TABLE ${TABLE_PREFIX}cron_runs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    trigger_id TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('schedule', 'manual')),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'blocked', 'cancelled')),
    task_id TEXT NOT NULL DEFAULT '',
    task_identifier TEXT NOT NULL DEFAULT '',
    workflow_run_id TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '',
    reported_status TEXT NOT NULL DEFAULT '',
    reported_summary TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX ${TABLE_PREFIX}cron_runs_trigger ON ${TABLE_PREFIX}cron_runs(workspace_id, trigger_id, id DESC);
CREATE INDEX ${TABLE_PREFIX}cron_runs_task ON ${TABLE_PREFIX}cron_runs(task_id) WHERE task_id <> '';

CREATE TABLE ${TABLE_PREFIX}cron_run_logs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}cron_runs(id) ON DELETE CASCADE,
    level TEXT NOT NULL CHECK (level IN ('system', 'milestone', 'report', 'error')),
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}cron_run_logs_run ON ${TABLE_PREFIX}cron_run_logs(run_id, id);
