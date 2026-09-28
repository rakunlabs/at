import type { MCPHTTPTool, MCPUpstream } from '../api/mcp-servers';
import { formBuilderTools } from './form-builder';

export interface SkillBuilderDraft extends Record<string, unknown> {
  name: string;
  description: string;
  category: string;
  tags: string[];
  system_prompt: string;
  context: '' | 'fork';
  agent: string;
}

export interface SkillBuilderCatalog extends Record<string, unknown> {
  agents: Array<{ id: string; name: string; description?: string }>;
}

export interface MCPSetBuilderDraft extends Record<string, unknown> {
  name: string;
  description: string;
  category: string;
  tags: string[];
  http_tools: MCPHTTPTool[];
  builtin_tools: string[];
  workflow_ids: string[];
  mcp_upstreams: MCPUpstream[];
}

export interface MCPSetBuilderCatalog extends Record<string, unknown> {
  builtin_tools: Array<{ id: string; name: string; description?: string }>;
  workflows: Array<{ id: string; name: string; description?: string }>;
}

const textFields = ['name', 'description', 'category'] as const;
const textSchema = Object.fromEntries(textFields.map(field => [field, { type: 'string' }]));
const tagsSchema = { type: 'array', items: { type: 'string' }, maxItems: 32 };

export const skillBuilder = formBuilderTools('skill', {
  type: 'object',
  additionalProperties: false,
  properties: {
    ...textSchema,
    tags: tagsSchema,
    system_prompt: { type: 'string' },
    context: { type: 'string', enum: ['', 'fork'] },
    agent: { type: 'string', description: 'Exact agent ID from list_skill_resources; required only for fork context.' },
  },
});

export const mcpSetBuilder = formBuilderTools('mcp_set', {
  type: 'object',
  additionalProperties: false,
  properties: {
    ...textSchema,
    tags: tagsSchema,
    builtin_tools: { type: 'array', items: { type: 'string' }, description: 'Exact built-in tool names from list_mcp_set_resources.' },
    workflow_ids: { type: 'array', items: { type: 'string' }, description: 'Exact workflow IDs from list_mcp_set_resources.' },
    http_tools: {
      type: 'array',
      items: {
        type: 'object', additionalProperties: false, required: ['name', 'description', 'method', 'url', 'input_schema'],
        properties: {
          name: { type: 'string' }, description: { type: 'string' },
          method: { type: 'string', enum: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD'] },
          url: { type: 'string' }, headers: { type: 'object', additionalProperties: { type: 'string' } },
          body_template: { type: 'string' }, input_schema: { type: 'object' },
        },
      },
    },
    mcp_upstreams: {
      type: 'array',
      items: {
        type: 'object', additionalProperties: false,
        properties: {
          url: { type: 'string' }, headers: { type: 'object', additionalProperties: { type: 'string' } },
          command: { type: 'string' }, args: { type: 'array', items: { type: 'string' } }, env: { type: 'object', additionalProperties: { type: 'string' } },
        },
      },
    },
  },
});

function object(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object.`);
  return value as Record<string, unknown>;
}

function strings(value: unknown, label: string): string[] {
  if (!Array.isArray(value) || !value.every(item => typeof item === 'string')) throw new Error(`${label} must be a list of text values.`);
  return [...new Set(value.map(item => item.trim()).filter(Boolean))];
}

function recordStrings(value: unknown, label: string): Record<string, string> | undefined {
  if (value === undefined) return undefined;
  const entry = object(value, label);
  if (!Object.values(entry).every(item => typeof item === 'string')) throw new Error(`${label} values must be text.`);
  return entry as Record<string, string>;
}

function changedFields<T extends Record<string, unknown>>(before: T, after: T): string[] {
  return Object.keys(after).filter(key => JSON.stringify(before[key]) !== JSON.stringify(after[key]));
}

export function applySkillBuilderPatch(current: SkillBuilderDraft, input: unknown, catalog: SkillBuilderCatalog): SkillBuilderDraft {
  const patch = object(input, 'Skill patch');
  const allowed = new Set(['name', 'description', 'category', 'tags', 'system_prompt', 'context', 'agent']);
  for (const key of Object.keys(patch)) if (!allowed.has(key)) throw new Error(`Unsupported field: ${key}`);
  const next = structuredClone(current);
  for (const field of [...textFields, 'system_prompt'] as const) {
    if (!(field in patch)) continue;
    if (typeof patch[field] !== 'string') throw new Error(`${field} must be text.`);
    next[field] = patch[field];
  }
  if ('name' in patch && !next.name.trim()) throw new Error('Skill name cannot be empty.');
  if ('tags' in patch) next.tags = strings(patch.tags, 'tags').slice(0, 32);
  if ('context' in patch) {
    if (patch.context !== '' && patch.context !== 'fork') throw new Error('context must be current or fork.');
    next.context = patch.context;
    if (!next.context && !('agent' in patch)) next.agent = '';
  }
  if ('agent' in patch) {
    if (typeof patch.agent !== 'string') throw new Error('agent must be an identifier.');
    if (patch.agent && !catalog.agents.some(agent => agent.id === patch.agent)) throw new Error('Choose an agent ID from list_skill_resources.');
    next.agent = patch.agent;
  }
  if (next.context === 'fork' && !next.agent) throw new Error('A forked skill requires an agent from list_skill_resources.');
  if (next.context !== 'fork' && next.agent) throw new Error('agent is only valid for forked skills.');
  return next;
}

export function applyMCPSetBuilderPatch(current: MCPSetBuilderDraft, input: unknown, catalog: MCPSetBuilderCatalog): MCPSetBuilderDraft {
  const patch = object(input, 'MCP Set patch');
  const allowed = new Set(['name', 'description', 'category', 'tags', 'http_tools', 'builtin_tools', 'workflow_ids', 'mcp_upstreams']);
  for (const key of Object.keys(patch)) if (!allowed.has(key)) throw new Error(`Unsupported field: ${key}`);
  const next = structuredClone(current);
  for (const field of textFields) {
    if (!(field in patch)) continue;
    if (typeof patch[field] !== 'string') throw new Error(`${field} must be text.`);
    next[field] = patch[field];
  }
  if ('name' in patch && !next.name.trim()) throw new Error('MCP Set name cannot be empty.');
  if ('tags' in patch) next.tags = strings(patch.tags, 'tags').slice(0, 32);
  if ('builtin_tools' in patch) {
    const values = strings(patch.builtin_tools, 'builtin_tools');
    const available = new Set([...catalog.builtin_tools.map(tool => tool.id), ...current.builtin_tools]);
    for (const value of values) if (!available.has(value)) throw new Error(`Unknown built-in tool: ${value}.`);
    next.builtin_tools = values;
  }
  if ('workflow_ids' in patch) {
    const values = strings(patch.workflow_ids, 'workflow_ids');
    const available = new Set([...catalog.workflows.map(workflow => workflow.id), ...current.workflow_ids]);
    for (const value of values) if (!available.has(value)) throw new Error(`Unknown workflow: ${value}.`);
    next.workflow_ids = values;
  }
  if ('http_tools' in patch) {
    if (!Array.isArray(patch.http_tools)) throw new Error('http_tools must be a list.');
    const seen = new Set<string>();
    next.http_tools = patch.http_tools.map((value, index) => {
      const tool = object(value, `http_tools[${index}]`);
      const name = typeof tool.name === 'string' ? tool.name.trim() : '';
      if (!name || seen.has(name)) throw new Error(`http_tools[${index}] needs a unique name.`);
      seen.add(name);
      const method = typeof tool.method === 'string' ? tool.method.toUpperCase() : 'GET';
      if (!['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD'].includes(method)) throw new Error(`Invalid HTTP method for ${name}.`);
      if (typeof tool.url !== 'string' || !tool.url.trim()) throw new Error(`${name} needs a URL.`);
      const schema = object(tool.input_schema, `${name} input_schema`);
      return {
        name,
        description: typeof tool.description === 'string' ? tool.description : '',
        method,
        url: tool.url.trim(),
        headers: recordStrings(tool.headers, `${name} headers`) || {},
        body_template: typeof tool.body_template === 'string' ? tool.body_template : '',
        input_schema: structuredClone(schema),
      };
    });
  }
  if ('mcp_upstreams' in patch) {
    if (!Array.isArray(patch.mcp_upstreams)) throw new Error('mcp_upstreams must be a list.');
    next.mcp_upstreams = patch.mcp_upstreams.map((value, index) => {
      const upstream = object(value, `mcp_upstreams[${index}]`);
      const url = typeof upstream.url === 'string' ? upstream.url.trim() : '';
      const command = typeof upstream.command === 'string' ? upstream.command.trim() : '';
      if (Boolean(url) === Boolean(command)) throw new Error(`mcp_upstreams[${index}] must contain either url or command.`);
      if (url) return { url, headers: recordStrings(upstream.headers, `mcp_upstreams[${index}] headers`) || {} };
      return { command, args: strings(upstream.args ?? [], `mcp_upstreams[${index}] args`), env: recordStrings(upstream.env, `mcp_upstreams[${index}] env`) || {} };
    });
  }
  return next;
}

export { changedFields };
