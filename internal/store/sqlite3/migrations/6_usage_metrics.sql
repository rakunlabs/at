-- Usage metrics: extend cost_events with latency, status, and error attribution
-- so the usage dashboard can chart requests/errors/latency alongside tokens/cost.
--
-- All columns are additive with safe defaults so existing rows remain valid.
-- SQLite requires one ALTER per column.
ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN latency_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN status TEXT NOT NULL DEFAULT 'ok';
ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN error_message TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_created_at ON ${TABLE_PREFIX}cost_events(created_at);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_provider ON ${TABLE_PREFIX}cost_events(provider);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_model ON ${TABLE_PREFIX}cost_events(model);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_status ON ${TABLE_PREFIX}cost_events(status);
