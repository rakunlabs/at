UPDATE ${TABLE_PREFIX}skills
SET system_prompt = concat(
        rtrim(system_prompt),
        CASE WHEN btrim(system_prompt) = '' THEN '' ELSE E'\n\n' END,
        E'## Legacy tool references\n\n',
        E'These legacy definitions are documentation only. They are not registered or executed as tools. Use capabilities already attached to the agent, such as built-in or MCP tools.\n\n',
        E'```json\n',
        jsonb_pretty(tools),
        E'\n```'
    ),
    tools = '[]'::jsonb
WHERE jsonb_typeof(tools) = 'array'
  AND jsonb_array_length(tools) > 0;
