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
