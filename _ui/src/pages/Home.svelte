<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { Database, Cpu, Layers, MessageSquare, ArrowRight, RefreshCw } from 'lucide-svelte';
  import DataTable from '@/lib/components/DataTable.svelte';
  import { deploymentOrigin } from '@/lib/helper/deployment-url';

  storeNavbar.title = 'Dashboard';

  // Deployment root, not the current route: location.pathname would show the
  // SPA fallback path on a deep link.
  let basePath = $derived(deploymentOrigin());

  let providers = $state<InfoProvider[]>([]);
  let storeType = $state('');
  let loading = $state(true);

  async function load() {
    loading = true;
    try {
      const info = await getInfo();
      providers = info.providers || [];
      storeType = info.store_type;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load info', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  let totalModels = $derived(
    providers.reduce((sum, p) => sum + (p.models && p.models.length > 0 ? p.models.length : p.default_model ? 1 : 0), 0)
  );

  let providerTypes = $derived(
    [...new Set(providers.map((p) => p.type))]
  );
</script>

<svelte:head>
  <title>AT | Dashboard</title>
</svelte:head>

<div class="w-full min-w-0 p-2 sm:p-4 max-w-6xl mx-auto">
  <!-- Stats -->
  <div class="grid grid-cols-2 sm:grid-cols-3 gap-3 sm:gap-4 mb-6">
    <!-- Providers count -->
    <div class="min-w-0 border border-dark-border p-4 sm:p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-dark-elevated">
          <Cpu size={14} class="text-dark-text-muted" />
        </div>
        <span class="text-xs text-dark-text-muted font-medium">Providers</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-dark-text">{providers.length}</div>
        <div class="break-words text-xs text-dark-text-muted mt-1">
          {#if providerTypes.length > 0}
            {providerTypes.join(', ')}
          {:else}
            none configured
          {/if}
        </div>
      {/if}
    </div>

    <!-- Models count -->
    <div class="min-w-0 border border-dark-border p-4 sm:p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-dark-elevated">
          <Layers size={14} class="text-dark-text-muted" />
        </div>
        <span class="text-xs text-dark-text-muted font-medium">Models</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-dark-text">{totalModels}</div>
        <div class="text-xs text-dark-text-muted mt-1">across {providers.length} provider{providers.length !== 1 ? 's' : ''}</div>
      {/if}
    </div>

    <!-- Store -->
    <div class="col-span-2 sm:col-span-1 min-w-0 border border-dark-border p-4 sm:p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-dark-elevated">
          <Database size={14} class="text-dark-text-muted" />
        </div>
        <span class="text-xs text-dark-text-muted font-medium">Store</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-dark-text capitalize">{storeType}</div>
        <div class="text-xs text-dark-text-muted mt-1">
          {storeType === 'postgres' || storeType === 'sqlite' ? 'persistent storage active' : storeType === 'memory' ? 'in-memory (non-persistent)' : 'YAML config only'}
        </div>
      {/if}
    </div>
  </div>

  <!-- Quick actions -->
  <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6">
    <a
      href="#/chats"
      class="border border-dark-border p-4 hover:border-dark-border-subtle flex items-center justify-between group"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-dark-elevated group-hover:bg-dark-highest">
          <MessageSquare size={16} class="text-dark-text-secondary" />
        </div>
        <div>
          <div class="font-medium text-sm text-dark-text">Chats</div>
          <div class="text-xs text-dark-text-muted">Test models with MCP servers, skills and tools</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-dark-text-faint group-hover:text-dark-text-muted" />
    </a>

    <a
      href="#/providers"
      class="border border-dark-border p-4 hover:border-dark-border-subtle flex items-center justify-between group"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-dark-elevated group-hover:bg-dark-highest">
          <Cpu size={16} class="text-dark-text-secondary" />
        </div>
        <div>
          <div class="font-medium text-sm text-dark-text">Manage Providers</div>
          <div class="text-xs text-dark-text-muted">Add, edit, or remove LLM providers</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-dark-text-faint group-hover:text-dark-text-muted" />
    </a>
  </div>

  <!-- Provider list -->
  <div class="flex items-center justify-between mb-2 mt-6 px-1">
    <span class="text-sm font-medium text-dark-text">Registered Providers</span>
    <button
      onclick={load}
      class="inline-flex size-11 sm:size-7 items-center justify-center hover:bg-dark-elevated text-dark-text-secondary hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
      title="Refresh"
      aria-label="Refresh providers"
      disabled={loading}
    >
      <RefreshCw size={14} />
    </button>
  </div>

  <DataTable
    items={providers}
    tableLabel="Registered providers and models"
    tableClass="table-fixed min-w-[40rem]"
    {loading}
    emptyIcon={Layers}
    emptyTitle="No providers registered"
  >
    {#snippet emptyAction()}
      <a href="#/providers" class="text-sm text-accent-text hover:text-accent underline underline-offset-2">
        Add your first provider
      </a>
    {/snippet}

    {#snippet header()}
      <th scope="col" class="w-[22%] text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Provider</th>
      <th scope="col" class="w-[18%] text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Type</th>
      <th scope="col" class="w-[30%] text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Default Model</th>
      <th scope="col" class="w-[30%] text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Models</th>
    {/snippet}

    {#snippet row(p)}
      <tr class="hover:bg-dark-surface">
        <td class="px-4 py-2.5 break-all align-top font-mono font-medium text-dark-text">{p.key}{#if p.shared}<span class="ml-2 font-sans text-xs font-normal text-dark-text-muted">Shared</span>{/if}</td>
        <td class="px-4 py-2.5 break-all align-top">
          <span class="px-2 py-0.5 text-xs bg-dark-elevated text-dark-text-secondary font-mono">{p.type}</span>
        </td>
        <td class="px-4 py-2.5 break-all align-top font-mono text-sm sm:text-xs text-dark-text-secondary">{p.default_model}</td>
        <td class="px-4 py-2.5 align-top text-sm sm:text-xs text-dark-text-secondary">
          {#if p.models && p.models.length > 0}
            <details>
              <summary class="min-h-11 sm:min-h-0 cursor-pointer py-2.5 sm:py-0 font-medium text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent">{p.models.length} model{p.models.length !== 1 ? 's' : ''}</summary>
              <ul class="mt-2 space-y-2 font-mono break-all">
                {#each p.models as model}<li>{model}</li>{/each}
              </ul>
            </details>
          {:else}
            {p.default_model ? '1 model' : 'No models configured'}
          {/if}
        </td>
      </tr>
    {/snippet}
  </DataTable>

  <!-- API endpoint info -->
  <div class="mt-4 border border-dark-border overflow-hidden">
    <div class="px-4 py-3 border-b border-dark-border">
      <span class="text-sm font-medium text-dark-text">API Endpoints</span>
    </div>
    <div class="p-4 space-y-2.5 text-sm font-mono">
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-green-900/20 border border-green-800 text-green-300 font-medium">POST</span>
        <span class="min-w-0 break-all text-dark-text-secondary">{basePath}/gateway/v1/chat/completions</span>
      </div>
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-blue-900/20 border border-blue-800 text-blue-300 font-medium">GET</span>
        <span class="min-w-0 break-all text-dark-text-secondary">{basePath}/gateway/v1/models</span>
      </div>
      <div class="break-words [overflow-wrap:anywhere] border-t border-dark-border pt-2.5 mt-2.5 text-xs text-dark-text-muted font-sans leading-relaxed">
        Use the model format <code class="font-mono bg-dark-elevated px-1.5 py-0.5 text-dark-text-secondary">provider_key/model_name</code> (e.g., <code class="font-mono bg-dark-elevated px-1.5 py-0.5 text-dark-text-secondary">anthropic/claude-haiku-4-5</code>).
      </div>
    </div>
  </div>
</div>
