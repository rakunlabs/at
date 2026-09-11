-- Collapse the task status vocabulary from ten values to seven.
--
-- Three of the ten were pure synonyms with no semantic difference, which left
-- both humans and LLM agents guessing: `open` meant `todo`, `review` meant
-- `in_review`, and `completed` meant `done`. Worse, different creation paths
-- picked different defaults for the same intent (the agent tool wrote `todo`,
-- the REST handler wrote `open`, the column defaulted to `open`), and no tool
-- schema constrained the value, so an agent could write any string at all and
-- the server stored it silently.
--
-- After this migration the column is constrained, so an invalid status fails
-- loudly at the boundary instead of becoming a task nobody's board displays.

UPDATE ${TABLE_PREFIX}tasks SET status = 'todo' WHERE status = 'open';
UPDATE ${TABLE_PREFIX}tasks SET status = 'in_review' WHERE status = 'review';
UPDATE ${TABLE_PREFIX}tasks SET status = 'done' WHERE status = 'completed';

-- Anything outside the vocabulary predates validation and cannot be mapped by
-- meaning. Park it in `todo` rather than dropping the row or failing the
-- migration: the work still exists and a person can re-triage it.
UPDATE ${TABLE_PREFIX}tasks
SET status = 'todo'
WHERE status NOT IN ('backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'cancelled');

-- `done` rows converted from `completed` keep whatever completed_at they had.
-- Rows that reached a terminal status through the non-stamping update path have
-- none; backfill from updated_at so duration reporting and the workspace
-- janitor (which sweeps on the terminal timestamp) stop skipping them.
UPDATE ${TABLE_PREFIX}tasks
SET completed_at = updated_at
WHERE status = 'done' AND completed_at IS NULL;

UPDATE ${TABLE_PREFIX}tasks
SET cancelled_at = updated_at
WHERE status = 'cancelled' AND cancelled_at IS NULL;

ALTER TABLE ${TABLE_PREFIX}tasks ALTER COLUMN status SET DEFAULT 'todo';

ALTER TABLE ${TABLE_PREFIX}tasks
ADD CONSTRAINT ${TABLE_PREFIX}tasks_status_check
CHECK (status IN ('backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'cancelled'));
