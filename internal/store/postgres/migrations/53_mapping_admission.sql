-- Claim-driven workspace admission. Empty keeps the previous behaviour, where a
-- mapping grants capabilities to an existing member and never creates one.
-- 'owner' is excluded at the schema level: sole ownership is not something a
-- provider claim may assert, and a revoked membership is never resurrected.
ALTER TABLE ${TABLE_PREFIX}workspace_permission_mappings
    ADD COLUMN admit_role TEXT NOT NULL DEFAULT ''
    CHECK (admit_role IN ('', 'viewer', 'member', 'admin'));
