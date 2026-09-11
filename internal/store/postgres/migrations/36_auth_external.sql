CREATE TABLE ${TABLE_PREFIX}auth_identity_providers (
    id TEXT PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT false,
    config JSONB NOT NULL,
    secret TEXT NOT NULL DEFAULT ''
);
CREATE TABLE ${TABLE_PREFIX}auth_identity_links (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_identity_providers(id),
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 1024),
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    email TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT false,
    asserted_permissions JSONB NOT NULL DEFAULT '{}',
    UNIQUE(provider_id, issuer, subject)
);
CREATE INDEX ${TABLE_PREFIX}auth_identity_links_user ON ${TABLE_PREFIX}auth_identity_links(user_id);
CREATE TABLE ${TABLE_PREFIX}auth_external_flows (
    hash TEXT PRIMARY KEY,
    user_id TEXT REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_identity_providers(id),
    provider_version BIGINT NOT NULL,
    source TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX ${TABLE_PREFIX}auth_external_flows_expiry ON ${TABLE_PREFIX}auth_external_flows(expires_at);
-- Serializes bounded flow/JIT admission across replicas; no advisory hash collisions.
CREATE TABLE ${TABLE_PREFIX}auth_external_admission (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton)
);
INSERT INTO ${TABLE_PREFIX}auth_external_admission DEFAULT VALUES;

CREATE TABLE ${TABLE_PREFIX}auth_external_sources (
    hash TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE ${TABLE_PREFIX}auth_external_session_provenance (
    session_id TEXT PRIMARY KEY REFERENCES ${TABLE_PREFIX}auth_sessions(hash) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_identity_providers(id),
    provider_version BIGINT NOT NULL,
    link_id TEXT NOT NULL
);
