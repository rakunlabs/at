ALTER TABLE ${TABLE_PREFIX}mcp_sets DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}mcp_sets_name_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}mcp_sets_workspace_name ON ${TABLE_PREFIX}mcp_sets(workspace_id,name);
ALTER TABLE ${TABLE_PREFIX}mcp_servers DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}mcp_servers_name_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}mcp_servers_workspace_name ON ${TABLE_PREFIX}mcp_servers(workspace_id,name);
CREATE INDEX ON ${TABLE_PREFIX}mcp_sets(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}mcp_servers(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}bot_configs(workspace_id,id);
UPDATE ${TABLE_PREFIX}workspace_memberships SET version=version+1;
