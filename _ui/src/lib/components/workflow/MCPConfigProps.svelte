<script lang="ts">
  import { listMCPSets, type MCPSet } from '@/lib/api/mcp-sets';

  let { data }: { data: Record<string, any> } = $props();

  let sets = $state<MCPSet[]>([]);
  let loading = $state(true);
  let loadError = $state('');

  async function loadSets() {
    loading = true;
    loadError = '';
    try {
      const res = await listMCPSets({ _limit: 500 });
      sets = res.data ?? [];
    } catch (e: any) {
      loadError = e?.response?.data?.message || 'Failed to load MCP sets';
    } finally {
      loading = false;
    }
  }

  loadSets();

  let selected = $derived<string[]>(Array.isArray(data.mcp_sets) ? data.mcp_sets : []);
  let missing = $derived(loading ? [] : selected.filter(name => !sets.some(s => s.name === name)));
  let legacyURLs = $derived<string[]>(Array.isArray(data.mcp_urls) ? data.mcp_urls : []);

  function toggle(name: string, checked: boolean) {
    data.mcp_sets = checked ? [...selected.filter(n => n !== name), name] : selected.filter(n => n !== name);
  }
</script>

<div>
  <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">MCP sets</span>
  {#if loading}
    <div class="mt-0.5 text-[11px] text-dark-text-muted">Loading MCP sets…</div>
  {:else if loadError}
    <div class="mt-0.5 text-[11px] text-red-400">
      {loadError}
      <button onclick={loadSets} class="ml-1 underline">Retry</button>
    </div>
  {:else if sets.length === 0}
    <div class="mt-0.5 text-[11px] text-dark-text-muted">
      No MCP sets yet. Register your MCP servers on the <a href="#/mcps" class="underline">MCP Sets page</a> first, then select them here.
    </div>
  {:else}
    <div class="mt-0.5 space-y-0.5">
      {#each sets as set (set.id)}
        <label class="flex items-start gap-1.5 text-[11px] text-dark-text-secondary cursor-pointer">
          <input
            type="checkbox"
            checked={selected.includes(set.name)}
            onchange={(e) => toggle(set.name, (e.target as HTMLInputElement).checked)}
            class="mt-0.5 border-dark-border-subtle"
          />
          <span class="min-w-0">
            <span class="font-mono">{set.name}</span>
            {#if set.description}<span class="block text-[10px] text-dark-text-muted">{set.description}</span>{/if}
          </span>
        </label>
      {/each}
    </div>
  {/if}
  {#each missing as name (name)}
    <div class="mt-1 flex items-center gap-1.5 border border-amber-900/60 bg-amber-900/10 px-2 py-1 text-[10px] text-amber-300">
      <span class="flex-1 min-w-0 truncate"><span class="font-mono">{name}</span> is not available in this workspace</span>
      <button onclick={() => toggle(name, false)} class="underline shrink-0">Remove</button>
    </div>
  {/each}
</div>

{#if legacyURLs.length > 0}
  <div class="mt-2 border border-amber-900/60 bg-amber-900/10 px-2 py-1.5 text-[10px] text-amber-300">
    <p>This step still has raw MCP URLs from an older version. They keep working, but cannot be edited here. Register them as an MCP set and select it above, then remove them.</p>
    <ul class="mt-1 space-y-0.5">
      {#each legacyURLs as url}<li class="truncate font-mono" title={url}>{url}</li>{/each}
    </ul>
    <button onclick={() => { data.mcp_urls = []; }} class="mt-1 underline">Remove raw URLs</button>
  </div>
{/if}

<div class="mt-2 px-2 py-1.5 bg-green-900/10 border border-green-900/60 text-[10px] text-green-400">
  Connect this node's <span class="font-mono font-medium">mcp</span> output to an Agent Call's <span class="font-mono font-medium">mcp</span> input. Credentials and permissions come from the MCP set.
</div>
