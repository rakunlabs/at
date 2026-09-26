-- User-owned coding environments. Runtime filesystem/container names are
-- derived from opaque IDs; no caller-selected host path is persisted.
CREATE TABLE ${TABLE_PREFIX}developer_spaces (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> ''),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ready','stopped','error')),
    image TEXT NOT NULL DEFAULT '',
    cpu_limit TEXT NOT NULL DEFAULT '',
    memory_limit TEXT NOT NULL DEFAULT '',
    disk_limit_bytes BIGINT NOT NULL DEFAULT 0 CHECK (disk_limit_bytes >= 0),
    config JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    last_active_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}developer_spaces_owner_name
    ON ${TABLE_PREFIX}developer_spaces(workspace_id, owner_user_id, lower(name));
CREATE INDEX ${TABLE_PREFIX}developer_spaces_owner_updated
    ON ${TABLE_PREFIX}developer_spaces(workspace_id, owner_user_id, updated_at DESC);

CREATE TABLE ${TABLE_PREFIX}developer_repositories (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_spaces(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> ''),
    remote_url TEXT NOT NULL CHECK (remote_url <> ''),
    default_branch TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','cloning','ready','error')),
    head_sha TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (space_id, name)
);
CREATE INDEX ${TABLE_PREFIX}developer_repositories_space
    ON ${TABLE_PREFIX}developer_repositories(space_id, updated_at DESC);

CREATE TABLE ${TABLE_PREFIX}developer_worktrees (
    id TEXT PRIMARY KEY,
    repository_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_repositories(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_spaces(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> ''),
    branch TEXT NOT NULL CHECK (branch <> ''),
    base_ref TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready','invalid','missing')),
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (repository_id, name)
);
CREATE INDEX ${TABLE_PREFIX}developer_worktrees_repository
    ON ${TABLE_PREFIX}developer_worktrees(repository_id, updated_at DESC);

-- Sessions survive worktree pruning so their conversation and snapshots remain
-- inspectable. The nullable relation is intentionally SET NULL.
CREATE TABLE ${TABLE_PREFIX}developer_sessions (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}developer_spaces(id) ON DELETE CASCADE,
    repository_id TEXT REFERENCES ${TABLE_PREFIX}developer_repositories(id) ON DELETE SET NULL,
    worktree_id TEXT REFERENCES ${TABLE_PREFIX}developer_worktrees(id) ON DELETE SET NULL,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL CHECK (mode IN ('plan','build','review')),
    status TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','running','waiting_permission','waiting_question','completed','failed','cancelled')),
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}developer_sessions_space
    ON ${TABLE_PREFIX}developer_sessions(space_id, updated_at DESC);
CREATE INDEX ${TABLE_PREFIX}developer_sessions_worktree
    ON ${TABLE_PREFIX}developer_sessions(worktree_id, updated_at DESC);
