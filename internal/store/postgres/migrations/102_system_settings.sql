-- Installation-owned settings. No credential content is stored here:
-- kubeconfig is an operator-provisioned host path.
CREATE TABLE ${TABLE_PREFIX}system_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    version BIGINT NOT NULL CHECK(version > 0),
    config JSONB NOT NULL
);
