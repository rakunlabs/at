-- Loopback MCP endpoints are no longer special-cased: dialling an MCP upstream
-- already requires Trusted host execution, so the per-URL allowlist added by
-- migration 96 protected nothing and only got in the way.
ALTER TABLE ${TABLE_PREFIX}agent_runtime_settings
    DROP COLUMN IF EXISTS trusted_local_mcp;
