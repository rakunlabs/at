CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}wakeup_requests (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    idempotency_key TEXT DEFAULT '',
    context TEXT DEFAULT '{}',
    coalesced_count INTEGER DEFAULT 1,
    run_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}wakeup_requests_agent_status ON ${TABLE_PREFIX}wakeup_requests(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}wakeup_requests_idempotency ON ${TABLE_PREFIX}wakeup_requests(idempotency_key);
