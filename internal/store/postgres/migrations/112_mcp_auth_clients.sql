-- MCP authorization server: remembered consent, Client ID Metadata
-- Documents and client cleanup.
--
-- Remembered consent lives on the grant (one account, one client, one MCP
-- server, one resource) and is honoured only for the exact redirect URI it
-- was given for, until remembered_until.
ALTER TABLE ${TABLE_PREFIX}mcp_auth_grants
    ADD COLUMN remembered_redirect TEXT NOT NULL DEFAULT '',
    ADD COLUMN remembered_until TIMESTAMPTZ;

-- A metadata-document client's client_id is the https URL of its metadata
-- document (draft-ietf-oauth-client-id-metadata-document). The row caches
-- the fetched document; metadata_expires_at says when to fetch it again.
ALTER TABLE ${TABLE_PREFIX}mcp_auth_clients
    ADD COLUMN metadata_document BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN metadata_expires_at TIMESTAMPTZ,
    ADD COLUMN client_uri TEXT NOT NULL DEFAULT '';
CREATE INDEX ${TABLE_PREFIX}mcp_auth_clients_metadata ON ${TABLE_PREFIX}mcp_auth_clients(created_at) WHERE metadata_document;
CREATE INDEX ${TABLE_PREFIX}mcp_auth_grants_client ON ${TABLE_PREFIX}mcp_auth_grants(client_id);
