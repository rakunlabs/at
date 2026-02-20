<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { Database, Cpu, Layers, MessageSquare, ArrowRight, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Dashboard';

  let providers = $state<InfoProvider[]>([]);
  let storeType = $state('');
  let loading = $state(true);

  async function load() {
    loading = true;
    try {
      const info = await getInfo();
      providers = info.providers;
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

<div class="p-6 max-w-5xl mx-auto">
  <!-- Stats -->
  <div class="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
    <!-- Providers count -->
    <div class="border border-gray-200 bg-white p-5 shadow-sm">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100">
          <Cpu size={14} class="text-gray-500" />
        </div>
        <span class="text-xs text-gray-500 uppercase tracking-wider font-medium">Providers</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900">{providers.length}</div>
        <div class="text-xs text-gray-500 mt-1">
          {#if providerTypes.length > 0}
            {providerTypes.join(', ')}
          {:else}
            none configured
          {/if}
        </div>
      {/if}
    </div>

    <!-- Models count -->
    <div class="border border-gray-200 bg-white p-5 shadow-sm">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100">
          <Layers size={14} class="text-gray-500" />
        </div>
        <span class="text-xs text-gray-500 uppercase tracking-wider font-medium">Models</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900">{totalModels}</div>
        <div class="text-xs text-gray-500 mt-1">across {providers.length} provider{providers.length !== 1 ? 's' : ''}</div>
      {/if}
    </div>

    <!-- Store -->
    <div class="border border-gray-200 bg-white p-5 shadow-sm">
      <div class="flex items-center gap-2 mb-2">
        <div class="p-1.5 bg-gray-100">
          <Database size={14} class="text-gray-500" />
        </div>
        <span class="text-xs text-gray-500 uppercase tracking-wider font-medium">Store</span>
      </div>
      {#if loading}
        <div class="text-2xl font-bold text-gray-200">--</div>
      {:else}
        <div class="text-2xl font-bold text-gray-900 capitalize">{storeType}</div>
        <div class="text-xs text-gray-500 mt-1">
          {storeType === 'postgres' ? 'persistent storage active' : 'YAML config only'}
        </div>
      {/if}
    </div>
  </div>

  <!-- Quick actions -->
  <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6">
    <a
      href="#/test"
      class="border border-gray-200 bg-white p-4 hover:border-gray-300 hover:shadow-sm flex items-center justify-between group transition-all"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-gray-100 group-hover:bg-gray-200 transition-colors">
          <MessageSquare size={16} class="text-gray-600" />
        </div>
        <div>
          <div class="font-medium text-sm text-gray-900">Chat Test</div>
          <div class="text-xs text-gray-500">Send messages to your providers</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-gray-300 group-hover:text-gray-500 transition-colors" />
    </a>

    <a
      href="#/providers"
      class="border border-gray-200 bg-white p-4 hover:border-gray-300 hover:shadow-sm flex items-center justify-between group transition-all"
    >
      <div class="flex items-center gap-3">
        <div class="p-2 bg-gray-100 group-hover:bg-gray-200 transition-colors">
          <Cpu size={16} class="text-gray-600" />
        </div>
        <div>
          <div class="font-medium text-sm text-gray-900">Manage Providers</div>
          <div class="text-xs text-gray-500">Add, edit, or remove LLM providers</div>
        </div>
      </div>
      <ArrowRight size={16} class="text-gray-300 group-hover:text-gray-500 transition-colors" />
    </a>
  </div>

  <!-- Provider list -->
  <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
    <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200">
      <span class="text-sm font-medium text-gray-900">Registered Providers</span>
      <button
        onclick={load}
        class="p-1 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
    </div>
    {#if loading}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
    {:else if providers.length === 0}
      <div class="px-4 py-10 text-center">
        <div class="text-gray-400 mb-2">No providers registered</div>
        <a href="#/providers" class="text-sm text-gray-500 hover:text-gray-900 underline underline-offset-2 transition-colors">
          Add your first provider
        </a>
      </div>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-100 bg-gray-50/50">
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Provider</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Type</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Default Model</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Models</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-50">
          {#each providers as p}
            <tr class="hover:bg-gray-50/50 transition-colors">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900">{p.key}</td>
              <td class="px-4 py-2.5">
                <span class="px-2 py-0.5 text-xs bg-gray-100 text-gray-600 font-mono">{p.type}</span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-gray-600">{p.default_model}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                {#if p.models && p.models.length > 0}
                  {p.models.length} model{p.models.length !== 1 ? 's' : ''}
                  <span class="text-gray-400 ml-1" title={p.models.join(', ')}>{p.models.slice(0, 3).join(', ')}{p.models.length > 3 ? '...' : ''}</span>
                {:else}
                  1 model
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>

  <!-- API endpoint info -->
  <div class="mt-4 border border-gray-200 bg-white shadow-sm overflow-hidden">
    <div class="px-4 py-3 border-b border-gray-200">
      <span class="text-sm font-medium text-gray-900">API Endpoints</span>
    </div>
    <div class="p-4 space-y-2.5 text-sm font-mono">
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 px-2 py-0.5 text-xs bg-green-50 border border-green-200 text-green-700 font-medium">POST</span>
        <span class="text-gray-700">/v1/chat/completions</span>
      </div>
      <div class="flex items-center gap-2.5">
        <span class="shrink-0 px-2 py-0.5 text-xs bg-blue-50 border border-blue-200 text-blue-700 font-medium">GET</span>
        <span class="text-gray-700">/v1/models</span>
      </div>
      <div class="border-t border-gray-100 pt-2.5 mt-2.5 text-xs text-gray-500 font-sans leading-relaxed">
        Use the model format <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-700">provider_key/model_name</code> (e.g., <code class="font-mono bg-gray-100 px-1.5 py-0.5 text-gray-700">anthropic/claude-haiku-4-5</code>)
      </div>
    </div>
  </div>
</div>
