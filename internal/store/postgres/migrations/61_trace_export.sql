CREATE TABLE ${TABLE_PREFIX}trace_export_settings (
    workspace_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    config TEXT NOT NULL
);
