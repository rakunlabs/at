-- Developer sessions gain the OpenCode-style workbench: a per-session
-- reasoning effort, a todo list the agent maintains, and undo/redo. Undo is
-- recorded as the user message to rewind to plus the file snapshot to return
-- to on redo; the messages after the revert point are deleted only when the
-- next prompt is sent.
ALTER TABLE ${TABLE_PREFIX}developer_sessions
    ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '',
    ADD COLUMN revert_message_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN revert_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN todos JSONB NOT NULL DEFAULT '[]';

-- snapshot is the file-tree hash taken before a user prompt ran (the shadow
-- Git repository in the space volume); kind marks compaction summaries.
ALTER TABLE ${TABLE_PREFIX}developer_session_messages
    ADD COLUMN snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'compaction'));
