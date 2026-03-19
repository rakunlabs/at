-- Add description, servers, urls columns to mcp_servers (absorbed from mcp_sets).
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN IF NOT EXISTS servers JSONB NOT NULL DEFAULT '[]';
ALTER TABLE ${TABLE_PREFIX}mcp_servers ADD COLUMN IF NOT EXISTS urls JSONB NOT NULL DEFAULT '[]';
