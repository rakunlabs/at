-- Runtime feature toggles. Definitions are hardcoded in the server catalog;
-- this table only stores enabled/disabled overrides.
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}feature_settings (
    key TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);
