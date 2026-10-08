-- Per-account connections and MCP OAuth.
--
-- A connection may now belong to one account (owner_user_id) instead of the
-- whole workspace, so an MCP upstream can run as the person (or agent) using
-- it. Workspace connections keep owner_user_id = '' and their existing names.
ALTER TABLE ${TABLE_PREFIX}connections
    ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS ${TABLE_PREFIX}connections_workspace_provider_name;
CREATE UNIQUE INDEX ${TABLE_PREFIX}connections_workspace_provider_name
    ON ${TABLE_PREFIX}connections(workspace_id, provider, name)
    WHERE owner_user_id = '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}connections_owner_provider_name
    ON ${TABLE_PREFIX}connections(workspace_id, owner_user_id, provider, name)
    WHERE owner_user_id <> '';
CREATE INDEX ${TABLE_PREFIX}connections_owner
    ON ${TABLE_PREFIX}connections(workspace_id, owner_user_id, provider);

-- Pending MCP OAuth authorizations. They live in the database rather than in
-- process memory because the authorization callback may reach another replica
-- than the one that started it. The PKCE verifier and any dynamically
-- registered client secret are encrypted with the installation key; rows are
-- single use and expire after ten minutes.
CREATE TABLE ${TABLE_PREFIX}mcp_oauth_pending (
    state_hash TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}mcp_oauth_pending_expires
    ON ${TABLE_PREFIX}mcp_oauth_pending(expires_at);
