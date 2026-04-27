-- Add per-task max_iterations override. 0 means "use the agent's default
-- max_iterations". Existing tasks default to 0 so behaviour is unchanged
-- for them.
--
-- SQLite's ADD COLUMN does not support IF NOT EXISTS in older versions; the
-- migration runner skips errors for already-applied migrations via the
-- migrations table, so this file is idempotent at the runner level.
ALTER TABLE ${TABLE_PREFIX}tasks ADD COLUMN max_iterations INTEGER NOT NULL DEFAULT 0;
