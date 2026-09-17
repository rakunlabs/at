-- Discoverable (usernameless) passkey login. A login ceremony now starts before
-- the relying party knows who is signing in: the authenticator picks the
-- credential and the assertion's credential ID identifies the account at finish
-- time. Such a challenge has no user to reference, so the column becomes
-- nullable. Enrollment challenges still carry their user, and the foreign key
-- keeps cascading when an account is deleted.
ALTER TABLE ${TABLE_PREFIX}auth_challenges ALTER COLUMN user_id DROP NOT NULL;
