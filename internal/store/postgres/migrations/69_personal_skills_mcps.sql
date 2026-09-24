ALTER TABLE ${TABLE_PREFIX}skills
    ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS ${TABLE_PREFIX}skills_workspace_name;
CREATE UNIQUE INDEX ${TABLE_PREFIX}skills_workspace_name
    ON ${TABLE_PREFIX}skills(workspace_id, name)
    WHERE owner_user_id = '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}skills_owner_name
    ON ${TABLE_PREFIX}skills(workspace_id, owner_user_id, name)
    WHERE owner_user_id <> '';
CREATE INDEX ${TABLE_PREFIX}skills_owner
    ON ${TABLE_PREFIX}skills(workspace_id, owner_user_id);

ALTER TABLE ${TABLE_PREFIX}mcp_sets
    ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS ${TABLE_PREFIX}mcp_sets_workspace_name;
CREATE UNIQUE INDEX ${TABLE_PREFIX}mcp_sets_workspace_name
    ON ${TABLE_PREFIX}mcp_sets(workspace_id, name)
    WHERE owner_user_id = '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}mcp_sets_owner_name
    ON ${TABLE_PREFIX}mcp_sets(workspace_id, owner_user_id, name)
    WHERE owner_user_id <> '';
CREATE INDEX ${TABLE_PREFIX}mcp_sets_owner
    ON ${TABLE_PREFIX}mcp_sets(workspace_id, owner_user_id);

UPDATE ${TABLE_PREFIX}workspace_memberships SET version=version+1;
