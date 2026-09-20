-- Preserve historical totals without guessing who initiated old calls.
-- No FK: accounting survives account deletion.
ALTER TABLE ${TABLE_PREFIX}cost_events
    ADD COLUMN user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN source TEXT NOT NULL DEFAULT '';

CREATE INDEX ${TABLE_PREFIX}cost_events_user_created_idx
    ON ${TABLE_PREFIX}cost_events (user_id, created_at);
CREATE INDEX ${TABLE_PREFIX}cost_events_workspace_user_created_idx
    ON ${TABLE_PREFIX}cost_events (workspace_id, user_id, created_at);
