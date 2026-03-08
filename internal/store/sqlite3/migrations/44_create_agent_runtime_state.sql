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
