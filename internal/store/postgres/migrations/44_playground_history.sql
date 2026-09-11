-- The personal chat feature is removed, data included. Drop the child first so
-- the foreign key does not block the parent.
DROP TABLE IF EXISTS ${TABLE_PREFIX}personal_messages;
DROP TABLE IF EXISTS ${TABLE_PREFIX}personal_conversations;

-- "privatechat.read"/"privatechat.write" left the capability registry with the
-- feature. Unknown capabilities are inert (AccessCapabilities is finite), but
-- stale entries would still be echoed back by the permissions API, so remove
-- them from every place a capability key is persisted: the denied-capability
-- table, and the three capability-keyed shapes inside the bundle JSONB
-- (keys[] array, key_patterns{} object, resource_ids{} object).
DELETE FROM ${TABLE_PREFIX}workspace_user_denied WHERE capability LIKE 'privatechat.%';

UPDATE ${TABLE_PREFIX}workspace_permissions
SET bundle = jsonb_set(bundle, '{keys}', COALESCE((
        SELECT jsonb_agg(entry)
        FROM jsonb_array_elements(bundle -> 'keys') AS entry
        WHERE entry #>> '{}' NOT LIKE 'privatechat.%'
    ), '[]'::jsonb))
WHERE jsonb_typeof(bundle -> 'keys') = 'array'
  AND EXISTS (
        SELECT 1 FROM jsonb_array_elements_text(bundle -> 'keys') AS entry
        WHERE entry LIKE 'privatechat.%'
    );

UPDATE ${TABLE_PREFIX}workspace_permissions
SET bundle = jsonb_set(bundle, '{key_patterns}', COALESCE((
        SELECT jsonb_object_agg(entry.key, entry.value)
        FROM jsonb_each(bundle -> 'key_patterns') AS entry
        WHERE entry.key NOT LIKE 'privatechat.%'
    ), '{}'::jsonb))
WHERE jsonb_typeof(bundle -> 'key_patterns') = 'object'
  AND EXISTS (
        SELECT 1 FROM jsonb_object_keys(bundle -> 'key_patterns') AS name
        WHERE name LIKE 'privatechat.%'
    );

UPDATE ${TABLE_PREFIX}workspace_permissions
SET bundle = jsonb_set(bundle, '{resource_ids}', COALESCE((
        SELECT jsonb_object_agg(entry.key, entry.value)
        FROM jsonb_each(bundle -> 'resource_ids') AS entry
        WHERE entry.key NOT LIKE 'privatechat.%'
    ), '{}'::jsonb))
WHERE jsonb_typeof(bundle -> 'resource_ids') = 'object'
  AND EXISTS (
        SELECT 1 FROM jsonb_object_keys(bundle -> 'resource_ids') AS name
        WHERE name LIKE 'privatechat.%'
    );

-- Playground history is a per-user private record of the Chat playground.
-- It is deliberately owner-scoped only: no workspace_id, no sharing, no
-- workspace capability. Every read and write filters on owner_user_id.
CREATE TABLE ${TABLE_PREFIX}playground_conversations (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL CHECK (owner_user_id <> ''),
    title TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    provider_key TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    -- Fork lineage: which conversation this one branched off, and at which
    -- message sequence. NULL for a conversation started from scratch. The
    -- self reference is ON DELETE SET NULL, never CASCADE: a fork is an
    -- independent conversation that must outlive the branch it came from,
    -- losing only the pointer back to it.
    forked_from_id TEXT REFERENCES ${TABLE_PREFIX}playground_conversations(id) ON DELETE SET NULL,
    forked_from_sequence BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}playground_conversations(owner_user_id, updated_at DESC, id);

CREATE TABLE ${TABLE_PREFIX}playground_messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}playground_conversations(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    provider_key TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- This constraint's backing index is exactly the (conversation_id,
    -- sequence) lookup/ordering index the reads need, so no separate index.
    UNIQUE (conversation_id, sequence)
);
