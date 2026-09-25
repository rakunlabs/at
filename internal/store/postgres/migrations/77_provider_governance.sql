-- Provider budgets are deliberately separate from provider credentials. A
-- policy can be changed without decrypting or rewriting the provider config.
CREATE TABLE ${TABLE_PREFIX}provider_budget_policies (
    id TEXT PRIMARY KEY,
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('provider', 'virtual_provider')),
    resource_id TEXT NOT NULL,
    total_limit_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (total_limit_cents >= 0),
    default_user_limit_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (default_user_limit_cents >= 0),
    budget_period TEXT NOT NULL DEFAULT 'monthly' CHECK (budget_period IN ('daily', 'weekly', 'monthly')),
    budget_reset_day INTEGER NOT NULL DEFAULT 1,
    budget_reset_time TEXT NOT NULL DEFAULT '00:00',
    budget_timezone TEXT NOT NULL DEFAULT 'UTC',
    enforce_unpriced BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    UNIQUE (resource_kind, resource_id)
);

CREATE TABLE ${TABLE_PREFIX}provider_budget_overrides (
    policy_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}provider_budget_policies(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}auth_users(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('custom', 'unlimited', 'blocked')),
    limit_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (limit_cents >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (policy_id, user_id)
);

CREATE TABLE ${TABLE_PREFIX}provider_budget_usage (
    policy_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}provider_budget_policies(id) ON DELETE CASCADE,
    period_start TIMESTAMPTZ NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    spent_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (spent_cents >= 0),
    reserved_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (reserved_cents >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (policy_id, period_start, user_id)
);

CREATE TABLE ${TABLE_PREFIX}provider_budget_reservations (
    reservation_id TEXT NOT NULL,
    policy_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}provider_budget_policies(id) ON DELETE CASCADE,
    period_start TIMESTAMPTZ NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    reserved_cents DOUBLE PRECISION NOT NULL CHECK (reserved_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (reservation_id, policy_id)
);

CREATE INDEX ${TABLE_PREFIX}provider_budget_reservations_created
    ON ${TABLE_PREFIX}provider_budget_reservations(created_at);

-- A virtual provider is a deterministic, credential-free catalogue. Every
-- public model alias maps to exactly one real provider/model target.
CREATE TABLE ${TABLE_PREFIX}virtual_providers (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, key)
);

CREATE TABLE ${TABLE_PREFIX}virtual_provider_models (
    virtual_provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}virtual_providers(id) ON DELETE CASCADE,
    alias TEXT NOT NULL,
    provider_ref TEXT NOT NULL,
    model TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (virtual_provider_id, alias)
);

CREATE TABLE ${TABLE_PREFIX}virtual_provider_grants (
    id TEXT PRIMARY KEY,
    virtual_provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}virtual_providers(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id) ON DELETE CASCADE,
    model_patterns JSONB NOT NULL DEFAULT '["*"]'::jsonb,
    allow_user_overrides BOOLEAN NOT NULL DEFAULT FALSE,
    max_user_limit_cents DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (max_user_limit_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',
    UNIQUE (virtual_provider_id, workspace_id)
);

CREATE INDEX ${TABLE_PREFIX}virtual_provider_grants_workspace
    ON ${TABLE_PREFIX}virtual_provider_grants(workspace_id);

CREATE RULE ${TABLE_PREFIX}providers_delete_budget AS
ON DELETE TO ${TABLE_PREFIX}providers DO ALSO
DELETE FROM ${TABLE_PREFIX}provider_budget_policies
WHERE resource_kind = 'provider' AND resource_id = OLD.id;

CREATE RULE ${TABLE_PREFIX}virtual_providers_delete_budget AS
ON DELETE TO ${TABLE_PREFIX}virtual_providers DO ALSO
DELETE FROM ${TABLE_PREFIX}provider_budget_policies
WHERE resource_kind = 'virtual_provider' AND resource_id = OLD.id;
