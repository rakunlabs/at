ALTER TABLE ${TABLE_PREFIX}playground_messages ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX ${TABLE_PREFIX}playground_messages_client_id_unique
    ON ${TABLE_PREFIX}playground_messages (conversation_id, client_id) WHERE client_id <> '';
