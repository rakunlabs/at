-- Add bot config fields that the BotConfig struct & UI already supported but
-- were not persisted: per-user container isolation knobs, per-bot allowed
-- agent allowlist, and speech-to-text settings (incl. "none" to disable).
--
-- SQLite's ADD COLUMN does not support IF NOT EXISTS in older versions; the
-- runner skips errors for already-applied migrations via the migrations table,
-- so this file is idempotent at the migration-runner level.
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN allowed_agent_ids TEXT NOT NULL DEFAULT '[]';
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN user_containers INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN container_image TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN container_cpu TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN container_memory TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN speech_to_text TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN whisper_model TEXT NOT NULL DEFAULT '';
