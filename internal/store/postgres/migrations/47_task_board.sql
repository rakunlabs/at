-- Per-workspace Kanban board layout.
--
-- The board used to be three columns hardcoded in the Svelte component, with a
-- status-to-column mapping nobody could change. That forced one shape on every
-- team and produced at least one misleading default: `blocked` tasks were shown
-- under "Done".
--
-- One row per workspace holding the ordered column list as JSONB. A workspace
-- with no row uses the shipped default, so this table stays empty until someone
-- actually customises their board.
CREATE TABLE ${TABLE_PREFIX}task_board_settings (
    workspace_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    columns JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_by TEXT NOT NULL DEFAULT ''
);
