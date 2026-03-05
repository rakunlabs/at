<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listMCPServers,
    createMCPServer,
    updateMCPServer,
    deleteMCPServer,
    type MCPServer,
    type MCPHTTPTool,
    type MCPUpstream,
  } from '@/lib/api/mcp-servers';
  import {
    listCollections,
    type RAGCollection,
  } from '@/lib/api/rag';
  import { listVariables, type Variable } from '@/lib/api/secrets';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import {
    Server,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Copy,
    ChevronDown,
    ChevronRight,
    Globe,
    Database,
    Network,
    Bot,
    Wand2,
  } from 'lucide-svelte';
  import { formatDate } from '@/lib/helper/format';
  import HTTPToolBuilderPanel from '@/lib/components/HTTPToolBuilderPanel.svelte';

  storeNavbar.title = 'MCP Servers';

  // ─── State ───

  let servers = $state<MCPServer[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let copiedName = $state<string | null>(null);
  let showAIPanel = $state(false);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');

  // RAG integration
  let formEnabledRAGTools = $state<string[]>([]);
  let formCollectionIds = $state<string[]>([]);
  let formFetchMode = $state('auto');
  let formGitCacheDir = $state('');
  let formDefaultNumResults = $state(10);
  let formTokenVariable = $state('');
  let formTokenUser = $state('');
  let formSSHKeyVariable = $state('');

  // HTTP tools
  let formHTTPTools = $state<MCPHTTPTool[]>([]);

  // Upstream MCP servers
  let formMCPUpstreams = $state<MCPUpstream[]>([]);

  // Skill tools
  let formEnabledSkills = $state<string[]>([]);

  // Helpers
  let collections = $state<RAGCollection[]>([]);
  let availableVariables = $state<Variable[]>([]);
  let availableSkills = $state<Skill[]>([]);

  // Section visibility
  let showRAGSection = $state(false);
  let showHTTPSection = $state(false);
  let showSkillsSection = $state(false);
  let showUpstreamSection = $state(false);

  const allRAGTools = [
    { id: 'rag_search', label: 'Search', desc: 'Search across collections' },
    { id: 'rag_list_collections', label: 'List Collections', desc: 'List available collections' },
    { id: 'rag_fetch_source', label: 'Fetch Source', desc: 'Fetch original file content' },
    { id: 'rag_search_and_fetch', label: 'Search & Fetch', desc: 'Search + auto-fetch full source files' },
    { id: 'rag_search_and_fetch_org', label: 'Search & Fetch Original', desc: 'Search + return only original files' },
  ];

  // ─── Load Data ───

  async function loadServers() {
    loading = true;
    try {
      const res = await listMCPServers({ _limit: 100 });
      servers = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load MCP servers', 'alert');
    } finally {
      loading = false;
    }
  }

  loadServers();

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

  loadCollections();
  loadVariables();
  loadSkills();

  // ─── Form Logic ───

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
    editingId = null;
    showForm = false;
    showRAGSection = false;
    showHTTPSection = false;
    showSkillsSection = false;
    showUpstreamSection = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(s: MCPServer) {
    resetForm();
    editingId = s.id;
    formName = s.name;
    formDescription = s.config.description || '';
    formEnabledRAGTools = s.config.enabled_rag_tools ?? [];
    formCollectionIds = s.config.collection_ids ?? [];
    formFetchMode = s.config.fetch_mode || 'auto';
    formGitCacheDir = s.config.git_cache_dir || '';
    formDefaultNumResults = s.config.default_num_results || 10;
    formTokenVariable = s.config.token_variable || '';
    formTokenUser = s.config.token_user || '';
    formSSHKeyVariable = s.config.ssh_key_variable || '';
    formHTTPTools = (s.config.http_tools ?? []).map(t => ({ ...t, headers: t.headers ? { ...t.headers } : {}, input_schema: t.input_schema ? JSON.parse(JSON.stringify(t.input_schema)) : { type: 'object', properties: {} } }));
    formMCPUpstreams = (s.config.mcp_upstreams ?? []).map(u => ({ ...u, headers: u.headers ? { ...u.headers } : {} }));
    formEnabledSkills = s.config.enabled_skills ?? [];
    showRAGSection = formEnabledRAGTools.length > 0;
    showHTTPSection = formHTTPTools.length > 0;
    showSkillsSection = formEnabledSkills.length > 0;
    showUpstreamSection = formMCPUpstreams.length > 0;
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('MCP server name is required', 'warn');
      return;
    }

    saving = true;
    try {
      const payload = {
        name: formName.trim(),
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
            .filter(u => u.url.trim().length > 0)
            .map(u => ({ url: u.url.trim(), headers: u.headers })),
          enabled_skills: formEnabledSkills,
        },
      };

      if (editingId) {
        await updateMCPServer(editingId, payload);
        addToast(`MCP server "${formName}" updated`);
      } else {
        await createMCPServer(payload);
        addToast(`MCP server "${formName}" created`);
      }
      resetForm();
      await loadServers();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save MCP server', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteMCPServer(id);
      addToast('MCP server deleted');
      deleteConfirm = null;
      await loadServers();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete MCP server', 'alert');
    }
  }

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

  function copyEndpoint(name: string) {
    const url = `${window.location.origin}/gateway/v1/mcp/${name}`;
    navigator.clipboard.writeText(url);
    copiedName = name;
    setTimeout(() => { copiedName = null; }, 2000);
  }

  function getCollectionName(id: string): string {
    const c = collections.find(c => c.id === id);
    return c?.name || id.slice(0, 8);
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
</script>

<div class="flex h-full">
<div class="flex-1 overflow-y-auto">
<div class="p-6 max-w-6xl mx-auto space-y-6">
  <!-- Header -->
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">MCP Servers</h1>
      <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">
        General MCP endpoints. Add RAG tools from your knowledge base and/or custom HTTP request tools.
        External agents connect via <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">POST /gateway/v1/mcp/&#123;name&#125;</code> with Bearer token auth.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={loadServers}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs whitespace-nowrap font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New MCP Server
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formName}` : 'New MCP Server'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Name -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="mcp-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
          <input
            id="mcp-name"
            type="text"
            bind:value={formName}
            placeholder="e.g., my-api, docs-search"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="mcp-desc" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
          <input
            id="mcp-desc"
            type="text"
            bind:value={formDescription}
            placeholder="What this MCP server provides (optional)"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
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

                  <!-- Tool Name -->
                  <div class="grid grid-cols-4 gap-2 items-center">
                    <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Name</label>
                    <input type="text" bind:value={tool.name} placeholder="e.g., get_user, create_ticket"
                      class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                  </div>

                  <!-- Tool Description -->
                  <div class="grid grid-cols-4 gap-2 items-center">
                    <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Description</label>
                    <input type="text" bind:value={tool.description} placeholder="What this tool does"
                      class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors" />
                  </div>

                  <!-- Method + URL -->
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

        <!-- ═══ Upstream MCP Servers Section ═══ -->
        <div class="border border-gray-200 dark:border-dark-border-subtle">
          <button
            type="button"
            onclick={() => showUpstreamSection = !showUpstreamSection}
            class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
          >
            {#if showUpstreamSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
            <Network size={14} />
            Upstream MCP Servers
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
                    <button type="button" onclick={() => { formMCPUpstreams = formMCPUpstreams.filter((_, idx) => idx !== i); }} class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Remove server">
                      <Trash2 size={12} />
                    </button>
                  </div>

                  <!-- URL -->
                  <div class="grid grid-cols-4 gap-2 items-center">
                    <label class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">URL</label>
                    <input
                      type="text"
                      bind:value={formMCPUpstreams[i].url}
                      placeholder="https://other-server:8000/sse"
                      class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
                    />
                  </div>

                  <!-- Headers -->
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
                </div>
              {/each}

              <button
                type="button"
                onclick={() => { formMCPUpstreams = [...formMCPUpstreams, { url: '', headers: {} }]; }}
                class="flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors w-full justify-center"
              >
                <Plus size={12} />
                Add Upstream Server
              </button>
              <p class="text-xs text-gray-400 dark:text-dark-text-muted">Tools from these upstream MCP servers will be merged into this endpoint.</p>
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
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
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

  <!-- Server List -->
  {#if loading}
    <div class="text-xs text-gray-400 dark:text-dark-text-muted py-4 text-center">Loading MCP servers...</div>
  {:else if servers.length === 0 && !showForm}
    <div class="border border-dashed border-gray-200 dark:border-dark-border py-8 text-center">
      <Server size={24} class="mx-auto text-gray-300 dark:text-dark-text-faint mb-2" />
      <p class="text-sm text-gray-500 dark:text-dark-text-muted mb-1">No MCP servers</p>
      <p class="text-xs text-gray-400 dark:text-dark-text-muted mb-3">Create an MCP server to expose tools to external agents</p>
    </div>
  {:else if servers.length > 0}
    <div class="border border-gray-200 dark:border-dark-border overflow-hidden">
      <table class="w-full">
        <thead>
          <tr class="bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border">
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Name</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Tools</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Endpoint</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
          </tr>
        </thead>
        <tbody>
          {#each servers as s}
            <tr class="border-t border-gray-100 dark:border-dark-border hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5">
                <div class="font-medium text-gray-900 dark:text-dark-text text-sm">{s.name}</div>
                {#if s.config.description}
                  <div class="text-xs text-gray-500 dark:text-dark-text-muted truncate max-w-48">{s.config.description}</div>
                {/if}
              </td>
              <td class="px-4 py-2.5">
                <div class="flex flex-wrap gap-1">
                  {#each (s.config.enabled_rag_tools ?? []) as tool}
                    <span class="text-xs font-mono px-1.5 py-0.5 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-800">{tool.replace('rag_', '')}</span>
                  {/each}
                  {#each (s.config.http_tools ?? []) as tool}
                    <span class="text-xs font-mono px-1.5 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border border-green-200 dark:border-green-800">{tool.name}</span>
                  {/each}
                  {#if (s.config.enabled_skills ?? []).length > 0}
                    <span class="text-xs font-mono px-1.5 py-0.5 bg-amber-50 dark:bg-amber-900/20 text-amber-600 dark:text-amber-400 border border-amber-200 dark:border-amber-800">{(s.config.enabled_skills ?? []).length} skills</span>
                  {/if}
                  {#if (s.config.mcp_upstreams ?? []).length > 0}
                    <span class="text-xs font-mono px-1.5 py-0.5 bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400 border border-purple-200 dark:border-purple-800">{(s.config.mcp_upstreams ?? []).length} upstream</span>
                  {/if}
                  {#if (s.config.enabled_rag_tools ?? []).length === 0 && (s.config.http_tools ?? []).length === 0 && (s.config.enabled_skills ?? []).length === 0 && (s.config.mcp_upstreams ?? []).length === 0}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted">No tools</span>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5">
                <button
                  onclick={() => copyEndpoint(s.name)}
                  class="flex items-center gap-1 text-xs font-mono text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors group"
                  title="Click to copy endpoint URL"
                >
                  <Copy size={10} class={copiedName === s.name ? 'text-green-500' : 'text-gray-400 dark:text-dark-text-faint group-hover:text-gray-500'} />
                  <span class="truncate max-w-48">.../mcp/{s.name}</span>
                </button>
              </td>
              <td class="px-4 py-2.5 text-right">
                {#if deleteConfirm === s.id}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => handleDelete(s.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors">Cancel</button>
                  </div>
                {:else}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => openEdit(s)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors" title="Edit">
                      <Pencil size={14} />
                    </button>
                    <button onclick={() => (deleteConfirm = s.id)} class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Delete">
                      <Trash2 size={14} />
                    </button>
                  </div>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
</div>

{#if showAIPanel}
  <HTTPToolBuilderPanel
    onclose={() => { showAIPanel = false; }}
    bind:formHTTPTools
  />
{/if}
</div>
