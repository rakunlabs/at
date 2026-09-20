-- Keep the JIT account ceiling independent of mutable/display usernames and
-- of identity links, which may be removed during account recovery.
ALTER TABLE ${TABLE_PREFIX}auth_users
    ADD COLUMN externally_provisioned BOOLEAN NOT NULL DEFAULT FALSE;

-- Preserve the population counted by the old admission predicate.
UPDATE ${TABLE_PREFIX}auth_users
SET externally_provisioned = TRUE
WHERE username LIKE 'external-%';
