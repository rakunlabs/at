ALTER TABLE ${TABLE_PREFIX}routing_profiles ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ON ${TABLE_PREFIX}routing_profiles(workspace_id, owner_user_id);
