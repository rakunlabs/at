CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    graph JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
