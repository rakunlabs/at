CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}labels (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#808080',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(organization_id, name)
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}task_labels (
    task_id TEXT NOT NULL,
    label_id TEXT NOT NULL,
    PRIMARY KEY (task_id, label_id)
);
