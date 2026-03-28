-- Consolidated initial schema for AT (SQLite3).
-- This single migration replaces the original 48 incremental migrations.

-- ============================================================================
-- Providers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}providers (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    config TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    allowed_providers TEXT DEFAULT NULL,
    allowed_models TEXT DEFAULT NULL,
    allowed_webhooks TEXT DEFAULT NULL,
    allowed_providers_mode TEXT NOT NULL DEFAULT '',
    allowed_models_mode TEXT NOT NULL DEFAULT '',
    allowed_webhooks_mode TEXT NOT NULL DEFAULT '',
    total_token_limit INTEGER DEFAULT NULL,
    limit_reset_interval TEXT DEFAULT NULL,
    last_reset_at DATETIME DEFAULT NULL,
    expires_at DATETIME DEFAULT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME DEFAULT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT ''
);

-- ============================================================================
-- Token Usage
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}token_usage (
    token_id TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    last_request_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    graph TEXT NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    active_version INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    graph TEXT NOT NULL DEFAULT '{"nodes":[],"edges":[]}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL DEFAULT '',
    UNIQUE (workflow_id, version)
);

-- ============================================================================
-- Triggers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}triggers (
    id TEXT PRIMARY KEY,
    workflow_id TEXT DEFAULT NULL,
    target_type TEXT NOT NULL DEFAULT 'workflow',
    target_id TEXT NOT NULL DEFAULT '',
    entry_node_id TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (type IN ('http', 'cron')),
    config TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1,
    alias TEXT DEFAULT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    tools TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    secret INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
-- Agents (consolidated — single config JSON column)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- RAG Collections (consolidated — single config JSON column)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_collections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- RAG States
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_states (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ============================================================================
-- MCP Servers
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}mcp_servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}',
    servers TEXT NOT NULL DEFAULT '[]',
    urls TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
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
    servers TEXT NOT NULL DEFAULT '[]',
    urls TEXT NOT NULL DEFAULT '[]',
    config TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

-- ============================================================================
-- Chat Sessions & Messages
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_id TEXT DEFAULT '',
    organization_id TEXT DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}chat_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
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
    channel_agents TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1,
    access_mode TEXT NOT NULL DEFAULT 'open',
    pending_approval INTEGER NOT NULL DEFAULT 0,
    allowed_users TEXT NOT NULL DEFAULT '[]',
    pending_users TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- ============================================================================
-- User Preferences
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}user_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL DEFAULT '{}',
    secret INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    issue_counter INTEGER DEFAULT 0,
    budget_monthly_cents INTEGER DEFAULT 0,
    spent_monthly_cents INTEGER DEFAULT 0,
    budget_reset_at TEXT DEFAULT NULL,
    require_board_approval_for_new_agents INTEGER DEFAULT 0,
    canvas_layout TEXT NOT NULL DEFAULT '{}',
    head_agent_id TEXT DEFAULT '',
    max_delegation_depth INTEGER DEFAULT 10,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    checked_out_at TEXT,
    identifier TEXT DEFAULT '',
    parent_id TEXT DEFAULT '',
    project_id TEXT DEFAULT '',
    billing_code TEXT DEFAULT '',
    priority_level TEXT DEFAULT '',
    request_depth INTEGER DEFAULT 0,
    started_at TEXT DEFAULT NULL,
    completed_at TEXT DEFAULT NULL,
    cancelled_at TEXT DEFAULT NULL,
    hidden_at TEXT DEFAULT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
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
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    monthly_limit REAL DEFAULT 0,
    current_spend REAL DEFAULT 0,
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_usage (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_id TEXT DEFAULT '',
    workflow_run_id TEXT DEFAULT '',
    session_id TEXT DEFAULT '',
    model TEXT NOT NULL,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    estimated_cost REAL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_usage_agent ON ${TABLE_PREFIX}agent_usage(agent_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_usage_created ON ${TABLE_PREFIX}agent_usage(created_at);

CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}model_pricing (
    id TEXT PRIMARY KEY,
    provider_key TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_price_per_1m REAL DEFAULT 0,
    completion_price_per_1m REAL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    details TEXT,
    created_at TEXT NOT NULL
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
    last_heartbeat_at TEXT NOT NULL,
    metadata TEXT DEFAULT '{}',
    updated_at TEXT NOT NULL
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
    context_snapshot TEXT DEFAULT '{}',
    usage_json TEXT DEFAULT '{}',
    result_json TEXT DEFAULT '{}',
    log_ref TEXT DEFAULT '',
    log_bytes INTEGER DEFAULT 0,
    log_sha256 TEXT DEFAULT '',
    stdout_excerpt TEXT DEFAULT '',
    stderr_excerpt TEXT DEFAULT '',
    session_id_before TEXT DEFAULT '',
    session_id_after TEXT DEFAULT '',
    started_at TEXT DEFAULT NULL,
    finished_at TEXT DEFAULT NULL,
    created_at TEXT NOT NULL
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
    context TEXT DEFAULT '{}',
    coalesced_count INTEGER DEFAULT 1,
    run_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
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
    state_json TEXT DEFAULT '{}',
    total_input_tokens INTEGER DEFAULT 0,
    total_output_tokens INTEGER DEFAULT 0,
    total_cost_cents INTEGER DEFAULT 0,
    last_run_id TEXT DEFAULT '',
    last_run_status TEXT DEFAULT '',
    last_error TEXT DEFAULT '',
    updated_at TEXT NOT NULL
);

-- ============================================================================
-- Agent Task Sessions
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_task_sessions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_key TEXT NOT NULL,
    adapter_type TEXT DEFAULT '',
    session_params_json TEXT DEFAULT '{}',
    session_display_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
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
    request_details TEXT DEFAULT '{}',
    decision_note TEXT DEFAULT '',
    decided_by_user_id TEXT DEFAULT '',
    decided_at TEXT DEFAULT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
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
    config_before TEXT NOT NULL DEFAULT '{}',
    config_after TEXT NOT NULL DEFAULT '{}',
    changed_by TEXT DEFAULT '',
    change_note TEXT DEFAULT '',
    created_at TEXT NOT NULL
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
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cost_cents REAL DEFAULT 0,
    created_at TEXT NOT NULL
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
    memory_model TEXT DEFAULT '',
    memory_provider TEXT DEFAULT '',
    memory_method TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(organization_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_org ON ${TABLE_PREFIX}organization_agents(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}org_agents_agent ON ${TABLE_PREFIX}organization_agents(agent_id);

-- ============================================================================
-- RAG Pages (original file content storage)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_pages (
    id TEXT PRIMARY KEY,
    collection_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}rag_collections(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    path TEXT DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    content_type TEXT DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    content_hash TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}rag_pages_collection_source ON ${TABLE_PREFIX}rag_pages(collection_id, source);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}rag_pages_collection_id ON ${TABLE_PREFIX}rag_pages(collection_id);

-- ============================================================================
-- Agent Memory (L0/L1 summaries)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_memory (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    task_identifier TEXT DEFAULT '',
    summary_l0 TEXT NOT NULL DEFAULT '',
    summary_l1 TEXT NOT NULL DEFAULT '',
    tags TEXT DEFAULT '[]',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_memory_agent_org ON ${TABLE_PREFIX}agent_memory(agent_id, organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_memory_org ON ${TABLE_PREFIX}agent_memory(organization_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}agent_memory_task ON ${TABLE_PREFIX}agent_memory(task_id);

-- ============================================================================
-- Agent Memory Messages (L2 full conversation)
-- ============================================================================
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agent_memory_messages (
    memory_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}agent_memory(id) ON DELETE CASCADE,
    messages TEXT NOT NULL DEFAULT '[]'
);
