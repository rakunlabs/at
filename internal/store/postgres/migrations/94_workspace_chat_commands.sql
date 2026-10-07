-- Chats slash commands shared inside one workspace. Listing and running is open
-- to every member admitted to Chats; owner_user_id is the immutable creator and
-- is enforced on update/delete by the store. Personal commands live in
-- user_preferences (`playground_commands`).
CREATE TABLE ${TABLE_PREFIX}workspace_chat_commands (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,31}$'),
    description TEXT NOT NULL DEFAULT '',
    template TEXT NOT NULL CHECK (length(template) BETWEEN 1 AND 32768),
    model TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}workspace_chat_commands_workspace_name
    ON ${TABLE_PREFIX}workspace_chat_commands(workspace_id, name);
