-- Shared configurable budget reset schedules and period-filtered cost lookup indexes.
ALTER TABLE ${TABLE_PREFIX}organizations
    ADD COLUMN IF NOT EXISTS budget_period TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_reset_day INTEGER DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_reset_time TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_timezone TEXT DEFAULT NULL;

ALTER TABLE ${TABLE_PREFIX}agent_budgets
    ADD COLUMN IF NOT EXISTS budget_period TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_reset_day INTEGER DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_reset_time TEXT DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS budget_timezone TEXT DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_agent_created_at
    ON ${TABLE_PREFIX}cost_events(agent_id, created_at);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}cost_events_organization_created_at
    ON ${TABLE_PREFIX}cost_events(organization_id, created_at);
