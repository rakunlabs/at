-- A mapping that admits somebody needs no bundle. The role it grants is already
-- a grant source, so requiring a bundle forced an empty placeholder to be
-- created for no reason other than satisfying the composite foreign key.
--
-- NULL rather than '': the key is (workspace_id, permission_id), and a composite
-- foreign key is relaxed only when one of its members is NULL (MATCH SIMPLE).
-- An empty string would have to reference a bundle row whose id is ''.
ALTER TABLE ${TABLE_PREFIX}workspace_permission_mappings ALTER COLUMN permission_id DROP NOT NULL;

-- UNIQUE treats NULLs as distinct, so the existing constraint stops preventing
-- duplicate rows once permission_id is NULL. NULLS NOT DISTINCT would need
-- PostgreSQL 15, so the bundle-less rows get their own partial index instead.
CREATE UNIQUE INDEX IF NOT EXISTS ${TABLE_PREFIX}workspace_permission_mappings_admit_only
    ON ${TABLE_PREFIX}workspace_permission_mappings(workspace_id, provider_id, claim_kind, claim_value)
    WHERE permission_id IS NULL;

-- A mapping with neither a bundle nor an admission role matches claims and then
-- does nothing, which is indistinguishable from a misconfiguration.
ALTER TABLE ${TABLE_PREFIX}workspace_permission_mappings
    ADD CONSTRAINT ${TABLE_PREFIX}workspace_permission_mappings_effect
    CHECK (permission_id IS NOT NULL OR admit_role <> '');
