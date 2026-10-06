<script lang="ts">
  import { listIntegrationPacks, getIntegrationPack, installIntegrationPack, createPack, deletePack, type IntegrationPackSummary, type IntegrationPack } from '@/lib/api/integration-packs';
  import { listPackSources, createPackSource, deletePackSource, syncPackSource, type PackSource } from '@/lib/api/pack-sources';
  import { addToast } from '@/lib/store/toast.svelte';
  import { Package, Check, RefreshCw, Wrench, WandSparkles, Bot, Building2, Plus, Trash2, GitBranch, ExternalLink, RefreshCcw } from 'lucide-svelte';

  let packs = $state<IntegrationPackSummary[]>([]);
  let loading = $state(true);
  let expandedSlug = $state<string | null>(null);
  let expandedPack = $state<IntegrationPack | null>(null);
  let installing = $state(false);

  // Install options
  let installSkills = $state(true);
  let installMCPSets = $state(true);
  let installAgents = $state(true);
  let installOrg = $state(false);

  // Pack sources
  let sources = $state<PackSource[]>([]);
  let showAddSource = $state(false);
  let sourceURL = $state('');
  let sourceBranch = $state('main');
  let addingSource = $state(false);
  let addingArpa = $state(false);

  const ARPA_URL = 'https://github.com/rakunlabs/arpa';

  let hasArpa = $derived(sources.some(s => s.url.includes('rakunlabs/arpa')));

  // Create pack form
  let showCreate = $state(false);
  let createSlug = $state('');
  let createName = $state('');
  let createDescription = $state('');
  let createCategory = $state('');

  async function loadPacks() {
    loading = true;
    try {
      packs = await listIntegrationPacks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load integration packs', 'alert');
    } finally {
      loading = false;
    }
  }

  async function toggleExpand(slug: string) {
    if (expandedSlug === slug) {
      expandedSlug = null;
      expandedPack = null;
      return;
    }
    try {
      expandedPack = await getIntegrationPack(slug);
      expandedSlug = slug;
      installSkills = true;
      installMCPSets = true;
      installAgents = true;
      installOrg = false;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load pack details', 'alert');
    }
  }

  async function handleInstall() {
    if (!expandedPack) return;
    installing = true;
    try {
      const agentNames = installAgents && expandedPack.components.agents
        ? expandedPack.components.agents.map(a => a.name)
        : [];
      const result = await installIntegrationPack(expandedPack.slug, {
        skills: installSkills,
        mcp_sets: installMCPSets,
        agents: agentNames,
        organization: installOrg,
      });
      const parts = [];
      if (result.skills_created > 0) parts.push(`${result.skills_created} skills`);
      if (result.mcp_sets_created > 0) parts.push(`${result.mcp_sets_created} MCPs`);
      if (result.agents_created > 0) parts.push(`${result.agents_created} agents`);
      if (result.organization_id) parts.push('1 organization');
      addToast(`Installed: ${parts.join(', ') || 'nothing selected'}`);
      expandedSlug = null;
      expandedPack = null;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to install pack', 'alert');
    } finally {
      installing = false;
    }
  }

  async function handleCreate() {
    try {
      await createPack({ slug: createSlug, name: createName, description: createDescription, category: createCategory });
      addToast(`Pack "${createName}" created`);
      showCreate = false;
      createSlug = ''; createName = ''; createDescription = ''; createCategory = '';
      await loadPacks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create pack', 'alert');
    }
  }

  async function handleDelete(slug: string) {
    try {
      await deletePack(slug);
      addToast(`Pack "${slug}" deleted`);
      await loadPacks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete pack', 'alert');
    }
  }

  async function handleAddArpa() {
    addingArpa = true;
    try {
      await createPackSource({ url: ARPA_URL, branch: 'main', name: 'arpa' });
      addToast('Official packs added. Syncing...');
      setTimeout(async () => { await loadSources(); await loadPacks(); }, 3000);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add official packs', 'alert');
    } finally {
      addingArpa = false;
    }
  }

  async function loadSources() {
    try {
      const result = await listPackSources();
      sources = result?.data || [];
    } catch { sources = []; }
  }

  async function handleAddSource() {
    if (!sourceURL) return;
    addingSource = true;
    try {
      await createPackSource({ url: sourceURL, branch: sourceBranch || 'main' });
      addToast('Pack source added. Syncing...');
      sourceURL = ''; sourceBranch = 'main'; showAddSource = false;
      // Wait a moment for async clone, then reload.
      setTimeout(async () => { await loadSources(); await loadPacks(); }, 3000);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add source', 'alert');
    } finally {
      addingSource = false;
    }
  }

  async function handleDeleteSource(id: string) {
    try {
      await deletePackSource(id);
      addToast('Pack source removed');
      await loadSources();
      await loadPacks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove source', 'alert');
    }
  }

  async function handleSyncSource(id: string) {
    try {
      await syncPackSource(id);
      addToast('Syncing...');
      setTimeout(async () => { await loadSources(); await loadPacks(); }, 3000);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to sync', 'alert');
    }
  }

  $effect(() => { loadPacks(); loadSources(); });
</script>

<div class="flex flex-col h-full">
  <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border bg-dark-base/50">
    <div class="flex items-center gap-2">
      <Package size={16} class="text-dark-text-muted" />
      <span class="text-sm font-medium text-dark-text">Integration Packs</span>
      <span class="text-xs text-dark-text-muted">({packs.length})</span>
    </div>
    <div class="flex items-center gap-1">
      {#if !hasArpa}
        <button onclick={handleAddArpa} disabled={addingArpa} class="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50 mr-1" title="Add official AT integration packs from rakunlabs/arpa">
          {#if addingArpa}
            <RefreshCw size={12} class="animate-spin" />
          {:else}
            <Package size={12} />
          {/if}
          Official Packs
        </button>
      {/if}
      <button onclick={() => showCreate = !showCreate} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Create Pack">
        <Plus size={14} />
      </button>
      <button onclick={() => showAddSource = !showAddSource} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Add Git Source">
        <GitBranch size={14} />
      </button>
      <button onclick={loadPacks} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Refresh">
        <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
      </button>
    </div>
  </div>

  <!-- Pack Sources Section -->
  {#if sources.length > 0 || showAddSource}
    <div class="mx-4 mt-4 border border-dark-border bg-dark-surface">
      <div class="flex items-center justify-between px-3 py-2 border-b border-dark-border bg-dark-base/50">
        <span class="text-xs font-medium text-dark-text-muted">Git Sources</span>
        <button onclick={() => showAddSource = !showAddSource} class="text-xs text-dark-text-muted hover:text-dark-text-secondary">
          {showAddSource ? 'Cancel' : '+ Add'}
        </button>
      </div>

      {#if showAddSource}
        <div class="p-3 border-b border-dark-border flex items-end gap-2">
          <label class="flex-1 block">
            <span class="block text-[10px] text-dark-text-muted mb-0.5">Repository URL</span>
            <input type="text" bind:value={sourceURL} placeholder="https://github.com/rakunlabs/arpa" class="w-full px-2 py-1 text-xs border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
          </label>
          <label class="w-24 block">
            <span class="block text-[10px] text-dark-text-muted mb-0.5">Branch</span>
            <input type="text" bind:value={sourceBranch} placeholder="main" class="w-full px-2 py-1 text-xs border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
          </label>
          <button onclick={handleAddSource} disabled={!sourceURL || addingSource} class="px-3 py-1 text-xs bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">
            {addingSource ? 'Adding...' : 'Add'}
          </button>
        </div>
      {/if}

      {#each sources as src}
        <div class="flex items-center justify-between px-3 py-2 text-xs border-b border-dark-border last:border-b-0">
          <div class="flex items-center gap-2 min-w-0">
            <GitBranch size={12} class="shrink-0 text-dark-text-muted" />
            <span class="font-medium text-dark-text-secondary truncate">{src.name}</span>
            <span class={["px-1 py-0.5 text-[10px] border", src.status === 'synced' ? 'bg-green-900/20 text-green-400 border-green-800' : src.status === 'error' ? 'bg-red-900/20 text-red-400 border-red-800' : 'bg-yellow-900/20 text-yellow-400 border-yellow-800']}>{src.status}</span>
            {#if src.error}
              <span class="text-[10px] text-red-500 truncate max-w-[200px]" title={src.error}>{src.error}</span>
            {/if}
          </div>
          <div class="flex items-center gap-1 shrink-0">
            {#if src.last_sync}
              <span class="text-[10px] text-dark-text-muted mr-2">{new Date(src.last_sync).toLocaleDateString()}</span>
            {/if}
            <button onclick={() => handleSyncSource(src.id)} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text" title="Sync">
              <RefreshCcw size={12} />
            </button>
            <button onclick={() => handleDeleteSource(src.id)} class="p-1 hover:bg-red-900/20 text-dark-text-muted hover:text-red-400" title="Remove">
              <Trash2 size={12} />
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if showCreate}
    <div class="mx-4 mt-4 p-4 border border-dark-border bg-dark-surface space-y-3">
      <div class="text-xs font-medium text-dark-text-secondary">Create New Pack</div>
      <div class="grid grid-cols-2 gap-3">
        <label class="block">
          <span class="block text-xs text-dark-text-muted mb-1">Slug (folder name)</span>
          <input type="text" bind:value={createSlug} placeholder="my-pack" class="w-full px-3 py-1.5 text-sm border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
        </label>
        <label class="block">
          <span class="block text-xs text-dark-text-muted mb-1">Name</span>
          <input type="text" bind:value={createName} placeholder="My Pack" class="w-full px-3 py-1.5 text-sm border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
        </label>
      </div>
      <label class="block">
        <span class="block text-xs text-dark-text-muted mb-1">Description</span>
        <input type="text" bind:value={createDescription} placeholder="What this pack provides" class="w-full px-3 py-1.5 text-sm border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
      </label>
      <label class="block">
        <span class="block text-xs text-dark-text-muted mb-1">Category</span>
        <input type="text" bind:value={createCategory} placeholder="e.g. Video Production" class="w-full px-3 py-1.5 text-sm border border-dark-border bg-dark-elevated text-dark-text focus:outline-none" />
      </label>
      <div class="flex gap-2 justify-end">
        <button onclick={() => showCreate = false} class="px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
        <button onclick={handleCreate} disabled={!createSlug || !createName} class="px-3 py-1.5 text-xs bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">Create</button>
      </div>
    </div>
  {/if}

  <div class="flex-1 overflow-y-auto p-4">
    {#if loading && packs.length === 0}
      <div class="flex items-center justify-center h-32 text-sm text-dark-text-muted">Loading...</div>
    {:else if packs.length === 0}
      <div class="flex flex-col items-center justify-center py-12 gap-4">
        <Package size={32} class="text-dark-text-muted" />
        <div class="text-center">
          <p class="text-sm text-dark-text-secondary mb-1">No integration packs available</p>
          <p class="text-xs text-dark-text-muted">Add the official pack repository to get started</p>
        </div>
        {#if !hasArpa}
          <button
            onclick={handleAddArpa}
            disabled={addingArpa}
            class="flex items-center gap-2 px-4 py-2 text-sm font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50"
          >
            {#if addingArpa}
              <RefreshCw size={14} class="animate-spin" />
              Adding...
            {:else}
              <Package size={14} />
              Browse Official Packs
            {/if}
          </button>
        {:else}
          <button onclick={async () => { for (const s of sources) { if (s.url.includes('rakunlabs/arpa')) await syncPackSource(s.id); } addToast('Syncing...'); setTimeout(async () => { await loadSources(); await loadPacks(); }, 3000); }} class="flex items-center gap-2 px-4 py-2 text-sm font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">
            <RefreshCcw size={14} />
            Sync Official Packs
          </button>
        {/if}
      </div>
    {:else}
      <div class="grid gap-4 grid-cols-1 md:grid-cols-2 lg:grid-cols-3">
        {#each packs as pack}
          <div class="border border-dark-border bg-dark-surface">
            <div class="p-4">
              <div class="flex items-start justify-between mb-2">
                <div>
                  <div class="flex items-center gap-1.5">
                    <h3 class="text-sm font-medium text-dark-text">{pack.name}</h3>
                  </div>
                  {#if pack.author}
                    <span class="text-[10px] text-dark-text-muted">by {pack.author}</span>
                  {/if}
                </div>
                <div class="flex items-center gap-1.5">
                  <span class="px-1.5 py-0.5 text-[10px] bg-dark-elevated text-dark-text-muted border border-dark-border">v{pack.version}</span>
                  {#if pack.source === 'git'}
                    <span class="px-1.5 py-0.5 text-[10px] bg-blue-900/20 text-blue-400 border border-blue-800">Git</span>
                  {:else if pack.source === 'user'}
                    <span class="px-1.5 py-0.5 text-[10px] bg-green-900/20 text-green-400 border border-green-800">User</span>
                  {/if}
                  {#if pack.source === 'user'}
                    <button onclick={(e: MouseEvent) => { e.stopPropagation(); handleDelete(pack.slug); }} class="p-1 hover:bg-red-900/20 text-dark-text-muted hover:text-red-500" title="Delete pack">
                      <Trash2 size={12} />
                    </button>
                  {/if}
                </div>
              </div>
              <p class="text-xs text-dark-text-secondary mb-3 line-clamp-2">{pack.description}</p>
              <div class="flex items-center gap-3 mb-3 text-[10px] text-dark-text-muted">
                {#if pack.counts.skills > 0}
                  <span class="flex items-center gap-1"><WandSparkles size={10} />{pack.counts.skills} skills</span>
                {/if}
                {#if pack.counts.mcp_sets > 0}
                  <span class="flex items-center gap-1"><Wrench size={10} />{pack.counts.mcp_sets} MCPs</span>
                {/if}
                {#if pack.counts.agents > 0}
                  <span class="flex items-center gap-1"><Bot size={10} />{pack.counts.agents} agents</span>
                {/if}
                {#if pack.counts.organization}
                  <span class="flex items-center gap-1"><Building2 size={10} />org</span>
                {/if}
              </div>
              {#if (pack.variables || []).length > 0}
                <div class="mb-3 text-[10px] text-dark-text-muted">
                  Needs: {(pack.variables || []).map(v => v.key).join(', ')}
                </div>
              {/if}
              <button
                onclick={() => toggleExpand(pack.slug)}
                class={["w-full py-1.5 text-xs font-medium ", expandedSlug === pack.slug ? 'bg-dark-elevated text-dark-text-secondary' : 'bg-accent text-dark-base hover:bg-accent-hover']}
              >
                {expandedSlug === pack.slug ? 'Close' : 'Install'}
              </button>
            </div>

            {#if expandedSlug === pack.slug && expandedPack}
              <div class="border-t border-dark-border p-4 bg-dark-base/50 space-y-3">
                <div class="text-xs font-medium text-dark-text-secondary">Choose components to install:</div>
                <label class="flex items-center gap-2 text-xs text-dark-text-secondary cursor-pointer">
                  <input type="checkbox" bind:checked={installSkills} class="accent-accent" />
                  <WandSparkles size={12} />
                  Skills ({(expandedPack.components.skills || []).length})
                </label>
                <label class="flex items-center gap-2 text-xs text-dark-text-secondary cursor-pointer">
                  <input type="checkbox" bind:checked={installMCPSets} class="accent-accent" />
                  <Wrench size={12} />
                  MCP Sets ({(expandedPack.components.mcp_sets || []).length})
                </label>
                {#if (expandedPack.components.agents || []).length > 0}
                  <label class="flex items-center gap-2 text-xs text-dark-text-secondary cursor-pointer">
                    <input type="checkbox" bind:checked={installAgents} class="accent-accent" />
                    <Bot size={12} />
                    Agents ({(expandedPack.components.agents || []).length})
                  </label>
                {/if}
                {#if expandedPack.components.organization}
                  <label class="flex items-center gap-2 text-xs text-dark-text-secondary cursor-pointer">
                    <input type="checkbox" bind:checked={installOrg} class="accent-accent" />
                    <Building2 size={12} />
                    Organization: {expandedPack.components.organization.name}
                  </label>
                {/if}

                <button
                  onclick={handleInstall}
                  disabled={installing || (!installSkills && !installMCPSets && !installAgents && !installOrg)}
                  class="w-full flex items-center justify-center gap-1.5 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50"
                >
                  {#if installing}
                    <RefreshCw size={12} class="animate-spin" />
                    Installing...
                  {:else}
                    <Check size={12} />
                    Confirm Install
                  {/if}
                </button>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
