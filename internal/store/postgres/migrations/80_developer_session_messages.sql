CREATE TABLE ${TABLE_PREFIX}developer_session_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_sessions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user','assistant','tool','system')),
    content JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}developer_session_messages_session
    ON ${TABLE_PREFIX}developer_session_messages(session_id, created_at, id);

CREATE TABLE ${TABLE_PREFIX}developer_session_snapshots (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_sessions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    step INTEGER NOT NULL CHECK (step >= 0),
    phase TEXT NOT NULL CHECK (phase IN ('before','after')),
    head_sha TEXT NOT NULL DEFAULT '',
    storage_object_id TEXT REFERENCES ${TABLE_PREFIX}storage_objects(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (session_id, step, phase)
);
