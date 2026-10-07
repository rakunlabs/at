-- Exact local (loopback) MCP endpoints agent runs may dial, managed from
-- Settings -> Execution instead of bootstrap YAML.
ALTER TABLE ${TABLE_PREFIX}agent_runtime_settings
    ADD COLUMN trusted_local_mcp JSONB NOT NULL DEFAULT '[]'::jsonb;
