-- Per-user chat sessions and agent ownership tiers.
--
-- chat_sessions.owner_user_id: the account that created the session through
-- the UI. Empty means "no browser owner" — bot/platform sessions and legacy
-- rows — which stay visible to installation/workspace administrators only.
-- Owner-scoped rows are visible to their owner alone (Playground precedent).
--
-- agents.owner_user_id: empty = workspace agent (shared inside the
-- workspace, unchanged behaviour). Non-empty = personal agent, visible to and
-- editable by its owner (and platform administrators). The "global" tier
-- rides config->>'shared_with_all_workspaces' like providers do, so it needs
-- no column.
ALTER TABLE ${TABLE_PREFIX}chat_sessions ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ${TABLE_PREFIX}chat_sessions_owner_idx ON ${TABLE_PREFIX}chat_sessions (workspace_id, owner_user_id, updated_at DESC);

ALTER TABLE ${TABLE_PREFIX}agents ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ${TABLE_PREFIX}agents_owner_idx ON ${TABLE_PREFIX}agents (workspace_id, owner_user_id);
