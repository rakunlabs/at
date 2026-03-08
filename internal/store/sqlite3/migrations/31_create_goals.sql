CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}goals (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    parent_goal_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    priority INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}goals_org ON ${TABLE_PREFIX}goals(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}goals_parent ON ${TABLE_PREFIX}goals(parent_goal_id);
