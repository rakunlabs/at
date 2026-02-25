CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}triggers (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workflows(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('http', 'cron')),
    config TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_workflow_id ON ${TABLE_PREFIX}triggers(workflow_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_type_enabled ON ${TABLE_PREFIX}triggers(type, enabled);
