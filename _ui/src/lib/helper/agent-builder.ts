import type { ToolDefinition } from './chat';

export interface AgentDraft {
  name: string;
  description: string;
  group: string;
  provider: string;
  model: string;
  reasoning_effort: string;
  system_prompt: string;
  skills: string[];
  mcp_sets: string[];
  workflows: string[];
  builtin_tools: string[];
  max_iterations: number;
  tool_timeout: number;
}
export interface BuilderResource { id: string; name: string; description?: string }
export interface AgentBuilderCatalog {
  providers: { key: string; type: string; models: string[]; default_model: string }[];
  skills: BuilderResource[];
  mcp_sets: BuilderResource[];
  workflows: BuilderResource[];
  builtin_tools: BuilderResource[];
}

const strings = ['name', 'description', 'group', 'provider', 'model', 'reasoning_effort', 'system_prompt'] as const;
const selections = ['skills', 'mcp_sets', 'workflows', 'builtin_tools'] as const;
const numbers = ['max_iterations', 'tool_timeout'] as const;

export const agentBuilderTools: ToolDefinition[] = [
  { type: 'function', function: { name: 'ask_agent_question', description: 'Ask a necessary clarification or answer a question that requires no form changes. Do not use this instead of update_agent_form when the user requests an edit.', parameters: { type: 'object', properties: { message: { type: 'string' } }, required: ['message'], additionalProperties: false } } },
  { type: 'function', function: { name: 'get_agent_form', description: 'Read the current unsaved agent form, including manual edits.', parameters: { type: 'object', properties: {}, additionalProperties: false } } },
  { type: 'function', function: { name: 'list_agent_resources', description: 'List the available provider/models, skills, MCP sets, workflows and built-in tools. Use exact identifiers from this catalog.', parameters: { type: 'object', properties: {}, additionalProperties: false } } },
  { type: 'function', function: {
    name: 'update_agent_form',
    description: 'Update only supplied fields in the unsaved agent form. Arrays replace the selection; an empty array clears it. This does not save or execute an agent.',
    parameters: {
      type: 'object', additionalProperties: false,
      properties: {
        ...Object.fromEntries(strings.map(key => [key, { type: 'string' }])),
        ...Object.fromEntries(selections.map(key => [key, { type: 'array', items: { type: 'string' }, description: 'Exact names from the catalog' }])),
        max_iterations: { type: 'integer', minimum: 1, maximum: 240 },
        tool_timeout: { type: 'integer', minimum: 1, maximum: 3600 },
      },
    },
  } },
];

/** Validate the whole patch before touching the form. Omitted fields survive. */
export function applyAgentDraftPatch(current: AgentDraft, input: unknown, catalog: AgentBuilderCatalog): AgentDraft {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('Expected an object of form fields.');
  const patch = input as Record<string, unknown>;
  const allowed: readonly string[] = [...strings, ...selections, ...numbers];
  for (const key of Object.keys(patch)) if (!allowed.includes(key)) throw new Error(`Unsupported field: ${key}`);
  const next = structuredClone(current);
  for (const key of strings) {
    if (!(key in patch)) continue;
    if (typeof patch[key] !== 'string') throw new Error(`${key} must be text.`);
    next[key] = patch[key];
  }
  if ('name' in patch && !next.name.trim()) throw new Error('Agent name cannot be empty.');
  for (const key of numbers) {
    if (!(key in patch)) continue;
    const value = patch[key];
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 1 || value > (key === 'max_iterations' ? 240 : 3600)) throw new Error(`Invalid ${key}.`);
    next[key] = value;
  }
  for (const key of selections) {
    if (!(key in patch)) continue;
    const value = patch[key];
    if (!Array.isArray(value) || !value.every(v => typeof v === 'string')) throw new Error(`${key} must be a list of identifiers.`);
    const available = new Set([...catalog[key].map(r => r.name), ...current[key]]);
    for (const id of value) if (!available.has(id)) throw new Error(`Unknown ${key} selection: ${id}. Read list_agent_resources first.`);
    next[key] = [...new Set(value)];
  }
  if ('provider' in patch || 'model' in patch) {
    const provider = catalog.providers.find(p => p.key === next.provider);
    if (!provider) throw new Error('Choose a provider from list_agent_resources.');
    if (next.provider !== current.provider && !('model' in patch)) next.model = provider.default_model || provider.models[0] || '';
    if (next.model && ![...provider.models, provider.default_model].includes(next.model) && !(next.provider === current.provider && next.model === current.model)) throw new Error('Choose a model listed for this provider.');
  }
  if ('reasoning_effort' in patch || next.provider !== current.provider) {
    const type = catalog.providers.find(p => p.key === next.provider)?.type;
    const options = ['openai', 'azure', 'vertex'].includes(type || '') ? ['', 'low', 'medium', 'high', 'xhigh']
      : ['anthropic', 'gemini', 'vertex-gemini', 'minimax'].includes(type || '') ? ['', 'low', 'medium', 'high'] : [''];
    if (next.provider !== current.provider && !('reasoning_effort' in patch)) next.reasoning_effort = '';
    if (!options.includes(next.reasoning_effort)) throw new Error('Reasoning effort is not supported by this provider.');
  }
  return next;
}
