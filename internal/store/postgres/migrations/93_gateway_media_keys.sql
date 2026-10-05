-- Gateway media download links carry their own credential: an MCP client's
-- shell (OpenCode, Claude Code) has no access to the API token configured in
-- the client, so a link that needs Authorization cannot be fetched by the
-- agent. Only the SHA-256 of the random key is stored; it expires.
ALTER TABLE ${TABLE_PREFIX}storage_objects ADD COLUMN download_key_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}storage_objects ADD COLUMN download_expires_at TIMESTAMPTZ;
