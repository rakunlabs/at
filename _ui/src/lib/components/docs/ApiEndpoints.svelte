<script lang="ts">
  import DocsEndpoint from './DocsEndpoint.svelte';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  interface Route {
    method: string;
    path: string;
    summary: string;
    section?: string;
  }

  const groups: { title: string; routes: Route[] }[] = [
    {
      title: 'Inference',
      routes: [
        { method: 'POST', path: '/gateway/v1/chat/completions', summary: 'OpenAI Chat Completions, sync or streaming.', section: 'chat-completions' },
        { method: 'POST', path: '/gateway/v1/responses', summary: 'OpenAI Responses API (stateless; no previous_response_id).', section: 'chat-completions' },
        { method: 'POST', path: '/gateway/v1/messages', summary: 'Native Anthropic Messages API.', section: 'anthropic-messages' },
        { method: 'POST', path: '/gateway/v1/embeddings', summary: 'Vector embeddings for a string or a batch.', section: 'embeddings' },
        { method: 'POST', path: '/gateway/v1/decisions', summary: 'Typed System 1 decisions.', section: 'decisions' },
      ],
    },
    {
      title: 'Media',
      routes: [
        { method: 'POST', path: '/gateway/v1/images/generations', summary: 'Generate images.', section: 'media' },
        { method: 'POST', path: '/gateway/v1/audio/speech', summary: 'Text to speech; returns raw audio bytes.', section: 'media' },
        { method: 'POST', path: '/gateway/v1/audio/transcriptions', summary: 'Speech to text (multipart upload).', section: 'media' },
        { method: 'POST', path: '/gateway/v1/moderations', summary: 'Classify text against a moderation model.', section: 'media' },
        { method: 'POST', path: '/gateway/v1/rerank', summary: 'Order documents by relevance to a query.', section: 'media' },
        { method: 'GET', path: '/gateway/v1/media/{id}', summary: 'Download media this token generated.', section: 'media' },
      ],
    },
    {
      title: 'Discovery & health',
      routes: [
        { method: 'GET', path: '/gateway/v1/models', summary: 'OpenAI-shape model list, filtered to the token.', section: 'available-models' },
        { method: 'GET', path: '/gateway/v1/model/info', summary: 'LiteLLM-shape metadata: limits, reasoning, prices.', section: 'available-models' },
        { method: 'GET', path: '/gateway/v1/health', summary: 'Liveness and per-provider state. No token needed.' },
        { method: 'GET', path: '/gateway/v1/health/{provider}', summary: 'Whether one provider is configured and available.' },
      ],
    },
    {
      title: 'Tracing',
      routes: [
        { method: 'POST', path: '/gateway/v1/scores', summary: 'Attach a quality score to a trace this token produced.', section: 'tracing' },
      ],
    },
    {
      title: 'Passthrough & MCP',
      routes: [
        { method: 'ANY', path: '/gateway/v1/providers/{provider}/*', summary: 'Forward to the provider’s native API with its credentials.', section: 'proxy' },
        { method: 'POST', path: '/gateway/v1/mcp/{name}', summary: 'Streamable HTTP MCP endpoint of an MCP server.', section: 'mcp-configuration' },
        { method: 'GET', path: '/gateway/v1/mcp/{name}/ws', summary: 'Raw WebSocket passthrough, when the server defines one.', section: 'mcp-configuration' },
        { method: 'GET', path: '/gateway/v1/claude-code/marketplace.json', summary: 'Claude Code plugin marketplace of public MCP servers.', section: 'claude-marketplace' },
      ],
    },
  ];
</script>

<div class="docs-prose">
  <p>
    All routes live under <code>{baseUrl}/gateway</code> and authenticate with an API token. An
    unknown path answers <code>404 unknown_endpoint</code> naming the correct base URL instead of
    returning the web UI.
  </p>
</div>

<div class="space-y-5">
  {#each groups as g (g.title)}
    <section class="space-y-1.5">
      <h2 class="text-xs font-medium text-dark-text-muted">{g.title}</h2>
      {#each g.routes as r (r.method + r.path)}
        {#if r.section}
          <a
            href={`#/docs?section=${r.section}`}
            class="block hover:[&>div]:bg-dark-surface focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent"
          >
            <DocsEndpoint method={r.method} path={r.path} summary={r.summary} />
          </a>
        {:else}
          <DocsEndpoint method={r.method} path={r.path} summary={r.summary} />
        {/if}
      {/each}
    </section>
  {/each}
</div>
