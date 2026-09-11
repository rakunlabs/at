<script lang="ts">
  import type { MCPServer } from '@/lib/api/mcp-servers';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import { opencodeMcpConfig } from './snippets';

  interface Props {
    baseUrl: string;
    servers: MCPServer[];
    /** Lifted so the chosen server survives navigating away and back. */
    selectedName?: string;
  }

  let { baseUrl, servers, selectedName = $bindable('') }: Props = $props();

  const selected = $derived(servers.find((s) => s.name === selectedName));
  const config = $derived(
    opencodeMcpConfig({
      baseUrl,
      serverName: selectedName,
      isPublic: Boolean(selected?.public),
    }),
  );

  const linkClass =
    'text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent';
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Add an AT-hosted MCP server to opencode by extending your
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >opencode.json</code
  >
  with an
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >mcp</code
  >
  entry of type
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >remote</code
  >. Private servers include an
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >Authorization</code
  > header; public servers omit it.
</p>

{#if servers.length > 1}
  <div>
    <label for="docs-mcp-server" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">
      MCP server
    </label>
    <select
      id="docs-mcp-server"
      bind:value={selectedName}
      class="mt-1 w-full border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 sm:w-64 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:focus-visible:outline-accent"
    >
      {#each servers as s (s.id)}
        <option value={s.name}>{s.name}{s.public ? ' (public)' : ''}</option>
      {/each}
    </select>
  </div>
{:else if servers.length === 0}
  <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    No MCP servers are configured yet — the example below uses
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >management</code
    >
    as a placeholder. Add servers on the
    <a href="#/mcp-servers" class={linkClass}>MCP Servers</a> page.
  </p>
{/if}

<DocsCodeBlock
  code={config}
  lang="json"
  label="~/.config/opencode/opencode.json"
  copyLabel="Copy MCP config"
/>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  The full endpoint list is on the <a href="#/mcp-servers" class={linkClass}>MCP Servers</a> page. Each
  entry advertises its own
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >/gateway/v1/mcp/&lt;name&gt;</code
  > URL — point opencode at any of them.
</p>
