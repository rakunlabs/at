-- Routing profiles gain a selection strategy and optional per-target weights.
-- 'priority' is the historical behaviour (configured order), so existing
-- profiles route exactly as before.
ALTER TABLE ${TABLE_PREFIX}routing_profiles ADD COLUMN IF NOT EXISTS strategy TEXT NOT NULL DEFAULT 'priority'
    CHECK (strategy IN ('priority', 'round_robin', 'weighted', 'least_cost', 'sticky'));
ALTER TABLE ${TABLE_PREFIX}routing_profiles ADD COLUMN IF NOT EXISTS weights JSONB NOT NULL DEFAULT '{}'::jsonb;
