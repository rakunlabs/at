-- Skill sharing / provenance metadata: version, author, license and
-- import source tracking (URL + SHA-256 checksum of the source payload).
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN author TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN license TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN source_checksum TEXT NOT NULL DEFAULT '';
