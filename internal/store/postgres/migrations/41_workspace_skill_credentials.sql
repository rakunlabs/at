ALTER TABLE ${TABLE_PREFIX}skills DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}skills_name_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}skills_workspace_name ON ${TABLE_PREFIX}skills(workspace_id,name);
ALTER TABLE ${TABLE_PREFIX}variables DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}variables_key_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}variables_workspace_key ON ${TABLE_PREFIX}variables(workspace_id,key);
ALTER TABLE ${TABLE_PREFIX}connections DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}connections_provider_name_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}connections_workspace_provider_name ON ${TABLE_PREFIX}connections(workspace_id,provider,name);
CREATE INDEX ON ${TABLE_PREFIX}skills(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}variables(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}connections(workspace_id,id);
-- New explicit use capabilities fence running executions from the older registry.
UPDATE ${TABLE_PREFIX}workspace_memberships SET version=version+1;
