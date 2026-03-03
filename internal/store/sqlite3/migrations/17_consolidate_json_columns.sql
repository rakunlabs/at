-- Consolidate agents table: move config columns into a single TEXT "config" column (JSON).
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}agents_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

INSERT INTO ${TABLE_PREFIX}agents_new (id, name, config, created_at, updated_at, created_by, updated_by)
SELECT
    id,
    name,
    json_object(
        'description', COALESCE(description, ''),
        'provider', COALESCE(provider, ''),
        'model', COALESCE(model, ''),
        'system_prompt', COALESCE(system_prompt, ''),
        'skills', json(COALESCE(skills, '[]')),
        'mcp_urls', json(COALESCE(mcp_urls, '[]')),
        'max_iterations', COALESCE(max_iterations, 10),
        'tool_timeout', COALESCE(tool_timeout, 60)
    ),
    created_at,
    updated_at,
    created_by,
    updated_by
FROM ${TABLE_PREFIX}agents;

DROP TABLE IF EXISTS ${TABLE_PREFIX}agents;
ALTER TABLE ${TABLE_PREFIX}agents_new RENAME TO ${TABLE_PREFIX}agents;

-- Consolidate rag_collections table: move config columns into a single TEXT "config" column (JSON).
CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}rag_collections_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    created_by TEXT,
    updated_by TEXT
);

INSERT INTO ${TABLE_PREFIX}rag_collections_new (id, name, config, created_at, updated_at, created_by, updated_by)
SELECT
    id,
    name,
    json_object(
        'description', COALESCE(description, ''),
        'vector_store', json(COALESCE(vector_store_config, '{}')),
        'embedding_provider', COALESCE(embedding_provider, ''),
        'embedding_model', COALESCE(embedding_model, ''),
        'embedding_url', COALESCE(embedding_url, ''),
        'embedding_api_type', COALESCE(embedding_api_type, 'openai'),
        'embedding_bearer_auth', json('false'),
        'chunk_size', COALESCE(chunk_size, 1000),
        'chunk_overlap', COALESCE(chunk_overlap, 200)
    ),
    created_at,
    updated_at,
    created_by,
    updated_by
FROM ${TABLE_PREFIX}rag_collections;

DROP TABLE IF EXISTS ${TABLE_PREFIX}rag_collections;
ALTER TABLE ${TABLE_PREFIX}rag_collections_new RENAME TO ${TABLE_PREFIX}rag_collections;
