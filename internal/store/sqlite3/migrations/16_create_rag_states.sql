CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_states (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
