-- OpenAI shut down the Chat Completions search-preview models
-- (gpt-4o-search-preview, gpt-4o-mini-search-preview) on 2026-07-23; calls now
-- answer 404 "model has been deprecated". Their supported Chat Completions
-- replacement is gpt-5-search-api, which accepts the same request shape
-- (messages + web_search_options) and returns the same url_citation
-- annotations, so stored handlers only need the model name changed.
--
-- Template sync cannot repair installed copies: migration 82 moved executable
-- handlers into paired MCP sets and rewrote the skill text, so the installed
-- checksum no longer matches and sync treats the skill as customized.
UPDATE ${TABLE_PREFIX}mcp_sets
SET config = replace(replace(config::text,
        'gpt-4o-mini-search-preview', 'gpt-5-search-api'),
        'gpt-4o-search-preview', 'gpt-5-search-api')::jsonb,
    updated_at = clock_timestamp()
WHERE config::text LIKE '%-search-preview%';

UPDATE ${TABLE_PREFIX}mcp_servers
SET config = replace(replace(config::text,
        'gpt-4o-mini-search-preview', 'gpt-5-search-api'),
        'gpt-4o-search-preview', 'gpt-5-search-api')::jsonb,
    updated_at = clock_timestamp()
WHERE config::text LIKE '%-search-preview%';

UPDATE ${TABLE_PREFIX}skills
SET system_prompt = replace(replace(system_prompt,
        'gpt-4o-mini-search-preview', 'gpt-5-search-api'),
        'gpt-4o-search-preview', 'gpt-5-search-api'),
    updated_at = clock_timestamp()
WHERE system_prompt LIKE '%-search-preview%';
