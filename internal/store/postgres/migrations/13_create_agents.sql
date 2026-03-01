CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    provider TEXT NOT NULL,
    model TEXT,
    system_prompt TEXT,
    skills JSONB,
    mcp_urls JSONB,
    max_iterations INTEGER DEFAULT 10,
    tool_timeout INTEGER DEFAULT 60,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);
