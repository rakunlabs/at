<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listMarketplaceSources,
    createMarketplaceSource,
    updateMarketplaceSource,
    deleteMarketplaceSource,
    getMarketplaceCatalog,
    getMarketplacePlugin,
    installFromMarketplace,
    type MarketplaceSource,
    type RemoteMarketplace,
    type RemotePluginDetail,
  } from '@/lib/api/marketplace';
  import { listMCPSets, type MCPSet } from '@/lib/api/mcp-sets';
  import { listGitCredentials, type GitCredential } from '@/lib/api/git-credentials';
  import {
    describeSourceURL,
    selectedVariables,
    needsStdioConfirmation,
    installableMCP,
    mcpSummary,
    variableMapping,
  } from '@/lib/helper/remote-marketplace';
  import { Plus, RefreshCw, Trash2, X, Globe, Package, WandSparkles, Server, AlertTriangle, Check, Download, Terminal, Link } from 'lucide-svelte';

  let sources = $state<MarketplaceSource[]>([]);
  let loadingSources = $state(true);
  let sourcesError = $state('');
  let selectedID = $state('');
  let catalog = $state<RemoteMarketplace | null>(null);
  let catalogLoading = $state(false);
  let catalogError = $state('');
  let pluginQuery = $state('');

  let showAdd = $state(false);
  let addURL = $state('');
  let addName = $state('');
  let addRef = $state('');
  let addPath = $state('');
  let addCredential = $state('');
  let adding = $state(false);
  let gitCredentials = $state<GitCredential[]>([]);

  // Plugin dialog
  let pluginName = $state<string | null>(null);
  let detail = $state<RemotePluginDetail | null>(null);
  let detailLoading = $state(false);
  let detailError = $state('');
  let pickSkills = $state<string[]>([]);
  let pickMCP = $state<string[]>([]);
  let skillScope = $state<'personal' | 'workspace'>('personal');
  let mcpTarget = $state<'new' | string>('new');
  let mcpSetName = $state('');
  let mcpScope = $state<'personal' | 'workspace'>('personal');
  let allowStdio = $state(false);
  let vars = $state<Record<string, { mode: 'var' | 'literal'; value: string }>>({});
  let mcpSets = $state<MCPSet[]>([]);
  let installing = $state(false);

  let selected = $derived(sources.find((s) => s.id === selectedID) ?? null);
  let filteredPlugins = $derived(
    (catalog?.plugins ?? []).filter((p) => {
      const q = pluginQuery.trim().toLowerCase();
      if (!q) return true;
      return [p.name, p.display_name, p.description, p.category, ...(p.tags ?? [])].some((v) => v?.toLowerCase().includes(q));
    }),
  );
  let neededVars = $derived(selectedVariables(detail, pickMCP));
  let stdioSelected = $derived(needsStdioConfirmation(detail, pickMCP));
  let urlKind = $derived(describeSourceURL(addURL));
  let isGitURL = $derived(urlKind !== '' && urlKind !== 'marketplace.json file');
  let canInstall = $derived((pickSkills.length > 0 || pickMCP.length > 0) && (!stdioSelected || allowStdio) && !installing);

  $effect(() => {
    for (const v of neededVars) if (!vars[v]) vars[v] = { mode: 'var', value: v.toLowerCase() };
  });

  loadSources();

  async function loadSources() {
    loadingSources = true;
    sourcesError = '';
    try {
      sources = await listMarketplaceSources();
      if (!sources.some((s) => s.id === selectedID)) {
        const first = sources.find((s) => s.enabled && s.type !== 'generic');
        if (first) selectSource(first.id);
      }
    } catch (e: any) {
      sourcesError = e?.response?.data?.message || 'Failed to load marketplaces';
    } finally {
      loadingSources = false;
    }
  }

  async function selectSource(id: string) {
    selectedID = id;
    catalog = null;
    catalogError = '';
    const src = sources.find((s) => s.id === id);
    if (!src || src.type === 'generic') return;
    catalogLoading = true;
    try {
      const result = await getMarketplaceCatalog(id);
      if (selectedID === id) catalog = result;
    } catch (e: any) {
      if (selectedID === id) catalogError = e?.response?.data?.message || 'Failed to load the marketplace';
    } finally {
      if (selectedID === id) catalogLoading = false;
    }
  }

  async function openAdd() {
    showAdd = true;
    try { gitCredentials = await listGitCredentials(); } catch { gitCredentials = []; }
  }

  async function handleAdd() {
    if (!addURL.trim()) {
      addToast('Enter a marketplace URL or repository', 'warn');
      return;
    }
    adding = true;
    try {
      const src = await createMarketplaceSource({
        name: addName.trim(), url: addURL.trim(), ref: addRef.trim() || undefined,
        path: addPath.trim() || undefined, credential_id: isGitURL ? addCredential || undefined : undefined, enabled: true,
      });
      addToast(`Marketplace "${src.name}" added`);
      showAdd = false;
      addURL = addName = addRef = addPath = addCredential = '';
      await loadSources();
      selectSource(src.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add marketplace', 'alert');
    } finally {
      adding = false;
    }
  }

  async function toggleEnabled(src: MarketplaceSource) {
    try {
      await updateMarketplaceSource(src.id, { ...src, enabled: !src.enabled });
      await loadSources();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update marketplace', 'alert');
    }
  }

  async function handleRemove(src: MarketplaceSource) {
    if (!confirm(`Remove marketplace "${src.name}"? Installed skills and MCP servers stay, but can no longer be updated from it.`)) return;
    try {
      await deleteMarketplaceSource(src.id);
      if (selectedID === src.id) { selectedID = ''; catalog = null; }
      await loadSources();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove marketplace', 'alert');
    }
  }

  async function openPlugin(name: string) {
    pluginName = name;
    detail = null;
    detailError = '';
    pickSkills = [];
    pickMCP = [];
    allowStdio = false;
    vars = {};
    mcpTarget = 'new';
    mcpSetName = name;
    detailLoading = true;
    try {
      const [d, setList] = await Promise.all([
        getMarketplacePlugin(selectedID, name),
        listMCPSets({ _limit: 500, _sort: 'name' }).catch(() => ({ data: [] as MCPSet[] })),
      ]);
      if (pluginName !== name) return;
      detail = d;
      mcpSets = setList.data || [];
      pickSkills = d.skills.filter((s) => !s.installed_id).map((s) => s.path);
      pickMCP = d.mcp_servers.filter((m) => installableMCP(m) && m.type === 'http').map((m) => m.name);
    } catch (e: any) {
      if (pluginName === name) detailError = e?.response?.data?.message || 'Failed to load the plugin';
    } finally {
      if (pluginName === name) detailLoading = false;
    }
  }

  function closePlugin() {
    pluginName = null;
    detail = null;
  }

  function toggle(list: string[], value: string): string[] {
    return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
  }

  async function handleInstall() {
    if (!detail || !pluginName) return;
    installing = true;
    try {
      const mapping = variableMapping(Object.fromEntries(neededVars.map((v) => [v, vars[v]])));
      const result = await installFromMarketplace(selectedID, {
        plugin: pluginName,
        skills: pickSkills,
        skill_scope: skillScope,
        mcp_servers: pickMCP,
        mcp_set_id: mcpTarget === 'new' ? undefined : mcpTarget,
        mcp_set_name: mcpTarget === 'new' ? mcpSetName.trim() : undefined,
        mcp_scope: mcpScope,
        variables: mapping,
        allow_stdio: allowStdio,
      });
      const parts = [];
      if (result.skills.length) parts.push(`${result.skills.length} skill${result.skills.length === 1 ? '' : 's'}`);
      if (result.mcp_set) parts.push(`${pickMCP.length} MCP server${pickMCP.length === 1 ? '' : 's'} in "${result.mcp_set.name}"`);
      addToast(`Installed ${parts.join(' and ')}`);
      closePlugin();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to install', 'alert');
    } finally {
      installing = false;
    }
  }
</script>

<div class="flex flex-col gap-4 lg:flex-row">
  <!-- Sources -->
  <div class="lg:w-72 shrink-0 border border-dark-border self-start w-full">
    <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
      <span class="text-sm font-medium text-dark-text">Marketplaces</span>
      <div class="flex items-center gap-1">
        <button onclick={loadSources} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Refresh"><RefreshCw size={13} /></button>
        <button onclick={openAdd} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Add marketplace"><Plus size={14} /></button>
      </div>
    </div>
    {#if loadingSources}
      <p class="p-4 text-xs text-dark-text-muted">Loading...</p>
    {:else if sourcesError}
      <div class="p-4 text-xs text-oc-red">{sourcesError} <button class="underline" onclick={loadSources}>Retry</button></div>
    {:else if sources.length === 0}
      <div class="p-4 text-xs text-dark-text-muted space-y-2">
        <p>No marketplaces yet.</p>
        <button onclick={openAdd} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-dark-base bg-accent hover:bg-accent-hover"><Plus size={12} /> Add marketplace</button>
      </div>
    {:else}
      <ul>
        {#each sources as src}
          <li class={['flex items-center gap-2 px-3 py-2 border-b border-dark-border-subtle last:border-b-0', selectedID === src.id ? 'bg-dark-elevated' : 'hover:bg-dark-surface']}>
            <button class="flex-1 min-w-0 text-left" onclick={() => selectSource(src.id)} disabled={src.type === 'generic'}>
              <span class={['block text-sm truncate', src.enabled ? 'text-dark-text' : 'text-dark-text-muted line-through']}>{src.name}</span>
              <span class="block text-[11px] text-dark-text-muted truncate">{src.type === 'generic' ? 'Legacy search source (not browsable)' : src.url}</span>
            </button>
            <button onclick={() => toggleEnabled(src)} class="text-[11px] px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:text-dark-text-secondary" title={src.enabled ? 'Disable' : 'Enable'}>{src.enabled ? 'On' : 'Off'}</button>
            <button onclick={() => handleRemove(src)} class="p-1 text-dark-text-muted hover:text-oc-red" title="Remove"><Trash2 size={12} /></button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- Catalog -->
  <div class="flex-1 min-w-0 space-y-3">
    {#if showAdd}
      <div class="border border-dark-border">
        <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
          <span class="text-sm font-medium text-dark-text">Add marketplace</span>
          <button onclick={() => (showAdd = false)} class="p-1 hover:bg-dark-elevated text-dark-text-muted"><X size={14} /></button>
        </div>
        <div class="p-4 space-y-3">
          <div>
            <label for="mp-url" class="block text-xs text-dark-text-secondary mb-1">Marketplace</label>
            <input id="mp-url" bind:value={addURL} placeholder="owner/repo, https://git.example/team/plugins.git or https://…/marketplace.json" class="w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text placeholder:text-dark-text-muted" />
            <p class="text-[11px] text-dark-text-muted mt-1">
              {urlKind ? `Detected: ${urlKind}.` : 'A Claude Code plugin marketplace (.claude-plugin/marketplace.json) or a repository of SKILL.md folders.'}
            </p>
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label for="mp-name" class="block text-xs text-dark-text-secondary mb-1">Name (optional)</label>
              <input id="mp-name" bind:value={addName} class="w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text" />
            </div>
            {#if isGitURL}
              <div>
                <label for="mp-ref" class="block text-xs text-dark-text-secondary mb-1">Branch or tag (optional)</label>
                <input id="mp-ref" bind:value={addRef} placeholder="default branch" class="w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text placeholder:text-dark-text-muted" />
              </div>
              <div>
                <label for="mp-path" class="block text-xs text-dark-text-secondary mb-1">Path in repository (optional)</label>
                <input id="mp-path" bind:value={addPath} placeholder=".claude-plugin/marketplace.json" class="w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text placeholder:text-dark-text-muted" />
              </div>
              <div>
                <label for="mp-cred" class="block text-xs text-dark-text-secondary mb-1">SSH deploy key (private repositories)</label>
                <select id="mp-cred" bind:value={addCredential} class="w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text">
                  <option value="">None</option>
                  {#each gitCredentials as credential}<option value={credential.id}>{credential.name} — {credential.host}</option>{/each}
                </select>
              </div>
            {/if}
          </div>
          <div class="flex justify-end gap-2">
            <button onclick={() => (showAdd = false)} class="px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
            <button onclick={handleAdd} disabled={adding} class="px-3 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">{adding ? 'Adding...' : 'Add'}</button>
          </div>
        </div>
      </div>
    {/if}

    {#if !selected}
      {#if !showAdd}
        <div class="border border-dark-border p-8 text-center text-sm text-dark-text-muted">
          <Globe size={22} class="mx-auto mb-2" />
          Add a remote marketplace, then pick the skills and MCP servers to install from its plugins.
        </div>
      {/if}
    {:else}
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div class="min-w-0">
          <h2 class="text-sm font-medium text-dark-text truncate">{catalog?.name || selected.name}</h2>
          {#if catalog?.description}<p class="text-xs text-dark-text-muted">{catalog.description}</p>{/if}
        </div>
        <div class="flex items-center gap-2">
          <input bind:value={pluginQuery} placeholder="Filter plugins" class="w-48 border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-xs text-dark-text placeholder:text-dark-text-muted" />
          <button onclick={() => selectSource(selected.id)} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted" title="Reload"><RefreshCw size={13} /></button>
        </div>
      </div>

      {#if catalogLoading}
        <div class="border border-dark-border p-8 text-center text-sm text-dark-text-muted">Fetching marketplace...</div>
      {:else if catalogError}
        <div class="border border-dark-border p-4 text-xs text-oc-red flex items-center justify-between gap-2">
          <span>{catalogError}</span>
          <button onclick={() => selectSource(selected.id)} class="px-2 py-1 border border-dark-border-subtle text-dark-text-secondary">Retry</button>
        </div>
      {:else if catalog}
        {#if filteredPlugins.length === 0}
          <div class="border border-dark-border p-8 text-center text-sm text-dark-text-muted">No plugins.</div>
        {:else}
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
            {#each filteredPlugins as plugin}
              <div class="border border-dark-border p-3 flex flex-col">
                <div class="flex items-start justify-between gap-2 mb-1">
                  <div class="min-w-0">
                    <h3 class="text-sm font-medium text-dark-text truncate">{plugin.display_name || plugin.name}</h3>
                    {#if plugin.display_name}<span class="text-[11px] text-dark-text-muted font-mono">{plugin.name}</span>{/if}
                  </div>
                  {#if plugin.version}<span class="text-[10px] px-1.5 py-0.5 bg-dark-elevated text-dark-text-muted shrink-0">v{plugin.version}</span>{/if}
                </div>
                <p class="text-xs text-dark-text-muted flex-1 line-clamp-3 mb-2">{plugin.description || 'No description'}</p>
                {#if plugin.category || plugin.tags?.length}
                  <div class="flex flex-wrap gap-1 mb-2">
                    {#if plugin.category}<span class="px-1.5 py-0.5 text-[10px] bg-dark-base border border-dark-border-subtle text-dark-text-muted">{plugin.category}</span>{/if}
                    {#each (plugin.tags ?? []).slice(0, 4) as tag}<span class="px-1.5 py-0.5 text-[10px] bg-dark-base text-dark-text-muted">{tag}</span>{/each}
                  </div>
                {/if}
                {#if plugin.installable}
                  <button onclick={() => openPlugin(plugin.name)} class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">
                    <Package size={12} /> View contents
                  </button>
                {:else}
                  <p class="text-[11px] text-dark-text-muted" title={plugin.reason}>Not installable: {plugin.reason}</p>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {/if}
    {/if}
  </div>
</div>

{#if pluginName}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="fixed inset-0 bg-black/50 z-50 flex items-start justify-center p-4 overflow-y-auto" onclick={closePlugin}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="bg-dark-surface border border-dark-border w-full max-w-2xl my-8" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.key === 'Escape' && closePlugin()} tabindex="-1">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
        <div class="min-w-0">
          <h3 class="text-sm font-medium text-dark-text truncate">{detail?.plugin.display_name || pluginName}</h3>
          <span class="text-[11px] text-dark-text-muted">from {selected?.name}</span>
        </div>
        <button onclick={closePlugin} class="p-1 hover:bg-dark-elevated text-dark-text-muted"><X size={14} /></button>
      </div>

      <div class="p-4 space-y-4 max-h-[70vh] overflow-y-auto">
        {#if detailLoading}
          <p class="text-sm text-dark-text-muted">Fetching plugin...</p>
        {:else if detailError}
          <p class="text-sm text-oc-red">{detailError}</p>
        {:else if detail}
          {#if detail.warnings?.length}
            <div class="border border-oc-peach/40 p-2 text-[11px] text-oc-peach space-y-0.5">
              {#each detail.warnings as warning}<p>{warning}</p>{/each}
            </div>
          {/if}

          <section>
            <h4 class="flex items-center gap-1.5 text-xs font-medium text-dark-text-secondary mb-2"><WandSparkles size={12} /> Skills ({detail.skills.length})</h4>
            {#if detail.skills.length === 0}
              <p class="text-xs text-dark-text-muted">This plugin contains no skills.</p>
            {:else}
              <ul class="border border-dark-border divide-y divide-dark-border-subtle">
                {#each detail.skills as skill}
                  <li class="flex items-start gap-2 px-3 py-2">
                    <input type="checkbox" class="mt-0.5" checked={pickSkills.includes(skill.path)} disabled={!!skill.installed_id} onchange={() => (pickSkills = toggle(pickSkills, skill.path))} aria-label="Install {skill.name}" />
                    <div class="flex-1 min-w-0">
                      <div class="flex items-center gap-2">
                        <span class="text-sm text-dark-text">{skill.name}</span>
                        {#if skill.installed_id}
                          <span class="text-[10px] px-1.5 py-0.5 border border-oc-green/40 text-oc-green flex items-center gap-1"><Check size={10} /> Installed</span>
                          {#if skill.update_available}<span class="text-[10px] px-1.5 py-0.5 border border-oc-peach/40 text-oc-peach">Update available — apply it from Skills</span>{/if}
                        {/if}
                      </div>
                      {#if skill.description}<p class="text-xs text-dark-text-muted">{skill.description}</p>{/if}
                      <p class="text-[11px] text-dark-text-faint font-mono">{skill.path}{skill.resources ? ` · ${skill.resources} file${skill.resources === 1 ? '' : 's'}` : ''}</p>
                    </div>
                  </li>
                {/each}
              </ul>
              <div class="flex items-center gap-3 mt-2 text-xs text-dark-text-secondary">
                <span>Install skills as</span>
                <label class="flex items-center gap-1"><input type="radio" bind:group={skillScope} value="personal" /> Personal</label>
                <label class="flex items-center gap-1"><input type="radio" bind:group={skillScope} value="workspace" /> Workspace</label>
              </div>
            {/if}
          </section>

          <section>
            <h4 class="flex items-center gap-1.5 text-xs font-medium text-dark-text-secondary mb-2"><Server size={12} /> MCP servers ({detail.mcp_servers.length})</h4>
            {#if detail.mcp_servers.length === 0}
              <p class="text-xs text-dark-text-muted">This plugin declares no MCP servers.</p>
            {:else}
              <ul class="border border-dark-border divide-y divide-dark-border-subtle">
                {#each detail.mcp_servers as mcp}
                  <li class="flex items-start gap-2 px-3 py-2">
                    <input type="checkbox" class="mt-0.5" checked={pickMCP.includes(mcp.name)} disabled={!installableMCP(mcp)} onchange={() => (pickMCP = toggle(pickMCP, mcp.name))} aria-label="Install {mcp.name}" />
                    <div class="flex-1 min-w-0">
                      <div class="flex items-center gap-2">
                        <span class="text-sm text-dark-text">{mcp.name}</span>
                        <span class="text-[10px] px-1.5 py-0.5 bg-dark-elevated text-dark-text-muted flex items-center gap-1">
                          {#if mcp.type === 'http'}<Link size={10} /> HTTP{:else}<Terminal size={10} /> Local command{/if}
                        </span>
                      </div>
                      <p class="text-[11px] text-dark-text-muted font-mono break-all">{mcpSummary(mcp)}</p>
                      {#if mcp.plugin_relative}<p class="text-[11px] text-dark-text-muted">Runs files bundled with the plugin, which AT does not install.</p>{/if}
                    </div>
                  </li>
                {/each}
              </ul>

              {#if pickMCP.length}
                <div class="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs">
                  <label class="flex flex-col gap-1 text-dark-text-secondary">
                    Add to MCP set
                    <select bind:value={mcpTarget} class="border border-dark-border-subtle bg-dark-elevated px-2 py-1.5 text-sm text-dark-text">
                      <option value="new">New set</option>
                      {#each mcpSets as set}<option value={set.id}>{set.name}{set.owner_user_id ? ' (personal)' : ''}</option>{/each}
                    </select>
                  </label>
                  {#if mcpTarget === 'new'}
                    <label class="flex flex-col gap-1 text-dark-text-secondary">
                      Set name
                      <input bind:value={mcpSetName} class="border border-dark-border-subtle bg-dark-elevated px-2 py-1.5 text-sm text-dark-text" />
                    </label>
                    <div class="flex items-center gap-3 text-dark-text-secondary sm:col-span-2">
                      <span>Set scope</span>
                      <label class="flex items-center gap-1"><input type="radio" bind:group={mcpScope} value="personal" /> Personal</label>
                      <label class="flex items-center gap-1"><input type="radio" bind:group={mcpScope} value="workspace" /> Workspace</label>
                    </div>
                  {/if}
                </div>

                {#if neededVars.length}
                  <div class="mt-3 border border-dark-border p-3 space-y-2">
                    <p class="text-xs text-dark-text-secondary">These servers expect values. Reference an AT variable (resolved on each call, never copied into the set) or enter a value. Leave blank to use the server's default.</p>
                    {#each neededVars as name}
                      <div class="flex items-center gap-2">
                        <code class="text-[11px] text-dark-text w-36 shrink-0 truncate">{'${'}{name}{'}'}</code>
                        <select bind:value={vars[name].mode} class="border border-dark-border-subtle bg-dark-elevated px-2 py-1 text-xs text-dark-text">
                          <option value="var">Variable</option>
                          <option value="literal">Value</option>
                        </select>
                        <input bind:value={vars[name].value} placeholder={vars[name].mode === 'var' ? 'variable key' : 'value'} class="flex-1 min-w-0 border border-dark-border-subtle bg-dark-elevated px-2 py-1 text-xs text-dark-text" />
                      </div>
                    {/each}
                  </div>
                {/if}

                {#if stdioSelected}
                  <label class="mt-3 flex items-start gap-2 border border-oc-peach/40 p-2 text-xs text-oc-peach">
                    <input type="checkbox" bind:checked={allowStdio} class="mt-0.5" />
                    <span><AlertTriangle size={12} class="inline -mt-0.5" /> Some selected servers run a command on this AT host. Only continue if you trust this marketplace and have reviewed the commands above.</span>
                  </label>
                {/if}
              {/if}
            {/if}
          </section>
        {/if}
      </div>

      <div class="flex items-center justify-between gap-2 px-4 py-3 border-t border-dark-border">
        <span class="text-[11px] text-dark-text-muted">Installed items stay linked to this marketplace and report updates.</span>
        <div class="flex gap-2">
          <button onclick={closePlugin} class="px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
          <button onclick={handleInstall} disabled={!canInstall} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">
            <Download size={12} /> {installing ? 'Installing...' : `Install ${pickSkills.length + pickMCP.length || ''}`}
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}
