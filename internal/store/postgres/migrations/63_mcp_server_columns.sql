-- These columns were added to 1_initial.sql without an upgrade migration.
-- Existing installations may still have the original config-only MCP table.
-- Keep current installations and partially repaired schemas intact.
ALTER TABLE ${TABLE_PREFIX}mcp_servers
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS servers JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS urls JSONB NOT NULL DEFAULT '[]';
