<script lang="ts">
  import type { MCPServer } from '@/lib/api/mcp-servers';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsCodeTabs from './DocsCodeTabs.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import { opencodeMcpConfig } from './snippets';

  interface Props {
    baseUrl: string;
    servers: MCPServer[];
    /** Lifted so the chosen server survives navigating away and back. */
    selectedName?: string;
  }

  let { baseUrl, servers, selectedName = $bindable('') }: Props = $props();

  let client = $state('opencode');

  const selected = $derived(servers.find((s) => s.name === selectedName));
  const name = $derived(selectedName || 'management');
  const isPublic = $derived(Boolean(selected?.public));
  const oauth = $derived(Boolean(selected?.config?.oauth?.enabled));
  const url = $derived(`${baseUrl}/gateway/v1/mcp/${name}`);

  const claudeCommand = $derived(
    isPublic || oauth
      ? `claude mcp add --transport http at-${name} ${url}`
      : `claude mcp add --transport http at-${name} ${url} \\\n  --header "Authorization: Bearer at_xxxxx"`,
  );
  const genericConfig = $derived(
    JSON.stringify(
      {
        mcpServers: {
          [`at-${name}`]: isPublic || oauth
            ? { type: 'http', url }
            : { type: 'http', url, headers: { Authorization: 'Bearer at_xxxxx' } },
        },
      },
      null,
      2,
    ),
  );

  const tabs = $derived([
    {
      id: 'opencode',
      label: 'opencode',
      lang: 'json',
      code: opencodeMcpConfig({ baseUrl, serverName: selectedName, isPublic: isPublic || oauth }),
    },
    { id: 'claude', label: 'Claude Code', lang: 'bash', code: claudeCommand },
    { id: 'json', label: 'Cursor / mcp.json', lang: 'json', code: genericConfig },
  ]);
</script>

<div class="docs-prose">
  <p>
    An <a href="#/mcp-servers">MCP server</a> publishes a selection of AT tools — MCP sets, built-in
    tools, workflows — at <code>/gateway/v1/mcp/&lt;name&gt;</code> using the Streamable HTTP
    transport. Any MCP client can connect to it.
  </p>
</div>

{#if servers.length > 0}
  <div class="flex flex-wrap items-center gap-2">
    <label for="docs-mcp-server" class="text-xs text-dark-text-muted">Server</label>
    <select
      id="docs-mcp-server"
      bind:value={selectedName}
      class="h-8 border border-dark-border-subtle bg-dark-base px-2 text-xs text-dark-text focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
    >
      {#each servers as s (s.id)}
        <option value={s.name}>{s.name}{s.public ? ' (public)' : ''}</option>
      {/each}
    </select>
    <span class="text-[11px] text-dark-text-muted">
      {isPublic ? 'Public — no token needed' : oauth ? 'Sign in with an AT account' : 'Requires an API token'}
    </span>
  </div>
{:else}
  <p class="text-sm leading-relaxed text-dark-text-secondary">
    No MCP servers are configured yet — the examples use <code class="text-oc-peach">management</code>
    as a placeholder. Create one on the
    <a href="#/mcp-servers" class="text-accent-text underline underline-offset-2">MCP servers</a> page.
  </p>
{/if}

<DocsCodeTabs {tabs} bind:active={client} name="mcp" />

<div class="docs-prose">
  <h3>Authentication</h3>
  <ul>
    <li><strong>API token</strong> — the default. Tools run under the server’s <em>Run as</em> identity.</li>
    <li>
      <strong>Sign-in</strong> — when the server enables it, MCP clients that support OAuth open a
      browser and ask you to approve access. Tools then run as <em>your</em> account, so connected
      services (GitHub, GitLab…) use your own credentials. Manage approved apps under
      <a href="#/connections">Connections</a>.
    </li>
    <li><strong>Public</strong> — anyone who can reach the URL may call the server without a token.</li>
  </ul>
</div>

<div class="docs-prose">
  <h3>Generating images from a coding agent</h3>
  <ol>
    <li>Add the <code>generate_image</code> built-in tool to an MCP server and set its Run as identity.</li>
    <li>Make sure an image-capable provider exists (OpenAI with an API key or ChatGPT sign-in, or MiniMax) and media storage is enabled.</li>
    <li>Connect the server as above.</li>
  </ol>
  <p>
    The tool result includes a preview the model can see and a <code>download_url</code> that works
    without the token for 24 hours:
  </p>
</div>
<DocsCodeBlock
  code={`curl -fsSL -o fox.png "<download_url>"`}
  lang="bash"
  label="Save a generated image"
  copyLabel="Copy command"
/>

<DocsCallout>
  <p>
    Long tool calls are kept alive automatically: after 10 seconds the response switches to a
    stream with keep-alive messages, so proxies and client timeouts do not cut it off.
  </p>
</DocsCallout>
