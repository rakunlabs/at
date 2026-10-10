ALTER TABLE ${TABLE_PREFIX}developer_sessions
    ADD COLUMN active_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN active_run_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN active_run_cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
