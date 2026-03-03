-- Consolidate agents table: move config columns into a single JSONB "config" column.
ALTER TABLE ${TABLE_PREFIX}agents ADD COLUMN IF NOT EXISTS config JSONB;

UPDATE ${TABLE_PREFIX}agents SET config = jsonb_build_object(
    'description', COALESCE(description, ''),
    'provider', COALESCE(provider, ''),
    'model', COALESCE(model, ''),
    'system_prompt', COALESCE(system_prompt, ''),
    'skills', COALESCE(skills, '[]'::jsonb),
    'mcp_urls', COALESCE(mcp_urls, '[]'::jsonb),
    'max_iterations', COALESCE(max_iterations, 10),
    'tool_timeout', COALESCE(tool_timeout, 60)
);

ALTER TABLE ${TABLE_PREFIX}agents ALTER COLUMN config SET NOT NULL;
ALTER TABLE ${TABLE_PREFIX}agents ALTER COLUMN config SET DEFAULT '{}'::jsonb;

ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS description;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS provider;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS model;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS system_prompt;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS skills;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS mcp_urls;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS max_iterations;
ALTER TABLE ${TABLE_PREFIX}agents DROP COLUMN IF EXISTS tool_timeout;

-- Consolidate rag_collections table: move config columns into a single JSONB "config" column.
ALTER TABLE ${TABLE_PREFIX}rag_collections ADD COLUMN IF NOT EXISTS config JSONB;

UPDATE ${TABLE_PREFIX}rag_collections SET config = jsonb_build_object(
    'description', COALESCE(description, ''),
    'vector_store', COALESCE(vector_store_config, '{}'::jsonb),
    'embedding_provider', COALESCE(embedding_provider, ''),
    'embedding_model', COALESCE(embedding_model, ''),
    'embedding_url', COALESCE(embedding_url, ''),
    'embedding_api_type', COALESCE(embedding_api_type, 'openai'),
    'embedding_bearer_auth', false,
    'chunk_size', COALESCE(chunk_size, 1000),
    'chunk_overlap', COALESCE(chunk_overlap, 200)
);

ALTER TABLE ${TABLE_PREFIX}rag_collections ALTER COLUMN config SET NOT NULL;
ALTER TABLE ${TABLE_PREFIX}rag_collections ALTER COLUMN config SET DEFAULT '{}'::jsonb;

ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS description;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS vector_store_config;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS embedding_provider;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS embedding_url;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS embedding_api_type;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS chunk_size;
ALTER TABLE ${TABLE_PREFIX}rag_collections DROP COLUMN IF EXISTS chunk_overlap;
