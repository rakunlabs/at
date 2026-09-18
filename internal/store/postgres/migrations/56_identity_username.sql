-- The username an external provider reports for an identity (OIDC
-- `preferred_username`, falling back to the strategy's resolved name). An
-- externally provisioned account is named `external-<ulid>` locally, which is
-- the account ID again and identifies nobody; the provider's own username is
-- the string an administrator recognises and types into a search box.
--
-- It lives on the link rather than on `auth_users.username`, which is the
-- unique key both local and external accounts sign in with: rewriting that
-- column would collide the first time two providers report the same handle.
-- This value is display metadata, refreshed from every sign-in, and is never an
-- identity — the account stays keyed on provider ID plus subject.
ALTER TABLE ${TABLE_PREFIX}auth_identity_links
    ADD COLUMN username TEXT NOT NULL DEFAULT '';
