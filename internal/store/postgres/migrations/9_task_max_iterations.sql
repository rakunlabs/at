-- Add per-task max_iterations override. 0 means "use the agent's default
-- max_iterations". Existing tasks default to 0 so behaviour is unchanged
-- for them.
ALTER TABLE ${TABLE_PREFIX}tasks ADD COLUMN IF NOT EXISTS max_iterations INTEGER NOT NULL DEFAULT 0;
