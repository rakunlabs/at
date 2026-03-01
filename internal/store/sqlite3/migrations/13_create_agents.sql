CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    provider TEXT NOT NULL,
    model TEXT,
    system_prompt TEXT,
    skills TEXT,
    mcp_urls TEXT,
    max_iterations INTEGER DEFAULT 10,
    tool_timeout INTEGER DEFAULT 60,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);
