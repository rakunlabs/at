CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}marketplace_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'generic',
    search_url TEXT NOT NULL DEFAULT '',
    top_url TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

