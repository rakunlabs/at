-- Installation-wide limits for ephemeral subagent execution.
CREATE TABLE ${TABLE_PREFIX}agent_runtime_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    version BIGINT NOT NULL CHECK(version > 0),
    max_background_subagents_per_owner INTEGER NOT NULL CHECK(max_background_subagents_per_owner BETWEEN 1 AND 128)
);
