CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}projects (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    goal_id TEXT DEFAULT '',
    lead_agent_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    color TEXT DEFAULT '',
    target_date TEXT DEFAULT NULL,
    archived_at TEXT DEFAULT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT DEFAULT '',
    updated_by TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_org ON ${TABLE_PREFIX}projects(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_goal ON ${TABLE_PREFIX}projects(goal_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_status ON ${TABLE_PREFIX}projects(status);
