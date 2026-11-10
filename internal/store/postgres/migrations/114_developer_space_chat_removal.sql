-- Developer Spaces no longer carry their own AI chat: the coding agent runs in
-- Chats, which reaches the space container through the Developer Space tool
-- source. Session transcripts, pending approvals, run receipts and snapshot
-- metadata are removed. SQL cannot reach the storage backend, so snapshot
-- blobs stay under workspaces/<workspace>/snapshots/developer-sessions/ until
-- an operator deletes them. Files in /workspace volumes and the spaces stay.
DELETE FROM ${TABLE_PREFIX}storage_objects WHERE namespace = 'snapshots' AND path LIKE 'developer-sessions/%';

DROP TABLE IF EXISTS ${TABLE_PREFIX}developer_pending_tools;
DROP TABLE IF EXISTS ${TABLE_PREFIX}developer_session_snapshots;
DROP TABLE IF EXISTS ${TABLE_PREFIX}developer_session_messages;
DROP TABLE IF EXISTS ${TABLE_PREFIX}developer_sessions;

-- Agent profiles (plan/build/review rules) lived in the space config.
UPDATE ${TABLE_PREFIX}developer_spaces
SET config = CASE WHEN config ? 'disable_formatter'
    THEN jsonb_build_object('disable_formatter', config->'disable_formatter')
    ELSE '{}'::jsonb END;
