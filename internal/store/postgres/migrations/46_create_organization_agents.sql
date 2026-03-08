CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}organization_agents (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    role TEXT DEFAULT '',
    title TEXT DEFAULT '',
    parent_agent_id TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(organization_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_org ON ${TABLE_PREFIX}organization_agents(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_agent ON ${TABLE_PREFIX}organization_agents(agent_id);
