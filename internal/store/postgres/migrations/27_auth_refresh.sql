-- Deliberately force reauthentication. A v25 bearer is never a refresh token.
DELETE FROM ${TABLE_PREFIX}auth_sessions;
ALTER TABLE ${TABLE_PREFIX}auth_sessions ADD COLUMN remember BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE ${TABLE_PREFIX}auth_sessions ADD COLUMN refresh_after TIMESTAMPTZ NOT NULL DEFAULT 'epoch';
CREATE INDEX ON ${TABLE_PREFIX}auth_sessions(user_id, expires_at);
CREATE TABLE ${TABLE_PREFIX}auth_credentials (
    hash TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_sessions(hash) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    consumed BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON ${TABLE_PREFIX}auth_credentials(session_id);
CREATE INDEX ON ${TABLE_PREFIX}auth_credentials(session_id) WHERE kind = 'access';
CREATE INDEX ON ${TABLE_PREFIX}auth_credentials(expires_at);
