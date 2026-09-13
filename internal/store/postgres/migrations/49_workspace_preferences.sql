-- Account-wide startup preference; browser tabs retain their own active selection.
CREATE TABLE ${TABLE_PREFIX}workspace_preferences (
    user_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    mode TEXT NOT NULL DEFAULT 'default' CHECK (mode IN ('default', 'last_used', 'workspace')),
    workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE SET NULL,
    last_workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE SET NULL
);
