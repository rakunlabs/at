-- Configurable media (image) storage for Playground attachments.
--
-- media_settings is a singleton policy row in the same shape as
-- auth_settings (37_auth_settings.sql): one row, a monotonic version used for
-- optimistic concurrency, and the whole configuration as one blob. The blob is
-- TEXT rather than JSONB because it is stored ENCRYPTED (AES-256-GCM, "enc:"
-- prefix) whenever the installation has an encryption key: it carries the S3
-- secret access key. Encrypted ciphertext is not valid JSON, so JSONB would
-- reject it, and no SQL predicate may ever need to read inside it.
CREATE TABLE ${TABLE_PREFIX}media_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    version BIGINT NOT NULL CHECK(version > 0),
    config TEXT NOT NULL
);

-- media_objects records one stored blob. It is owner-scoped only: no
-- workspace, no sharing, no workspace capability, exactly like
-- playground_conversations. Every read and write filters on owner_user_id, so
-- a foreign object is indistinguishable from a missing one.
--
-- The blob itself lives in the configured backend under storage_key; this
-- table is the authoritative record of which backend owns it, what content
-- type was sniffed at upload time (never the client's claim) and its sha256.
-- backend is pinned per row because the administrator may switch backends
-- later: already-stored objects must keep resolving against the backend that
-- actually holds them.
CREATE TABLE ${TABLE_PREFIX}media_objects (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL CHECK (owner_user_id <> ''),
    backend TEXT NOT NULL CHECK (backend IN ('filesystem', 's3')),
    storage_key TEXT NOT NULL CHECK (storage_key <> ''),
    content_type TEXT NOT NULL CHECK (content_type <> ''),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    checksum TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}media_objects(owner_user_id, created_at DESC);
