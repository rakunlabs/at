-- The person's display name as the external provider reports it (OIDC `name`,
-- or `given_name` + `family_name`). `username` holds the handle; this is the
-- human name shown next to it and reported to agents by `whoami`.
--
-- Display metadata only, refreshed from every sign-in like `username` and
-- `email`; the link stays keyed on provider ID plus subject.
ALTER TABLE ${TABLE_PREFIX}auth_identity_links
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
