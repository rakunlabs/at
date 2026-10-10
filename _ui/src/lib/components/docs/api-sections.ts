import type { DocsSearchable } from '@/lib/helper/docs-nav';

/**
 * The reference is a fixed, individually addressable list of pages grouped
 * into sidebar sections. The `id` is the value of `?section=` in the URL and
 * must stay stable (old ids are kept working through DOCS_SECTION_ALIASES).
 * `body` is the search index (never rendered) — keep it in sync when a page's
 * content changes so the sidebar search stays honest.
 */
export interface ApiSectionMeta extends DocsSearchable {
  id: string;
  title: string;
  description: string;
  body: string;
  group: ApiGroupId;
}

export type ApiGroupId = 'start' | 'gateway' | 'integrations';

export interface ApiGroupMeta {
  id: ApiGroupId;
  title: string;
  description: string;
}

export const apiGroups: ApiGroupMeta[] = [
  {
    id: 'start',
    title: 'Get started',
    description: 'Base URL, tokens and a first request.',
  },
  {
    id: 'gateway',
    title: 'Gateway API',
    description: 'Every endpoint, AT extensions, routing and errors.',
  },
  {
    id: 'integrations',
    title: 'Integrations',
    description: 'Configure coding agents and MCP clients.',
  },
];

export const apiSections: ApiSectionMeta[] = [
  // ─── Get started ───
  {
    id: 'overview',
    group: 'start',
    title: 'Overview',
    description: 'What the gateway is and where everything lives.',
    body: 'introduction getting started openai compatible anthropic base url provider_key/model_name gateway',
  },
  {
    id: 'quickstart',
    group: 'start',
    title: 'Quickstart',
    description: 'Create a token and send a first request in any language.',
    body: 'python javascript js node go golang curl openai sdk snippet sample code examples first request token',
  },
  {
    id: 'authentication',
    group: 'start',
    title: 'Authentication',
    description: 'API tokens, scoping, limits, pause and rotation.',
    body: 'authorization header bearer token at_ x-api-key personal workspace scoped providers models spend limit pause rotate 401',
  },
  {
    id: 'available-models',
    group: 'start',
    title: 'Models',
    description: 'How models are addressed, and every model on this instance.',
    body: 'models catalog list provider key copy model id routing profile v1/models model/info curl enumerate discovery',
  },

  // ─── Gateway API ───
  {
    id: 'endpoints',
    group: 'gateway',
    title: 'Endpoint reference',
    description: 'Every route under /gateway/v1.',
    body: 'endpoints routes chat completions messages responses embeddings images audio speech transcriptions moderations rerank decisions scores media health models model info mcp providers passthrough',
  },
  {
    id: 'chat-completions',
    group: 'gateway',
    title: 'Chat & Responses',
    description: 'OpenAI Chat Completions and Responses, with AT extensions.',
    body: 'chat completions responses streaming stream_options include_usage tool_choice parallel_tool_calls response_format reasoning_effort n seed at_fallbacks extra_body mock_response timeout_ms idempotency-key headers x-at-model-used',
  },
  {
    id: 'anthropic-messages',
    group: 'gateway',
    title: 'Anthropic Messages',
    description: 'Native /v1/messages for Claude Code and other Anthropic clients.',
    body: 'anthropic messages claude code cline roo kilo anthropic_base_url anthropic_auth_token x-api-key native',
  },
  {
    id: 'routing',
    group: 'gateway',
    title: 'Routing & fallbacks',
    description: 'Fallback chains, routing profiles and provider cooldown.',
    body: 'at_fallbacks routing profile fallback chain retry 429 529 5xx cooldown rate limit x-at-routing-profile x-at-model-used streaming commitment',
  },
  {
    id: 'embeddings',
    group: 'gateway',
    title: 'Embeddings',
    description: 'Single or batched vector embeddings.',
    body: 'embeddings vector batch input input_type dimensions base64 search document query classification clustering limits cohere',
  },
  {
    id: 'media',
    group: 'gateway',
    title: 'Images, audio & more',
    description: 'Image generation, speech, transcription, moderation and rerank.',
    body: 'images generations gpt-image dall-e audio speech tts transcriptions whisper multipart moderations rerank cohere media download',
  },
  {
    id: 'decisions',
    group: 'gateway',
    title: 'Decisions',
    description: 'Typed System 1 decisions from a systemone provider.',
    body: 'decisions system 1 systemone laya typesafe jev choice score noul criteria questions state confidence',
  },
  {
    id: 'proxy',
    group: 'gateway',
    title: 'Provider passthrough',
    description: 'Call any provider-native endpoint through the gateway.',
    body: 'gateway proxy provider path passthrough native gemini file search credential injection forwarding bedrock cohere',
  },
  {
    id: 'tracing',
    group: 'gateway',
    title: 'Tracing & scores',
    description: 'Label traces from your client and attach quality scores.',
    body: 'trace x-at-trace-id x-at-session-id x-at-trace-name x-at-tags x-at-environment x-at-release x-at-user scores langfuse session',
  },
  {
    id: 'errors',
    group: 'gateway',
    title: 'Errors',
    description: 'Error envelope, status codes and common causes.',
    body: 'errors error envelope status code 400 401 403 404 409 429 502 503 unknown_endpoint model_not_found rate_limit_exceeded budget',
  },

  // ─── Integrations ───
  {
    id: 'opencode-config',
    group: 'integrations',
    title: 'opencode',
    description: 'Generate an opencode.json provider block.',
    body: 'opencode config json provider openai-compatible baseurl models discovery plugin litellm v2 providers package settings plugins',
  },
  {
    id: 'mcp-configuration',
    group: 'integrations',
    title: 'MCP servers',
    description: 'Attach an AT-hosted MCP server to an MCP client.',
    body: 'mcp model context protocol remote server opencode.json claude code authorization public private generate_image oauth sign in',
  },
  {
    id: 'claude-marketplace',
    group: 'integrations',
    title: 'Claude Code marketplace',
    description: 'Install public MCP servers as Claude Code plugins.',
    body: 'claude code plugin marketplace zip manifest json install reload-plugins offline',
  },
];

export const apiSectionIds = apiSections.map((s) => s.id);

export function findApiSection(id: string): ApiSectionMeta | undefined {
  return apiSections.find((s) => s.id === id);
}

export function sectionsInGroup(group: ApiGroupId): ApiSectionMeta[] {
  return apiSections.filter((s) => s.group === group);
}
