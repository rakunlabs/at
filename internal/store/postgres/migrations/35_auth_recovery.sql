-- Recovery authority is stored as purpose-bound hashed transactions in the
-- account security record, sharing its account row lock and version fence.
CREATE TABLE ${TABLE_PREFIX}auth_recovery_events (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    source TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('issue', 'redeem')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}auth_recovery_events(user_id, created_at);
