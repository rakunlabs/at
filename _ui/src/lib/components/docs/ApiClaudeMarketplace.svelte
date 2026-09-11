<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import { claudeMarketplaceCommands } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  const jsonUrl = $derived(`${baseUrl}/gateway/v1/claude-code/marketplace.json`);
  const zipUrl = $derived(`${baseUrl}/gateway/v1/claude-code/marketplace.zip`);
  const commands = $derived(claudeMarketplaceCommands(jsonUrl, zipUrl));

  const codeClass =
    'font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary';
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Public MCP Servers are exported as Claude Code plugin packages. The marketplace JSON points plugin
  sources at AT-hosted ZIP downloads, so Claude Code can install plugins directly from this AT
  instance. The ZIP export remains available for offline/local marketplaces because its
  <code class={codeClass}>marketplace.json</code>
  uses relative plugin sources. Unzip it, add the directory locally, or commit the generated directory
  to a Git repo and share that repo as your team marketplace.
</p>

<dl class="grid gap-px border border-gray-200 dark:border-dark-border bg-gray-200 dark:bg-dark-border">
  <div class="bg-white dark:bg-dark-surface p-3">
    <dt class="text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary">
      Marketplace ZIP
    </dt>
    <dd class="mt-1 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">{zipUrl}</dd>
  </div>
  <div class="bg-white dark:bg-dark-surface p-3">
    <dt class="text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary">
      Manifest JSON
    </dt>
    <dd class="mt-1 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">{jsonUrl}</dd>
  </div>
</dl>

<DocsCodeBlock
  code={commands}
  lang="bash"
  label="Claude Code"
  copyLabel="Copy marketplace commands"
/>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  For a one-off session, each public MCP Server also exposes a per-server plugin ZIP at
  <code class={codeClass}>/gateway/v1/claude-code/plugins/&lt;name&gt;/plugin.zip</code>. Direct remote
  MCP configuration remains the better fit for agents that only need tools, not Claude plugin
  installation.
</p>
