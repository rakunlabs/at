<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listMCPServers,
    createMCPServer,
    updateMCPServer,
    deleteMCPServer,
    exportMCPServer,
    importMCPServer,
    type MCPServer,
  } from '@/lib/api/mcp-servers';
  import { listBuiltinTools, type BuiltinToolDef } from '@/lib/api/mcp';
  import { listMCPSets, type MCPSet } from '@/lib/api/mcp-sets';
  import { listWorkflows, type Workflow } from '@/lib/api/workflows';
  import {
    Server,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Copy,
    Wrench,
    Layers,
    GitBranch,
    Download,
    Upload,
  } from 'lucide-svelte';

  storeNavbar.title = 'MCP Servers';

  // ─── State ───

  let servers = $state<MCPServer[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let copiedName = $state<string | null>(null);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formPublic = $state(false);
  let formMCPSets = $state<string[]>([]);
  let formBuiltinTools = $state<string[]>([]);
  let formWorkflowIds = $state<string[]>([]);
  let formWSURL = $state('');
  let formWSHeaders = $state<Array<{ key: string; value: string }>>([]);
  let formWSPassQueryParams = $state('');
  let formWSPassHeaders = $state('');

  // Helpers
  let builtinToolDefs = $state<BuiltinToolDef[]>([]);
  let availableMCPSets = $state<MCPSet[]>([]);
  let availableWorkflows = $state<Workflow[]>([]);

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

  async function loadBuiltinToolDefs() {
    try {
      const res = await listBuiltinTools();
      builtinToolDefs = res.tools || [];
    } catch {}
  }

  loadBuiltinToolDefs();

  async function loadMCPSets() {
    try {
      const res = await listMCPSets({ _limit: 500 });
      availableMCPSets = res.data || [];
    } catch {}
  }

  loadMCPSets();

  async function loadWorkflows() {
    try {
      const res = await listWorkflows({ _limit: 500 });
      availableWorkflows = res.data || [];
    } catch {}
  }

  loadWorkflows();

  // ─── Form Logic ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formPublic = false;
    formMCPSets = [];
    formBuiltinTools = [];
    formWorkflowIds = [];
    formWSURL = '';
    formWSHeaders = [];
    formWSPassQueryParams = '';
    formWSPassHeaders = '';
    editingId = null;
    showForm = false;
  }

  function parseCSVList(value: string) {
    return value.split(',').map((item) => item.trim()).filter(Boolean);
  }

  function recordToKVList(record?: Record<string, string>) {
    return Object.entries(record || {}).map(([key, value]) => ({ key, value }));
  }

  function kvListToRecord(items: Array<{ key: string; value: string }>) {
    const out: Record<string, string> = {};
    for (const item of items) {
      const key = item.key.trim();
      const value = item.value.trim();
      if (key && value) out[key] = value;
    }
    return out;
  }

  function addWSHeader() {
    formWSHeaders = [...formWSHeaders, { key: '', value: '' }];
  }

  function removeWSHeader(index: number) {
    formWSHeaders = formWSHeaders.filter((_, i) => i !== index);
  }

  function updateWSHeader(index: number, field: 'key' | 'value', value: string) {
    const next = [...formWSHeaders];
    next[index] = { ...next[index], [field]: value };
    formWSHeaders = next;
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
    formPublic = Boolean(s.public);
    formMCPSets = [...(s.servers || [])];
    formBuiltinTools = s.config.enabled_builtin_tools ?? [];
    formWorkflowIds = s.config.workflow_ids ?? [];
    formWSURL = s.config.ws_upstream?.url || '';
    formWSHeaders = recordToKVList(s.config.ws_upstream?.headers);
    formWSPassQueryParams = (s.config.ws_upstream?.pass_query_params || []).join(', ');
    formWSPassHeaders = (s.config.ws_upstream?.pass_headers || []).join(', ');
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('MCP server name is required', 'warn');
      return;
    }

    saving = true;
    try {
      const existing = editingId ? servers.find(s => s.id === editingId) : null;
      const config: any = {
        ...(existing?.config || {}),
        description: formDescription.trim(),
        enabled_builtin_tools: formBuiltinTools,
        workflow_ids: formWorkflowIds,
      };

      const wsURL = formWSURL.trim();
      if (wsURL) {
        const wsHeaders = kvListToRecord(formWSHeaders);
        const passQueryParams = parseCSVList(formWSPassQueryParams);
        const passHeaders = parseCSVList(formWSPassHeaders);
        config.ws_upstream = { url: wsURL };
        if (Object.keys(wsHeaders).length > 0) {
          config.ws_upstream.headers = wsHeaders;
        }
        if (passQueryParams.length > 0) {
          config.ws_upstream.pass_query_params = passQueryParams;
        }
        if (passHeaders.length > 0) {
          config.ws_upstream.pass_headers = passHeaders;
        }
      } else {
        delete config.ws_upstream;
      }

      const payload = {
        name: formName.trim(),
        public: formPublic,
        servers: formMCPSets,
        config,
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

  function copyEndpoint(name: string) {
    const url = `${window.location.origin}/gateway/v1/mcp/${name}`;
    navigator.clipboard.writeText(url);
    copiedName = `mcp:${name}`;
    setTimeout(() => { copiedName = null; }, 2000);
  }

  function copyWSEndpoint(name: string) {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${window.location.host}/gateway/v1/mcp/${name}/ws`;
    navigator.clipboard.writeText(url);
    copiedName = `ws:${name}`;
    setTimeout(() => { copiedName = null; }, 2000);
  }

  // ─── Export / Import ───

  async function handleExportServer(server: MCPServer) {
    try {
      const data = await exportMCPServer(server.id);
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${server.name}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      addToast(`Exported "${server.name}" as ${server.name}.json`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to export server', 'alert');
    }
  }

  let serverImportFileInput: HTMLInputElement;
  async function handleImportServerFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      const data = JSON.parse(text);
      await importMCPServer(data);
      addToast(`Imported MCP server from "${file.name}"`);
      await loadServers();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import MCP server', 'alert');
    }
    input.value = '';
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
        Gateway endpoints that serve tools to external agents.
        Connect via <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">POST /gateway/v1/mcp/&#123;name&#125;</code>; Bearer token auth is required unless Public mode is enabled.
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
        onclick={() => serverImportFileInput.click()}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
        title="Import MCP server from JSON file"
      >
        <Upload size={12} />
        Import
      </button>
      <input
        bind:this={serverImportFileInput}
        type="file"
        accept=".json"
        onchange={handleImportServerFile}
        class="hidden"
      />
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

        <!-- Public Access -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Access</span>
          <label class="col-span-3 flex items-start gap-2 cursor-pointer border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 p-3">
            <input
              type="checkbox"
              bind:checked={formPublic}
              class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
            />
            <span class="text-xs text-gray-600 dark:text-dark-text-secondary leading-relaxed">
              <span class="font-medium text-gray-800 dark:text-dark-text">Public endpoint</span>
              <span class="block text-gray-400 dark:text-dark-text-muted mt-0.5">Allow unauthenticated MCP clients to list and call this server's tools. Only enable this for tools safe to expose without an AT token.</span>
            </span>
          </label>
        </div>

        <!-- WebSocket Passthrough -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">
            <div class="flex items-center gap-1.5">
              <Server size={14} />
              WebSocket
            </div>
          </span>
          <div class="col-span-3 space-y-3 border border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30 p-3">
            <div>
              <label for="mcp-ws-url" class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Upstream URL</label>
              <input
                id="mcp-ws-url"
                type="text"
                bind:value={formWSURL}
                placeholder="ws://localhost:9001/socket or wss://example.com/events"
                class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
              />
              <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">
                Optional raw passthrough at <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">/gateway/v1/mcp/&#123;name&#125;/ws</code>. Supports <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">ws://</code>, <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">wss://</code>, and <code class="px-1 py-0.5 bg-gray-100 dark:bg-dark-elevated">&#123;&#123;var:key&#125;&#125;</code> secrets.
              </p>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label for="mcp-ws-pass-query" class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Pass Query Params</label>
                <input
                  id="mcp-ws-pass-query"
                  type="text"
                  bind:value={formWSPassQueryParams}
                  placeholder="tabId, providerId"
                  class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
                <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Comma-separated allowlist. Empty forwards all query params except AT's <code>token</code>.</p>
              </div>
              <div>
                <label for="mcp-ws-pass-headers" class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Pass Headers</label>
                <input
                  id="mcp-ws-pass-headers"
                  type="text"
                  bind:value={formWSPassHeaders}
                  placeholder="X-Client-Trace, Sec-WebSocket-Protocol"
                  class="mt-1 w-full border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
                <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Comma-separated client headers to explicitly copy. <code>Authorization</code> and <code>Cookie</code> are blocked.</p>
              </div>
            </div>

            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Upstream Headers</span>
                <button
                  type="button"
                  onclick={addWSHeader}
                  class="flex items-center gap-1 px-2 py-1 text-[11px] border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-white dark:hover:bg-dark-elevated transition-colors"
                >
                  <Plus size={11} />
                  Add Header
                </button>
              </div>

              {#if formWSHeaders.length === 0}
                <p class="text-xs text-gray-400 dark:text-dark-text-muted">No headers configured. Add one if the upstream WebSocket needs a token, e.g. <code>Authorization: Bearer &#123;&#123;var:tool_token&#125;&#125;</code>.</p>
              {:else}
                <div class="space-y-1.5">
                  {#each formWSHeaders as header, i}
                    <div class="grid grid-cols-[1fr_1fr_auto] gap-2 items-center">
                      <input
                        type="text"
                        value={header.key}
                        oninput={(e) => updateWSHeader(i, 'key', e.currentTarget.value)}
                        placeholder="Header name"
                        class="border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                      <input
                        type="text"
                        value={header.value}
                        oninput={(e) => updateWSHeader(i, 'value', e.currentTarget.value)}
                        placeholder="Header value or &#123;&#123;var:key&#125;&#125;"
                        class="border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                      <button
                        type="button"
                        onclick={() => removeWSHeader(i)}
                        class="p-1.5 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
                        title="Remove header"
                      >
                        <X size={13} />
                      </button>
                    </div>
                  {/each}
                </div>
              {/if}

              <p class="text-xs text-amber-600 dark:text-amber-400">
                Client auth headers and cookies are not forwarded to the upstream; only headers configured here are injected.
              </p>
            </div>
          </div>
        </div>

        <!-- Internal MCPs -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">
            <div class="flex items-center gap-1.5">
              <Layers size={14} />
              Internal MCPs
            </div>
          </span>
          <div class="col-span-3">
            {#if availableMCPSets.length > 0}
              <div class="space-y-1.5 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each availableMCPSets as mcp}
                  <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-white dark:hover:bg-dark-elevated transition-colors">
                    <input
                      type="checkbox"
                      checked={formMCPSets.includes(mcp.name)}
                      onchange={() => {
                        if (formMCPSets.includes(mcp.name)) {
                          formMCPSets = formMCPSets.filter(n => n !== mcp.name);
                        } else {
                          formMCPSets = [...formMCPSets, mcp.name];
                        }
                      }}
                      class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                    />
                    <div class="flex-1 min-w-0">
                      <span class="text-xs font-mono font-medium text-gray-700 dark:text-dark-text-secondary">{mcp.name}</span>
                      {#if mcp.description}
                        <div class="text-xs text-gray-400 dark:text-dark-text-muted truncate">{mcp.description}</div>
                      {/if}
                    </div>
                  </label>
                {/each}
              </div>
              <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Select internal MCPs to aggregate and expose through this server's gateway endpoint.</p>
            {:else}
              <span class="text-xs text-gray-400 dark:text-dark-text-muted">No internal MCPs configured. Add them on the <a href="#/mcps" class="underline hover:text-gray-600 dark:hover:text-dark-text-secondary">MCP page</a>.</span>
            {/if}
          </div>
        </div>

        <!-- Builtin Tools -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">
            <div class="flex items-center gap-1.5">
              <Wrench size={14} />
              Builtin Tools
            </div>
          </span>
          <div class="col-span-3">
            {#if builtinToolDefs.length > 0}
              <div class="space-y-1.5 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each builtinToolDefs as tool}
                  <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-white dark:hover:bg-dark-elevated transition-colors">
                    <input
                      type="checkbox"
                      checked={formBuiltinTools.includes(tool.name)}
                      onchange={() => {
                        if (formBuiltinTools.includes(tool.name)) {
                          formBuiltinTools = formBuiltinTools.filter(n => n !== tool.name);
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
              </div>
              <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Server-side builtin tools (file ops, shell, etc.) available on this endpoint.</p>
            {:else}
              <span class="text-xs text-gray-400 dark:text-dark-text-muted">No builtin tools available.</span>
            {/if}
          </div>
        </div>

        <!-- Workflows -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">
            <div class="flex items-center gap-1.5">
              <GitBranch size={14} />
              Workflows
            </div>
          </span>
          <div class="col-span-3">
            {#if availableWorkflows.length > 0}
              <div class="space-y-1.5 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each availableWorkflows as wf}
                  <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-white dark:hover:bg-dark-elevated transition-colors">
                    <input
                      type="checkbox"
                      checked={formWorkflowIds.includes(wf.id)}
                      onchange={() => {
                        if (formWorkflowIds.includes(wf.id)) {
                          formWorkflowIds = formWorkflowIds.filter(id => id !== wf.id);
                        } else {
                          formWorkflowIds = [...formWorkflowIds, wf.id];
                        }
                      }}
                      class="mt-0.5 w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                    />
                    <div class="flex-1 min-w-0">
                      <span class="text-xs font-mono font-medium text-gray-700 dark:text-dark-text-secondary">{wf.name}</span>
                      {#if wf.description}
                        <div class="text-xs text-gray-400 dark:text-dark-text-muted truncate">{wf.description}</div>
                      {/if}
                    </div>
                  </label>
                {/each}
              </div>
              <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Expose selected workflows as individual MCP tools on this endpoint.</p>
            {:else}
              <span class="text-xs text-gray-400 dark:text-dark-text-muted">No workflows available.</span>
            {/if}
          </div>
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
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Endpoint</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
          </tr>
        </thead>
        <tbody>
          {#each servers as s}
            <tr class="border-t border-gray-100 dark:border-dark-border hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2">
                  <div class="font-medium text-gray-900 dark:text-dark-text text-sm">{s.name}</div>
                  {#if s.public}
                    <span class="px-1.5 py-0.5 text-[10px] uppercase tracking-wide bg-green-50 dark:bg-green-950/20 text-green-700 dark:text-green-400 border border-green-200 dark:border-green-900">Public</span>
                  {/if}
                  {#if s.config.ws_upstream?.url}
                    <span class="px-1.5 py-0.5 text-[10px] uppercase tracking-wide bg-blue-50 dark:bg-blue-950/20 text-blue-700 dark:text-blue-400 border border-blue-200 dark:border-blue-900">WS</span>
                  {/if}
                </div>
                {#if s.config.description}
                  <div class="text-xs text-gray-500 dark:text-dark-text-muted truncate max-w-64">{s.config.description}</div>
                {/if}
                {#if (s.servers || []).length > 0}
                  <div class="flex items-center gap-1 mt-1 flex-wrap">
                    {#each s.servers || [] as mcpName}
                      <span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-mono bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400 border border-purple-200 dark:border-purple-800">
                        <Layers size={9} />
                        {mcpName}
                      </span>
                    {/each}
                  </div>
                {/if}
                {#if (s.config.enabled_builtin_tools || []).length > 0}
                  <div class="flex items-center gap-1 mt-1 flex-wrap">
                    {#each s.config.enabled_builtin_tools || [] as tool}
                      <span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-mono bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border">
                        <Wrench size={9} />
                        {tool}
                      </span>
                    {/each}
                  </div>
                {/if}
              </td>
              <td class="px-4 py-2.5">
                <div class="space-y-1">
                  <button
                    onclick={() => copyEndpoint(s.name)}
                    class="flex items-center gap-1 text-xs font-mono text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors group"
                    title="Click to copy MCP endpoint URL"
                  >
                    <Copy size={10} class={copiedName === `mcp:${s.name}` ? 'text-green-500' : 'text-gray-400 dark:text-dark-text-faint group-hover:text-gray-500'} />
                    <span class="truncate max-w-48">.../mcp/{s.name}</span>
                  </button>
                  {#if s.config.ws_upstream?.url}
                    <button
                      onclick={() => copyWSEndpoint(s.name)}
                      class="flex items-center gap-1 text-xs font-mono text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 transition-colors group"
                      title="Click to copy WebSocket passthrough URL"
                    >
                      <Copy size={10} class={copiedName === `ws:${s.name}` ? 'text-green-500' : 'text-blue-400 dark:text-blue-500 group-hover:text-blue-500'} />
                      <span class="truncate max-w-48">.../mcp/{s.name}/ws</span>
                    </button>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-right">
                {#if deleteConfirm === s.id}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => handleDelete(s.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors">Cancel</button>
                  </div>
                {:else}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => handleExportServer(s)} class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors" title="Export as JSON">
                      <Download size={14} />
                    </button>
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
</div>
