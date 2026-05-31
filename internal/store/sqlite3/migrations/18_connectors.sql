-- Connectors: data-driven definitions of external-service connection TYPES.
-- These replace the formerly hardcoded google/youtube OAuth catalog. A
-- connector holds NO secrets — only auth endpoints, scopes, and a credential
-- field schema — so it is stored unencrypted. Built-in connectors ship as
-- embedded JSON and are merged in at runtime; rows in this table are either
-- user-defined connectors or overrides of a built-in (matched by slug).
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}connectors (
    slug TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    auth_kind TEXT NOT NULL DEFAULT 'token',
    oauth TEXT NOT NULL DEFAULT '{}',
    fields TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);
