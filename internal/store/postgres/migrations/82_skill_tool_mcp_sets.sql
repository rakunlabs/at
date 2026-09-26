-- Migration 73 intentionally made skills documentation-only, but its legacy
-- JSON block is sufficient to move those handlers into an independently
-- admitted MCP set. Keep the skill text intact; only reconstruct executable
-- configuration in the MCP resource plane.
WITH recovered AS (
    SELECT s.*,
           split_part(
               split_part(
                   s.system_prompt,
                   E'## Legacy tool references\n\nThese legacy definitions are documentation only. They are not registered or executed as tools. Use capabilities already attached to the agent, such as built-in or MCP tools.\n\n```json\n',
                   2
               ),
               E'\n```', 1
           )::jsonb AS legacy_tools
    FROM ${TABLE_PREFIX}skills s
    WHERE s.tools = '[]'::jsonb
      AND s.system_prompt LIKE E'%## Legacy tool references%```json\n[%'
), tool_sets AS (
    SELECT r.*,
           (
               SELECT jsonb_agg(tool || jsonb_build_object('source_skill_id', r.id))
               FROM jsonb_array_elements(r.legacy_tools) AS tool
           ) AS inline_tools
    FROM recovered r
    WHERE jsonb_typeof(r.legacy_tools) = 'array'
      AND jsonb_array_length(r.legacy_tools) > 0
)
INSERT INTO ${TABLE_PREFIX}mcp_sets (
    id, workspace_id, owner_user_id, name, description, category, tags,
    config, servers, urls, created_at, updated_at, created_by, updated_by
)
SELECT 'skill-tools-' || id,
       workspace_id,
       owner_user_id,
       '__skill_tools_' || id,
       'Executable tools migrated from skill ' || name,
       'Skill tools',
       '[]'::jsonb,
       jsonb_build_object('inline_tools', inline_tools),
       '[]'::jsonb,
       '[]'::jsonb,
       created_at,
       clock_timestamp(),
       created_by,
       updated_by
FROM tool_sets
ON CONFLICT (id) DO UPDATE SET
    config = EXCLUDED.config,
    description = EXCLUDED.description,
    updated_at = EXCLUDED.updated_at,
    updated_by = EXCLUDED.updated_by;

UPDATE ${TABLE_PREFIX}skills
SET system_prompt = replace(
    system_prompt,
    'These legacy definitions are documentation only. They are not registered or executed as tools. Use capabilities already attached to the agent, such as built-in or MCP tools.',
    'These definitions are a documentation copy. Executable copies, when installed, are provided through a separately authorized MCP tool pack; otherwise use capabilities already attached to the agent.'
)
WHERE system_prompt LIKE '%## Legacy tool references%';
