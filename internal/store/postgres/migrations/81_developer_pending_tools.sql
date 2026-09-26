CREATE TABLE ${TABLE_PREFIX}developer_pending_tools (
    session_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}developer_sessions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    tool_call JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
