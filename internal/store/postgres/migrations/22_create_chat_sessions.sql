CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    FOREIGN KEY (session_id) REFERENCES ${TABLE_PREFIX}chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}chat_messages_session
    ON ${TABLE_PREFIX}chat_messages(session_id, created_at ASC);
