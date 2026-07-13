-- Unified trace/observation model: llm_calls rows become observations
-- (generation | tool | event) nested via parent_observation_id. The
-- audit_log table is replaced by observations and dropped.
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS observation_type TEXT NOT NULL DEFAULT 'generation';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS parent_observation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS input TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS output TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS level TEXT NOT NULL DEFAULT 'default';
ALTER TABLE ${TABLE_PREFIX}llm_calls ADD COLUMN IF NOT EXISTS metadata TEXT NOT NULL DEFAULT '';

UPDATE ${TABLE_PREFIX}llm_calls SET observation_type = 'generation' WHERE observation_type = '';

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_task_id ON ${TABLE_PREFIX}llm_calls(task_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_parent_observation_id ON ${TABLE_PREFIX}llm_calls(parent_observation_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}llm_calls_observation_type ON ${TABLE_PREFIX}llm_calls(observation_type);

DROP TABLE IF EXISTS ${TABLE_PREFIX}audit_log;
