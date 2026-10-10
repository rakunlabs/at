<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import { claudeMarketplaceCommands } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  const jsonUrl = $derived(`${baseUrl}/gateway/v1/claude-code/marketplace.json`);
  const zipUrl = $derived(`${baseUrl}/gateway/v1/claude-code/marketplace.zip`);
  const commands = $derived(claudeMarketplaceCommands(jsonUrl, zipUrl));
</script>

<div class="docs-prose">
  <p>
    Every <strong>public</strong> MCP server is also published as a Claude Code plugin. Add this
    instance as a marketplace once, and your team can install any of them with
    <code>/plugin install</code>.
  </p>
</div>

<dl class="grid gap-px border border-dark-border bg-dark-border">
  <div class="bg-dark-base px-3 py-2.5">
    <dt class="text-[11px] text-dark-text-muted">Marketplace manifest</dt>
    <dd class="mt-1 break-all font-mono text-[12.5px] text-dark-text">{jsonUrl}</dd>
  </div>
  <div class="bg-dark-base px-3 py-2.5">
    <dt class="text-[11px] text-dark-text-muted">Offline export (ZIP)</dt>
    <dd class="mt-1 break-all font-mono text-[12.5px] text-dark-text">{zipUrl}</dd>
  </div>
</dl>

<DocsCodeBlock code={commands} lang="bash" label="Claude Code" copyLabel="Copy marketplace commands" />

<div class="docs-prose">
  <p>
    The ZIP uses relative plugin sources, so it can be unzipped locally or committed to a Git
    repository and shared as a team marketplace. A single server is also available as
    <code>/gateway/v1/claude-code/plugins/&lt;name&gt;/plugin.zip</code>.
  </p>
</div>

<DocsCallout>
  <p>
    Only need the tools, not a plugin? Connect the server directly — see
    <a href="#/docs?section=mcp-configuration">MCP servers</a>.
  </p>
</DocsCallout>
