-- Add task_id and organization_id columns to chat_sessions for task-contextual chat.
ALTER TABLE ${TABLE_PREFIX}chat_sessions ADD COLUMN IF NOT EXISTS task_id TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}chat_sessions ADD COLUMN IF NOT EXISTS organization_id TEXT DEFAULT '';
