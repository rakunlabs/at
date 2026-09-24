ALTER TABLE ${TABLE_PREFIX}providers
    ALTER COLUMN workspace_id DROP NOT NULL;

ALTER TABLE ${TABLE_PREFIX}providers
    ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';

ALTER TABLE ${TABLE_PREFIX}providers
    ADD CONSTRAINT ${TABLE_PREFIX}providers_ownership_check CHECK (
        (owner_user_id = '' AND workspace_id IS NOT NULL)
        OR
        (owner_user_id <> '' AND workspace_id IS NULL)
    );

DROP INDEX IF EXISTS ${TABLE_PREFIX}providers_workspace_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}providers_workspace_key
    ON ${TABLE_PREFIX}providers(workspace_id, key)
    WHERE owner_user_id = '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}providers_owner_key
    ON ${TABLE_PREFIX}providers(owner_user_id, key)
    WHERE owner_user_id <> '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}providers_id_owner
    ON ${TABLE_PREFIX}providers(id, owner_user_id);

CREATE TABLE ${TABLE_PREFIX}personal_provider_grants (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    owner_user_id TEXT NOT NULL CHECK (owner_user_id <> ''),
    workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    global BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    CHECK ((global AND workspace_id IS NULL) OR (NOT global AND workspace_id IS NOT NULL)),
    FOREIGN KEY (provider_id, owner_user_id)
        REFERENCES ${TABLE_PREFIX}providers(id, owner_user_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX ${TABLE_PREFIX}personal_provider_workspace_grant
    ON ${TABLE_PREFIX}personal_provider_grants(provider_id, workspace_id)
    WHERE NOT global;
CREATE UNIQUE INDEX ${TABLE_PREFIX}personal_provider_global_grant
    ON ${TABLE_PREFIX}personal_provider_grants(provider_id)
    WHERE global;
