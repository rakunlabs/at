CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}tasks (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    goal_id TEXT DEFAULT '',
    assigned_agent_id TEXT DEFAULT '',
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    priority INTEGER DEFAULT 0,
    result TEXT DEFAULT '',
    checked_out_by TEXT DEFAULT '',
    checked_out_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_agent ON ${TABLE_PREFIX}tasks(assigned_agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_goal ON ${TABLE_PREFIX}tasks(goal_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_status ON ${TABLE_PREFIX}tasks(status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_org ON ${TABLE_PREFIX}tasks(organization_id);
