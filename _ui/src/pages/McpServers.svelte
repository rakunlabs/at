<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import ExecutionBinding from '@/lib/components/ExecutionBinding.svelte';
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
  import ImageGenerationSettings from '@/lib/components/ImageGenerationSettings.svelte';
  import { imageGenerationConfig, imageGenerationForm, type ImageGenerationForm } from '@/lib/helper/image-generation';
  import { deploymentUrl, deploymentWsUrl } from '@/lib/helper/deployment-url';
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
    Image as ImageIcon,
    ChevronDown,
    ChevronRight,
    Settings2,
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
  let formImageGeneration = $state<ImageGenerationForm>(imageGenerationForm());
  let showBuiltinToolsSection = $state(false);
  let showWorkflowsSection = $state(false);
  let showAdvanced = $state(false);
  const advancedSummary = $derived(
    [
      formBuiltinTools.length ? `${formBuiltinTools.length} builtin` : '',
      formWorkflowIds.length ? `${formWorkflowIds.length} workflow` : '',
      formWSURL.trim() ? 'WebSocket' : '',
    ]
      .filter(Boolean)
      .join(', '),
  );

  function mcpSetSummary(set: MCPSet): string {
    const cfg = set.config || {};
    const parts: string[] = [];
    const builtin = cfg.enabled_builtin_tools?.length ?? 0;
    if (builtin) parts.push(`${builtin} builtin`);
    if (cfg.workflow_ids?.length) parts.push(`${cfg.workflow_ids.length} workflow`);
    const upstreams = (cfg.mcp_upstreams?.length ?? 0) + (set.urls?.length ?? 0);
    if (upstreams) parts.push(`${upstreams} upstream`);
    const http = (cfg.http_tools?.length ?? 0) + (cfg.inline_tools?.length ?? 0);
    if (http) parts.push(`${http} custom`);
    if (cfg.enabled_builtin_tools?.includes('generate_image') && cfg.image_generation?.provider) {
      parts.push(`images: ${cfg.image_generation.provider}`);
    }
    return parts.join(' · ');
  }

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
    formImageGeneration = imageGenerationForm();
    showBuiltinToolsSection = false;
    showWorkflowsSection = false;
    showAdvanced = false;
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
    formImageGeneration = imageGenerationForm(s.config.image_generation);
    // Existing servers that carry tools directly keep them visible.
    showAdvanced = advancedSummary !== '';
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

      const imageGeneration = imageGenerationConfig(formImageGeneration, formBuiltinTools);
      if (imageGeneration) {
        config.image_generation = imageGeneration;
      } else {
        delete config.image_generation;
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
        const created = await createMCPServer(payload);
        editingId = created.id;
        addToast('MCP server saved. Configure its execution identity below before connecting.');
        await loadServers();
        return;
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

  // Both endpoints are registered under the deployment base path, so the
  // copied URL has to carry it or it 404s on a sub-path deployment.
  function copyEndpoint(name: string) {
    navigator.clipboard.writeText(deploymentUrl(`gateway/v1/mcp/${name}`));
    copiedName = `mcp:${name}`;
    setTimeout(() => { copiedName = null; }, 2000);
  }

  function copyWSEndpoint(name: string) {
    navigator.clipboard.writeText(deploymentWsUrl(`gateway/v1/mcp/${name}/ws`));
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
      <h1 class="text-lg font-semibold text-dark-text">MCP Servers</h1>
      <p class="text-xs text-dark-text-muted mt-1">
        Gateway endpoints that serve tools to external agents.
        Connect via <code class="px-1 py-0.5 bg-dark-elevated">POST /gateway/v1/mcp/&#123;name&#125;</code>; Bearer token auth is required unless Public mode is enabled.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={loadServers}
        class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={() => serverImportFileInput.click()}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated"
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
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs whitespace-nowrap font-medium text-dark-base bg-accent hover:bg-accent-hover"
      >
        <Plus size={12} />
        New MCP Server
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-dark-border bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border bg-dark-base">
        <span class="text-sm font-medium text-dark-text">
          {editingId ? `Edit: ${formName}` : 'New MCP Server'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        {#if editingId}
          {#key editingId}<ExecutionBinding kind="mcp" subjectId={editingId} />{/key}
        {:else}
          <p class="text-xs text-dark-text-secondary">Save the server first, then select its execution identity before connecting an MCP client.</p>
        {/if}
        <!-- Name -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="mcp-name" class="text-sm font-medium text-dark-text-secondary">Name</label>
          <input
            id="mcp-name"
            type="text"
            bind:value={formName}
            placeholder="e.g., my-api, docs-search"
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
          />
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="mcp-desc" class="text-sm font-medium text-dark-text-secondary">Description</label>
          <input
            id="mcp-desc"
            type="text"
            bind:value={formDescription}
            placeholder="What this MCP server provides (optional)"
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
          />
        </div>

        <!-- Public Access -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-dark-text-secondary pt-1.5">Access</span>
          <label class="col-span-3 flex items-start gap-2 cursor-pointer border border-dark-border bg-dark-base/30 p-3">
            <input
              type="checkbox"
              bind:checked={formPublic}
              class="mt-0.5 w-3.5 h-3.5 bg-dark-elevated border-dark-border-subtle accent-accent"
            />
            <span class="text-xs text-dark-text-secondary leading-relaxed">
              <span class="font-medium text-dark-text">Public endpoint</span>
              <span class="block text-dark-text-muted mt-0.5">Allow unauthenticated MCP clients to list and call this server's tools. Only enable this for tools safe to expose without an AT token.</span>
            </span>
          </label>
        </div>

        <!-- MCP sets: where this server's tools come from -->
        <div class="border border-dark-border">
          <div class="flex items-center justify-between gap-2 px-3 py-2 bg-dark-base border-b border-dark-border-subtle">
            <div class="flex items-center gap-2 text-sm font-medium text-dark-text-secondary">
              <Layers size={14} />
              MCP sets
              {#if formMCPSets.length > 0}
                <span class="text-xs font-normal text-dark-text-muted">({formMCPSets.length} selected)</span>
              {/if}
            </div>
            <a href="#/mcps" class="flex items-center gap-1 text-xs text-dark-text-muted hover:text-dark-text">
              <Plus size={12} />
              New set
            </a>
          </div>
          <div class="p-3">
            {#if availableMCPSets.length > 0}
              <div class="space-y-1.5">
                {#each availableMCPSets as mcp (mcp.id)}
                  <div class="flex items-start gap-2 p-2 border border-dark-border hover:bg-dark-elevated">
                    <input
                      id={`mcp-set-${mcp.id}`}
                      type="checkbox"
                      checked={formMCPSets.includes(mcp.name)}
                      onchange={() => {
                        if (formMCPSets.includes(mcp.name)) {
                          formMCPSets = formMCPSets.filter(n => n !== mcp.name);
                        } else {
                          formMCPSets = [...formMCPSets, mcp.name];
                        }
                      }}
                      class="mt-0.5 w-3.5 h-3.5 bg-dark-elevated border-dark-border-subtle accent-accent"
                    />
                    <label for={`mcp-set-${mcp.id}`} class="flex-1 min-w-0 cursor-pointer">
                      <div class="flex items-center gap-1.5 flex-wrap">
                        <span class="text-xs font-mono font-medium text-dark-text-secondary">{mcp.name}</span>
                        <span class="px-1 py-px text-[10px] border border-dark-border text-dark-text-muted">{mcp.owner_user_id ? 'personal' : 'workspace'}</span>
                        {#if mcpSetSummary(mcp)}
                          <span class="text-[11px] text-dark-text-muted">{mcpSetSummary(mcp)}</span>
                        {/if}
                      </div>
                      {#if mcp.description}
                        <div class="text-xs text-dark-text-muted truncate">{mcp.description}</div>
                      {/if}
                    </label>
                    <a
                      href={`#/mcps?tab=${mcp.owner_user_id ? 'my-mcps' : 'workspace-mcps'}&edit=${encodeURIComponent(mcp.id)}`}
                      class="shrink-0 p-1 text-dark-text-muted hover:text-dark-text-secondary"
                      title="Edit this set (tools, image generation, upstreams)"
                    >
                      <Pencil size={12} />
                    </a>
                  </div>
                {/each}
              </div>
              <p class="text-xs text-dark-text-muted mt-2">
                Build tools in an MCP set — builtin tools, image generation settings, workflows, upstream MCPs — then expose one or more sets here. The same set can also be attached to agents and Chats.
              </p>
            {:else}
              <p class="text-xs text-dark-text-muted">
                No MCP sets yet. <a href="#/mcps" class="underline hover:text-dark-text-secondary">Create one on the MCP page</a>, add its tools there, then select it here.
              </p>
            {/if}
          </div>
        </div>

        <!-- Advanced -->
        <div class="border border-dark-border">
          <button
            type="button"
            onclick={() => (showAdvanced = !showAdvanced)}
            aria-expanded={showAdvanced}
            class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-dark-text-secondary hover:bg-dark-elevated"
          >
            {#if showAdvanced}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
            <Settings2 size={14} />
            Advanced
            <span class="text-xs font-normal text-dark-text-muted truncate">
              {advancedSummary || 'tools directly on this server, WebSocket passthrough'}
            </span>
          </button>
          {#if showAdvanced}
          <div class="p-3 space-y-4 border-t border-dark-border-subtle">
          <p class="text-xs text-dark-text-muted">
            Tools added here exist only on this server. Prefer an MCP set above unless you need a one-off.
          </p>
        <!-- Builtin Tools -->
        <div class="border border-dark-border">
          <button
            type="button"
            onclick={() => (showBuiltinToolsSection = !showBuiltinToolsSection)}
            aria-expanded={showBuiltinToolsSection}
            class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-dark-text-secondary hover:bg-dark-elevated"
          >
            {#if showBuiltinToolsSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
            <Wrench size={14} />
            Builtin Tools
            {#if formBuiltinTools.length > 0}
              <span class="text-xs font-normal text-dark-text-muted truncate">({formBuiltinTools.length}: {formBuiltinTools.join(', ')})</span>
            {/if}
          </button>
          {#if showBuiltinToolsSection}
            <div class="px-3 pb-3 pt-2 border-t border-dark-border-subtle">
              {#if builtinToolDefs.length > 0}
                <div class="space-y-1.5">
                  {#each builtinToolDefs as tool}
                    <label class="flex items-start gap-2 cursor-pointer p-2 border border-dark-border hover:bg-dark-elevated">
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
                        class="mt-0.5 w-3.5 h-3.5 bg-dark-elevated border-dark-border-subtle accent-accent"
                      />
                      <div class="flex-1 min-w-0">
                        <span class="text-xs font-mono font-medium text-dark-text-secondary">{tool.name}</span>
                        {#if tool.description}
                          <div class="text-xs text-dark-text-muted truncate">{tool.description}</div>
                        {/if}
                      </div>
                    </label>
                  {/each}
                </div>
                <p class="text-xs text-dark-text-muted mt-1">Server-side builtin tools (file ops, shell, etc.) available on this endpoint.</p>
              {:else}
                <span class="text-xs text-dark-text-muted">No builtin tools available.</span>
              {/if}
            </div>
          {/if}
        </div>

        {#if formBuiltinTools.includes('generate_image')}
          <!-- Image generation -->
          <div class="border border-dark-border">
            <div class="flex items-center gap-2 px-3 py-2 text-sm font-medium text-dark-text-secondary bg-dark-base border-b border-dark-border-subtle">
              <ImageIcon size={14} />
              Image generation
              <span class="text-xs font-normal text-dark-text-muted">settings for generate_image</span>
            </div>
            <div class="p-3">
              <ImageGenerationSettings bind:value={formImageGeneration} />
            </div>
          </div>
        {/if}
        <!-- Workflows -->
        <div class="border border-dark-border">
          <button
            type="button"
            onclick={() => (showWorkflowsSection = !showWorkflowsSection)}
            aria-expanded={showWorkflowsSection}
            class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-dark-text-secondary hover:bg-dark-elevated"
          >
            {#if showWorkflowsSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
            <GitBranch size={14} />
            Workflows
            {#if formWorkflowIds.length > 0}
              <span class="text-xs font-normal text-dark-text-muted">({formWorkflowIds.length} selected)</span>
            {/if}
          </button>
          {#if showWorkflowsSection}
            <div class="px-3 pb-3 pt-2 border-t border-dark-border-subtle">
              {#if availableWorkflows.length > 0}
                <div class="space-y-1.5">
                  {#each availableWorkflows as wf}
                    <label class="flex items-start gap-2 cursor-pointer p-2 border border-dark-border hover:bg-dark-elevated">
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
                        class="mt-0.5 w-3.5 h-3.5 bg-dark-elevated border-dark-border-subtle accent-accent"
                      />
                      <div class="flex-1 min-w-0">
                        <span class="text-xs font-mono font-medium text-dark-text-secondary">{wf.name}</span>
                        {#if wf.description}
                          <div class="text-xs text-dark-text-muted truncate">{wf.description}</div>
                        {/if}
                      </div>
                    </label>
                  {/each}
                </div>
                <p class="text-xs text-dark-text-muted mt-1">Expose selected workflows as individual MCP tools on this endpoint.</p>
              {:else}
                <span class="text-xs text-dark-text-muted">No workflows available.</span>
              {/if}
            </div>
          {/if}
        </div>

        <!-- WebSocket Passthrough -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-dark-text-secondary pt-1.5">
            <div class="flex items-center gap-1.5">
              <Server size={14} />
              WebSocket
            </div>
          </span>
          <div class="col-span-3 space-y-3 border border-dark-border bg-dark-base/30 p-3">
            <div>
              <label for="mcp-ws-url" class="text-xs font-medium text-dark-text-secondary">Upstream URL</label>
              <input
                id="mcp-ws-url"
                type="text"
                bind:value={formWSURL}
                placeholder="ws://localhost:9001/socket or wss://example.com/events"
                class="mt-1 w-full border border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
              />
              <p class="text-xs text-dark-text-muted mt-1">
                Optional raw passthrough at <code class="px-1 py-0.5 bg-dark-elevated">/gateway/v1/mcp/&#123;name&#125;/ws</code>. Supports <code class="px-1 py-0.5 bg-dark-elevated">ws://</code>, <code class="px-1 py-0.5 bg-dark-elevated">wss://</code>, and <code class="px-1 py-0.5 bg-dark-elevated">&#123;&#123;var:key&#125;&#125;</code> secrets.
              </p>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label for="mcp-ws-pass-query" class="text-xs font-medium text-dark-text-secondary">Pass Query Params</label>
                <input
                  id="mcp-ws-pass-query"
                  type="text"
                  bind:value={formWSPassQueryParams}
                  placeholder="tabId, providerId"
                  class="mt-1 w-full border border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
                />
                <p class="text-xs text-dark-text-muted mt-1">Comma-separated allowlist. Empty forwards all query params except AT's <code>token</code>.</p>
              </div>
              <div>
                <label for="mcp-ws-pass-headers" class="text-xs font-medium text-dark-text-secondary">Pass Headers</label>
                <input
                  id="mcp-ws-pass-headers"
                  type="text"
                  bind:value={formWSPassHeaders}
                  placeholder="X-Client-Trace, Sec-WebSocket-Protocol"
                  class="mt-1 w-full border border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
                />
                <p class="text-xs text-dark-text-muted mt-1">Comma-separated client headers to explicitly copy. <code>Authorization</code> and <code>Cookie</code> are blocked.</p>
              </div>
            </div>

            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-dark-text-secondary">Upstream Headers</span>
                <button
                  type="button"
                  onclick={addWSHeader}
                  class="flex items-center gap-1 px-2 py-1 text-[11px] border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated"
                >
                  <Plus size={11} />
                  Add Header
                </button>
              </div>

              {#if formWSHeaders.length === 0}
                <p class="text-xs text-dark-text-muted">No headers configured. Add one if the upstream WebSocket needs a token, e.g. <code>Authorization: Bearer &#123;&#123;var:tool_token&#125;&#125;</code>.</p>
              {:else}
                <div class="space-y-1.5">
                  {#each formWSHeaders as header, i}
                    <div class="grid grid-cols-[1fr_1fr_auto] gap-2 items-center">
                      <input
                        type="text"
                        value={header.key}
                        oninput={(e) => updateWSHeader(i, 'key', e.currentTarget.value)}
                        placeholder="Header name"
                        class="border border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
                      />
                      <input
                        type="text"
                        value={header.value}
                        oninput={(e) => updateWSHeader(i, 'value', e.currentTarget.value)}
                        placeholder="Header value or &#123;&#123;var:key&#125;&#125;"
                        class="border border-dark-border-subtle px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
                      />
                      <button
                        type="button"
                        onclick={() => removeWSHeader(i)}
                        class="p-1.5 text-dark-text-muted hover:text-red-400 hover:bg-red-900/20"
                        title="Remove header"
                      >
                        <X size={13} />
                      </button>
                    </div>
                  {/each}
                </div>
              {/if}

              <p class="text-xs text-amber-400">
                Client auth headers and cookies are not forwarded to the upstream; only headers configured here are injected.
              </p>
            </div>
          </div>
        </div>

          </div>
          {/if}
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-dark-border">
          <button
            type="button"
            onclick={resetForm}
            class="px-3 py-1.5 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={saving}
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-dark-base bg-accent hover:bg-accent-hover disabled:opacity-50"
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
    <div class="text-xs text-dark-text-muted py-4 text-center">Loading MCP servers...</div>
  {:else if servers.length === 0 && !showForm}
    <div class="border border-dashed border-dark-border py-8 text-center">
      <Server size={24} class="mx-auto text-dark-text-faint mb-2" />
      <p class="text-sm text-dark-text-muted mb-1">No MCP servers</p>
      <p class="text-xs text-dark-text-muted mb-3">Create an MCP server to expose tools to external agents</p>
    </div>
  {:else if servers.length > 0 && !showForm}
    <div class="border border-dark-border overflow-hidden">
      <table class="w-full">
        <thead>
          <tr class="bg-dark-base border-b border-dark-border">
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Name</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Endpoint</th>
            <th class="text-right px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
          </tr>
        </thead>
        <tbody>
          {#each servers as s}
            <tr class="border-t border-dark-border hover:bg-dark-elevated/50">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2">
                  <div class="font-medium text-dark-text text-sm">{s.name}</div>
                  {#if s.public}
                    <span class="px-1.5 py-0.5 text-[10px] uppercase tracking-wide bg-green-950/20 text-green-400 border border-green-900">Public</span>
                  {/if}
                  {#if s.config.ws_upstream?.url}
                    <span class="px-1.5 py-0.5 text-[10px] uppercase tracking-wide bg-blue-950/20 text-blue-400 border border-blue-900">WS</span>
                  {/if}
                </div>
                {#if s.config.description}
                  <div class="text-xs text-dark-text-muted truncate max-w-64">{s.config.description}</div>
                {/if}
                {#if (s.servers || []).length > 0}
                  <div class="flex items-center gap-1 mt-1 flex-wrap">
                    {#each s.servers || [] as mcpName}
                      <span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-mono bg-purple-900/20 text-purple-400 border border-purple-800">
                        <Layers size={9} />
                        {mcpName}
                      </span>
                    {/each}
                  </div>
                {/if}
                {#if (s.config.enabled_builtin_tools || []).length > 0}
                  <div class="flex items-center gap-1 mt-1 flex-wrap">
                    {#each s.config.enabled_builtin_tools || [] as tool}
                      <span class="inline-flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-mono bg-dark-elevated text-dark-text-muted border border-dark-border">
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
                    class="flex items-center gap-1 text-xs font-mono text-dark-text-muted hover:text-dark-text-secondary group"
                    title="Click to copy MCP endpoint URL"
                  >
                    <Copy size={10} class={copiedName === `mcp:${s.name}` ? 'text-green-500' : 'text-dark-text-faint group-hover:text-dark-text-muted'} />
                    <span class="truncate max-w-48">.../mcp/{s.name}</span>
                  </button>
                  {#if s.config.ws_upstream?.url}
                    <button
                      onclick={() => copyWSEndpoint(s.name)}
                      class="flex items-center gap-1 text-xs font-mono text-blue-400 hover:text-blue-300 group"
                      title="Click to copy WebSocket passthrough URL"
                    >
                      <Copy size={10} class={copiedName === `ws:${s.name}` ? 'text-green-500' : 'text-blue-500 group-hover:text-blue-500'} />
                      <span class="truncate max-w-48">.../mcp/{s.name}/ws</span>
                    </button>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-right">
                {#if deleteConfirm === s.id}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => handleDelete(s.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs text-dark-text-muted hover:text-dark-text-secondary">Cancel</button>
                  </div>
                {:else}
                  <div class="flex items-center gap-1 justify-end">
                    <button onclick={() => handleExportServer(s)} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text" title="Export as JSON">
                      <Download size={14} />
                    </button>
                    <button onclick={() => openEdit(s)} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text" title="Edit">
                      <Pencil size={14} />
                    </button>
                    <button onclick={() => (deleteConfirm = s.id)} class="p-1.5 hover:bg-red-900/20 text-dark-text-muted hover:text-red-400" title="Delete">
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
