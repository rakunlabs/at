CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}mcp_sets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    servers JSONB NOT NULL DEFAULT '[]',
    urls JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);
