CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}workflow_versions (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    graph JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workflow_id, version)
);

ALTER TABLE ${TABLE_PREFIX}workflows ADD COLUMN IF NOT EXISTS active_version INTEGER;
