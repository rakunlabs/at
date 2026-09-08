CREATE TABLE ${TABLE_PREFIX}auth_passkeys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    credential_id BYTEA NOT NULL UNIQUE,
    credential JSONB NOT NULL,
    sign_count BIGINT NOT NULL CHECK (sign_count BETWEEN 0 AND 4294967295),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX ON ${TABLE_PREFIX}auth_passkeys(user_id);

CREATE TABLE ${TABLE_PREFIX}auth_challenges (
    hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    data JSONB NOT NULL
);
CREATE INDEX ON ${TABLE_PREFIX}auth_challenges(expires_at);
CREATE INDEX ON ${TABLE_PREFIX}auth_challenges(user_id);
