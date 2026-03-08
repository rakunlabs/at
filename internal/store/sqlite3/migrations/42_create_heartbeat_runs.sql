CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}heartbeat_runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    invocation_source TEXT NOT NULL DEFAULT 'on_demand',
    trigger_detail TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    context_snapshot TEXT DEFAULT '{}',
    usage_json TEXT DEFAULT '{}',
    result_json TEXT DEFAULT '{}',
    log_ref TEXT DEFAULT '',
    log_bytes INTEGER DEFAULT 0,
    log_sha256 TEXT DEFAULT '',
    stdout_excerpt TEXT DEFAULT '',
    stderr_excerpt TEXT DEFAULT '',
    session_id_before TEXT DEFAULT '',
    session_id_after TEXT DEFAULT '',
    started_at TEXT DEFAULT NULL,
    finished_at TEXT DEFAULT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}heartbeat_runs_agent ON ${TABLE_PREFIX}heartbeat_runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}heartbeat_runs_status ON ${TABLE_PREFIX}heartbeat_runs(status);
