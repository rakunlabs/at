CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_heartbeats (
    agent_id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'healthy',
    last_heartbeat_at TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_heartbeats_status ON ${TABLE_PREFIX}agent_heartbeats(status);
