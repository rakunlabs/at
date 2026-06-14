-- Skill sharing / provenance metadata: version, author, license and
-- import source tracking (URL + SHA-256 checksum of the source payload).
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS version TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS license TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS source_checksum TEXT NOT NULL DEFAULT '';
