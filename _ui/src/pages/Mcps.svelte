<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listMCPSets, createMCPSet, updateMCPSet, deleteMCPSet, type MCPSet } from '@/lib/api/mcp-sets';
  import { type MCPHTTPTool, type MCPUpstream } from '@/lib/api/mcp-servers';
  import { listCollections, type RAGCollection } from '@/lib/api/rag';
  import { listVariables, type Variable } from '@/lib/api/secrets';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listBuiltinTools, type BuiltinToolDef } from '@/lib/api/mcp';
  import { Layers, Plus, Pencil, Trash2, X, Save, RefreshCw, ChevronDown, ChevronRight, Globe, Database, Network, Wand2, Bot, Store, Download, Check, Package, Wrench } from 'lucide-svelte';
  import { listMCPTemplates, installMCPTemplate, type MCPTemplate } from '@/lib/api/mcp-templates';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import HTTPToolBuilderPanel from '@/lib/components/HTTPToolBuilderPanel.svelte';

  storeNavbar.title = 'MCP';

  // ─── Tab State ───

  let activeTab = $state<'my-mcps' | 'store'>('my-mcps');

  // ─── Store State ───

  let mcpTemplates = $state<MCPTemplate[]>([]);
  let storeLoading = $state(false);
  let selectedCategory = $state('');
  let installedSlugs = $state<Set<string>>(new Set());

  async function loadTemplates() {
    storeLoading = true;
    try {
      const cat = selectedCategory || undefined;
      mcpTemplates = await listMCPTemplates(cat);
      const setNames = new Set(sets.map((s) => s.name));
      installedSlugs = new Set(mcpTemplates.filter((t) => setNames.has(t.mcp_server.name)).map((t) => t.slug));
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load templates', 'alert');
    } finally {
      storeLoading = false;
    }
  }

  async function handleInstallTemplate(slug: string) {
    try {
      await installMCPTemplate(slug);
      addToast('MCP installed from template');
      installedSlugs = new Set([...installedSlugs, slug]);
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to install template', 'alert');
    }
  }

  function selectCategory(cat: string) {
    selectedCategory = cat === selectedCategory ? '' : cat;
    loadTemplates();
  }

  $effect(() => {
    if (activeTab === 'store') {
      loadTemplates();
    }
  });

  // ─── State ───

  let sets = $state<MCPSet[]>([]);
  let collections = $state<RAGCollection[]>([]);
  let availableVariables = $state<Variable[]>([]);
  let availableSkills = $state<Skill[]>([]);
  let builtinToolDefs = $state<BuiltinToolDef[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  let showAIPanel = $state(false);

  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');

  // Config form fields (RAG/HTTP/External/Skills)
  let formEnabledRAGTools = $state<string[]>([]);
  let formCollectionIds = $state<string[]>([]);
  let formFetchMode = $state('auto');
  let formGitCacheDir = $state('');
  let formDefaultNumResults = $state(10);
  let formTokenVariable = $state('');
  let formTokenUser = $state('');
  let formSSHKeyVariable = $state('');
  let formHTTPTools = $state<MCPHTTPTool[]>([]);
  let formMCPUpstreams = $state<MCPUpstream[]>([]);
  let formEnabledSkills = $state<string[]>([]);
  let formBuiltinTools = $state<string[]>([]);

  // Section visibility
  let showRAGSection = $state(false);
  let showHTTPSection = $state(false);
  let showSkillsSection = $state(false);
  let showBuiltinToolsSection = $state(false);
  let showUpstreamSection = $state(false);

  const allRAGTools = [
    { id: 'rag_search', label: 'Search', desc: 'Search across collections' },
    { id: 'rag_list_collections', label: 'List Collections', desc: 'List available collections' },
    { id: 'rag_fetch_source', label: 'Fetch Source', desc: 'Fetch original file content' },
    { id: 'rag_search_and_fetch', label: 'Search & Fetch', desc: 'Search + auto-fetch full source files' },
    { id: 'rag_search_and_fetch_org', label: 'Search & Fetch Original', desc: 'Search + return only original files' },
  ];

  // ─── Load ───

  async function loadData() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) {
        params['name[like]'] = `%${searchQuery}%`;
      }
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;

      const sResult = await listMCPSets(params);
      sets = sResult.data || [];
      total = sResult.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.message || 'Failed to load data', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadCollections() {
    try {
      const res = await listCollections({ _limit: 500 });
      collections = res.data || [];
    } catch {}
  }

  async function loadVariables() {
    try {
      const res = await listVariables({ _limit: 500 });
      availableVariables = res.data || [];
    } catch {}
  }

  async function loadSkills() {
    try {
      const res = await listSkills({ _limit: 500 });
      availableSkills = res.data || [];
    } catch {}
  }

  async function loadBuiltinToolDefs() {
    try {
      const res = await listBuiltinTools();
      builtinToolDefs = res.tools || [];
    } catch {}
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    loadData();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    loadData();
  }

  loadData();
  loadCollections();
  loadVariables();
  loadSkills();
  loadBuiltinToolDefs();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formEnabledRAGTools = [];
    formCollectionIds = [];
    formFetchMode = 'auto';
    formGitCacheDir = '';
    formDefaultNumResults = 10;
    formTokenVariable = '';
    formTokenUser = '';
    formSSHKeyVariable = '';
    formHTTPTools = [];
    formMCPUpstreams = [];
    formEnabledSkills = [];
    formBuiltinTools = [];
    editingId = null;
    showForm = false;
    showRAGSection = false;
    showHTTPSection = false;
    showSkillsSection = false;
    showBuiltinToolsSection = false;
    showUpstreamSection = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(set: MCPSet) {
    resetForm();
    editingId = set.id;
    formName = set.name;
    formDescription = set.description;
    // Config fields
    const cfg = set.config || {} as any;
    formEnabledRAGTools = cfg.enabled_rag_tools ?? [];
    formCollectionIds = cfg.collection_ids ?? [];
    formFetchMode = cfg.fetch_mode || 'auto';
    formGitCacheDir = cfg.git_cache_dir || '';
    formDefaultNumResults = cfg.default_num_results || 10;
    formTokenVariable = cfg.token_variable || '';
    formTokenUser = cfg.token_user || '';
    formSSHKeyVariable = cfg.ssh_key_variable || '';
    formHTTPTools = (cfg.http_tools ?? []).map((t: MCPHTTPTool) => ({ ...t, headers: t.headers ? { ...t.headers } : {}, input_schema: t.input_schema ? JSON.parse(JSON.stringify(t.input_schema)) : { type: 'object', properties: {} } }));
    formMCPUpstreams = (cfg.mcp_upstreams ?? []).map((u: MCPUpstream) => ({ ...u, headers: u.headers ? { ...u.headers } : undefined, args: u.args ? [...u.args] : undefined, env: u.env ? { ...u.env } : undefined }));
    formEnabledSkills = cfg.enabled_skills ?? [];
    formBuiltinTools = cfg.enabled_builtin_tools ?? [];
    showRAGSection = formEnabledRAGTools.length > 0;
    showHTTPSection = formHTTPTools.length > 0;
    showSkillsSection = formEnabledSkills.length > 0;
    showBuiltinToolsSection = formBuiltinTools.length > 0;
    showUpstreamSection = formMCPUpstreams.length > 0;
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Name is required', 'warn');
      return;
    }

    saving = true;
    try {
      const payload = {
        name: formName.trim(),
        description: formDescription.trim(),
        config: {
          description: formDescription.trim(),
          enabled_rag_tools: formEnabledRAGTools,
          collection_ids: formCollectionIds,
          fetch_mode: formFetchMode,
          git_cache_dir: formGitCacheDir.trim(),
          default_num_results: Number(formDefaultNumResults) || 10,
          token_variable: formTokenVariable.trim(),
          token_user: formTokenUser.trim(),
          ssh_key_variable: formSSHKeyVariable.trim(),
          http_tools: formHTTPTools.map(t => ({
            ...t,
            name: t.name.trim(),
            description: t.description.trim(),
            method: t.method || 'GET',
            url: t.url.trim(),
          })),
          mcp_upstreams: formMCPUpstreams
            .filter(u => (u.url?.trim() || '').length > 0 || (u.command?.trim() || '').length > 0)
            .map(u => u.command !== undefined
              ? { command: u.command!.trim(), args: u.args, env: u.env }
              : { url: u.url!.trim(), headers: u.headers }),
          enabled_skills: formEnabledSkills,
          enabled_builtin_tools: formBuiltinTools,
        },
      };

      if (editingId) {
        await updateMCPSet(editingId, payload);
        addToast(`MCP "${formName}" updated`);
      } else {
        await createMCPSet(payload);
        addToast(`MCP "${formName}" created`);
      }
      resetForm();
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save MCP', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteMCPSet(id);
      addToast('MCP deleted');
      deleteConfirm = null;
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete MCP', 'alert');
    }
  }

  // ─── Tool Config Helpers ───

  function toggleRAGTool(toolId: string) {
    if (formEnabledRAGTools.includes(toolId)) {
      formEnabledRAGTools = formEnabledRAGTools.filter(t => t !== toolId);
    } else {
      formEnabledRAGTools = [...formEnabledRAGTools, toolId];
    }
  }

  function toggleCollection(colId: string) {
    if (formCollectionIds.includes(colId)) {
      formCollectionIds = formCollectionIds.filter(c => c !== colId);
    } else {
      formCollectionIds = [...formCollectionIds, colId];
    }
  }

  function addHTTPTool() {
    formHTTPTools = [...formHTTPTools, {
      name: '',
      description: '',
      method: 'GET',
      url: '',
      headers: {},
      body_template: '',
      input_schema: { type: 'object', properties: {} },
    }];
  }

  function removeHTTPTool(index: number) {
    formHTTPTools = formHTTPTools.filter((_, i) => i !== index);
  }

  // HTTP tool header management
  let httpToolNewHeaderKey = $state<Record<number, string>>({});
  let httpToolNewHeaderValue = $state<Record<number, string>>({});

  function addHeader(toolIndex: number) {
    const key = (httpToolNewHeaderKey[toolIndex] || '').trim();
    const value = (httpToolNewHeaderValue[toolIndex] || '').trim();
    if (!key) return;
    const tool = formHTTPTools[toolIndex];
    if (!tool.headers) tool.headers = {};
    tool.headers[key] = value;
    formHTTPTools = [...formHTTPTools];
    httpToolNewHeaderKey[toolIndex] = '';
    httpToolNewHeaderValue[toolIndex] = '';
  }

  function removeHeader(toolIndex: number, key: string) {
    const tool = formHTTPTools[toolIndex];
    if (tool.headers) {
      delete tool.headers[key];
      formHTTPTools = [...formHTTPTools];
    }
  }

  // Upstream MCP server header management
  let upstreamNewHeaderKey = $state<Record<number, string>>({});
  let upstreamNewHeaderValue = $state<Record<number, string>>({});

  function addUpstreamHeader(index: number) {
    const key = (upstreamNewHeaderKey[index] || '').trim();
    const value = (upstreamNewHeaderValue[index] || '').trim();
    if (!key) return;
    const upstream = formMCPUpstreams[index];
    if (!upstream.headers) upstream.headers = {};
    upstream.headers[key] = value;
    formMCPUpstreams = [...formMCPUpstreams];
    upstreamNewHeaderKey[index] = '';
    upstreamNewHeaderValue[index] = '';
  }

  // Upstream MCP server env management
  let upstreamNewEnvKey = $state<Record<number, string>>({});
  let upstreamNewEnvValue = $state<Record<number, string>>({});

  function addUpstreamEnv(index: number) {
    const key = (upstreamNewEnvKey[index] || '').trim();
    const value = (upstreamNewEnvValue[index] || '').trim();
    if (!key) return;
    const upstream = formMCPUpstreams[index];
    if (!upstream.env) upstream.env = {};
    upstream.env[key] = value;
    formMCPUpstreams = [...formMCPUpstreams];
    upstreamNewEnvKey[index] = '';
    upstreamNewEnvValue[index] = '';
  }

  // NPM package quick-add
  let npmPackageInput = $state('');

  function addNpmPackage() {
    const raw = npmPackageInput.trim();
    if (!raw) return;
    // Split "package arg1 arg2" into parts; first part is the package name
    const parts = raw.split(/\s+/);
    formMCPUpstreams = [...formMCPUpstreams, { command: 'npx', args: parts, env: {} }];
    npmPackageInput = '';
    showUpstreamSection = true;
  }

  // Input schema editing as JSON string per tool
  let httpToolSchemaText = $state<Record<number, string>>({});

  function getSchemaText(index: number): string {
    if (httpToolSchemaText[index] !== undefined) return httpToolSchemaText[index];
    return JSON.stringify(formHTTPTools[index]?.input_schema || { type: 'object', properties: {} }, null, 2);
  }

  function setSchemaText(index: number, value: string) {
    httpToolSchemaText[index] = value;
    try {
      formHTTPTools[index].input_schema = JSON.parse(value);
    } catch {}
  }

  function getCollectionName(id: string): string {
    const c = collections.find(c => c.id === id);
    return c?.name || id.slice(0, 8);
  }
</script>

<svelte:head>
  <title>AT | MCP</title>
</svelte:head>

<div class="flex h-full">
<div class="flex h-full flex-1 min-w-0">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <!-- Tab Bar -->
      <div class="flex items-center gap-4 mb-4 border-b border-gray-200 dark:border-dark-border">
        <button
          onclick={() => (activeTab = 'my-mcps')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 transition-colors {activeTab === 'my-mcps' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Layers size={14} />
          My MCPs
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
        </button>
        <button
          onclick={() => (activeTab = 'store')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 transition-colors {activeTab === 'store' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Store size={14} />
          MCP Store
        </button>
      </div>

      {#if activeTab === 'my-mcps'}
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Layers size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">MCP</h2>
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={loadData}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
          >
            <Plus size={12} />
            New MCP
          </button>
        </div>
      </div>
      <!-- Inline Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {editingId ? `Edit: ${formName}` : 'New MCP'}
            </span>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
            <!-- Name -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                id="form-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., dev_tools, production_apis"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Description -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                id="form-description"
                type="text"
                bind:value={formDescription}
                placeholder="What this MCP contains"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- ═══ RAG Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showRAGSection = !showRAGSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
              >
                {#if showRAGSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <Database size={14} />
                RAG Tools
                {#if formEnabledRAGTools.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formEnabledRAGTools.length} enabled)</span>
                {/if}
              </button>

              {#if showRAGSection}
                <div class="px-4 pb-4 pt-2 space-y-4 border-t border-gray-200 dark:border-dark-border-subtle">
                  <!-- Enabled RAG Tools -->
                  <div class="grid grid-cols-4 gap-3 items-start">
                    <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Tools</span>
                    <div class="col-span-3 space-y-1.5">
                      {#each allRAGTools as tool}
                        <label class="flex items-center gap-2 cursor-pointer">
                          <input
                            type="checkbox"
                            checked={formEnabledRAGTools.includes(tool.id)}
                            onchange={() => toggleRAGTool(tool.id)}
                            class="w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                          />
                          <span class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary">{tool.id}</span>
                          <span class="text-xs text-gray-400 dark:text-dark-text-muted">- {tool.desc}</span>
                        </label>
                      {/each}
                    </div>
                  </div>

                  <!-- Collections -->
                  <div class="grid grid-cols-4 gap-3 items-start">
                    <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Collections</span>
                    <div class="col-span-3">
                      {#if collections.length > 0}
                        <div class="flex flex-wrap gap-1.5">
                          {#each collections as c}
                            <button
                              type="button"
                              onclick={() => toggleCollection(c.id)}
                              class={[
                                'px-2 py-1 text-xs border transition-colors',
                                formCollectionIds.includes(c.id)
                                  ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                                  : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                              ]}
                            >
                              {c.name}
                            </button>
                          {/each}
                        </div>
                        <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all collections accessible</p>
                      {:else}
                        <span class="text-xs text-gray-400 dark:text-dark-text-muted">No collections available</span>
                      {/if}
                    </div>
                  </div>

                  <!-- Fetch Mode -->
                  <div class="grid grid-cols-4 gap-3 items-center">
                    <label for="mcp-fetch-mode" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Fetch Mode</label>
                    <select
                      id="mcp-fetch-mode"
                      bind:value={formFetchMode}
                      class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
                    >
                      <option value="auto">auto (git cache first, then HTTP)</option>
                      <option value="local">local (git cache only)</option>
                      <option value="remote">remote (HTTP only)</option>
                    </select>
                  </div>

                  <!-- Git Auth -->
                  <div class="space-y-3 border border-gray-200 dark:border-dark-border-subtle p-3">
                    <p class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide">Git Authentication</p>
                    <div class="grid grid-cols-4 gap-3 items-center">
                      <label for="mcp-token-var" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Token Variable</label>
                      <input id="mcp-token-var" type="text" list="mcp-var-list" bind:value={formTokenVariable} placeholder="e.g. github_token"
                        class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                    </div>
                    <div class="grid grid-cols-4 gap-3 items-center">
                      <label for="mcp-token-user" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Token User</label>
                      <input id="mcp-token-user" type="text" bind:value={formTokenUser} placeholder="x-token-auth (default)"
                        class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                    </div>
                    <div class="grid grid-cols-4 gap-3 items-center">
                      <label for="mcp-ssh-key-var" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">SSH Key Variable</label>
                      <input id="mcp-ssh-key-var" type="text" list="mcp-var-list" bind:value={formSSHKeyVariable} placeholder="e.g. deploy_ssh_key"
                        class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                    </div>
                    <datalist id="mcp-var-list">
                      {#each availableVariables as v}
                        <option value={v.key}>{v.key}{v.description ? ` — ${v.description}` : ''}</option>
                      {/each}
                    </datalist>
                  </div>

                  <!-- Git Cache Dir -->
                  <div class="grid grid-cols-4 gap-3 items-center">
                    <label for="mcp-git-cache" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Git Cache Dir</label>
                    <input id="mcp-git-cache" type="text" bind:value={formGitCacheDir} placeholder="/tmp/at-git-cache (default)"
                      class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                  </div>

                  <!-- Default Num Results -->
                  <div class="grid grid-cols-4 gap-3 items-center">
                    <label for="mcp-num-results" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Default Results</label>
                    <div class="col-span-3 flex items-center gap-2">
                      <input id="mcp-num-results" type="number" bind:value={formDefaultNumResults} min={1} max={100}
                        class="w-24 border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-sm font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                      <span class="text-xs text-gray-400 dark:text-dark-text-muted">Default number of search results</span>
                    </div>
                  </div>
                </div>
              {/if}
            </div>

            <!-- ═══ HTTP Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showHTTPSection = !showHTTPSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
              >
                {#if showHTTPSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <Globe size={14} />
                HTTP Tools
                {#if formHTTPTools.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formHTTPTools.length} tools)</span>
                {/if}
              </button>

              {#if showHTTPSection}
                <div class="px-4 pb-4 pt-2 space-y-3 border-t border-gray-200 dark:border-dark-border-subtle">
                  {#each formHTTPTools as tool, i}
                    <div class="border border-gray-200 dark:border-dark-border p-3 space-y-3 relative">
                      <div class="flex items-center justify-between">
                        <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted">Tool #{i + 1}</span>
                        <button type="button" onclick={() => removeHTTPTool(i)} class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Remove tool">
                          <Trash2 size={12} />
                        </button>
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Name</label>
                        <input type="text" bind:value={tool.name} placeholder="e.g., get_user, create_ticket"
                          class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Description</label>
                        <input type="text" bind:value={tool.description} placeholder="What this tool does"
                          class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Request</label>
                        <div class="col-span-3 flex gap-2">
                          <select bind:value={tool.method}
                            class="border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text transition-colors w-24">
                            <option value="GET">GET</option>
                            <option value="POST">POST</option>
                            <option value="PUT">PUT</option>
                            <option value="DELETE">DELETE</option>
                            <option value="PATCH">PATCH</option>
                            <option value="HEAD">HEAD</option>
                          </select>
                          <input type="text" bind:value={tool.url} placeholder={"https://api.example.com/{{.id}}"}
                            class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                        </div>
                      </div>

                      <!-- Headers -->
                      <div class="grid grid-cols-4 gap-2 items-start">
                        <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Headers</label>
                        <div class="col-span-3 space-y-1">
                          {#if tool.headers}
                            {#each Object.entries(tool.headers) as [hk, hv]}
                              <div class="flex items-center gap-1">
                                <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
                                <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{hv}</span>
                                <button type="button" onclick={() => removeHeader(i, hk)} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 transition-colors">
                                  <X size={10} />
                                </button>
                              </div>
                            {/each}
                          {/if}
                          <div class="flex items-center gap-1">
                            <input type="text" placeholder="Key" bind:value={httpToolNewHeaderKey[i]}
                              class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                            <input type="text" placeholder="Value" bind:value={httpToolNewHeaderValue[i]}
                              class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                            <button type="button" onclick={() => addHeader(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary transition-colors">
                              Add
                            </button>
                          </div>
                          <p class="text-xs text-gray-400 dark:text-dark-text-muted">Use <code class="font-mono">{"{{var:key}}"}</code> to reference a variable value</p>
                        </div>
                      </div>

                      <!-- Body Template -->
                      {#if tool.method === 'POST' || tool.method === 'PUT' || tool.method === 'PATCH'}
                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Body</label>
                          <textarea bind:value={tool.body_template} placeholder={'{"key": "{{.value}}"}'}
                            rows="3"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors resize-y"></textarea>
                        </div>
                      {/if}

                      <!-- Input Schema -->
                      <div class="grid grid-cols-4 gap-2 items-start">
                        <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Input Schema</label>
                        <textarea
                          value={getSchemaText(i)}
                          oninput={(e) => setSchemaText(i, (e.target as HTMLTextAreaElement).value)}
                          rows="4"
                          placeholder={'{"type": "object", "properties": {}, "required": []}'}
                          class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors resize-y"
                        ></textarea>
                      </div>
                    </div>
                  {/each}

                  <div class="flex gap-2">
                    <button
                      type="button"
                      onclick={addHTTPTool}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors justify-center"
                    >
                      <Plus size={12} />
                      Add HTTP Tool
                    </button>
                    <button
                      type="button"
                      onclick={() => { showAIPanel = !showAIPanel; }}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors {showAIPanel ? 'bg-accent-muted text-accent dark:text-accent-text border border-accent/30' : 'border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                      title="Toggle AI HTTP Tool Builder"
                    >
                      <Bot size={12} />
                      AI Builder
                    </button>
                  </div>
                </div>
              {/if}
            </div>

            <!-- ═══ Skill Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showSkillsSection = !showSkillsSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
              >
                {#if showSkillsSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <Wand2 size={14} />
                Skill Tools
                {#if formEnabledSkills.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formEnabledSkills.length} skills)</span>
                {/if}
              </button>

              {#if showSkillsSection}
                <div class="px-4 pb-4 pt-2 space-y-2 border-t border-gray-200 dark:border-dark-border-subtle">
                  {#if availableSkills.length > 0}
                    {#each availableSkills as skill}
                      <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">
                        <input
                          type="checkbox"
                          checked={formEnabledSkills.includes(skill.name)}
                          onchange={() => {
                            if (formEnabledSkills.includes(skill.name)) {
                              formEnabledSkills = formEnabledSkills.filter(s => s !== skill.name);
                            } else {
                              formEnabledSkills = [...formEnabledSkills, skill.name];
                            }
                          }}
                          class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                        />
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-2">
                            <span class="text-xs font-mono font-medium text-gray-700 dark:text-dark-text-secondary">{skill.name}</span>
                            <span class="text-xs text-gray-400 dark:text-dark-text-muted">{skill.tools?.length || 0} tools</span>
                          </div>
                          {#if skill.description}
                            <div class="text-xs text-gray-400 dark:text-dark-text-muted truncate">{skill.description}</div>
                          {/if}
                        </div>
                      </label>
                    {/each}
                  {:else}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted">No skills available. Create skills first.</span>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- ═══ Builtin Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showBuiltinToolsSection = !showBuiltinToolsSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
              >
                {#if showBuiltinToolsSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <Wrench size={14} />
                Builtin Tools
                {#if formBuiltinTools.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formBuiltinTools.length} tools)</span>
                {/if}
              </button>

              {#if showBuiltinToolsSection}
                <div class="px-4 pb-4 pt-2 space-y-2 border-t border-gray-200 dark:border-dark-border-subtle">
                  {#if builtinToolDefs.length > 0}
                    {#each builtinToolDefs as tool}
                      <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">
                        <input
                          type="checkbox"
                          checked={formBuiltinTools.includes(tool.name)}
                          onchange={() => {
                            if (formBuiltinTools.includes(tool.name)) {
                              formBuiltinTools = formBuiltinTools.filter(t => t !== tool.name);
                            } else {
                              formBuiltinTools = [...formBuiltinTools, tool.name];
                            }
                          }}
                          class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                        />
                        <div class="flex-1 min-w-0">
                          <span class="text-xs font-mono font-medium text-gray-700 dark:text-dark-text-secondary">{tool.name}</span>
                          {#if tool.description}
                            <div class="text-xs text-gray-400 dark:text-dark-text-muted truncate">{tool.description}</div>
                          {/if}
                        </div>
                      </label>
                    {/each}
                  {:else}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted">No builtin tools available.</span>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- ═══ Upstream MCP Servers Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showUpstreamSection = !showUpstreamSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
              >
                {#if showUpstreamSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <Network size={14} />
                External MCP
                {#if formMCPUpstreams.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formMCPUpstreams.length} servers)</span>
                {/if}
              </button>

              {#if showUpstreamSection}
                <div class="px-4 pb-4 pt-2 space-y-3 border-t border-gray-200 dark:border-dark-border-subtle">
                  {#each formMCPUpstreams as upstream, i}
                    <div class="border border-gray-200 dark:border-dark-border p-3 space-y-3 relative">
                      <div class="flex items-center justify-between">
                        <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted">Server #{i + 1}</span>
                        <div class="flex items-center gap-2">
                          <div class="flex items-center gap-1 text-xs">
                            <button type="button" onclick={() => { formMCPUpstreams[i] = { url: upstream.url || '', headers: upstream.headers || {} }; formMCPUpstreams = [...formMCPUpstreams]; }}
                              class="px-1.5 py-0.5 transition-colors {!upstream.command ? 'bg-gray-800 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}">
                              HTTP
                            </button>
                            <button type="button" onclick={() => { formMCPUpstreams[i] = { command: upstream.command || '', args: upstream.args || [], env: upstream.env || {} }; formMCPUpstreams = [...formMCPUpstreams]; }}
                              class="px-1.5 py-0.5 transition-colors {upstream.command !== undefined && upstream.command !== null ? 'bg-gray-800 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}">
                              Local
                            </button>
                          </div>
                          <button type="button" onclick={() => { formMCPUpstreams = formMCPUpstreams.filter((_, idx) => idx !== i); }} class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Remove server">
                            <Trash2 size={12} />
                          </button>
                        </div>
                      </div>

                      {#if upstream.command !== undefined && upstream.command !== null}
                        <!-- Local Command mode -->
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Command</label>
                          <input
                            type="text"
                            bind:value={formMCPUpstreams[i].command}
                            placeholder="npx"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                          />
                        </div>
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Args</label>
                          <input
                            type="text"
                            value={(upstream.args ?? []).join(' ')}
                            oninput={(e: Event) => { formMCPUpstreams[i].args = (e.target as HTMLInputElement).value.split(/\s+/).filter(Boolean); formMCPUpstreams = [...formMCPUpstreams]; }}
                            placeholder="@playwright/mcp@latest --headless"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                          />
                        </div>
                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Env</label>
                          <div class="col-span-3 space-y-1">
                            {#if upstream.env}
                              {#each Object.entries(upstream.env) as [ek, ev]}
                                <div class="flex items-center gap-1">
                                  <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{ek}=</span>
                                  <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{ev}</span>
                                  <button type="button" onclick={() => { if (upstream.env) { delete upstream.env[ek]; formMCPUpstreams = [...formMCPUpstreams]; } }} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 transition-colors">
                                    <X size={10} />
                                  </button>
                                </div>
                              {/each}
                            {/if}
                            <div class="flex items-center gap-1">
                              <input type="text" placeholder="Key" bind:value={upstreamNewEnvKey[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                              <input type="text" placeholder="Value" bind:value={upstreamNewEnvValue[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                              <button type="button" onclick={() => addUpstreamEnv(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary transition-colors">
                                Add
                              </button>
                            </div>
                          </div>
                        </div>
                      {:else}
                        <!-- HTTP mode -->
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">URL</label>
                          <input
                            type="text"
                            bind:value={formMCPUpstreams[i].url}
                            placeholder="https://other-server:8000/sse"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                          />
                        </div>

                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Headers</label>
                          <div class="col-span-3 space-y-1">
                            {#if upstream.headers}
                              {#each Object.entries(upstream.headers) as [hk, hv]}
                                <div class="flex items-center gap-1">
                                  <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
                                  <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{hv}</span>
                                  <button type="button" onclick={() => { if (upstream.headers) { delete upstream.headers[hk]; formMCPUpstreams = [...formMCPUpstreams]; } }} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 transition-colors">
                                    <X size={10} />
                                  </button>
                                </div>
                              {/each}
                            {/if}
                            <div class="flex items-center gap-1">
                              <input type="text" placeholder="Key" bind:value={upstreamNewHeaderKey[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                              <input type="text" placeholder="Value" bind:value={upstreamNewHeaderValue[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text transition-colors" />
                              <button type="button" onclick={() => addUpstreamHeader(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary transition-colors">
                                Add
                              </button>
                            </div>
                            <p class="text-xs text-gray-400 dark:text-dark-text-muted">Use <code class="font-mono">{"{{var:key}}"}</code> to reference a variable value</p>
                          </div>
                        </div>
                      {/if}
                    </div>
                  {/each}

                  <!-- NPM Package quick-add -->
                  <div class="flex items-center gap-2">
                    <Package size={14} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                    <input
                      type="text"
                      bind:value={npmPackageInput}
                      placeholder="@playwright/mcp@latest --headless"
                      onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') { e.preventDefault(); addNpmPackage(); } }}
                      class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                    />
                    <button
                      type="button"
                      onclick={addNpmPackage}
                      class="px-3 py-1.5 text-xs font-medium bg-gray-800 dark:bg-accent text-white hover:bg-gray-700 dark:hover:bg-accent-hover transition-colors shrink-0"
                    >
                      Add NPM
                    </button>
                  </div>
                  <p class="text-xs text-gray-400 dark:text-dark-text-muted">Add an npm package as <code class="font-mono">npx &lt;package&gt;</code> MCP server. Append flags after the package name.</p>

                  <div class="flex gap-2">
                    <button
                      type="button"
                      onclick={() => { formMCPUpstreams = [...formMCPUpstreams, { url: '', headers: {} }]; }}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors justify-center"
                    >
                      <Globe size={12} />
                      Add HTTP Server
                    </button>
                    <button
                      type="button"
                      onclick={() => { formMCPUpstreams = [...formMCPUpstreams, { command: '', args: [], env: {} }]; }}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors justify-center"
                    >
                      <Bot size={12} />
                      Add Local Command
                    </button>
                  </div>
                  <p class="text-xs text-gray-400 dark:text-dark-text-muted">Tools from these upstream MCP servers will be merged into this MCP's tools.</p>
                </div>
              {/if}
            </div>

            <!-- Actions -->
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
              <button
                type="button"
                onclick={resetForm}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
              >
                <Save size={14} />
                {#if saving}
                  Saving...
                {:else}
                  {editingId ? 'Update' : 'Create'}
                {/if}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <!-- Set list -->
      {#if loading || sets.length > 0 || !showForm}
        <DataTable
          items={sets}
          {loading}
          {total}
          {limit}
          bind:offset
          onchange={loadData}
          onsearch={handleSearch}
          searchPlaceholder="Search by name..."
          emptyIcon={Layers}
          emptyTitle="No MCPs configured"
          emptyDescription="MCPs bundle tools and custom URLs for agents"
        >
          {#snippet header()}
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Tools</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(set)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{set.name}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-64 truncate" title={set.description}>
                {set.description || '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                <div class="flex flex-wrap gap-1">
                  {#if (set.config?.enabled_rag_tools ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-800 font-mono">RAG</span>
                  {/if}
                  {#if (set.config?.http_tools ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border border-green-200 dark:border-green-800 font-mono">{(set.config.http_tools ?? []).length} HTTP</span>
                  {/if}
                  {#if (set.config?.enabled_skills ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-amber-50 dark:bg-amber-900/20 text-amber-600 dark:text-amber-400 border border-amber-200 dark:border-amber-800 font-mono">{(set.config.enabled_skills ?? []).length} skills</span>
                  {/if}
                  {#if (set.config?.mcp_upstreams ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400 border border-purple-200 dark:border-purple-800 font-mono">{(set.config.mcp_upstreams ?? []).length} external</span>
                  {/if}
                  {#if (set.config?.enabled_builtin_tools ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-slate-50 dark:bg-slate-900/20 text-slate-600 dark:text-slate-400 border border-slate-200 dark:border-slate-800 font-mono">{(set.config.enabled_builtin_tools ?? []).length} builtin</span>
                  {/if}
                  {#if !(set.config?.enabled_rag_tools?.length) && !(set.config?.http_tools?.length) && !(set.config?.enabled_skills?.length) && !(set.config?.mcp_upstreams?.length) && !(set.config?.enabled_builtin_tools?.length)}
                    <span class="text-gray-400 dark:text-dark-text-muted">-</span>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => openEdit(set)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === set.id}
                    <button
                      onclick={() => handleDelete(set.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = set.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {/snippet}
        </DataTable>
      {/if}
      {/if}

      <!-- MCP Store Tab -->
      {#if activeTab === 'store'}
        <!-- Category Filters -->
        {#if mcpTemplates.length > 0}
          {@const categories = [...new Set(mcpTemplates.map((t) => t.category))]}
          <div class="flex items-center gap-2 mb-4 flex-wrap">
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">Filter:</span>
            {#each categories as cat}
              <button
                onclick={() => selectCategory(cat)}
                class="px-2.5 py-1 text-xs font-medium transition-colors {selectedCategory === cat ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}"
              >
                {cat}
              </button>
            {/each}
            {#if selectedCategory}
              <button
                onclick={() => selectCategory('')}
                class="px-2 py-1 text-xs text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
              >
                Clear
              </button>
            {/if}
          </div>
        {/if}

        {#if storeLoading}
          <div class="flex items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <RefreshCw size={16} class="animate-spin mr-2" />
            Loading templates...
          </div>
        {:else if mcpTemplates.length === 0}
          <div class="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <Store size={24} class="mb-2" />
            <p class="text-sm">No templates available</p>
          </div>
        {:else}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {#each mcpTemplates as tmpl}
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 flex flex-col">
                <div class="flex items-start justify-between mb-2">
                  <div>
                    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">{tmpl.name}</h3>
                    <span class="inline-block mt-1 px-2 py-0.5 text-[10px] font-medium bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">
                      {tmpl.category}
                    </span>
                  </div>
                  {#if installedSlugs.has(tmpl.slug)}
                    <span class="flex items-center gap-1 px-2 py-1 text-xs font-medium text-green-600 dark:text-green-400 bg-green-50 dark:bg-green-900/20">
                      <Check size={12} />
                      Installed
                    </span>
                  {/if}
                </div>
                <p class="text-xs text-gray-500 dark:text-dark-text-muted mb-3 flex-1">{tmpl.description}</p>

                <!-- Tags -->
                {#if tmpl.tags && tmpl.tags.length > 0}
                  <div class="flex flex-wrap gap-1 mb-3">
                    {#each tmpl.tags as tag}
                      <span class="px-1.5 py-0.5 text-[10px] bg-gray-50 dark:bg-dark-base text-gray-400 dark:text-dark-text-muted">{tag}</span>
                    {/each}
                  </div>
                {/if}

                <!-- Config preview -->
                <div class="text-xs text-gray-500 dark:text-dark-text-muted mb-3 space-y-0.5">
                  {#if (tmpl.mcp_server.config.enabled_rag_tools ?? []).length > 0}
                    <div><span class="font-mono px-1 py-0.5 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400">RAG</span> {tmpl.mcp_server.config.enabled_rag_tools?.join(', ')}</div>
                  {/if}
                  {#if (tmpl.mcp_server.config.mcp_upstreams ?? []).length > 0}
                    <div><span class="font-mono px-1 py-0.5 bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400">External</span> {tmpl.mcp_server.config.mcp_upstreams?.map(u => u.command ? `${u.command} ${(u.args ?? []).join(' ')}` : new URL(u.url!).hostname).join(', ')}</div>
                  {/if}
                  {#if (tmpl.mcp_server.config.http_tools ?? []).length > 0}
                    <div><span class="font-mono px-1 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400">HTTP</span> {tmpl.mcp_server.config.http_tools?.length} tool{(tmpl.mcp_server.config.http_tools?.length ?? 0) !== 1 ? 's' : ''}</div>
                  {/if}
                </div>

                <!-- Install button -->
                {#if !installedSlugs.has(tmpl.slug)}
                  <button
                    onclick={() => handleInstallTemplate(tmpl.slug)}
                    class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
                  >
                    <Download size={12} />
                    Install
                  </button>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {/if}
    </div>
  </div>
</div>

{#if showAIPanel}
  <HTTPToolBuilderPanel
    onclose={() => { showAIPanel = false; }}
    bind:formHTTPTools
  />
{/if}
</div>
