CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}issue_comments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    author_type TEXT NOT NULL DEFAULT 'user',
    author_id TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    parent_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}issue_comments_task ON ${TABLE_PREFIX}issue_comments(task_id);
