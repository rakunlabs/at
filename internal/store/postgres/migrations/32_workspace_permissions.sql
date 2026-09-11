CREATE TABLE ${TABLE_PREFIX}workspace_permissions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    key TEXT NOT NULL,
    bundle JSONB NOT NULL,
    UNIQUE(workspace_id,key),
    UNIQUE(workspace_id,id)
);
CREATE TABLE ${TABLE_PREFIX}workspace_user_permissions (
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    permission_id TEXT NOT NULL,
    PRIMARY KEY(workspace_id,user_id,permission_id),
    FOREIGN KEY(workspace_id,user_id) REFERENCES ${TABLE_PREFIX}workspace_memberships(workspace_id,user_id),
    FOREIGN KEY(workspace_id,permission_id) REFERENCES ${TABLE_PREFIX}workspace_permissions(workspace_id,id) ON DELETE CASCADE
);
CREATE TABLE ${TABLE_PREFIX}workspace_user_denied (
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    PRIMARY KEY(workspace_id,user_id,capability),
    FOREIGN KEY(workspace_id,user_id) REFERENCES ${TABLE_PREFIX}workspace_memberships(workspace_id,user_id)
);
-- provider_id is immutable external-provider identity, never its display key.
-- Migration 36 creates the provider table; writes validate it transactionally.
CREATE TABLE ${TABLE_PREFIX}workspace_permission_mappings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    claim_kind TEXT NOT NULL CHECK(claim_kind IN ('roles','groups','permissions','scope','scopes')),
    claim_value TEXT NOT NULL CHECK(length(claim_value) BETWEEN 1 AND 1024),
    permission_id TEXT NOT NULL,
    UNIQUE(workspace_id,provider_id,claim_kind,claim_value,permission_id),
    FOREIGN KEY(workspace_id,permission_id) REFERENCES ${TABLE_PREFIX}workspace_permissions(workspace_id,id) ON DELETE CASCADE
);
