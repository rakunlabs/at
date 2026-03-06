CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}mcp_sets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    servers TEXT NOT NULL DEFAULT '[]',
    urls TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    created_by TEXT,
    updated_by TEXT
);
