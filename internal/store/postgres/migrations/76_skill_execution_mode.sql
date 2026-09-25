-- Execution mode is selected by the calling agent for each forked skill run.
-- The former skill-level default is intentionally discarded.
ALTER TABLE ${TABLE_PREFIX}skills
    DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}skills_execution_context_check;

ALTER TABLE ${TABLE_PREFIX}skills
    DROP COLUMN IF EXISTS execution_background;

ALTER TABLE ${TABLE_PREFIX}skills
    ADD CONSTRAINT ${TABLE_PREFIX}skills_execution_context_check
    CHECK (
        (execution_context = '' AND execution_agent = '')
        OR
        (execution_context = 'fork' AND btrim(execution_agent) <> '')
    );
