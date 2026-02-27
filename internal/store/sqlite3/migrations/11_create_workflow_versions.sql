CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}workflow_versions (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    graph TEXT NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (workflow_id, version)
);

ALTER TABLE ${TABLE_PREFIX}workflows ADD COLUMN active_version INTEGER;
