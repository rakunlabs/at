CREATE TABLE ${TABLE_PREFIX}chat_shares (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    source_conversation_id TEXT REFERENCES ${TABLE_PREFIX}playground_conversations(id) ON DELETE SET NULL,
    source_owner_user_id TEXT NOT NULL CHECK (source_owner_user_id <> ''),
    through_sequence BIGINT NOT NULL CHECK (through_sequence > 0),
    current_version BIGINT NOT NULL CHECK (current_version > 0),
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX ${TABLE_PREFIX}chat_shares_source_workspace
    ON ${TABLE_PREFIX}chat_shares(source_conversation_id, workspace_id)
    WHERE source_conversation_id IS NOT NULL;
CREATE INDEX ${TABLE_PREFIX}chat_shares_workspace_updated
    ON ${TABLE_PREFIX}chat_shares(workspace_id, updated_at DESC, id);

CREATE TABLE ${TABLE_PREFIX}chat_share_versions (
    share_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}chat_shares(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    payload JSONB NOT NULL,
    options JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (share_id, version)
);

ALTER TABLE ${TABLE_PREFIX}playground_conversations
    ADD COLUMN imported_from_share_id TEXT REFERENCES ${TABLE_PREFIX}chat_shares(id) ON DELETE SET NULL,
    ADD COLUMN imported_from_share_version BIGINT,
    ADD CONSTRAINT playground_import_provenance_pair CHECK (
        (imported_from_share_id IS NULL AND imported_from_share_version IS NULL) OR
        (imported_from_share_id IS NOT NULL AND imported_from_share_version > 0)
    );

CREATE TABLE ${TABLE_PREFIX}chat_share_media (
    share_id TEXT NOT NULL,
    version BIGINT NOT NULL,
    media_object_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}media_objects(id) ON DELETE CASCADE,
    PRIMARY KEY (share_id, version, media_object_id),
    FOREIGN KEY (share_id, version) REFERENCES ${TABLE_PREFIX}chat_share_versions(share_id, version) ON DELETE CASCADE
);

CREATE FUNCTION ${TABLE_PREFIX}revoke_chat_shares_on_source_delete() RETURNS trigger
LANGUAGE plpgsql AS '
BEGIN
    UPDATE ${TABLE_PREFIX}chat_shares
       SET revoked_at = COALESCE(revoked_at, clock_timestamp()),
           updated_at = clock_timestamp()
     WHERE source_conversation_id = OLD.id;
    DELETE FROM ${TABLE_PREFIX}chat_share_media
     WHERE share_id IN (SELECT id FROM ${TABLE_PREFIX}chat_shares WHERE source_conversation_id = OLD.id);
    RETURN OLD;
END;
';

CREATE TRIGGER ${TABLE_PREFIX}chat_share_source_delete
BEFORE DELETE ON ${TABLE_PREFIX}playground_conversations
FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}revoke_chat_shares_on_source_delete();

CREATE FUNCTION ${TABLE_PREFIX}clear_chat_share_import_provenance() RETURNS trigger
LANGUAGE plpgsql AS '
BEGIN
    UPDATE ${TABLE_PREFIX}playground_conversations
       SET imported_from_share_id = NULL, imported_from_share_version = NULL
     WHERE imported_from_share_id = OLD.id;
    RETURN OLD;
END;
';
CREATE TRIGGER ${TABLE_PREFIX}chat_share_delete
BEFORE DELETE ON ${TABLE_PREFIX}chat_shares
FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}clear_chat_share_import_provenance();

CREATE FUNCTION ${TABLE_PREFIX}delete_chat_share_media_object() RETURNS trigger
LANGUAGE plpgsql AS '
BEGIN
    DELETE FROM ${TABLE_PREFIX}media_objects WHERE id = OLD.media_object_id;
    RETURN OLD;
END;
';
CREATE TRIGGER ${TABLE_PREFIX}chat_share_media_delete
AFTER DELETE ON ${TABLE_PREFIX}chat_share_media
FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}delete_chat_share_media_object();
