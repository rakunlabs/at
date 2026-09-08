ALTER TABLE ${TABLE_PREFIX}auth_sessions ADD COLUMN transport TEXT NOT NULL DEFAULT 'web' CHECK (transport IN ('web', 'mobile'));
CREATE TABLE ${TABLE_PREFIX}auth_mobile_requests (
    id TEXT PRIMARY KEY,
    challenge TEXT NOT NULL,
    state TEXT NOT NULL,
    remember BOOLEAN NOT NULL,
    device_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 0,
    web_session TEXT NOT NULL DEFAULT '',
    code_hash TEXT NOT NULL DEFAULT '',
    code_expires_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch'
);
CREATE UNIQUE INDEX ON ${TABLE_PREFIX}auth_mobile_requests(code_hash) WHERE code_hash <> '';
CREATE INDEX ON ${TABLE_PREFIX}auth_mobile_requests(expires_at);
