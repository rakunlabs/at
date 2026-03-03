<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { Database, Cpu, Layers, MessageSquare, ArrowRight, RefreshCw } from 'lucide-svelte';
  import DataTable from '@/lib/components/DataTable.svelte';

  storeNavbar.title = 'Dashboard';

  let basePath = $derived(
    window.location.pathname.replace(/\/+$/, '')
  );

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
    providers.reduce((sum, p) => sum + (p.models && p.models.length > 0 ? p.models.length : 1), 0)
  );

  let providerTypes = $derived(
    [...new Set(providers.map((p) => p.type))]
  );
</script>

<svelte:head>
  <title>AT | Dashboard</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Stats -->
  <div class="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
    <!-- Providers count -->
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100 dark:bg-dark-elevated">
          <Cpu size={14} class="text-gray-500 dark:text-dark-text-muted" />
        </div>
        <span class="text-xs text-gray-500 dark:text-dark-text-muted uppercase tracking-wider font-medium">Providers</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200 dark:text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900 dark:text-dark-text">{providers.length}</div>
        <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">
          {#if providerTypes.length > 0}
            {providerTypes.join(', ')}
          {:else}
            none configured
          {/if}
        </div>
      {/if}
    </div>

    <!-- Models count -->
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100 dark:bg-dark-elevated">
          <Layers size={14} class="text-gray-500 dark:text-dark-text-muted" />
        </div>
        <span class="text-xs text-gray-500 dark:text-dark-text-muted uppercase tracking-wider font-medium">Models</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200 dark:text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900 dark:text-dark-text">{totalModels}</div>
        <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">across {providers.length} provider{providers.length !== 1 ? 's' : ''}</div>
      {/if}
    </div>

    <!-- Store -->
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-5">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100 dark:bg-dark-elevated">
          <Database size={14} class="text-gray-500 dark:text-dark-text-muted" />
        </div>
        <span class="text-xs text-gray-500 dark:text-dark-text-muted uppercase tracking-wider font-medium">Store</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200 dark:text-dark-text-faint">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900 dark:text-dark-text capitalize">{storeType}</div>
        <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">
          {storeType === 'postgres' || storeType === 'sqlite' ? 'persistent storage active' : storeType === 'memory' ? 'in-memory (non-persistent)' : 'YAML config only'}
        </div>
      {/if}
    </div>
  </div>

  <!-- Quick actions -->
  <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6">
    <a
      href="#/chat"
      class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 hover:border-gray-300 dark:hover:border-dark-border-subtle hover:shadow-sm flex items-center justify-between group transition-all"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-gray-100 dark:bg-dark-elevated group-hover:bg-gray-200 dark:group-hover:bg-dark-highest transition-colors">
          <MessageSquare size={16} class="text-gray-600 dark:text-dark-text-secondary" />
        </div>
        <div>
          <div class="font-medium text-sm text-gray-900 dark:text-dark-text">Chat</div>
          <div class="text-xs text-gray-500 dark:text-dark-text-muted">Send messages to your providers</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-gray-300 dark:text-dark-text-faint group-hover:text-gray-500 dark:group-hover:text-dark-text-muted transition-colors" />
    </a>

    <a
      href="#/providers"
      class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 hover:border-gray-300 dark:hover:border-dark-border-subtle hover:shadow-sm flex items-center justify-between group transition-all"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-gray-100 dark:bg-dark-elevated group-hover:bg-gray-200 dark:group-hover:bg-dark-highest transition-colors">
          <Cpu size={16} class="text-gray-600 dark:text-dark-text-secondary" />
        </div>
        <div>
          <div class="font-medium text-sm text-gray-900 dark:text-dark-text">Manage Providers</div>
          <div class="text-xs text-gray-500 dark:text-dark-text-muted">Add, edit, or remove LLM providers</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-gray-300 dark:text-dark-text-faint group-hover:text-gray-500 dark:group-hover:text-dark-text-muted transition-colors" />
    </a>
  </div>

  <!-- Provider list -->
  <div class="flex items-center justify-between mb-2 mt-6 px-1">
    <span class="text-sm font-medium text-gray-900 dark:text-dark-text">Registered Providers</span>
    <button
      onclick={load}
      class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors rounded"
      title="Refresh"
    >
      <RefreshCw size={14} />
    </button>
  </div>

  <DataTable
    items={providers}
    {loading}
    emptyIcon={Layers}
    emptyTitle="No providers registered"
  >
    {#snippet emptyAction()}
      <a href="#/providers" class="text-sm text-gray-500 dark:text-accent-text hover:text-gray-900 dark:hover:text-accent underline underline-offset-2 transition-colors">
        Add your first provider
      </a>
    {/snippet}

    {#snippet header()}
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Provider</th>
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Type</th>
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Default Model</th>
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Models</th>
    {/snippet}

    {#snippet row(p)}
      <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
        <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{p.key}</td>
        <td class="px-4 py-2.5">
          <span class="px-2 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary font-mono">{p.type}</span>
        </td>
        <td class="px-4 py-2.5 font-mono text-xs text-gray-600 dark:text-dark-text-secondary">{p.default_model}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
          {#if p.models && p.models.length > 0}
            {p.models.length} model{p.models.length !== 1 ? 's' : ''}
            <span class="text-gray-400 dark:text-dark-text-muted ml-1" title={p.models.join(', ')}>{p.models.slice(0, 3).join(', ')}{p.models.length > 3 ? '...' : ''}</span>
          {:else}
            1 model
          {/if}
        </td>
      </tr>
    {/snippet}
  </DataTable>

  <!-- API endpoint info -->
  <div class="mt-4 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
      <span class="text-sm font-medium text-gray-900 dark:text-dark-text">API Endpoints</span>
    </div>
    <div class="p-4 space-y-2.5 text-sm font-mono">
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 text-green-700 dark:text-green-300 font-medium">POST</span>
        <span class="text-gray-700 dark:text-dark-text-secondary">{basePath}/gateway/v1/chat/completions</span>
      </div>
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 w-12 text-center px-2 py-0.5 text-xs bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 text-blue-700 dark:text-blue-300 font-medium">GET</span>
        <span class="text-gray-700 dark:text-dark-text-secondary">{basePath}/gateway/v1/models</span>
      </div>
      <div class="border-t border-gray-100 dark:border-dark-border pt-2.5 mt-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-sans leading-relaxed">
        Use the model format <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-gray-700 dark:text-dark-text-secondary">provider_key/model_name</code> (e.g., <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-gray-700 dark:text-dark-text-secondary">anthropic/claude-haiku-4-5</code>).
      </div>
    </div>
  </div>
</div>