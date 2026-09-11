CREATE TABLE ${TABLE_PREFIX}auth_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    version BIGINT NOT NULL CHECK(version > 0),
    config JSONB NOT NULL
);

-- Seal old installations with an existing local administrator even when it was
-- created via an older administrative import rather than the bootstrap API.
UPDATE ${TABLE_PREFIX}auth_bootstrap SET claimed = TRUE
WHERE EXISTS (SELECT 1 FROM ${TABLE_PREFIX}auth_users WHERE admin AND password_hash <> '');

-- Keep an external administrator usable whenever local primary login is off.
-- Every relevant mutation serializes on the policy row, including provider
-- edits, unlink, recovery and account disable. A rejected transaction rolls back
-- its credential/session changes too. SQLSTATE 23514 is mapped to auth conflict.
CREATE FUNCTION ${TABLE_PREFIX}auth_settings_primary_guard() RETURNS trigger
LANGUAGE plpgsql AS '
DECLARE policy JSONB;
BEGIN
    SELECT config INTO policy FROM ${TABLE_PREFIX}auth_settings WHERE singleton FOR UPDATE;
    IF policy IS NOT NULL AND NOT (policy->>''local_login_enabled'')::boolean THEN
        IF NOT EXISTS (
            SELECT 1 FROM ${TABLE_PREFIX}auth_users u
            JOIN ${TABLE_PREFIX}auth_identity_links l ON l.user_id = u.id
            JOIN ${TABLE_PREFIX}auth_identity_providers p ON p.id = l.provider_id
            WHERE u.admin AND NOT u.disabled AND p.enabled
        ) THEN
            RAISE EXCEPTION ''local login requires a usable external administrator'' USING ERRCODE = ''23514'';
        END IF;
    END IF;
    RETURN NULL;
END;
';
CREATE CONSTRAINT TRIGGER auth_settings_primary_guard AFTER INSERT OR UPDATE OR DELETE ON ${TABLE_PREFIX}auth_settings
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}auth_settings_primary_guard();
CREATE CONSTRAINT TRIGGER auth_settings_primary_guard AFTER UPDATE OR DELETE ON ${TABLE_PREFIX}auth_users
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}auth_settings_primary_guard();
CREATE CONSTRAINT TRIGGER auth_settings_primary_guard AFTER UPDATE OR DELETE ON ${TABLE_PREFIX}auth_identity_providers
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}auth_settings_primary_guard();
CREATE CONSTRAINT TRIGGER auth_settings_primary_guard AFTER UPDATE OR DELETE ON ${TABLE_PREFIX}auth_identity_links
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}auth_settings_primary_guard();

-- Fence in-flight local password/passkey/MFA completions when policy changes.
-- External provenance is written in the same transaction as its session. Mobile
-- families derive from a verified live browser approval, not a new local primary.
CREATE FUNCTION ${TABLE_PREFIX}auth_settings_session_guard() RETURNS trigger
LANGUAGE plpgsql AS '
DECLARE policy JSONB;
BEGIN
    SELECT config INTO policy FROM ${TABLE_PREFIX}auth_settings WHERE singleton FOR UPDATE;
    IF policy IS NOT NULL AND NEW.transport = ''web''
       AND NOT (policy->>''local_login_enabled'')::boolean
       AND NOT EXISTS (SELECT 1 FROM ${TABLE_PREFIX}auth_external_session_provenance WHERE session_id = NEW.hash) THEN
        RAISE EXCEPTION ''local login is disabled'' USING ERRCODE = ''23514'';
    END IF;
    RETURN NULL;
END;
';
CREATE CONSTRAINT TRIGGER auth_settings_session_guard AFTER INSERT ON ${TABLE_PREFIX}auth_sessions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ${TABLE_PREFIX}auth_settings_session_guard();
