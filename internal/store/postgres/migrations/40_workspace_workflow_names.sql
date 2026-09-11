DROP INDEX IF EXISTS idx_${TABLE_PREFIX}workflows_name;
CREATE UNIQUE INDEX ${TABLE_PREFIX}workflows_workspace_name ON ${TABLE_PREFIX}workflows(workspace_id,name);
ALTER TABLE ${TABLE_PREFIX}node_configs DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}node_configs_name_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}node_configs_workspace_name ON ${TABLE_PREFIX}node_configs(workspace_id,name);
CREATE INDEX ON ${TABLE_PREFIX}workflows(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}workflow_versions(workspace_id,workflow_id,version);
CREATE INDEX ON ${TABLE_PREFIX}triggers(workspace_id,workflow_id);
