CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}user_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL DEFAULT '{}',
    secret INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}user_preferences_user_id
    ON ${TABLE_PREFIX}user_preferences(user_id);
