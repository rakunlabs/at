-- Add task_id and organization_id columns to chat_sessions for task-contextual chat.
-- SQLite doesn't support IF NOT EXISTS for ADD COLUMN, so we handle errors in Go.
ALTER TABLE ${TABLE_PREFIX}chat_sessions ADD COLUMN task_id TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}chat_sessions ADD COLUMN organization_id TEXT DEFAULT '';
