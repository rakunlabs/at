# Skills

Skills are reusable Markdown instructions and bundled text resources. Loading a
skill adds guidance to an agent's context; it never registers or executes a
tool. Code blocks are examples only.

Skills can tell an agent when and how to use capabilities configured elsewhere:

- built-in tools enabled on the agent
- tools supplied by an MCP server or MCP Set
- workflows attached as tools

## SKILL.md format

The preferred portable format is `SKILL.md`:

```markdown
---
name: incident-review
description: Review an incident and prepare follow-up actions
version: 1.0.0
---

# Incident review

Use the attached monitoring MCP tools to gather evidence. Summarize the
timeline, contributing factors, and follow-up actions. Do not claim a tool ran
unless its result is present in the conversation.
```

A skill folder may include additional text resources. Agents can load those
resources on demand with `read_skill_resource`. Markdown is never parsed for
tool definitions.

## Legacy imports

AT still accepts old JSON exports containing a `tools` array so installations
can migrate without losing content. On import or persistence, each legacy tool
definition is appended to the skill's Markdown under **Legacy tool references**
and the executable tool list is cleared. The preserved handler source is inert
reference material.

## Import and export

### UI

- **Export** downloads `SKILL.md`.
- **Import URL** accepts a `SKILL.md`, a compatible legacy JSON export, or a Git
  repository containing skill folders.
- The skill-folder editor manages `SKILL.md` and bundled resources together.

### API

```bash
# Export SKILL.md
curl -L /api/v1/skills/{id}/export -o SKILL.md

# Import a skill payload
curl -X POST /api/v1/skills/import \
  -H 'Content-Type: application/json' \
  -d @skill.json

# Import SKILL.md or a repository
curl -X POST /api/v1/skills/import-url \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/SKILL.md"}'
```

## Skill Store

AT ships built-in documentation templates in the **Skill Store** tab. Installing
a template creates an ordinary skill record. Older embedded templates may still
contain legacy tool JSON; AT normalizes it into inert Markdown before display or
persistence.

```bash
# List templates
curl /api/v1/skill-templates

# Install one
curl -X POST /api/v1/skill-templates/{slug}/install
```

## Variables and credentials

Agents may see variable names, descriptions, policy, and whether a variable is
secret. Secret values are not exposed to skill Markdown, arbitrary JavaScript,
or Bash. A secret can be resolved only through an approved typed reference at a
controlled tool boundary. Initially this is supported for HTTPS `http_request`
headers and requires both the tool and destination host to be allowed on the
variable.

Example header value:

```json
{
  "$ref": "variable://api_token",
  "prefix": "Bearer "
}
```

## Contributing built-in templates

Add a JSON file under `internal/server/skill_templates/` with template metadata
and a documentation-only `skill.system_prompt`. Do not add executable handlers.
Resources may be bundled for on-demand reading.
