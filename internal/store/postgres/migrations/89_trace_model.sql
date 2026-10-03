-- Langfuse-style trace model.
--
-- Observations gain real timestamps: created_at was second-precision text
-- stamped when the work *ended*, so a waterfall had to guess every start
-- from latency. started_at/ended_at are millisecond timestamps; existing rows
-- are backfilled from created_at - latency_ms, which is what every reader
-- derived before.
ALTER TABLE ${TABLE_PREFIX}llm_calls
    ADD COLUMN started_at TIMESTAMPTZ,
    ADD COLUMN ended_at TIMESTAMPTZ,
    ADD COLUMN user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN environment TEXT NOT NULL DEFAULT '',
    ADD COLUMN release TEXT NOT NULL DEFAULT '';

UPDATE ${TABLE_PREFIX}llm_calls
SET ended_at = COALESCE(NULLIF(created_at, '')::timestamptz, clock_timestamp());
UPDATE ${TABLE_PREFIX}llm_calls
SET started_at = ended_at - make_interval(secs => GREATEST(latency_ms, 0) / 1000.0);

ALTER TABLE ${TABLE_PREFIX}llm_calls
    ALTER COLUMN started_at SET NOT NULL,
    ALTER COLUMN started_at SET DEFAULT clock_timestamp(),
    ALTER COLUMN ended_at SET NOT NULL,
    ALTER COLUMN ended_at SET DEFAULT clock_timestamp();

-- Metadata was JSON stored as TEXT and therefore unfilterable. Every writer
-- used json.Marshal, so the cast is lossless; '' becomes NULL.
ALTER TABLE ${TABLE_PREFIX}llm_calls ALTER COLUMN metadata DROP DEFAULT;
ALTER TABLE ${TABLE_PREFIX}llm_calls ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE ${TABLE_PREFIX}llm_calls ALTER COLUMN metadata TYPE JSONB USING NULLIF(metadata, '')::jsonb;

-- Every trace query is workspace-scoped; workspace_id had no index at all.
CREATE INDEX ${TABLE_PREFIX}llm_calls_workspace_started ON ${TABLE_PREFIX}llm_calls(workspace_id, started_at DESC);
CREATE INDEX ${TABLE_PREFIX}llm_calls_workspace_trace ON ${TABLE_PREFIX}llm_calls(workspace_id, trace_id, started_at);
CREATE INDEX ${TABLE_PREFIX}llm_calls_workspace_session ON ${TABLE_PREFIX}llm_calls(workspace_id, session_id) WHERE session_id <> '';

-- Trace-level attributes that no single observation owns. Aggregates (tokens,
-- cost, duration, counts) are still computed from observations at read time,
-- so they cannot drift from the rows they summarize. Input/output previews are
-- written only while body capture (llm_audit) is on and expire with bodies.
CREATE TABLE ${TABLE_PREFIX}llm_traces (
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL CHECK (trace_id <> ''),
    name TEXT NOT NULL DEFAULT '',
    end_user TEXT NOT NULL DEFAULT '',
    tags TEXT[] NOT NULL DEFAULT '{}',
    input TEXT NOT NULL DEFAULT '',
    output TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, trace_id)
);
CREATE INDEX ${TABLE_PREFIX}llm_traces_tags ON ${TABLE_PREFIX}llm_traces USING GIN (tags);
CREATE INDEX ${TABLE_PREFIX}llm_traces_updated ON ${TABLE_PREFIX}llm_traces(updated_at);

-- Quality scores on a trace or on one of its observations: manual
-- annotations from the UI or values posted by a client through the gateway.
CREATE TABLE ${TABLE_PREFIX}trace_scores (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL CHECK (trace_id <> ''),
    observation_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL CHECK (name <> ''),
    data_type TEXT NOT NULL CHECK (data_type IN ('numeric', 'boolean', 'categorical')),
    value DOUBLE PRECISION,
    string_value TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('annotation', 'api')),
    comment TEXT NOT NULL DEFAULT '',
    author_user_id TEXT NOT NULL DEFAULT '',
    token_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ${TABLE_PREFIX}trace_scores_trace ON ${TABLE_PREFIX}trace_scores(workspace_id, trace_id);
CREATE INDEX ${TABLE_PREFIX}trace_scores_name ON ${TABLE_PREFIX}trace_scores(workspace_id, name, created_at DESC);

-- Per-account bookmarks.
CREATE TABLE ${TABLE_PREFIX}trace_bookmarks (
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL CHECK (trace_id <> ''),
    user_id TEXT NOT NULL CHECK (user_id <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, user_id, trace_id)
);
