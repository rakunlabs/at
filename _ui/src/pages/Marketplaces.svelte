<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listMarketplaces,
    createMarketplace,
    updateMarketplace,
    deleteMarketplace,
    type Marketplace,
    type MarketplaceMCPServer,
  } from '@/lib/api/marketplaces';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listMCPServers, type MCPServer, type MCPUpstream } from '@/lib/api/mcp-servers';
  import { listMCPSets, type MCPSet } from '@/lib/api/mcp-sets';
  import {
    Package,
    Plus,
    RefreshCw,
    Save,
    X,
    Copy,
    Check,
    Pencil,
    Trash2,
    Server,
    WandSparkles,
    Search,
    Link,
    Terminal,
    Layers,
    AlertTriangle,
    ListPlus,
  } from 'lucide-svelte';

  storeNavbar.title = 'Marketplaces';

  let marketplaces = $state<Marketplace[]>([]);
  let skills = $state<Skill[]>([]);
  let mcpServers = $state<MCPServer[]>([]);
  let mcpSets = $state<MCPSet[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let copiedId = $state<string | null>(null);
  let searchQuery = $state('');
  let skillQuery = $state('');
  let mcpSetQuery = $state('');

  let formName = $state('');
  let formDescription = $state('');
  let formSkills = $state<string[]>([]);
  let formMCPServers = $state<string[]>([]);
  let formDirectMCPServers = $state<MarketplaceMCPServer[]>([]);
  // Tracks where each direct MCP entry came from so the picker can show check state.
  // key = entry.name, value = "mcpset:<setId>:<upstreamIndex>" | "manual"
  let directSources = $state<Record<string, string>>({});

  let directMode = $state<'pick' | 'manual'>('pick');
  let directName = $state('');
  let directDescription = $state('');
  let directURL = $state('');
  let directCommand = $state('');
  let directArgs = $state('');
  let directHeaders = $state<Array<{ key: string; value: string }>>([]);
  let directEnv = $state<Array<{ key: string; value: string }>>([]);

  let publicMCPServers = $derived(mcpServers.filter((server) => server.public));
  let filteredMarketplaces = $derived(filterMarketplaces(marketplaces, searchQuery));
  let filteredSkills = $derived(filterSkills(skills, skillQuery));
  let mcpSetsWithUpstreams = $derived(
    mcpSets.filter((s) => (s.config?.mcp_upstreams || []).length > 0),
  );
  let filteredMCPSetsWithUpstreams = $derived(
    filterMCPSets(mcpSetsWithUpstreams, mcpSetQuery),
  );

  async function loadAll() {
    loading = true;
    try {
      const [marketRes, skillRes, mcpRes, mcpSetRes] = await Promise.all([
        listMarketplaces({ _limit: 500, _sort: 'name' }),
        listSkills({ _limit: 500, _sort: 'name' }),
        listMCPServers({ _limit: 500, _sort: 'name' }),
        listMCPSets({ _limit: 500, _sort: 'name' }),
      ]);
      marketplaces = marketRes.data || [];
      skills = skillRes.data || [];
      mcpServers = mcpRes.data || [];
      mcpSets = mcpSetRes.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load marketplaces', 'alert');
    } finally {
      loading = false;
    }
  }

  loadAll();

  function filterMarketplaces(items: Marketplace[], query: string): Marketplace[] {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((market) => {
      return (
        market.name.toLowerCase().includes(q) ||
        (market.description || '').toLowerCase().includes(q) ||
        (market.skills || []).some((ref) => ref.toLowerCase().includes(q)) ||
        (market.mcp_servers || []).some((ref) => ref.toLowerCase().includes(q)) ||
        (market.direct_mcp_servers || []).some((server) => server.name.toLowerCase().includes(q))
      );
    });
  }

  function filterSkills(items: Skill[], query: string): Skill[] {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((skill) => {
      return (
        skill.name.toLowerCase().includes(q) ||
        (skill.description || '').toLowerCase().includes(q) ||
        (skill.category || '').toLowerCase().includes(q)
      );
    });
  }

  function filterMCPSets(items: MCPSet[], query: string): MCPSet[] {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((set) => {
      return (
        set.name.toLowerCase().includes(q) ||
        (set.description || '').toLowerCase().includes(q) ||
        (set.category || '').toLowerCase().includes(q) ||
        (set.config?.mcp_upstreams || []).some(
          (u) =>
            (u.url || '').toLowerCase().includes(q) ||
            (u.command || '').toLowerCase().includes(q),
        )
      );
    });
  }

  function resetForm() {
    formName = '';
    formDescription = '';
    formSkills = [];
    formMCPServers = [];
    formDirectMCPServers = [];
    directSources = {};
    directMode = 'pick';
    directName = '';
    directDescription = '';
    directURL = '';
    directCommand = '';
    directArgs = '';
    directHeaders = [];
    directEnv = [];
    skillQuery = '';
    mcpSetQuery = '';
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(market: Marketplace) {
    resetForm();
    editingId = market.id;
    formName = market.name;
    formDescription = market.description || '';
    formSkills = [...(market.skills || [])];
    formMCPServers = [...(market.mcp_servers || [])];
    formDirectMCPServers = (market.direct_mcp_servers || []).map((server) => ({ ...server }));
    // Re-derive source mapping by matching upstream URL/command against known MCPSets.
    directSources = inferDirectSources(formDirectMCPServers, mcpSets);
    showForm = true;
  }

  function inferDirectSources(
    entries: MarketplaceMCPServer[],
    sets: MCPSet[],
  ): Record<string, string> {
    const out: Record<string, string> = {};
    const usedKeys = new Set<string>();
    for (const entry of entries) {
      let matched = '';
      for (const set of sets) {
        const upstreams = set.config?.mcp_upstreams || [];
        for (let i = 0; i < upstreams.length; i++) {
          const key = `mcpset:${set.id}:${i}`;
          if (usedKeys.has(key)) continue;
          if (upstreamMatches(upstreams[i], entry)) {
            matched = key;
            usedKeys.add(key);
            break;
          }
        }
        if (matched) break;
      }
      out[entry.name] = matched || 'manual';
    }
    return out;
  }

  function upstreamMatches(upstream: MCPUpstream, entry: MarketplaceMCPServer): boolean {
    if (upstream.url && entry.url) return upstream.url === entry.url;
    if (upstream.command && entry.command) {
      if (upstream.command !== entry.command) return false;
      const a = (upstream.args || []).join(' ');
      const b = (entry.args || []).join(' ');
      return a === b;
    }
    return false;
  }

  async function handleSubmit() {
    const name = formName.trim();
    if (!name) {
      addToast('Marketplace name is required', 'warn');
      return;
    }
    if (formSkills.length === 0 && formMCPServers.length === 0 && formDirectMCPServers.length === 0) {
      addToast('Select at least one Skill or MCP Server', 'warn');
      return;
    }

    saving = true;
    try {
      const payload: Partial<Marketplace> = {
        name,
        description: formDescription.trim(),
        skills: formSkills,
        mcp_servers: formMCPServers,
        direct_mcp_servers: formDirectMCPServers,
      };

      if (editingId) {
        await updateMarketplace(editingId, payload);
        addToast(`Marketplace "${name}" updated`);
      } else {
        await createMarketplace(payload);
        addToast(`Marketplace "${name}" created`);
      }
      resetForm();
      await loadAll();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save marketplace', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteMarketplace(id);
      deleteConfirm = null;
      addToast('Marketplace deleted');
      await loadAll();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete marketplace', 'alert');
    }
  }

  function marketplaceURL(market: Marketplace): string {
    return `${window.location.origin}/gateway/v1/claude-code/marketplace.json?market=${encodeURIComponent(market.name)}`;
  }

  function installCommand(market: Marketplace): string {
    return `/plugin marketplace add ${marketplaceURL(market)}`;
  }

  async function copyText(id: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copiedId = id;
      addToast('Copied');
      setTimeout(() => {
        if (copiedId === id) copiedId = null;
      }, 2000);
    } catch {
      addToast('Failed to copy', 'alert');
    }
  }

  function toggleRef(list: string[], ref: string): string[] {
    if (list.includes(ref)) return list.filter((item) => item !== ref);
    return [...list, ref];
  }

  function addDirectMCP() {
    const name = directName.trim();
    const url = directURL.trim();
    const command = directCommand.trim();
    if (!name) {
      addToast('Direct MCP name is required', 'warn');
      return;
    }
    if (!url && !command) {
      addToast('Direct MCP URL or command is required', 'warn');
      return;
    }
    if (formDirectMCPServers.some((server) => server.name === name)) {
      addToast('Direct MCP name already exists in this marketplace', 'warn');
      return;
    }

    const server: MarketplaceMCPServer = { name, description: directDescription.trim() };
    if (url) {
      server.type = 'http';
      server.url = url;
      const headers = kvListToRecord(directHeaders);
      if (Object.keys(headers).length > 0) server.headers = headers;
    }
    if (command) {
      server.type = server.type || 'stdio';
      server.command = command;
      server.args = directArgs.split(/\s+/).map((arg) => arg.trim()).filter(Boolean);
      const env = kvListToRecord(directEnv);
      if (Object.keys(env).length > 0) server.env = env;
    }
    formDirectMCPServers = [...formDirectMCPServers, server];
    directSources = { ...directSources, [server.name]: 'manual' };
    directName = '';
    directDescription = '';
    directURL = '';
    directCommand = '';
    directArgs = '';
    directHeaders = [];
    directEnv = [];
  }

  function removeDirectMCP(name: string) {
    formDirectMCPServers = formDirectMCPServers.filter((server) => server.name !== name);
    if (directSources[name]) {
      const next = { ...directSources };
      delete next[name];
      directSources = next;
    }
  }

  // ─── Pick-from-installed helpers ───

  function upstreamSourceKey(setId: string, idx: number): string {
    return `mcpset:${setId}:${idx}`;
  }

  function isUpstreamAdded(setId: string, idx: number): boolean {
    const key = upstreamSourceKey(setId, idx);
    return Object.values(directSources).includes(key);
  }

  function deriveUpstreamName(setName: string, idx: number, total: number): string {
    if (total <= 1) return setName;
    return `${setName}-${idx + 1}`;
  }

  function uniqueDirectName(base: string): string {
    if (!formDirectMCPServers.some((s) => s.name === base)) return base;
    let i = 2;
    while (formDirectMCPServers.some((s) => s.name === `${base}-${i}`)) i++;
    return `${base}-${i}`;
  }

  function upstreamToMarketplaceMCP(
    set: MCPSet,
    upstream: MCPUpstream,
    idx: number,
    total: number,
  ): MarketplaceMCPServer | null {
    if (!upstream.url && !upstream.command) return null;
    const baseName = deriveUpstreamName(set.name, idx, total);
    const name = uniqueDirectName(baseName);
    const server: MarketplaceMCPServer = {
      name,
      description: set.description ? set.description : `Imported from MCPSet "${set.name}"`,
    };
    if (upstream.url) {
      server.type = 'http';
      server.url = upstream.url;
      if (upstream.headers && Object.keys(upstream.headers).length > 0) {
        server.headers = { ...upstream.headers };
      }
    } else if (upstream.command) {
      server.type = 'stdio';
      server.command = upstream.command;
      server.args = [...(upstream.args || [])];
      if (upstream.env && Object.keys(upstream.env).length > 0) {
        server.env = { ...upstream.env };
      }
    }
    return server;
  }

  function toggleUpstream(set: MCPSet, idx: number) {
    const key = upstreamSourceKey(set.id, idx);
    const existingName = Object.keys(directSources).find((n) => directSources[n] === key);
    if (existingName) {
      removeDirectMCP(existingName);
      return;
    }
    const upstreams = set.config?.mcp_upstreams || [];
    const upstream = upstreams[idx];
    if (!upstream) return;
    const server = upstreamToMarketplaceMCP(set, upstream, idx, upstreams.length);
    if (!server) {
      addToast('Upstream has no URL or command', 'warn');
      return;
    }
    formDirectMCPServers = [...formDirectMCPServers, server];
    directSources = { ...directSources, [server.name]: key };
  }

  function addAllFromSet(set: MCPSet) {
    const upstreams = set.config?.mcp_upstreams || [];
    let added = 0;
    for (let i = 0; i < upstreams.length; i++) {
      if (isUpstreamAdded(set.id, i)) continue;
      const server = upstreamToMarketplaceMCP(set, upstreams[i], i, upstreams.length);
      if (!server) continue;
      formDirectMCPServers = [...formDirectMCPServers, server];
      directSources = {
        ...directSources,
        [server.name]: upstreamSourceKey(set.id, i),
      };
      added++;
    }
    if (added > 0) addToast(`Added ${added} upstream${added === 1 ? '' : 's'} from "${set.name}"`);
  }

  function kvListToRecord(list: Array<{ key: string; value: string }>): Record<string, string> {
    const out: Record<string, string> = {};
    for (const { key, value } of list) {
      const k = key.trim();
      if (!k) continue;
      out[k] = value;
    }
    return out;
  }

  function hasSecrets(server: MarketplaceMCPServer): boolean {
    return (
      Object.keys(server.headers || {}).length > 0 ||
      Object.keys(server.env || {}).length > 0
    );
  }

  function secretSummary(server: MarketplaceMCPServer): string {
    const h = Object.keys(server.headers || {}).length;
    const e = Object.keys(server.env || {}).length;
    const parts: string[] = [];
    if (h > 0) parts.push(`${h} header${h === 1 ? '' : 's'}`);
    if (e > 0) parts.push(`${e} env`);
    return parts.join(' · ');
  }

  function sourceLabel(name: string): string {
    const src = directSources[name];
    if (!src) return '';
    if (src === 'manual') return 'manual';
    const setId = src.split(':')[1];
    const set = mcpSets.find((s) => s.id === setId);
    return set ? `from ${set.name}` : 'mcpset';
  }

  function skillLabel(ref: string): string {
    return skills.find((skill) => skill.id === ref || skill.name === ref)?.name || ref;
  }

  function mcpServerLabel(ref: string): string {
    return mcpServers.find((server) => server.id === ref || server.name === ref)?.name || ref;
  }
</script>

<svelte:head>
  <title>AT | Marketplaces</title>
</svelte:head>

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-6xl mx-auto space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div class="flex items-center gap-2">
            <Package size={18} class="text-gray-500 dark:text-dark-text-muted" />
            <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Marketplaces</h1>
          </div>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1 max-w-2xl">
            Create named Claude Code marketplace JSON feeds from Skills, AT-hosted public MCP Servers, and direct MCP configs that the client will load itself.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <button onclick={loadAll} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Refresh">
            <RefreshCw size={14} />
          </button>
          <button onclick={openCreate} class="flex items-center gap-1.5 px-3 py-1.5 text-xs whitespace-nowrap font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
            <Plus size={12} />
            New Marketplace
          </button>
        </div>
      </div>

      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">{editingId ? `Edit: ${formName}` : 'New Marketplace'}</span>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
              <X size={14} />
            </button>
          </div>

          <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-5">
            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-center">
              <label for="market-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input id="market-name" type="text" bind:value={formName} placeholder="e.g., mymarket" class="md:col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-center">
              <label for="market-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input id="market-description" type="text" bind:value={formDescription} placeholder="What this marketplace contains" class="md:col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <div class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">
                Skills
                <div class="text-xs font-normal text-gray-400 dark:text-dark-text-muted mt-1">{formSkills.length} selected</div>
              </div>
              <div class="md:col-span-3 space-y-3">
                <div class="relative">
                  <Search size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
                  <input type="text" bind:value={skillQuery} placeholder="Filter skills" class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                </div>
                <div class="border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 max-h-72 overflow-y-auto">
                  {#if filteredSkills.length === 0}
                    <div class="p-3 text-xs text-gray-400 dark:text-dark-text-muted">No skills found.</div>
                  {:else}
                    {#each filteredSkills as skill}
                      <label class="flex items-start gap-3 p-3 border-b border-gray-100 dark:border-dark-border last:border-b-0 hover:bg-white dark:hover:bg-dark-elevated cursor-pointer transition-colors">
                        <input type="checkbox" checked={formSkills.includes(skill.id)} onchange={() => (formSkills = toggleRef(formSkills, skill.id))} class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent" />
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-2">
                            <WandSparkles size={12} class="text-gray-400 dark:text-dark-text-muted" />
                            <span class="text-xs font-mono font-medium text-gray-800 dark:text-dark-text">{skill.name}</span>
                            <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">{(skill.tools || []).length} tools</span>
                          </div>
                          {#if skill.description}
                            <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1 line-clamp-2">{skill.description}</p>
                          {/if}
                        </div>
                      </label>
                    {/each}
                  {/if}
                </div>
              </div>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <div class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">
                AT MCP Servers
                <div class="text-xs font-normal text-gray-400 dark:text-dark-text-muted mt-1">{formMCPServers.length} selected</div>
              </div>
              <div class="md:col-span-3 border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 max-h-64 overflow-y-auto">
                {#if publicMCPServers.length === 0}
                  <div class="p-3 text-xs text-gray-400 dark:text-dark-text-muted">No public MCP Servers. Enable Public endpoint on MCP Servers first.</div>
                {:else}
                  {#each publicMCPServers as server}
                    <label class="flex items-start gap-3 p-3 border-b border-gray-100 dark:border-dark-border last:border-b-0 hover:bg-white dark:hover:bg-dark-elevated cursor-pointer transition-colors">
                      <input type="checkbox" checked={formMCPServers.includes(server.id)} onchange={() => (formMCPServers = toggleRef(formMCPServers, server.id))} class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent" />
                      <div class="flex-1 min-w-0">
                        <div class="flex items-center gap-2">
                          <Server size={12} class="text-gray-400 dark:text-dark-text-muted" />
                          <span class="text-xs font-mono font-medium text-gray-800 dark:text-dark-text">{server.name}</span>
                        </div>
                        {#if server.config.description || server.description}
                          <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1 line-clamp-2">{server.config.description || server.description}</p>
                        {/if}
                      </div>
                    </label>
                  {/each}
                {/if}
              </div>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-4 md:items-start">
              <div class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1">
                Direct MCP
                <div class="text-xs font-normal text-gray-400 dark:text-dark-text-muted mt-1">{formDirectMCPServers.length} configured</div>
                <div class="flex items-start gap-1 mt-2 p-2 border border-amber-200 dark:border-amber-900/40 bg-amber-50/60 dark:bg-amber-950/20 text-[10px] text-amber-700 dark:text-amber-300 leading-tight">
                  <AlertTriangle size={11} class="shrink-0 mt-0.5" />
                  <span>Direct MCP config is published verbatim in the public marketplace JSON. Strip secrets from headers/env before saving.</span>
                </div>
              </div>
              <div class="md:col-span-3 space-y-3">
                <!-- Mode toggle -->
                <div class="inline-flex items-center gap-1 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-base p-0.5">
                  <button type="button" onclick={() => (directMode = 'pick')} class={["flex items-center gap-1.5 px-3 py-1 text-xs font-medium transition-colors", directMode === 'pick' ? 'bg-gray-900 text-white dark:bg-accent dark:text-white' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated']}>
                    <Layers size={12} />
                    From installed MCPs
                  </button>
                  <button type="button" onclick={() => (directMode = 'manual')} class={["flex items-center gap-1.5 px-3 py-1 text-xs font-medium transition-colors", directMode === 'manual' ? 'bg-gray-900 text-white dark:bg-accent dark:text-white' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated']}>
                    <Plus size={12} />
                    Manual entry
                  </button>
                </div>

                {#if directMode === 'pick'}
                  <div class="space-y-2">
                    <div class="relative">
                      <Search size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
                      <input type="text" bind:value={mcpSetQuery} placeholder="Filter installed MCPs by name, URL, or command" class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                    </div>
                    <div class="border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 max-h-96 overflow-y-auto">
                      {#if mcpSetsWithUpstreams.length === 0}
                        <div class="p-4 text-xs text-gray-400 dark:text-dark-text-muted text-center">
                          No installed MCPs found.
                          <br />
                          Configure them on the <a href="#/mcps" class="underline hover:text-gray-600 dark:hover:text-dark-text-secondary">MCPs page</a> with upstream URL or command.
                        </div>
                      {:else if filteredMCPSetsWithUpstreams.length === 0}
                        <div class="p-4 text-xs text-gray-400 dark:text-dark-text-muted text-center">No MCPs match the filter.</div>
                      {:else}
                        {#each filteredMCPSetsWithUpstreams as set}
                          {@const upstreams = set.config?.mcp_upstreams || []}
                          {@const addedCount = upstreams.filter((_, i) => isUpstreamAdded(set.id, i)).length}
                          <div class="border-b border-gray-100 dark:border-dark-border last:border-b-0">
                            <div class="flex items-center justify-between px-3 py-2 bg-white dark:bg-dark-elevated/60 border-b border-gray-100 dark:border-dark-border">
                              <div class="min-w-0 flex-1">
                                <div class="flex items-center gap-2">
                                  <Layers size={12} class="text-gray-400 dark:text-dark-text-muted" />
                                  <span class="text-xs font-mono font-medium text-gray-800 dark:text-dark-text truncate">{set.name}</span>
                                  <span class="text-[10px] px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">{upstreams.length} upstream{upstreams.length === 1 ? '' : 's'}</span>
                                  {#if addedCount > 0}
                                    <span class="text-[10px] px-1.5 py-0.5 bg-green-50 dark:bg-green-950/30 text-green-700 dark:text-green-300">{addedCount} added</span>
                                  {/if}
                                </div>
                                {#if set.description}
                                  <p class="text-[11px] text-gray-500 dark:text-dark-text-muted mt-0.5 line-clamp-1">{set.description}</p>
                                {/if}
                              </div>
                              <button type="button" onclick={() => addAllFromSet(set)} disabled={addedCount === upstreams.length} class="flex items-center gap-1 px-2 py-1 text-[10px] font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-40 disabled:cursor-not-allowed transition-colors">
                                <ListPlus size={11} />
                                Add all
                              </button>
                            </div>
                            {#each upstreams as upstream, idx}
                              {@const added = isUpstreamAdded(set.id, idx)}
                              {@const headerCount = Object.keys(upstream.headers || {}).length}
                              {@const envCount = Object.keys(upstream.env || {}).length}
                              <label class="flex items-start gap-3 px-3 py-2 hover:bg-white dark:hover:bg-dark-elevated cursor-pointer transition-colors">
                                <input type="checkbox" checked={added} onchange={() => toggleUpstream(set, idx)} class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent" />
                                <div class="flex-1 min-w-0">
                                  <div class="flex items-center gap-2">
                                    {#if upstream.url}
                                      <Link size={11} class="text-gray-400 shrink-0" />
                                      <span class="text-[10px] uppercase tracking-wide text-gray-400">http</span>
                                      <code class="text-xs text-gray-700 dark:text-dark-text-secondary truncate">{upstream.url}</code>
                                    {:else if upstream.command}
                                      <Terminal size={11} class="text-gray-400 shrink-0" />
                                      <span class="text-[10px] uppercase tracking-wide text-gray-400">stdio</span>
                                      <code class="text-xs text-gray-700 dark:text-dark-text-secondary truncate">{upstream.command} {(upstream.args || []).join(' ')}</code>
                                    {:else}
                                      <span class="text-xs text-gray-400 italic">(empty upstream)</span>
                                    {/if}
                                  </div>
                                  {#if headerCount > 0 || envCount > 0}
                                    <div class="text-[10px] text-amber-600 dark:text-amber-400 mt-0.5 flex items-center gap-1">
                                      <AlertTriangle size={10} />
                                      <span>Includes {[headerCount > 0 ? `${headerCount} header${headerCount === 1 ? '' : 's'}` : '', envCount > 0 ? `${envCount} env var${envCount === 1 ? '' : 's'}` : ''].filter(Boolean).join(' · ')} — review before publishing</span>
                                    </div>
                                  {/if}
                                </div>
                              </label>
                            {/each}
                          </div>
                        {/each}
                      {/if}
                    </div>
                  </div>
                {:else}
                  <div class="border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 p-3 space-y-2">
                    <div class="grid gap-2 sm:grid-cols-2">
                      <input type="text" bind:value={directName} placeholder="name, e.g. docs-search" class="border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                      <input type="text" bind:value={directDescription} placeholder="description optional" class="border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                    </div>
                    <div class="grid gap-2 sm:grid-cols-2">
                      <div class="relative">
                        <Link size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
                        <input type="text" bind:value={directURL} placeholder="remote URL, e.g. https://.../mcp" class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                      </div>
                      <div class="relative">
                        <Terminal size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
                        <input type="text" bind:value={directCommand} placeholder="stdio command, e.g. npx" class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                      </div>
                    </div>
                    <input type="text" bind:value={directArgs} placeholder="stdio args, space separated" class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />

                    {#if directURL}
                      <div class="space-y-1.5 pt-1">
                        <div class="flex items-center justify-between">
                          <span class="text-[11px] font-medium text-gray-500 dark:text-dark-text-muted">Headers ({directHeaders.length})</span>
                          <button type="button" onclick={() => (directHeaders = [...directHeaders, { key: '', value: '' }])} class="flex items-center gap-1 text-[11px] text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text"><Plus size={10} />Header</button>
                        </div>
                        {#each directHeaders as header, i}
                          <div class="flex gap-1.5">
                            <input type="text" bind:value={header.key} placeholder="Header name" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
                            <input type="text" bind:value={header.value} placeholder="value" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
                            <button type="button" onclick={() => (directHeaders = directHeaders.filter((_, x) => x !== i))} class="p-1 text-gray-400 hover:text-red-600"><X size={11} /></button>
                          </div>
                        {/each}
                      </div>
                    {/if}

                    {#if directCommand}
                      <div class="space-y-1.5 pt-1">
                        <div class="flex items-center justify-between">
                          <span class="text-[11px] font-medium text-gray-500 dark:text-dark-text-muted">Env vars ({directEnv.length})</span>
                          <button type="button" onclick={() => (directEnv = [...directEnv, { key: '', value: '' }])} class="flex items-center gap-1 text-[11px] text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text"><Plus size={10} />Env var</button>
                        </div>
                        {#each directEnv as env, i}
                          <div class="flex gap-1.5">
                            <input type="text" bind:value={env.key} placeholder="VAR_NAME" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
                            <input type="text" bind:value={env.value} placeholder="value" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
                            <button type="button" onclick={() => (directEnv = directEnv.filter((_, x) => x !== i))} class="p-1 text-gray-400 hover:text-red-600"><X size={11} /></button>
                          </div>
                        {/each}
                      </div>
                    {/if}

                    <div class="flex justify-end pt-1">
                      <button type="button" onclick={addDirectMCP} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-white dark:hover:bg-dark-elevated transition-colors">
                        <Plus size={11} />
                        Add MCP
                      </button>
                    </div>
                  </div>
                {/if}

                {#if formDirectMCPServers.length > 0}
                  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
                    <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border text-xs font-medium text-gray-600 dark:text-dark-text-secondary flex items-center justify-between">
                      <span>Added direct MCPs ({formDirectMCPServers.length})</span>
                    </div>
                    {#each formDirectMCPServers as server}
                      <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border last:border-b-0 flex items-start gap-2">
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-2 flex-wrap">
                            {#if server.url}
                              <Link size={11} class="text-gray-400 shrink-0" />
                            {:else}
                              <Terminal size={11} class="text-gray-400 shrink-0" />
                            {/if}
                            <span class="text-xs font-mono font-medium text-gray-800 dark:text-dark-text">{server.name}</span>
                            <span class="text-[10px] uppercase tracking-wide text-gray-400">{server.url ? 'http' : 'stdio'}</span>
                            {#if directSources[server.name] && directSources[server.name] !== 'manual'}
                              <span class="text-[10px] px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">{sourceLabel(server.name)}</span>
                            {/if}
                          </div>
                          <code class="text-[11px] text-gray-500 dark:text-dark-text-muted truncate block mt-0.5">
                            {server.url || `${server.command || ''} ${(server.args || []).join(' ')}`.trim()}
                          </code>
                          {#if hasSecrets(server)}
                            <div class="text-[10px] text-amber-600 dark:text-amber-400 mt-0.5 flex items-center gap-1">
                              <AlertTriangle size={10} />
                              {secretSummary(server)} — will be published verbatim
                            </div>
                          {/if}
                        </div>
                        <button type="button" onclick={() => removeDirectMCP(server.name)} class="p-1 text-gray-400 hover:text-red-600 dark:hover:text-red-400 transition-colors shrink-0" title="Remove">
                          <X size={12} />
                        </button>
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
            </div>

            <div class="flex justify-end gap-2 pt-2 border-t border-gray-200 dark:border-dark-border">
              <button type="button" onclick={resetForm} class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Cancel</button>
              <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
                <Save size={12} />
                {saving ? 'Saving...' : 'Save Marketplace'}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="relative w-full sm:w-72">
          <Search size={13} class="absolute left-3 top-2.5 text-gray-400 dark:text-dark-text-muted" />
          <input type="text" bind:value={searchQuery} placeholder="Search marketplaces" class="w-full border border-gray-300 dark:border-dark-border-subtle pl-8 pr-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
        </div>
        <div class="text-xs text-gray-400 dark:text-dark-text-muted">{filteredMarketplaces.length} of {marketplaces.length} marketplaces</div>
      </div>

      {#if loading}
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-8 text-center text-sm text-gray-400 dark:text-dark-text-muted">Loading marketplaces...</div>
      {:else if filteredMarketplaces.length === 0}
        <div class="border border-dashed border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface p-8 text-center">
          <Package size={24} class="mx-auto text-gray-300 dark:text-dark-text-muted mb-2" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">No marketplaces found</h2>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Create one to expose selected Skills and MCP servers as a direct JSON marketplace.</p>
          <button onclick={openCreate} class="mt-4 inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
            <Plus size={12} />
            New Marketplace
          </button>
        </div>
      {:else}
        <div class="grid gap-3">
          {#each filteredMarketplaces as market}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
              <div class="p-4 flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <Package size={15} class="text-gray-400 dark:text-dark-text-muted" />
                    <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text font-mono">{market.name}</h2>
                    <span class="px-2 py-0.5 text-[10px] uppercase tracking-wide bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-secondary">{(market.skills || []).length} skills</span>
                    <span class="px-2 py-0.5 text-[10px] uppercase tracking-wide bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-secondary">{(market.mcp_servers || []).length} at mcp</span>
                    <span class="px-2 py-0.5 text-[10px] uppercase tracking-wide bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-secondary">{(market.direct_mcp_servers || []).length} direct mcp</span>
                  </div>
                  {#if market.description}
                    <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">{market.description}</p>
                  {/if}
                </div>

                <div class="flex items-center gap-1 shrink-0">
                  <button onclick={() => copyText(`url-${market.id}`, marketplaceURL(market))} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Copy JSON URL">
                    {#if copiedId === `url-${market.id}`}<Check size={14} />{:else}<Copy size={14} />{/if}
                  </button>
                  <button onclick={() => openEdit(market)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Edit">
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === market.id}
                    <button onclick={() => handleDelete(market.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Cancel</button>
                  {:else}
                    <button onclick={() => (deleteConfirm = market.id)} class="p-1.5 hover:bg-red-50 dark:hover:bg-red-950/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Delete">
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </div>

              <div class="px-4 pb-4 space-y-3">
                <button onclick={() => copyText(`url-${market.id}`, marketplaceURL(market))} class="w-full flex items-center gap-2 text-left border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-3 py-2 hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors">
                  <Copy size={13} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                  <code class="text-xs text-gray-600 dark:text-dark-text-secondary truncate">{marketplaceURL(market)}</code>
                </button>

                <button onclick={() => copyText(`cmd-${market.id}`, installCommand(market))} class="w-full flex items-center gap-2 text-left border border-blue-100 dark:border-blue-900 bg-blue-50/60 dark:bg-blue-950/20 px-3 py-2 hover:bg-blue-50 dark:hover:bg-blue-950/30 transition-colors">
                  {#if copiedId === `cmd-${market.id}`}<Check size={13} class="text-blue-600 dark:text-blue-300 shrink-0" />{:else}<Copy size={13} class="text-blue-600 dark:text-blue-300 shrink-0" />{/if}
                  <code class="text-xs text-blue-800 dark:text-blue-200 truncate">{installCommand(market)}</code>
                </button>

                <div class="grid gap-3 md:grid-cols-3">
                  <div>
                    <div class="text-xs font-medium text-gray-500 dark:text-dark-text-secondary mb-2">Skills</div>
                    <div class="flex flex-wrap gap-1.5">
                      {#each market.skills || [] as ref}<span class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary">{skillLabel(ref)}</span>{/each}
                    </div>
                  </div>
                  <div>
                    <div class="text-xs font-medium text-gray-500 dark:text-dark-text-secondary mb-2">AT MCP Servers</div>
                    <div class="flex flex-wrap gap-1.5">
                      {#each market.mcp_servers || [] as ref}<span class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary">{mcpServerLabel(ref)}</span>{/each}
                    </div>
                  </div>
                  <div>
                    <div class="text-xs font-medium text-gray-500 dark:text-dark-text-secondary mb-2">Direct MCP</div>
                    <div class="flex flex-wrap gap-1.5">
                      {#each market.direct_mcp_servers || [] as server}<span class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-gray-700 dark:text-dark-text-secondary">{server.name}</span>{/each}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
