-- Empty ownership preserves existing workspace tokens.
ALTER TABLE ${TABLE_PREFIX}tokens ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ${TABLE_PREFIX}tokens_owner_idx ON ${TABLE_PREFIX}tokens (workspace_id, owner_user_id);
