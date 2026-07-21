-- RAG removal + gateway MCP scoping persistence.
--
-- 1. The RAG engine has been removed from AT; drop its tables.
-- 2. The former in-memory-only allowed_rag_mcps token restriction has been
--    renamed to allowed_mcps (it scopes ALL gateway MCP endpoints) and is now
--    persisted alongside the other allowed_* fields.

DROP TABLE IF EXISTS ${TABLE_PREFIX}rag_pages;
DROP TABLE IF EXISTS ${TABLE_PREFIX}rag_states;
DROP TABLE IF EXISTS ${TABLE_PREFIX}rag_collections;

ALTER TABLE ${TABLE_PREFIX}tokens
    ADD COLUMN IF NOT EXISTS allowed_mcps JSONB DEFAULT NULL;
ALTER TABLE ${TABLE_PREFIX}tokens
    ADD COLUMN IF NOT EXISTS allowed_mcps_mode TEXT NOT NULL DEFAULT '';
