CREATE TABLE ${TABLE_PREFIX}auth_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    admin BOOLEAN NOT NULL DEFAULT FALSE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    session_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ${TABLE_PREFIX}auth_sessions (
    hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON ${TABLE_PREFIX}auth_sessions(user_id);
CREATE INDEX ON ${TABLE_PREFIX}auth_sessions(expires_at);

-- Never reset this latch, even if the original admin is removed or disabled.
CREATE TABLE ${TABLE_PREFIX}auth_bootstrap (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    claimed BOOLEAN NOT NULL DEFAULT FALSE
);
INSERT INTO ${TABLE_PREFIX}auth_bootstrap(singleton) VALUES (TRUE);
