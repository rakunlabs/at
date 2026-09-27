-- Developer Spaces become one persistent space per account and workspace.
-- Projects are plain folders inside the space volume, so the repository and
-- worktree records are removed; sessions remember the folder they work in.
DELETE FROM ${TABLE_PREFIX}developer_spaces s
USING ${TABLE_PREFIX}developer_spaces keep
WHERE s.workspace_id = keep.workspace_id
  AND s.owner_user_id = keep.owner_user_id
  AND (keep.created_at, keep.id) < (s.created_at, s.id);

DROP INDEX IF EXISTS ${TABLE_PREFIX}developer_spaces_owner_name;
CREATE UNIQUE INDEX ${TABLE_PREFIX}developer_spaces_owner
    ON ${TABLE_PREFIX}developer_spaces(workspace_id, owner_user_id);

ALTER TABLE ${TABLE_PREFIX}developer_sessions
    ADD COLUMN project_path TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS ${TABLE_PREFIX}developer_sessions_worktree;
ALTER TABLE ${TABLE_PREFIX}developer_sessions DROP COLUMN worktree_id;
ALTER TABLE ${TABLE_PREFIX}developer_sessions DROP COLUMN repository_id;

DROP TABLE ${TABLE_PREFIX}developer_worktrees;
DROP TABLE ${TABLE_PREFIX}developer_repositories;
