ALTER TABLE skills
    ADD COLUMN IF NOT EXISTS execution_context TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS execution_agent TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS execution_background BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE skills
    DROP CONSTRAINT IF EXISTS skills_execution_context_check;

ALTER TABLE skills
    ADD CONSTRAINT skills_execution_context_check
    CHECK (
        (execution_context = '' AND execution_agent = '' AND execution_background = FALSE)
        OR
        (execution_context = 'fork' AND btrim(execution_agent) <> '')
    );
