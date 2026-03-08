CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_task_sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_key TEXT NOT NULL,
    adapter_type TEXT DEFAULT '',
    session_params_json JSONB DEFAULT '{}',
    session_display_id TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    UNIQUE(agent_id, task_key)
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}approvals (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    requested_by_type TEXT NOT NULL DEFAULT 'user',
    requested_by_id TEXT NOT NULL DEFAULT '',
    request_details JSONB DEFAULT '{}',
    decision_note TEXT DEFAULT '',
    decided_by_user_id TEXT DEFAULT '',
    decided_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}approvals_org_status ON ${TABLE_PREFIX}approvals(organization_id, status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}approvals_status ON ${TABLE_PREFIX}approvals(status);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_config_revisions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    config_before JSONB NOT NULL DEFAULT '{}',
    config_after JSONB NOT NULL DEFAULT '{}',
    changed_by TEXT DEFAULT '',
    change_note TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_config_revisions_agent ON ${TABLE_PREFIX}agent_config_revisions(agent_id);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}cost_events (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    agent_id TEXT NOT NULL,
    task_id TEXT DEFAULT '',
    project_id TEXT DEFAULT '',
    goal_id TEXT DEFAULT '',
    billing_code TEXT DEFAULT '',
    run_id TEXT DEFAULT '',
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens BIGINT DEFAULT 0,
    output_tokens BIGINT DEFAULT 0,
    cost_cents DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_agent ON ${TABLE_PREFIX}cost_events(agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_org ON ${TABLE_PREFIX}cost_events(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_billing ON ${TABLE_PREFIX}cost_events(billing_code);
