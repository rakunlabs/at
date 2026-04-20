-- Enforce unique workflow names so agents can reference workflows by name
-- (same as skills and MCP sets). Existing duplicates are dedup'd by suffixing
-- the offending rows with a short id fragment so the UNIQUE index can apply.
--
-- Runs in three phases:
--   1. Detect duplicates and rename all-but-one in each group
--   2. Create the UNIQUE index
--
-- SQLite does not support procedural SQL, so we use a CTE + UPDATE pattern
-- that works for this shape.
UPDATE ${TABLE_PREFIX}workflows
SET name = name || '-' || substr(id, 1, 8)
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (PARTITION BY name ORDER BY created_at, id) AS rn
          FROM ${TABLE_PREFIX}workflows
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}workflows_name
    ON ${TABLE_PREFIX}workflows(name);
