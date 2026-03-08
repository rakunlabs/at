-- Consolidated initial schema for AT (PostgreSQL).
-- This single migration replaces the original 48 incremental migrations.

-- ============================================================================
-- Providers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}providers (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- API Tokens
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    allowed_providers JSONB DEFAULT NULL,
    allowed_models JSONB DEFAULT NULL,
    allowed_webhooks JSONB DEFAULT NULL,
    allowed_providers_mode TEXT NOT NULL DEFAULT '',
    allowed_models_mode TEXT NOT NULL DEFAULT '',
    allowed_webhooks_mode TEXT NOT NULL DEFAULT '',
    total_token_limit BIGINT DEFAULT NULL,
    limit_reset_interval TEXT DEFAULT NULL,
    last_reset_at TIMESTAMPTZ DEFAULT NULL,
    expires_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ DEFAULT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Token Usage
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}token_usage (
    token_id TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    request_count BIGINT NOT NULL DEFAULT 0,
    last_request_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (token_id, model),
    FOREIGN KEY (token_id) REFERENCES ${TABLE_PREFIX}tokens(id) ON DELETE CASCADE
);

-- ============================================================================
-- Workflows
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    graph JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    active_version INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Workflow Versions
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}workflow_versions (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    graph JSONB NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    UNIQUE (workflow_id, version)
);

-- ============================================================================
-- Triggers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}triggers (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workflows(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('http', 'cron')),
    config JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    alias TEXT DEFAULT NULL,
    public BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_workflow_id ON ${TABLE_PREFIX}triggers(workflow_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_type_enabled ON ${TABLE_PREFIX}triggers(type, enabled);
CREATE UNIQUE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}triggers_alias ON ${TABLE_PREFIX}triggers(alias) WHERE alias IS NOT NULL;

-- ============================================================================
-- Skills
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}skills (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    tools JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Variables (originally "secrets", renamed)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}variables (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    secret BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Node Configs
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}node_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Agents (consolidated — single config JSONB column)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- RAG Collections (consolidated — single config JSONB column)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_collections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- RAG States
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_states (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================================
-- RAG MCP Servers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_mcp_servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- MCP Servers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}mcp_servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- MCP Sets
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}mcp_sets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    servers JSONB NOT NULL DEFAULT '[]',
    urls JSONB NOT NULL DEFAULT '[]',
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- Chat Sessions & Messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    FOREIGN KEY (session_id) REFERENCES ${TABLE_PREFIX}chat_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}chat_messages_session
    ON ${TABLE_PREFIX}chat_messages(session_id, created_at ASC);

-- ============================================================================
-- Bot Configs
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}bot_configs (
    id TEXT PRIMARY KEY,
    platform TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    token TEXT NOT NULL DEFAULT '',
    default_agent_id TEXT NOT NULL DEFAULT '',
    channel_agents JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    access_mode TEXT NOT NULL DEFAULT 'open',
    pending_approval BOOLEAN NOT NULL DEFAULT FALSE,
    allowed_users JSONB NOT NULL DEFAULT '[]',
    pending_users JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- Marketplace Sources
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}marketplace_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'generic',
    search_url TEXT NOT NULL DEFAULT '',
    top_url TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- ============================================================================
-- User Preferences
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}user_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value JSONB NOT NULL DEFAULT '{}',
    secret BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}user_preferences_user_id
    ON ${TABLE_PREFIX}user_preferences(user_id);

-- ============================================================================
-- Organizations
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}organizations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    issue_prefix TEXT DEFAULT '',
    issue_counter BIGINT DEFAULT 0,
    budget_monthly_cents BIGINT DEFAULT 0,
    spent_monthly_cents BIGINT DEFAULT 0,
    budget_reset_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    require_board_approval_for_new_agents BOOLEAN DEFAULT FALSE,
    canvas_layout JSONB NOT NULL DEFAULT '{}',
    head_agent_id TEXT DEFAULT '',
    max_delegation_depth INTEGER DEFAULT 10,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- Goals
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}goals (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    parent_goal_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    priority INTEGER DEFAULT 0,
    level TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}goals_org ON ${TABLE_PREFIX}goals(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}goals_parent ON ${TABLE_PREFIX}goals(parent_goal_id);

-- ============================================================================
-- Tasks
-- ============================================================================
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
    checked_out_at TIMESTAMP WITH TIME ZONE,
    identifier TEXT DEFAULT '',
    parent_id TEXT DEFAULT '',
    project_id TEXT DEFAULT '',
    billing_code TEXT DEFAULT '',
    priority_level TEXT DEFAULT '',
    request_depth INTEGER DEFAULT 0,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    completed_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    cancelled_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    hidden_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_agent ON ${TABLE_PREFIX}tasks(assigned_agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_goal ON ${TABLE_PREFIX}tasks(goal_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_status ON ${TABLE_PREFIX}tasks(status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_org ON ${TABLE_PREFIX}tasks(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_identifier ON ${TABLE_PREFIX}tasks(identifier);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}tasks_parent ON ${TABLE_PREFIX}tasks(parent_id);

-- ============================================================================
-- Projects
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}projects (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    goal_id TEXT DEFAULT '',
    lead_agent_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    color TEXT DEFAULT '',
    target_date TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by TEXT DEFAULT '',
    updated_by TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_org ON ${TABLE_PREFIX}projects(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_goal ON ${TABLE_PREFIX}projects(goal_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}projects_status ON ${TABLE_PREFIX}projects(status);

-- ============================================================================
-- Issue Comments
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}issue_comments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    author_type TEXT NOT NULL DEFAULT 'user',
    author_id TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    parent_id TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}issue_comments_task ON ${TABLE_PREFIX}issue_comments(task_id);

-- ============================================================================
-- Labels & Task Labels
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}labels (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#808080',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    UNIQUE(organization_id, name)
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}task_labels (
    task_id TEXT NOT NULL,
    label_id TEXT NOT NULL,
    PRIMARY KEY (task_id, label_id)
);

-- ============================================================================
-- Agent Budgets, Usage & Model Pricing
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_budgets (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL UNIQUE,
    monthly_limit DOUBLE PRECISION DEFAULT 0,
    current_spend DOUBLE PRECISION DEFAULT 0,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_usage (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_id TEXT DEFAULT '',
    workflow_run_id TEXT DEFAULT '',
    session_id TEXT DEFAULT '',
    model TEXT NOT NULL,
    prompt_tokens BIGINT DEFAULT 0,
    completion_tokens BIGINT DEFAULT 0,
    total_tokens BIGINT DEFAULT 0,
    estimated_cost DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_usage_agent ON ${TABLE_PREFIX}agent_usage(agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_usage_created ON ${TABLE_PREFIX}agent_usage(created_at);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}model_pricing (
    id TEXT PRIMARY KEY,
    provider_key TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_price_per_1m DOUBLE PRECISION DEFAULT 0,
    completion_price_per_1m DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    UNIQUE(provider_key, model)
);

-- ============================================================================
-- Audit Log
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}audit_log (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    details JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_resource ON ${TABLE_PREFIX}audit_log(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_actor ON ${TABLE_PREFIX}audit_log(actor_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_created ON ${TABLE_PREFIX}audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_org ON ${TABLE_PREFIX}audit_log(organization_id);

-- ============================================================================
-- Agent Heartbeats
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_heartbeats (
    agent_id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'healthy',
    last_heartbeat_at TIMESTAMP WITH TIME ZONE NOT NULL,
    metadata JSONB DEFAULT '{}',
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_heartbeats_status ON ${TABLE_PREFIX}agent_heartbeats(status);

-- ============================================================================
-- Heartbeat Runs
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}heartbeat_runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    organization_id TEXT DEFAULT '',
    invocation_source TEXT NOT NULL DEFAULT 'on_demand',
    trigger_detail TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    context_snapshot JSONB DEFAULT '{}',
    usage_json JSONB DEFAULT '{}',
    result_json JSONB DEFAULT '{}',
    log_ref TEXT DEFAULT '',
    log_bytes BIGINT DEFAULT 0,
    log_sha256 TEXT DEFAULT '',
    stdout_excerpt TEXT DEFAULT '',
    stderr_excerpt TEXT DEFAULT '',
    session_id_before TEXT DEFAULT '',
    session_id_after TEXT DEFAULT '',
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    finished_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}heartbeat_runs_agent ON ${TABLE_PREFIX}heartbeat_runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}heartbeat_runs_status ON ${TABLE_PREFIX}heartbeat_runs(status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}heartbeat_runs_org ON ${TABLE_PREFIX}heartbeat_runs(organization_id);

-- ============================================================================
-- Wakeup Requests
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}wakeup_requests (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    organization_id TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    idempotency_key TEXT DEFAULT '',
    context JSONB DEFAULT '{}',
    coalesced_count INTEGER DEFAULT 1,
    run_id TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}wakeup_requests_agent_status ON ${TABLE_PREFIX}wakeup_requests(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}wakeup_requests_idempotency ON ${TABLE_PREFIX}wakeup_requests(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}wakeup_requests_org ON ${TABLE_PREFIX}wakeup_requests(organization_id);

-- ============================================================================
-- Agent Runtime State
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_runtime_state (
    agent_id TEXT PRIMARY KEY,
    session_id TEXT DEFAULT '',
    state_json JSONB DEFAULT '{}',
    total_input_tokens BIGINT DEFAULT 0,
    total_output_tokens BIGINT DEFAULT 0,
    total_cost_cents BIGINT DEFAULT 0,
    last_run_id TEXT DEFAULT '',
    last_run_status TEXT DEFAULT '',
    last_error TEXT DEFAULT '',
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- ============================================================================
-- Agent Task Sessions
-- ============================================================================
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

-- ============================================================================
-- Approvals
-- ============================================================================
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

-- ============================================================================
-- Agent Config Revisions
-- ============================================================================
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

-- ============================================================================
-- Cost Events
-- ============================================================================
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

-- ============================================================================
-- Organization Agents
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}organization_agents (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    role TEXT DEFAULT '',
    title TEXT DEFAULT '',
    parent_agent_id TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    heartbeat_schedule TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(organization_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_org ON ${TABLE_PREFIX}organization_agents(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_agent ON ${TABLE_PREFIX}organization_agents(agent_id);
