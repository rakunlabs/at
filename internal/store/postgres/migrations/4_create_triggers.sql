CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}triggers (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workflows(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('http', 'cron')),
    config JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_workflow_id ON ${TABLE_PREFIX}triggers(workflow_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_type_enabled ON ${TABLE_PREFIX}triggers(type, enabled);
