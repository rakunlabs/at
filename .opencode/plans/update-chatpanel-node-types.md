# Plan: Update ChatPanel.svelte with Complete Node Type Knowledge

## File: `_ui/src/lib/components/workflow/ChatPanel.svelte`

## Change 1: Update `add_node` tool enum (line 87-91)

Replace the current 14-type enum with all 20 types:

**Old (line 89):**
```
enum: ['input', 'output', 'llm_call', 'agent_call', 'template', 'http_trigger', 'cron_trigger', 'http_request', 'conditional', 'loop', 'script', 'skill_config', 'mcp_config', 'memory_config'],
```

**New:**
```
enum: ['input', 'output', 'llm_call', 'agent_call', 'template', 'workflow_call', 'http_trigger', 'cron_trigger', 'http_request', 'email', 'conditional', 'loop', 'script', 'exec', 'log', 'skill_config', 'mcp_config', 'memory_config', 'group', 'sticky_note'],
```

Also update the data field description (line 101) from:
```
description: 'Node-specific configuration. Must include "label" field.',
```
To:
```
description: 'Node-specific configuration. Must include "label" field (except sticky_note which uses "text" instead).',
```

---

## Change 2: Update system prompt (lines 205-317)

### 2a. Fix `http_trigger` entry (lines 219-222)

**Old:**
```
### http_trigger
- Output handles: id="output" (port: data)
- Data fields: label, trigger_id (auto-assigned on save)
- Webhook receives: method, path, query, headers, body (as reader)
```

**New:**
```
### http_trigger
- Output handles: id="output" (port: data)
- Data fields: label, trigger_id (auto-assigned on save), alias (optional URL alias path), public (boolean, skip auth when true)
- Webhook receives: method, path, query, headers, body (as reader)
```

### 2b. Fix `cron_trigger` entry (lines 224-226)

**Old:**
```
### cron_trigger
- Output handles: id="output" (port: data)
- Data fields: label, schedule (cron expression, e.g. "*/5 * * * *"), payload (object)
```

**New:**
```
### cron_trigger
- Output handles: id="output" (port: data)
- Data fields: label, schedule (cron expression, e.g. "*/5 * * * *"), timezone (IANA timezone e.g. "America/New_York", empty = UTC), payload (object)
```

### 2c. Add 4 missing node types (insert before "## Available Providers" section, after the `exec` entry)

Insert these sections after the `### exec` entry (after line 293):

```
### log
- Input handles: id="input" (port: data)
- Output handles: id="output" (port: data)
- Data fields: label, level ("debug", "info", "warn", or "error"), message (Go template string, e.g. "Processing {{.data}}")
- Pass-through node: outputs the same data it receives. Logs the rendered message at the specified level.

### workflow_call
- Input handles: id="input" (port: data)
- Output handles: id="output" (port: data)
- Data fields: label, workflow_id (ID of child workflow), workflow_name (display name), inputs (object mapping child workflow input field names to Go template values)
- Executes another workflow as a sub-workflow. Input data is available in Go templates. Child workflow outputs are passed to the output port.

### group
- No input or output handles (visual only)
- Data fields: label, color (CSS hex color, default "#22c55e")
- Visual grouping container. Drag to resize. Nodes placed inside are visually grouped but not functionally connected.
- Style: { width: 400, height: 300 } (set via style property, not data)

### sticky_note
- No input or output handles (visual only)
- Data fields: text (markdown content), color (CSS hex color, default "#fef08a")
- NOTE: sticky_note uses "text" instead of "label". Do NOT include a "label" field.
- Visual annotation. Double-click to edit text. Does not participate in workflow execution.
- Style: { width: 200, height: 150 } (set via style property, not data)
```

### 2d. Update "Important" section (lines 313-317)

Add a note about group/sticky_note:

After the existing bullet `- Always include a "label" field in node data`, add:
```
- Exception: sticky_note nodes use "text" instead of "label"
- group and sticky_note nodes are visual-only; they have no handles and cannot be connected with edges
```

---

## Change 3: Add `defaultNodeData()` helper and update `add_node` handler

### 3a. Add helper function (insert before `executeToolCall`, around line 322)

```typescript
  function defaultNodeData(type: string): Record<string, any> {
    switch (type) {
      case 'input': return { label: 'Input' };
      case 'output': return { label: 'Output' };
      case 'llm_call': return { label: 'LLM Call', provider: '', model: '', system_prompt: '' };
      case 'agent_call': return { label: 'Agent Call', provider: '', model: '', system_prompt: '', max_iterations: 10 };
      case 'skill_config': return { label: 'Skill Config', skills: [] };
      case 'mcp_config': return { label: 'MCP Config', mcp_urls: [] };
      case 'memory_config': return { label: 'Memory' };
      case 'template': return { label: 'Template', template: '', variables: [] };
      case 'workflow_call': return { label: 'Workflow Call', workflow_id: '', workflow_name: '', inputs: {} };
      case 'http_trigger': return { label: 'HTTP Trigger', trigger_id: '', alias: '', public: false };
      case 'cron_trigger': return { label: 'Cron Trigger', schedule: '', timezone: '', payload: {} };
      case 'http_request': return { label: 'HTTP Request', url: '', method: 'GET', headers: {}, body: '', timeout: 30, proxy: '', insecure_skip_verify: false, retry: false };
      case 'conditional': return { label: 'Conditional', expression: '' };
      case 'loop': return { label: 'Loop', expression: '' };
      case 'script': return { label: 'Script', code: '', input_count: 1 };
      case 'exec': return { label: 'Exec', command: '', working_dir: '', timeout: 60, sandbox_root: '/tmp/at-sandbox', input_count: 1 };
      case 'email': return { label: 'Email', config_id: '', to: '', cc: '', bcc: '', subject: '', body: '', content_type: 'text/plain', from: '', reply_to: '' };
      case 'log': return { label: 'Log', level: 'info', message: '' };
      case 'group': return { label: 'Group', color: '#22c55e' };
      case 'sticky_note': return { text: 'Double-click to edit...', color: '#fef08a' };
      default: return {};
    }
  }
```

### 3b. Update `add_node` handler (lines 331-342)

**Old:**
```typescript
        case 'add_node': {
          const { type, position, data, id } = args;
          nodeIdCounter++;
          const nodeId = id || `${type}_ai_${nodeIdCounter}`;
          flow.addNode({
            id: nodeId,
            type,
            position: { x: position.x, y: position.y },
            data: data || {},
          });
          return JSON.stringify({ success: true, id: nodeId });
        }
```

**New:**
```typescript
        case 'add_node': {
          const { type, position, data, id } = args;
          nodeIdCounter++;
          const nodeId = id || `${type}_ai_${nodeIdCounter}`;
          const defaults = defaultNodeData(type);
          const nodeData = { ...defaults, ...(data || {}) };
          const nodeOpts: any = {
            id: nodeId,
            type,
            position: { x: position.x, y: position.y },
            data: nodeData,
          };
          // Visual-only nodes need explicit dimensions
          if (type === 'group') {
            nodeOpts.style = { width: 400, height: 300 };
          } else if (type === 'sticky_note') {
            nodeOpts.style = { width: 200, height: 150 };
          }
          flow.addNode(nodeOpts);
          return JSON.stringify({ success: true, id: nodeId });
        }
```

---

## Change 4: Run `svelte-check`

After all edits, run:
```sh
cd _ui && pnpm svelte-check
```

Verify 0 errors.

---

## Summary of all changes

| Area | What changes |
|------|-------------|
| Tool enum (line 89) | Add 6 missing types: `email`, `exec`, `log`, `workflow_call`, `group`, `sticky_note` |
| System prompt - `http_trigger` | Add `alias`, `public` fields |
| System prompt - `cron_trigger` | Add `timezone` field |
| System prompt - new entries | Add `log`, `workflow_call`, `group`, `sticky_note` sections |
| System prompt - Important section | Add sticky_note/group exceptions |
| `defaultNodeData()` helper | New function with defaults for all 20 types |
| `add_node` handler | Merge defaults with AI-provided data; add style for group/sticky_note |
