-- Connections: named, reusable credential sets for external service providers.
-- One row per "account" (e.g. a single YouTube channel). Multiple connections
-- can exist for the same provider; agents reference them by ID.
--
-- The credentials column holds the full ConnectionCredentials JSON blob,
-- encrypted with the database AES-256-GCM key (enc:<base64> prefix).
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}connections (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    name TEXT NOT NULL,
    account_label TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    credentials TEXT NOT NULL DEFAULT '{}',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    UNIQUE(provider, name)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}connections_provider
    ON ${TABLE_PREFIX}connections(provider);
