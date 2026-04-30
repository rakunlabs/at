-- Add user-configurable slash commands to bot configs.
-- Stored as a JSON array of BotCustomCommand objects.
ALTER TABLE ${TABLE_PREFIX}bot_configs
    ADD COLUMN IF NOT EXISTS custom_commands JSONB NOT NULL DEFAULT '[]';
