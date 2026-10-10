-- Stop/Reset close admission durably before cancelling runs or touching storage.
-- No timeout implicitly reopens a space; only an explicit Start may do that.
ALTER TABLE ${TABLE_PREFIX}developer_spaces
    ADD COLUMN execution_suspended BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN active_control_id TEXT NOT NULL DEFAULT '';
