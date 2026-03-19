-- Add description, servers, urls columns to mcp_servers (absorbed from mcp_sets).
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN servers TEXT NOT NULL DEFAULT '[]';
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN urls TEXT NOT NULL DEFAULT '[]';
