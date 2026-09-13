ALTER TABLE ${TABLE_PREFIX}auth_users ADD COLUMN last_login_at TIMESTAMPTZ;
ALTER TABLE ${TABLE_PREFIX}auth_users ADD COLUMN last_login_ip TEXT NOT NULL DEFAULT '';

CREATE TABLE ${TABLE_PREFIX}auth_login_events (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('login_success', 'login_failed', 'login_locked', 'login_blocked', 'login_unlocked', 'sessions_revoked')),
    source_ip TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}auth_login_events(user_id, created_at DESC, id DESC);
CREATE INDEX ON ${TABLE_PREFIX}auth_login_events(created_at);
