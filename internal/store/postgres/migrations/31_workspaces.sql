CREATE TABLE ${TABLE_PREFIX}workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
    archived BOOLEAN NOT NULL DEFAULT false,
    execution_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE ${TABLE_PREFIX}workspace_memberships (
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id),
    role TEXT NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    version BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (workspace_id,user_id)
);
CREATE INDEX ON ${TABLE_PREFIX}workspace_memberships(user_id, status);
CREATE TABLE ${TABLE_PREFIX}workspace_invitations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    role TEXT NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    user_id TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    issuer_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed BOOLEAN NOT NULL DEFAULT false,
    CHECK ((user_id <> '') <> (email <> ''))
);
CREATE INDEX ON ${TABLE_PREFIX}workspace_invitations(workspace_id, expires_at);
INSERT INTO ${TABLE_PREFIX}workspaces(id,name,execution_enabled) VALUES ('legacy-default','Default workspace',true);
INSERT INTO ${TABLE_PREFIX}workspace_memberships(workspace_id,user_id,role)
SELECT 'legacy-default', id, 'owner' FROM ${TABLE_PREFIX}auth_users WHERE admin AND NOT disabled;
