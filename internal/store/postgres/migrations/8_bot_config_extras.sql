-- Add bot config fields that the BotConfig struct & UI already supported but
-- were not persisted: per-user container isolation knobs, per-bot allowed
-- agent allowlist, and speech-to-text settings (incl. "none" to disable).
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS allowed_agent_ids JSONB NOT NULL DEFAULT '[]';
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS user_containers BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS container_cpu TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS container_memory TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS speech_to_text TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS whisper_model TEXT NOT NULL DEFAULT '';
