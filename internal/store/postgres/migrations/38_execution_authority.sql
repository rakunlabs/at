CREATE TABLE ${TABLE_PREFIX}execution_policies (
    workspace_id TEXT PRIMARY KEY,
    version BIGINT NOT NULL,
    data JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE ${TABLE_PREFIX}execution_provenance (
    run_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON ${TABLE_PREFIX}execution_provenance(workspace_id, created_at);

CREATE TABLE ${TABLE_PREFIX}execution_service_bindings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    owner_user_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('bot', 'trigger')),
    subject_id TEXT NOT NULL,
    version BIGINT NOT NULL,
    membership_version BIGINT NOT NULL,
    policy_version BIGINT NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(kind, subject_id)
);
