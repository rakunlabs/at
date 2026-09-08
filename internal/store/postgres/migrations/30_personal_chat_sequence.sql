ALTER TABLE ${TABLE_PREFIX}personal_messages ADD COLUMN sequence BIGINT;

-- Historical admission order was not recorded. Preserve the previously exposed
-- ID order for existing rows; all future admissions allocate under the parent lock.
WITH ordered AS (
    SELECT id, row_number() OVER (PARTITION BY conversation_id ORDER BY id) AS sequence
    FROM ${TABLE_PREFIX}personal_messages
)
UPDATE ${TABLE_PREFIX}personal_messages AS m
SET sequence = ordered.sequence,
    data = m.data || jsonb_build_object('sequence', ordered.sequence)
FROM ordered WHERE m.id = ordered.id;

ALTER TABLE ${TABLE_PREFIX}personal_messages ALTER COLUMN sequence SET NOT NULL;
ALTER TABLE ${TABLE_PREFIX}personal_messages ADD CHECK (sequence > 0);
CREATE UNIQUE INDEX ON ${TABLE_PREFIX}personal_messages(conversation_id, sequence);
