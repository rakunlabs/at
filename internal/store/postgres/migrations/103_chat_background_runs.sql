-- A Chats conversation can be answered by a server-side run that keeps going
-- after the browser leaves. The receipt makes the run the only writer of the
-- transcript while it is live, across replicas.
ALTER TABLE ${TABLE_PREFIX}playground_conversations
    ADD COLUMN active_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN active_run_workspace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN active_run_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN active_run_cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
