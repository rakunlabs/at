CREATE TABLE ${TABLE_PREFIX}provider_quota_usage (
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}providers(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    period TEXT NOT NULL CHECK (period IN ('day', 'month')),
    window_start TIMESTAMPTZ NOT NULL,
    requests BIGINT NOT NULL DEFAULT 0,
    tokens BIGINT NOT NULL DEFAULT 0,
    reserved BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, model, period)
);
-- Reservations from crashed calls remain charged conservatively until rollover;
-- stale ones are pruned when this provider next receives a request.
CREATE TABLE ${TABLE_PREFIX}provider_quota_reservations (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}providers(id) ON DELETE CASCADE,
    pools JSONB NOT NULL,
    estimate BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
