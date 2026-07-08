-- LLM call audit log: one row per upstream provider call with full
-- request/response bodies (Langfuse-style tracing). Bodies are capped at
-- 256 KB inline; larger payloads are spilled to disk (request_ref /
-- response_ref). Rows are swept by the retention janitor.
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}llm_calls (
    id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'gateway',
    endpoint TEXT NOT NULL DEFAULT '',
    token_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    task_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    organization_id TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    requested_model TEXT NOT NULL DEFAULT '',
    request_body TEXT NOT NULL DEFAULT '',
    response_body TEXT NOT NULL DEFAULT '',
    request_bytes INTEGER NOT NULL DEFAULT 0,
    response_bytes INTEGER NOT NULL DEFAULT 0,
    request_truncated INTEGER NOT NULL DEFAULT 0,
    response_truncated INTEGER NOT NULL DEFAULT 0,
    request_ref TEXT NOT NULL DEFAULT '',
    response_ref TEXT NOT NULL DEFAULT '',
    streamed INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cost_cents REAL NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    time_to_first_token_ms INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    finish_reason TEXT NOT NULL DEFAULT '',
    user_field TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_created_at ON ${TABLE_PREFIX}llm_calls(created_at);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_trace_id ON ${TABLE_PREFIX}llm_calls(trace_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_session_id ON ${TABLE_PREFIX}llm_calls(session_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_provider ON ${TABLE_PREFIX}llm_calls(provider);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_model ON ${TABLE_PREFIX}llm_calls(model);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_status ON ${TABLE_PREFIX}llm_calls(status);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_token_id ON ${TABLE_PREFIX}llm_calls(token_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_agent_id ON ${TABLE_PREFIX}llm_calls(agent_id);
