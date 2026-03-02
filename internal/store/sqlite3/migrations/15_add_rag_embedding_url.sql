ALTER TABLE ${TABLE_PREFIX}rag_collections ADD COLUMN embedding_url TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}rag_collections ADD COLUMN embedding_api_type TEXT DEFAULT 'openai';
