-- Agent gateway: API tokens that may run agents on behalf of people proven
-- by an identity provider token.
ALTER TABLE ${TABLE_PREFIX}tokens
    ADD COLUMN act_for_users BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN allowed_agents_mode TEXT NOT NULL DEFAULT '',
    ADD COLUMN allowed_agents JSONB;
