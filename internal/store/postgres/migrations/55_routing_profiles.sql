-- Workspace-scoped named model chains. A gateway request whose model names a
-- profile is expanded to targets and routed through the existing fallback path.
--
-- name excludes '/' at the schema level because absence of a slash is what
-- distinguishes a profile name from a direct provider/model reference during
-- chain resolution; a stored name containing one would be unreachable.
-- UNIQUE(workspace_id,name) keeps names per workspace rather than global.
CREATE TABLE ${TABLE_PREFIX}routing_profiles (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200 AND position('/' in name) = 0),
    description TEXT NOT NULL DEFAULT '',
    targets JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    UNIQUE(workspace_id,name),
    UNIQUE(workspace_id,id)
);
CREATE INDEX ON ${TABLE_PREFIX}routing_profiles(workspace_id, name);
