-- Media is selected through a workspace-admitted route. Persist that scope so
-- one account using multiple workspaces cannot read an attachment from another
-- workspace merely by retaining its object ID. Existing objects predate
-- workspace scoping and belong to the legacy default workspace.
ALTER TABLE ${TABLE_PREFIX}media_objects
    ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'legacy-default'
        REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE;

-- Share copies have an unambiguous historical workspace through their share.
-- Ordinary old Playground media has no persisted workspace provenance, so it
-- intentionally remains in legacy-default rather than being guessed.
UPDATE ${TABLE_PREFIX}media_objects AS media
   SET workspace_id = share.workspace_id
  FROM ${TABLE_PREFIX}chat_share_media AS link
  JOIN ${TABLE_PREFIX}chat_shares AS share ON share.id = link.share_id
 WHERE media.id = link.media_object_id;

DROP INDEX IF EXISTS ${TABLE_PREFIX}media_objects_owner_user_id_created_at_idx;
CREATE INDEX ${TABLE_PREFIX}media_objects_workspace_owner_created
    ON ${TABLE_PREFIX}media_objects(workspace_id, owner_user_id, created_at DESC);
