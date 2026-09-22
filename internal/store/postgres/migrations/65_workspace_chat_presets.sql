-- Named Chats setups shared inside one workspace. Reading and applying is open
-- to every member admitted to Chats; owner_user_id is the immutable creator and
-- is enforced on update/delete by the store.
CREATE TABLE ${TABLE_PREFIX}workspace_chat_presets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
    setup JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}workspace_chat_presets_workspace_name
    ON ${TABLE_PREFIX}workspace_chat_presets(workspace_id, lower(name));
CREATE INDEX ${TABLE_PREFIX}workspace_chat_presets_workspace_updated
    ON ${TABLE_PREFIX}workspace_chat_presets(workspace_id, updated_at DESC);
