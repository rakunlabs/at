ALTER TABLE ${TABLE_PREFIX}rag_collections ADD COLUMN IF NOT EXISTS embedding_url TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}rag_collections ADD COLUMN IF NOT EXISTS embedding_api_type TEXT DEFAULT 'openai';
