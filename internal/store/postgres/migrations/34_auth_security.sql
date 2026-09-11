CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}auth_security (
    user_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    data JSONB NOT NULL DEFAULT '{}'
);
CREATE TABLE ${TABLE_PREFIX}auth_security_sources (
    hash TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL
);
CREATE INDEX ON ${TABLE_PREFIX}auth_security_sources(expires_at);
