-- AT as an MCP authorization server (MCP authorization spec, server side).
--
-- A gateway MCP server with config.oauth.enabled accepts OAuth access tokens
-- issued here in addition to API tokens. A token names one account and one
-- MCP server; calls run as that account under its live workspace access.
--
-- Clients are either dynamically registered (RFC 7591) or pre-registered by a
-- workspace administrator for one MCP server. Secrets, codes, access and
-- refresh tokens are stored only as SHA-256 hashes.
CREATE TABLE ${TABLE_PREFIX}mcp_auth_clients (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL UNIQUE,
    -- NULL for a dynamically registered client, which is installation-wide
    -- and may request any server whose policy admits dynamic clients.
    workspace_id TEXT REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    mcp_server_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    redirect_uris JSONB NOT NULL DEFAULT '[]',
    secret_hash TEXT NOT NULL DEFAULT '',
    dynamic BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_clients_server ON ${TABLE_PREFIX}mcp_auth_clients(workspace_id, mcp_server_id);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_clients_dynamic ON ${TABLE_PREFIX}mcp_auth_clients(created_at) WHERE dynamic;

-- A grant is one account's consent for one client to reach one MCP server.
-- Access and refresh tokens hang off it, so revoking the grant ends them all.
CREATE TABLE ${TABLE_PREFIX}mcp_auth_grants (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    mcp_server_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_name TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_grants_user ON ${TABLE_PREFIX}mcp_auth_grants(workspace_id, user_id);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_grants_server ON ${TABLE_PREFIX}mcp_auth_grants(workspace_id, mcp_server_id);

-- Single-use authorization codes (PKCE S256 required), ten-minute lifetime.
CREATE TABLE ${TABLE_PREFIX}mcp_auth_codes (
    code_hash TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}mcp_auth_grants(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_codes_expires ON ${TABLE_PREFIX}mcp_auth_codes(expires_at);

-- Access tokens (kind 'access', one hour) and rotating refresh tokens
-- (kind 'refresh', thirty days, single use).
CREATE TABLE ${TABLE_PREFIX}mcp_auth_tokens (
    token_hash TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}mcp_auth_grants(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_tokens_grant ON ${TABLE_PREFIX}mcp_auth_tokens(grant_id);
CREATE INDEX ${TABLE_PREFIX}mcp_auth_tokens_expires ON ${TABLE_PREFIX}mcp_auth_tokens(expires_at);
