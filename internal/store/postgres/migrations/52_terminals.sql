CREATE TABLE ${TABLE_PREFIX}terminal_sessions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL,
    target_name TEXT NOT NULL,
    username TEXT NOT NULL,
    title TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ON ${TABLE_PREFIX}terminal_sessions(owner_id, position, id);
CREATE TABLE ${TABLE_PREFIX}terminal_preferences (
    owner_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    data JSONB NOT NULL DEFAULT '{}'
);
