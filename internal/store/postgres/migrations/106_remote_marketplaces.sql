-- Marketplace sources become remote Claude Code plugin marketplaces: a direct
-- marketplace.json URL (type 'url') or a git repository (type 'git'; GitHub
-- owner/repo shorthand is expanded on write). Legacy 'generic' search sources
-- are kept as rows but are no longer browsable.
ALTER TABLE ${TABLE_PREFIX}marketplace_sources ADD COLUMN IF NOT EXISTS url TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}marketplace_sources ADD COLUMN IF NOT EXISTS ref TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}marketplace_sources ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}marketplace_sources ADD COLUMN IF NOT EXISTS credential_id TEXT NOT NULL DEFAULT '';
