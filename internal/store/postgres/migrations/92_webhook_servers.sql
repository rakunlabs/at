-- Dedicated webhook listeners: an installation administrator opens extra ports
-- (for example :5050) whose only routes are the webhooks bound to them. The
-- config column holds an encrypted JSON blob (TLS key, CIDR allowlist, limits).
CREATE TABLE ${TABLE_PREFIX}webhook_servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    bind_host TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    base_path TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    all_workspaces BOOLEAN NOT NULL DEFAULT FALSE,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}webhook_servers_address ON ${TABLE_PREFIX}webhook_servers(bind_host, port);

-- Workspaces whose webhooks may be published on a server (ignored while
-- all_workspaces is set).
CREATE TABLE ${TABLE_PREFIX}webhook_server_workspaces (
    server_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}webhook_servers(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, workspace_id)
);
CREATE INDEX ${TABLE_PREFIX}webhook_server_workspaces_workspace ON ${TABLE_PREFIX}webhook_server_workspaces(workspace_id);

-- A trigger published on a server. An empty path means "reachable by the
-- trigger's ID or alias"; a custom path is unique per server.
CREATE TABLE ${TABLE_PREFIX}trigger_webhook_routes (
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    trigger_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}triggers(id) ON DELETE CASCADE,
    server_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}webhook_servers(id) ON DELETE CASCADE,
    path TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (trigger_id, server_id)
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}trigger_webhook_routes_path ON ${TABLE_PREFIX}trigger_webhook_routes(server_id, path) WHERE path <> '';
CREATE INDEX ${TABLE_PREFIX}trigger_webhook_routes_server ON ${TABLE_PREFIX}trigger_webhook_routes(server_id);

-- Zero value keeps the historical behaviour: every HTTP trigger is reachable
-- on the main /webhooks/{id} route unless explicitly hidden.
ALTER TABLE ${TABLE_PREFIX}triggers ADD COLUMN hide_from_main BOOLEAN NOT NULL DEFAULT FALSE;
-- Encrypted signing secret for HMAC signature verification. Write-only.
ALTER TABLE ${TABLE_PREFIX}triggers ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';

-- Recent deliveries per webhook (bounded per trigger on insert).
CREATE TABLE ${TABLE_PREFIX}webhook_deliveries (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    trigger_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL DEFAULT '',
    server_id TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    status INTEGER NOT NULL DEFAULT 0,
    run_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    client_ip TEXT NOT NULL DEFAULT '',
    body_bytes BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}webhook_deliveries_trigger ON ${TABLE_PREFIX}webhook_deliveries(workspace_id, trigger_id, id DESC);
