CREATE TABLE ${TABLE_PREFIX}personal_conversations (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL CHECK (owner_user_id <> ''),
    title TEXT NOT NULL,
    provider_key TEXT NOT NULL,
    model TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}personal_conversations(owner_user_id, id);
CREATE TABLE ${TABLE_PREFIX}personal_messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}personal_conversations(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'streaming', 'completed', 'failed', 'cancelled')),
    data JSONB NOT NULL,
    lease_token TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
    deadline TIMESTAMPTZ NOT NULL DEFAULT 'epoch',
    UNIQUE (conversation_id, request_id, role)
);
CREATE INDEX ON ${TABLE_PREFIX}personal_messages(conversation_id, id);
CREATE UNIQUE INDEX ON ${TABLE_PREFIX}personal_messages(conversation_id) WHERE status IN ('pending', 'streaming');
