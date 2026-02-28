# Plan: Update ChatPanel.svelte with Complete Node Type Knowledge

**STATUS: Changes 1-4 already applied. Changes 5-6 below are pending.**

## File: `_ui/src/lib/components/workflow/ChatPanel.svelte`

## Already Applied

- Change 1: Updated `add_node` tool enum (14 -> 20 types)
- Change 2: Updated system prompt (fixed http_trigger, cron_trigger; added log, workflow_call, group, sticky_note)
- Change 3: Added `defaultNodeData()` helper and updated `add_node` handler with default merging + group/sticky_note styles
- Change 4: svelte-check passed (0 errors)

---

## Change 5: Add imports for skills, variables, node configs

Add to the import section (after the existing imports, around line 3-12):

```typescript
import { listSkills } from '@/lib/api/skills';
import { listVariables } from '@/lib/api/secrets';
import { listNodeConfigs } from '@/lib/api/node-configs';
```

---

## Change 6: Add state variables, loaders, and system prompt sections

### 6a. Add state + loaders (after existing `loadProviders()` call, around line 203)

Insert after `loadProviders();`:

```typescript
  let skillsInfo = $state<{ name: string; description: string }[]>([]);
  let variablesInfo = $state<{ key: string; description: string }[]>([]);
  let nodeConfigsInfo = $state<{ id: string; name: string; type: string }[]>([]);

  async function loadSkills() {
    try {
      const skills = await listSkills();
      skillsInfo = skills.map(s => ({ name: s.name, description: s.description }));
    } catch {}
  }

  async function loadVariables() {
    try {
      const vars = await listVariables();
      variablesInfo = vars.map(v => ({ key: v.key, description: v.description }));
    } catch {}
  }

  async function loadNodeConfigs() {
    try {
      const configs = await listNodeConfigs();
      nodeConfigsInfo = configs.map(c => ({ id: c.id, name: c.name, type: c.type }));
    } catch {}
  }

  loadSkills();
  loadVariables();
  loadNodeConfigs();
```

### 6b. Add sections to system prompt

Insert after the existing providers section (after "When creating llm_call or agent_call nodes, use the provider key..." line), before `## Edge Connection Rules`:

```
## Available Skills
${skillsInfo.length > 0 ? skillsInfo.map(s => `- "${s.name}": ${s.description}`).join('\n') : '- No skills configured yet'}

When creating skill_config nodes, use skill names from this list in the "skills" array.
Skills provide tool capabilities to agent_call nodes connected via skill_config.

## Available Variables
${variablesInfo.length > 0 ? variablesInfo.map(v => `- "${v.key}"${v.description ? ': ' + v.description : ''}`).join('\n') : '- No variables configured yet'}

Variables are accessed differently depending on context:
- In JavaScript nodes (script, conditional, loop): use getVar("key") function
- In Go template nodes (template, http_request, email, log, exec): variables must be resolved by an upstream script node using getVar() and passed as data; there is no direct getVar in Go templates
- In bash tool handlers (skills): available as $VAR_KEY environment variables (uppercase, dots/hyphens replaced with underscores)

## Available Node Configs
${nodeConfigsInfo.length > 0 ? nodeConfigsInfo.map(c => `- id="${c.id}" name="${c.name}" type="${c.type}"`).join('\n') : '- No node configs configured yet'}

When creating email nodes, set the "config_id" field to a node config ID of type "email" from this list.
Node configs contain pre-configured connection settings (e.g. SMTP for email).
```

---

## Change 7: Run `svelte-check`

After all edits, run:
```sh
cd _ui && pnpm svelte-check
```

Verify 0 errors.

---

## Exact edit instructions (for implementation)

### Edit A: Add imports

Find:
```typescript
import { type FlowState, type FlowNode, type FlowEdge } from 'kaykay';
```

Replace with:
```typescript
import { type FlowState, type FlowNode, type FlowEdge } from 'kaykay';
import { listSkills } from '@/lib/api/skills';
import { listVariables } from '@/lib/api/secrets';
import { listNodeConfigs } from '@/lib/api/node-configs';
```

### Edit B: Add state + loaders

Find:
```typescript
  loadProviders();

  const systemPrompt = $derived(
```

Replace with:
```typescript
  loadProviders();

  let skillsInfo = $state<{ name: string; description: string }[]>([]);
  let variablesInfo = $state<{ key: string; description: string }[]>([]);
  let nodeConfigsInfo = $state<{ id: string; name: string; type: string }[]>([]);

  async function loadSkills() {
    try {
      const skills = await listSkills();
      skillsInfo = skills.map(s => ({ name: s.name, description: s.description }));
    } catch {}
  }

  async function loadVariables() {
    try {
      const vars = await listVariables();
      variablesInfo = vars.map(v => ({ key: v.key, description: v.description }));
    } catch {}
  }

  async function loadNodeConfigs() {
    try {
      const configs = await listNodeConfigs();
      nodeConfigsInfo = configs.map(c => ({ id: c.id, name: c.name, type: c.type }));
    } catch {}
  }

  loadSkills();
  loadVariables();
  loadNodeConfigs();

  const systemPrompt = $derived(
```

### Edit C: Add system prompt sections

Find (the line after the providers usage note):
```
When creating llm_call or agent_call nodes, use the provider key for the "provider" field and the model name for the "model" field from the list above.

## Edge Connection Rules
```

Replace with:
```
When creating llm_call or agent_call nodes, use the provider key for the "provider" field and the model name for the "model" field from the list above.

## Available Skills
${skillsInfo.length > 0 ? skillsInfo.map(s => `- "${s.name}": ${s.description}`).join('\n') : '- No skills configured yet'}

When creating skill_config nodes, use skill names from this list in the "skills" array.
Skills provide tool capabilities to agent_call nodes connected via skill_config.

## Available Variables
${variablesInfo.length > 0 ? variablesInfo.map(v => `- "${v.key}"${v.description ? ': ' + v.description : ''}`).join('\n') : '- No variables configured yet'}

Variables are accessed differently depending on context:
- In JavaScript nodes (script, conditional, loop): use getVar("key") function
- In Go template nodes (template, http_request, email, log, exec): variables must be resolved by an upstream script node using getVar() and passed as data; there is no direct getVar in Go templates
- In bash tool handlers (skills): available as $VAR_KEY environment variables (uppercase, dots/hyphens replaced with underscores)

## Available Node Configs
${nodeConfigsInfo.length > 0 ? nodeConfigsInfo.map(c => `- id="${c.id}" name="${c.name}" type="${c.type}"`).join('\n') : '- No node configs configured yet'}

When creating email nodes, set the "config_id" field to a node config ID of type "email" from this list.
Node configs contain pre-configured connection settings (e.g. SMTP for email).

## Edge Connection Rules
```
