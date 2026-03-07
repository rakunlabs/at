CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}user_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value JSONB NOT NULL DEFAULT '{}',
    secret BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}user_preferences_user_id
    ON ${TABLE_PREFIX}user_preferences(user_id);
