-- Add user-configurable slash commands to bot configs.
-- Stored as a JSON array of BotCustomCommand objects.
ALTER TABLE ${TABLE_PREFIX}bot_configs ADD COLUMN custom_commands TEXT NOT NULL DEFAULT '[]';
