-- Media produced through the gateway (for example generate_image called over
-- MCP by OpenCode) records the API token that produced it, so that token — and
-- only that token — can download it from /gateway/v1/media/{id}. Empty for
-- every object created by a browser session or agent run.
ALTER TABLE ${TABLE_PREFIX}storage_objects ADD COLUMN token_id TEXT NOT NULL DEFAULT '';
CREATE INDEX ${TABLE_PREFIX}storage_objects_token ON ${TABLE_PREFIX}storage_objects(token_id) WHERE token_id <> '';
