-- A developer session may run as one of the workspace's agents instead of a
-- built-in profile. Empty keeps the built-in profile named by mode. There is
-- no foreign key: agents can be deleted, and the session then reports that
-- its agent is gone instead of disappearing with it.
ALTER TABLE ${TABLE_PREFIX}developer_sessions
    ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';
