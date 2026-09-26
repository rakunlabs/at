-- Durable workspace files share the installation Storage backend with Chat
-- media while retaining their own authorization and mutable path semantics.
ALTER TABLE ${TABLE_PREFIX}media_settings RENAME TO ${TABLE_PREFIX}storage_settings;

CREATE TABLE ${TABLE_PREFIX}storage_objects (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL CHECK (namespace <> ''),
    path TEXT NOT NULL CHECK (path <> ''),
    backend TEXT NOT NULL CHECK (backend IN ('filesystem', 's3')),
    storage_key TEXT NOT NULL CHECK (storage_key <> ''),
    content_type TEXT NOT NULL CHECK (content_type <> ''),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    checksum TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, namespace, path)
);

CREATE INDEX ${TABLE_PREFIX}storage_objects_workspace_namespace_path
    ON ${TABLE_PREFIX}storage_objects(workspace_id, namespace, path);

-- Chat attachments are objects in the same catalog. Preserve their opaque IDs
-- because transcripts and share manifests address them directly.
INSERT INTO ${TABLE_PREFIX}storage_objects (
    id, workspace_id, owner_user_id, namespace, path, backend, storage_key,
    content_type, size_bytes, checksum, created_at, updated_at
)
SELECT id, workspace_id, owner_user_id, 'media', id, backend, storage_key,
       content_type, size_bytes, checksum, created_at, created_at
  FROM ${TABLE_PREFIX}media_objects;

ALTER TABLE ${TABLE_PREFIX}chat_share_media
    DROP CONSTRAINT ${TABLE_PREFIX}chat_share_media_media_object_id_fkey;
ALTER TABLE ${TABLE_PREFIX}chat_share_media
    ADD CONSTRAINT ${TABLE_PREFIX}chat_share_media_media_object_id_fkey
    FOREIGN KEY (media_object_id) REFERENCES ${TABLE_PREFIX}storage_objects(id) ON DELETE CASCADE;

CREATE OR REPLACE FUNCTION ${TABLE_PREFIX}delete_chat_share_media_object() RETURNS trigger
LANGUAGE plpgsql AS '
BEGIN
    DELETE FROM ${TABLE_PREFIX}storage_objects WHERE id = OLD.media_object_id AND namespace = ''media'';
    RETURN OLD;
END;
';

DROP TABLE ${TABLE_PREFIX}media_objects;
