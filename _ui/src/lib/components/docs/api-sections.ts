import type { DocsSearchable } from '@/lib/helper/docs-nav';

/**
 * The API reference is a fixed, individually addressable list of sections. The
 * `id` is the value of `?section=` in the URL, `body` is the search index
 * (never rendered) — keep it in sync when a section's content changes so the
 * sidebar search stays honest.
 */
export interface ApiSectionMeta extends DocsSearchable {
  id: string;
  title: string;
  description: string;
  body: string;
  /** Sections whose content is a grid rather than prose get the wider measure. */
  wide?: boolean;
}

export const apiSections: ApiSectionMeta[] = [
  {
    id: 'overview',
    title: 'Overview',
    description: 'What this gateway is and how models are addressed.',
    body: 'openai compatible api sdk http client provider_key/model_name introduction getting started',
  },
  {
    id: 'endpoints',
    title: 'Endpoints',
    description: 'The two core HTTP endpoints.',
    body: 'post chat completions get models routes url paths',
  },
  {
    id: 'proxy',
    title: 'Proxy endpoint',
    description: 'Call any provider endpoint directly through the gateway.',
    body: 'gateway proxy provider path passthrough gemini file search credential injection forwarding',
  },
  {
    id: 'authentication',
    title: 'Authentication',
    description: 'Bearer tokens, scoping, and where to create them.',
    body: 'authorization header bearer token at_ scoped providers models tokens page api key',
  },
  {
    id: 'code-examples',
    title: 'Code examples',
    description: 'Python, JavaScript, Go and curl.',
    body: 'python javascript js node go golang curl openai sdk snippet sample quickstart',
  },
  {
    id: 'opencode-config',
    title: 'Opencode config',
    description: 'Generate an opencode.json provider block from your models.',
    body: 'opencode config json provider ai-sdk openai-compatible baseurl models select',
  },
  {
    id: 'mcp-configuration',
    title: 'MCP configuration',
    description: 'Attach an AT-hosted MCP server to opencode.',
    body: 'mcp model context protocol remote server opencode.json authorization public private',
  },
  {
    id: 'claude-marketplace',
    title: 'Claude Code marketplace',
    description: 'Install public MCP servers as Claude Code plugins.',
    body: 'claude code plugin marketplace zip manifest json install reload-plugins offline',
  },
  {
    id: 'list-models',
    title: 'List models',
    description: 'Enumerate every model the gateway exposes.',
    body: 'curl models list enumerate available discovery',
  },
  {
    id: 'available-models',
    title: 'Available models',
    description: 'Every model id configured on this instance.',
    body: 'models catalog provider key copy model id list configured',
    wide: true,
  },
];

export const apiSectionIds = apiSections.map((s) => s.id);

export function findApiSection(id: string): ApiSectionMeta | undefined {
  return apiSections.find((s) => s.id === id);
}
