# Skills

Skills are reusable bundles of a system prompt fragment and tools (JS/bash handlers) that agents can reference. They allow you to package integrations as portable, shareable units.

## Skill JSON Format

A skill's portable JSON format (used for import/export and templates):

```json
{
  "name": "my_skill",
  "description": "What this skill does",
  "system_prompt": "Instructions for the agent when using this skill",
  "tools": [
    {
      "name": "tool_name",
      "description": "What this tool does",
      "inputSchema": {
        "type": "object",
        "properties": {
          "param1": { "type": "string", "description": "A parameter" }
        },
        "required": ["param1"]
      },
      "handler": "var result = httpGet(args.param1); return result.body;",
      "handler_type": "js"
    }
  ]
}
```

## Handler API Reference

### JavaScript Handlers (`handler_type: "js"`)

Tool arguments are available as `args` (object). The handler must return a string.

Built-in functions:
- `httpGet(url, headers)` — HTTP GET, returns `{status, body}`
- `httpPost(url, body, headers)` — HTTP POST, returns `{status, body}`
- `httpPut(url, body, headers)` — HTTP PUT, returns `{status, body}`
- `httpDelete(url, headers)` — HTTP DELETE, returns `{status, body}`
- `getVar(key)` — Get a variable by key from the AT variable store
- `btoa(str)` / `atob(str)` — Base64 encode/decode
- `JSON.parse()` / `JSON.stringify()` — JSON handling
- `encodeURIComponent()` / `decodeURIComponent()` — URL encoding

### Bash Handlers (`handler_type: "bash"`)

Tool arguments are passed as environment variables prefixed with `ARG_` (uppercase). For example, argument `url` becomes `$ARG_URL`.

All AT variables are also available as environment variables.

The handler's stdout is captured as the tool result.

## Import / Export

### Via UI

- **Export**: Click the download icon on any skill row → downloads a `.json` file
- **Import from URL**: Click "Import URL" in the toolbar → paste a URL to a skill JSON file
- **Copy/Paste**: Use the clipboard copy/paste buttons for quick sharing

### Via API

```bash
# Export a skill
curl GET /api/v1/skills/{id}/export

# Import a skill from JSON body
curl -X POST /api/v1/skills/import \
  -H 'Content-Type: application/json' \
  -d @skill.json

# Import a skill from URL
curl -X POST /api/v1/skills/import-url \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://example.com/skill.json"}'
```

## Skill Store (Predefined Templates)

AT ships with built-in skill templates accessible from the **Skill Store** tab on the Skills page. Templates can be installed with one click.

### Available Templates

| Template | Category | Description |
|----------|----------|-------------|
| Gmail Reader | Email | Search and read Gmail messages |
| Google Calendar | Productivity | List and create calendar events |
| GitHub Issues | Development | List, create, and comment on issues |
| Slack Messages | Communication | Read and send Slack messages |
| Jira Tasks | Project Management | Search and create Jira issues |
| Web Scraper | Utilities | Fetch and extract web content |
| JSON API Client | Utilities | Generic REST API client |

### Template API

```bash
# List all templates
curl GET /api/v1/skill-templates

# Filter by category
curl GET /api/v1/skill-templates?category=Development

# Get a single template
curl GET /api/v1/skill-templates/github-issues

# Install a template (creates a skill in the DB)
curl -X POST /api/v1/skill-templates/github-issues/install
```

## Required Variables Convention

Templates declare `required_variables` — variables the skill's handlers expect at runtime (fetched via `getVar()` in JS or environment variables in bash). Before using an installed template skill, create these variables in the AT Variables page. Variables marked `secret: true` should contain sensitive values like API tokens.

## Contributing Built-in Templates

To add a new predefined skill template:

1. Create a JSON file in `internal/server/skill_templates/`
2. Follow this format:

```json
{
  "slug": "my-integration",
  "name": "My Integration",
  "description": "Short description",
  "category": "Category Name",
  "tags": ["tag1", "tag2"],
  "required_variables": [
    {
      "key": "my_api_key",
      "description": "API key for My Service",
      "secret": true
    }
  ],
  "skill": {
    "name": "my_integration",
    "description": "Description for the installed skill",
    "system_prompt": "Instructions for agents using this skill",
    "tools": [ ... ]
  }
}
```

3. The file is automatically embedded in the binary at build time
4. It will appear in the Skill Store after the next build
