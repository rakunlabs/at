-- Enforce unique workflow names. Dedup existing duplicates before the index.
UPDATE ${TABLE_PREFIX}workflows
SET name = name || '-' || substr(id, 1, 8)
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (PARTITION BY name ORDER BY created_at, id) AS rn
          FROM ${TABLE_PREFIX}workflows
    ) sub
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}workflows_name
    ON ${TABLE_PREFIX}workflows(name);
