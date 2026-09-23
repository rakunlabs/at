<script lang="ts">
  import { routeChoice } from '@/lib/helper/route-choice.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { can } from '@/lib/store/workspace.svelte';
  import { isNativeAdmin } from '@/lib/store/auth.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listMCPSets, createMCPSet, updateMCPSet, deleteMCPSet, exportMCPSet, importMCPSet, getMCPSetStdioStatus, restartMCPSetStdio, stopMCPSetStdio, inspectMCPSetUpstreams, type MCPSet, type MCPStdioUpstreamStatus, type MCPUpstreamInspection } from '@/lib/api/mcp-sets';
  import { type MCPHTTPTool, type MCPUpstream } from '@/lib/api/mcp-servers';
  import { listMCPBinaries, uploadMCPBinary, deleteMCPBinary, listStdioProcesses, type MCPBinary, type StdioProcess } from '@/lib/api/mcp-binaries';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listBuiltinTools, type BuiltinToolDef } from '@/lib/api/mcp';
  import { listWorkflows, type Workflow } from '@/lib/api/workflows';
  import { Layers, Plus, Pencil, Trash2, X, Save, RefreshCw, ChevronDown, ChevronRight, Globe, Network, Wand2, Bot, Store, Download, Upload, Check, Package, Wrench, GitBranch, HardDrive, RotateCw, Square, Copy } from 'lucide-svelte';
  import { listMCPTemplates, installMCPTemplate, type MCPTemplate } from '@/lib/api/mcp-templates';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import HTTPToolBuilderPanel from '@/lib/components/HTTPToolBuilderPanel.svelte';

  storeNavbar.title = 'MCP';

  // ─── Tab State ───

  const tabRoute = routeChoice('tab', ['my-mcps', 'store', 'binaries'] as const, 'my-mcps');
  let activeTab = $derived(tabRoute.value);
  let mayWrite = $derived(isNativeAdmin() || can('mcp.write'));
  let mayUse = $derived(isNativeAdmin() || can('mcp.use'));
  let platformAdmin = $derived(isNativeAdmin());

  $effect(() => {
    if (activeTab === 'binaries' && !platformAdmin) tabRoute.value = 'my-mcps';
  });

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

  // ─── My MCPs Category Filter ───

  let mySelectedCategory = $state('');
  let myCategories = $derived([...new Set((sets || []).map((s) => s.category).filter((c): c is string => Boolean(c)))].sort());
  let filteredSets = $derived(
    mySelectedCategory
      ? (sets || []).filter((s) => s.category === mySelectedCategory)
      : sets || []
  );
  let availableSkills = $state<Skill[]>([]);
  let builtinToolDefs = $state<BuiltinToolDef[]>([]);
  let availableWorkflows = $state<Workflow[]>([]);
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
  let limit = $state(25);
  let total = $state(0);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formCategory = $state('');
  let formTags = $state<string[]>([]);

  // Config form fields (HTTP/External/Skills)
  let formHTTPTools = $state<MCPHTTPTool[]>([]);
  let formMCPUpstreams = $state<MCPUpstream[]>([]);
  let formEnabledSkills = $state<string[]>([]);
  let formBuiltinTools = $state<string[]>([]);
  let formWorkflowIds = $state<string[]>([]);

  // Section visibility
  let showHTTPSection = $state(false);
  let showSkillsSection = $state(false);
  let showBuiltinToolsSection = $state(false);
  let showWorkflowsSection = $state(false);
  let showUpstreamSection = $state(false);


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

  async function loadWorkflows() {
    try {
      const res = await listWorkflows({ _limit: 500 });
      availableWorkflows = res.data || [];
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
  loadSkills();
  loadBuiltinToolDefs();
  loadWorkflows();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formCategory = '';
    formTags = [];
    formHTTPTools = [];
    formMCPUpstreams = [];
    formEnabledSkills = [];
    formBuiltinTools = [];
    formWorkflowIds = [];
    editingId = null;
    showForm = false;
    showHTTPSection = false;
    showSkillsSection = false;
    showBuiltinToolsSection = false;
    showWorkflowsSection = false;
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
    formCategory = set.category || '';
    formTags = set.tags ? [...set.tags] : [];
    // Config fields
    const cfg = set.config || {} as any;
    formHTTPTools = (cfg.http_tools ?? []).map((t: MCPHTTPTool) => ({ ...t, headers: t.headers ? { ...t.headers } : {}, input_schema: t.input_schema ? JSON.parse(JSON.stringify(t.input_schema)) : { type: 'object', properties: {} } }));
    formMCPUpstreams = (cfg.mcp_upstreams ?? []).map((u: MCPUpstream) => ({ ...u, headers: u.headers ? { ...u.headers } : undefined, args: u.args ? [...u.args] : undefined, env: u.env ? { ...u.env } : undefined }));
    formEnabledSkills = cfg.enabled_skills ?? [];
    formBuiltinTools = cfg.enabled_builtin_tools ?? [];
    formWorkflowIds = cfg.workflow_ids ?? [];
    showHTTPSection = formHTTPTools.length > 0;
    showSkillsSection = formEnabledSkills.length > 0;
    showBuiltinToolsSection = formBuiltinTools.length > 0;
    showWorkflowsSection = formWorkflowIds.length > 0;
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
        category: formCategory.trim() || undefined,
        tags: formTags.length > 0 ? formTags : undefined,
        config: {
          description: formDescription.trim(),
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
          workflow_ids: formWorkflowIds,
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

  // ─── Export / Import ───

  async function handleExportMCPSet(set: MCPSet) {
    try {
      const data = await exportMCPSet(set.id);
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${set.name}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      addToast(`Exported "${set.name}" as ${set.name}.json`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to export MCP', 'alert');
    }
  }

  let mcpImportFileInput = $state<HTMLInputElement | undefined>(undefined);
  async function handleImportMCPFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      const data = JSON.parse(text);
      await importMCPSet(data);
      addToast(`Imported MCP from "${file.name}"`);
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import MCP', 'alert');
    }
    input.value = '';
  }

  // ─── Tool Config Helpers ───

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

  // ─── Stdio process status / restart ───

  function hasStdioUpstreams(set: MCPSet): boolean {
    return (set.config?.mcp_upstreams ?? []).some((u) => (u.command ?? '') !== '');
  }

  // set id → per-upstream stdio status (index refers to config.mcp_upstreams)
  let stdioStatus = $state<Record<string, MCPStdioUpstreamStatus[]>>({});
  let stdioBusy = $state<Record<string, boolean>>({});
  let upstreamInspections = $state<Record<string, MCPUpstreamInspection[]>>({});
  let inspectionBusy = $state<Record<string, boolean>>({});

  async function refreshStdioStatus(setId: string) {
    try {
      const res = await getMCPSetStdioStatus(setId);
      stdioStatus[setId] = res.upstreams || [];
    } catch {}
  }

  async function refreshAllStdioStatuses() {
    if (!platformAdmin) return;
    await Promise.all((sets || []).filter(hasStdioUpstreams).map((s) => refreshStdioStatus(s.id)));
  }

  async function handleRestartStdio(setId: string, index?: number) {
    stdioBusy[setId] = true;
    try {
      const res = await restartMCPSetStdio(setId, index);
      stdioStatus[setId] = res.upstreams || [];
      const failed = (res.upstreams || []).filter((u) => u.error);
      if (failed.length > 0) {
        addToast(`Restart failed: ${failed[0].error}`, 'alert');
      } else {
        addToast('Local MCP process restarted');
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to restart', 'alert');
    } finally {
      stdioBusy[setId] = false;
    }
  }

  async function handleStopStdio(setId: string, index?: number) {
    stdioBusy[setId] = true;
    try {
      await stopMCPSetStdio(setId, index);
      await refreshStdioStatus(setId);
      addToast('Local MCP process stopped');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to stop', 'alert');
    } finally {
      stdioBusy[setId] = false;
    }
  }

  function stdioSummary(setId: string): { running: number; total: number } | null {
    const st = stdioStatus[setId];
    if (!st) return null;
    return { running: st.filter((u) => u.running).length, total: st.length };
  }

  async function handleInspectUpstreams(setId: string, index?: number) {
    inspectionBusy[setId] = true;
    try {
      const result = await inspectMCPSetUpstreams(setId, index);
      if (index === undefined) {
        upstreamInspections[setId] = result.upstreams || [];
      } else {
        const retained = (upstreamInspections[setId] || []).filter((item) => item.index !== index);
        upstreamInspections[setId] = [...retained, ...(result.upstreams || [])].sort((a, b) => a.index - b.index);
      }
      const failed = result.upstreams.filter((item) => item.error);
      const toolCount = result.upstreams.reduce((sum, item) => sum + item.tool_count, 0);
      if (failed.length > 0) {
        addToast(`MCP check completed: ${toolCount} tools found, ${failed.length} upstream${failed.length === 1 ? '' : 's'} failed`, 'warn');
      } else {
        addToast(`MCP check completed: ${toolCount} tools found`);
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to inspect MCP upstreams', 'alert');
    } finally {
      inspectionBusy[setId] = false;
    }
  }

  function inspectionSummary(setId: string): { connected: number; total: number; tools: number } | null {
    const items = upstreamInspections[setId];
    if (!items) return null;
    return {
      connected: items.filter((item) => !item.error).length,
      total: items.length,
      tools: items.reduce((sum, item) => sum + item.tool_count, 0),
    };
  }

  $effect(() => {
    // Refresh stdio statuses whenever the visible set list changes.
    if (platformAdmin && sets.length > 0) refreshAllStdioStatuses();
  });

  // ─── Binaries tab (persistent MCP program library) ───

  let binDir = $state('');
  let binFiles = $state<MCPBinary[]>([]);
  let binLoading = $state(false);
  let binUploading = $state(false);
  let binExecutable = $state(true);
  let binDeleteConfirm = $state<string | null>(null);
  let binFileInput = $state<HTMLInputElement | undefined>(undefined);
  let stdioProcesses = $state<StdioProcess[]>([]);

  async function loadBinaries() {
    binLoading = true;
    try {
      const res = await listMCPBinaries();
      binDir = res.dir;
      binFiles = res.files || [];
      stdioProcesses = await listStdioProcesses();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load binaries', 'alert');
    } finally {
      binLoading = false;
    }
  }

  async function handleBinaryUpload(event: Event) {
    const input = event.target as HTMLInputElement;
    const files = input.files;
    if (!files || files.length === 0) return;
    binUploading = true;
    try {
      for (const file of Array.from(files)) {
        const res = await uploadMCPBinary(file, { executable: binExecutable });
        if (res.extracted) {
          addToast(`"${file.name}" extracted (${res.files} files) → ${res.name}/`);
        } else {
          addToast(`"${file.name}" uploaded`);
        }
      }
      await loadBinaries();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Upload failed', 'alert');
    } finally {
      binUploading = false;
      input.value = '';
    }
  }

  async function handleBinaryDelete(name: string) {
    try {
      await deleteMCPBinary(name);
      binDeleteConfirm = null;
      addToast(`"${name}" deleted`);
      await loadBinaries();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete', 'alert');
    }
  }

  function copyBinaryPath(name: string) {
    const path = `${binDir}/${name}`;
    navigator.clipboard?.writeText(path).then(
      () => addToast('Path copied'),
      () => addToast(path, 'warn'),
    );
  }

  function fmtSize(size: number): string {
    if (size < 1024) return `${size} B`;
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
  }

  function fmtUptime(seconds: number): string {
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  }

  $effect(() => {
    if (activeTab === 'binaries') {
      loadBinaries();
    }
  });

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

<svelte:head>
  <title>AT | MCP</title>
</svelte:head>

<div class="flex h-full">
<div class="flex h-full flex-1 min-w-0">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-6xl mx-auto">
      <!-- Tab Bar -->
      <div class="flex items-center gap-4 mb-4 border-b border-gray-200 dark:border-dark-border">
        <button
          onclick={() => (tabRoute.value = 'my-mcps')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'my-mcps' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Layers size={14} />
          My MCPs
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
        </button>
        <button
          onclick={() => (tabRoute.value = 'store')}
          class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'store' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
        >
          <Store size={14} />
          MCP Store
        </button>
        {#if platformAdmin}
          <button
            onclick={() => (tabRoute.value = 'binaries')}
            class="flex items-center gap-1.5 px-1 pb-2 text-sm font-medium border-b-2 {activeTab === 'binaries' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
          >
            <HardDrive size={14} />
            Binaries
          </button>
        {/if}
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
          {#if !mayWrite}
            <span class="text-xs text-gray-500 dark:text-dark-text-muted" title="The mcp.write capability is required to change MCP Sets">Read only</span>
          {/if}
          <button
            onclick={loadData}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          {#if mayWrite}
            <button
              onclick={() => mcpImportFileInput?.click()}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
              title="Import MCP from JSON file"
            >
              <Upload size={12} />
              Import
            </button>
            <input
              bind:this={mcpImportFileInput}
              type="file"
              accept=".json"
              onchange={handleImportMCPFile}
              class="hidden"
            />
            <button
              onclick={openCreate}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
            >
              <Plus size={12} />
              New MCP
            </button>
          {/if}
        </div>
      </div>
      <!-- Category Filter Chips -->
      {#if myCategories.length > 0}
        <div class="flex items-center gap-2 px-4 py-2 flex-wrap">
          <button
            onclick={() => mySelectedCategory = ''}
            class={["px-2 py-0.5 text-xs border ", !mySelectedCategory ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}
          >All</button>
          {#each myCategories as cat}
            <button
              onclick={() => mySelectedCategory = cat}
              class={["px-2 py-0.5 text-xs border ", mySelectedCategory === cat ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent' : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}
            >{cat}</button>
          {/each}
        </div>
      {/if}

      <!-- Inline Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {editingId ? `Edit: ${formName}` : 'New MCP'}
            </span>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary ">
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
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Description -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <div class="col-span-3">
                <input
                  id="form-description"
                  type="text"
                  bind:value={formDescription}
                  placeholder="What this MCP set contains"
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
                <p class="mt-1 text-xs text-gray-400 dark:text-dark-text-muted">Name and description identify this set inside AT. Upstream server details and tool descriptions are discovered from the MCP server.</p>
              </div>
            </div>

            <!-- Category -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-category" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Category</label>
              <input
                id="form-category"
                type="text"
                bind:value={formCategory}
                placeholder="e.g. OpenMontage, Utilities"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Tags -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-tags" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Tags</label>
              <input
                id="form-tags"
                type="text"
                value={formTags.join(', ')}
                oninput={(e) => { formTags = (e.target as HTMLInputElement).value.split(',').map(t => t.trim()).filter(Boolean); }}
                placeholder="e.g. video, production"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- ═══ HTTP Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showHTTPSection = !showHTTPSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
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
                        <button type="button" onclick={() => removeHTTPTool(i)} class="p-1 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300" title="Remove tool">
                          <Trash2 size={12} />
                        </button>
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="contents">
                          <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Name</span>
                          <input type="text" bind:value={tool.name} placeholder="e.g., get_user, create_ticket"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted " />
                        </label>
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="contents">
                          <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Description</span>
                          <input type="text" bind:value={tool.description} placeholder="What this tool does"
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted " />
                        </label>
                      </div>

                      <div class="grid grid-cols-4 gap-2 items-center">
                        <label class="contents">
                          <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Request</span>
                          <div class="col-span-3 flex gap-2">
                          <select bind:value={tool.method}
                            class="border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text w-24">
                            <option value="GET">GET</option>
                            <option value="POST">POST</option>
                            <option value="PUT">PUT</option>
                            <option value="DELETE">DELETE</option>
                            <option value="PATCH">PATCH</option>
                            <option value="HEAD">HEAD</option>
                          </select>
                          <input type="text" bind:value={tool.url} placeholder={"https://api.example.com/{{.id}}"}
                            class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted " />
                          </div>
                        </label>
                      </div>

                      <!-- Headers -->
                      <div class="grid grid-cols-4 gap-2 items-start">
                        <label class="contents">
                          <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Headers</span>
                          <div class="col-span-3 space-y-1">
                          {#if tool.headers}
                            {#each Object.entries(tool.headers) as [hk, hv]}
                              <div class="flex items-center gap-1">
                                <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
                                <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{hv}</span>
                                <button type="button" onclick={() => removeHeader(i, hk)} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 ">
                                  <X size={10} />
                                </button>
                              </div>
                            {/each}
                          {/if}
                          <div class="flex items-center gap-1">
                            <input type="text" placeholder="Key" bind:value={httpToolNewHeaderKey[i]}
                              class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                            <input type="text" placeholder="Value" bind:value={httpToolNewHeaderValue[i]}
                              class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                            <button type="button" onclick={() => addHeader(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary ">
                              Add
                            </button>
                          </div>
                          <p class="text-xs text-gray-400 dark:text-dark-text-muted">Use <code class="font-mono">{"{{var:key}}"}</code> to reference a variable value</p>
                          </div>
                        </label>
                      </div>

                      <!-- Body Template -->
                      {#if tool.method === 'POST' || tool.method === 'PUT' || tool.method === 'PATCH'}
                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Body</span>
                            <textarea bind:value={tool.body_template} placeholder={'{"key": "{{.value}}"}'}
                              rows="3"
                              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted resize-y"></textarea>
                          </label>
                        </div>
                      {/if}

                      <!-- Input Schema -->
                      <div class="grid grid-cols-4 gap-2 items-start">
                        <label class="contents">
                          <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Input Schema</span>
                          <textarea
                            value={getSchemaText(i)}
                            oninput={(e) => setSchemaText(i, (e.target as HTMLTextAreaElement).value)}
                            rows="4"
                            placeholder={'{"type": "object", "properties": {}, "required": []}'}
                            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted resize-y"
                          ></textarea>
                        </label>
                      </div>
                    </div>
                  {/each}

                  <div class="flex gap-2">
                    <button
                      type="button"
                      onclick={addHTTPTool}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary justify-center"
                    >
                      <Plus size={12} />
                      Add HTTP Tool
                    </button>
                    {#if platformAdmin}
                      <button
                        type="button"
                        onclick={() => { showAIPanel = !showAIPanel; }}
                        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium {showAIPanel ? 'bg-accent-muted text-accent dark:text-accent-text border border-accent/30' : 'border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                        title="Toggle AI HTTP Tool Builder"
                      >
                        <Bot size={12} />
                        AI Builder
                      </button>
                    {/if}
                  </div>
                </div>
              {/if}
            </div>

            <!-- ═══ Skill Tools Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showSkillsSection = !showSkillsSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
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
                      <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated ">
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
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
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
                      <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated ">
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

            <!-- ═══ Workflows Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showWorkflowsSection = !showWorkflowsSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
              >
                {#if showWorkflowsSection}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
                <GitBranch size={14} />
                Workflows
                {#if formWorkflowIds.length > 0}
                  <span class="text-xs text-gray-400 dark:text-dark-text-muted">({formWorkflowIds.length} selected)</span>
                {/if}
              </button>

              {#if showWorkflowsSection}
                <div class="px-4 pb-4 pt-2 space-y-2 border-t border-gray-200 dark:border-dark-border-subtle">
                  {#if availableWorkflows.length > 0}
                    {#each availableWorkflows as wf}
                      <label class="flex items-start gap-2 cursor-pointer p-2 border border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated ">
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
                  {:else}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted">No workflows available.</span>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- ═══ Upstream MCP Servers Section ═══ -->
            <div class="border border-gray-200 dark:border-dark-border-subtle">
              <button
                type="button"
                onclick={() => showUpstreamSection = !showUpstreamSection}
                class="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
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
                              class="px-1.5 py-0.5 {!upstream.command ? 'bg-gray-800 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}">
                              HTTP
                            </button>
                            <button type="button" onclick={() => { formMCPUpstreams[i] = { command: upstream.command || '', args: upstream.args || [], env: upstream.env || {} }; formMCPUpstreams = [...formMCPUpstreams]; }}
                              class="px-1.5 py-0.5 {upstream.command !== undefined && upstream.command !== null ? 'bg-gray-800 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}">
                              Local
                            </button>
                          </div>
                          <button type="button" onclick={() => { formMCPUpstreams = formMCPUpstreams.filter((_, idx) => idx !== i); }} class="p-1 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300" title="Remove server">
                            <Trash2 size={12} />
                          </button>
                        </div>
                      </div>

                      {#if upstream.command !== undefined && upstream.command !== null}
                        <!-- Local Command mode -->
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Command</span>
                            <input
                              type="text"
                              bind:value={formMCPUpstreams[i].command}
                              placeholder="npx"
                              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
                            />
                          </label>
                        </div>
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Args</span>
                            <input
                              type="text"
                              value={(upstream.args ?? []).join(' ')}
                              oninput={(e: Event) => { formMCPUpstreams[i].args = (e.target as HTMLInputElement).value.split(/\s+/).filter(Boolean); formMCPUpstreams = [...formMCPUpstreams]; }}
                              placeholder="@playwright/mcp@latest --headless"
                              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
                            />
                          </label>
                        </div>
                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Env</span>
                            <div class="col-span-3 space-y-1">
                            {#if upstream.env}
                              {#each Object.entries(upstream.env) as [ek, ev]}
                                <div class="flex items-center gap-1">
                                  <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{ek}=</span>
                                  <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{ev}</span>
                                  <button type="button" onclick={() => { if (upstream.env) { delete upstream.env[ek]; formMCPUpstreams = [...formMCPUpstreams]; } }} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 ">
                                    <X size={10} />
                                  </button>
                                </div>
                              {/each}
                            {/if}
                            <div class="flex items-center gap-1">
                              <input type="text" placeholder="Key" bind:value={upstreamNewEnvKey[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                              <input type="text" placeholder="Value" bind:value={upstreamNewEnvValue[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                              <button type="button" onclick={() => addUpstreamEnv(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary ">
                                Add
                              </button>
                            </div>
                            </div>
                          </label>
                        </div>
                        {#if editingId && platformAdmin}
                          {@const st = (stdioStatus[editingId] || []).find((u) => u.index === i)}
                          <div class="flex items-center gap-2 pt-1 border-t border-gray-100 dark:border-dark-border text-xs">
                            {#if st?.running}
                              <span class="flex items-center gap-1 text-green-600 dark:text-green-400">
                                <span class="w-1.5 h-1.5 bg-green-500 inline-block"></span>
                                Running — pid {st.pid}{st.uptime_seconds !== undefined ? ` · up ${fmtUptime(st.uptime_seconds)}` : ''}
                              </span>
                            {:else if st?.exit_error}
                              <span class="text-red-500 dark:text-red-400 truncate" title={st.exit_error}>Exited: {st.exit_error}</span>
                            {:else}
                              <span class="text-gray-400 dark:text-dark-text-muted">Not running — starts on first use</span>
                            {/if}
                            {#if st?.error}
                              <span class="text-red-500 dark:text-red-400 truncate" title={st.error}>{st.error}</span>
                            {/if}
                            <span class="ml-auto flex items-center gap-1">
                              <button type="button" onclick={() => handleRestartStdio(editingId!, i)} disabled={stdioBusy[editingId]}
                                class="flex items-center gap-1 px-1.5 py-0.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
                                title="Kill and respawn this process (picks up saved env/config changes)">
                                <RotateCw size={10} class={stdioBusy[editingId] ? 'animate-spin' : ''} />
                                Restart
                              </button>
                              {#if st?.running}
                                <button type="button" onclick={() => handleStopStdio(editingId!, i)} disabled={stdioBusy[editingId]}
                                  class="flex items-center gap-1 px-1.5 py-0.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
                                  title="Stop this process (next tool call respawns it)">
                                  <Square size={10} />
                                  Stop
                                </button>
                              {/if}
                            </span>
                          </div>
                          <p class="text-xs text-gray-400 dark:text-dark-text-muted">Status reflects the saved configuration; save your changes before restarting.</p>
                        {/if}
                      {:else}
                        <!-- HTTP mode -->
                        <div class="grid grid-cols-4 gap-2 items-center">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">URL</span>
                            <input
                              type="text"
                              bind:value={formMCPUpstreams[i].url}
                              placeholder="https://other-server:8000/mcp"
                              class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
                            />
                          </label>
                          <div class="col-start-2 col-span-3 text-[10px] text-gray-400 dark:text-dark-text-muted">
                            Streamable HTTP endpoint, used exactly as entered. JSON or SSE responses are detected automatically. Legacy GET <code class="font-mono">/sse</code> transport is not supported.
                          </div>
                        </div>

                        <div class="grid grid-cols-4 gap-2 items-start">
                          <label class="contents">
                            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary pt-1">Headers</span>
                            <div class="col-span-3 space-y-1">
                            {#if upstream.headers}
                              {#each Object.entries(upstream.headers) as [hk, hv]}
                                <div class="flex items-center gap-1">
                                  <span class="text-xs font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
                                  <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted truncate">{hv}</span>
                                  <button type="button" onclick={() => { if (upstream.headers) { delete upstream.headers[hk]; formMCPUpstreams = [...formMCPUpstreams]; } }} class="ml-auto p-0.5 text-gray-400 hover:text-red-500 ">
                                    <X size={10} />
                                  </button>
                                </div>
                              {/each}
                            {/if}
                            <div class="flex items-center gap-1">
                              <input type="text" placeholder="Key" bind:value={upstreamNewHeaderKey[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                              <input type="text" placeholder="Value" bind:value={upstreamNewHeaderValue[i]}
                                class="flex-1 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text " />
                              <button type="button" onclick={() => addUpstreamHeader(i)} class="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-dark-elevated hover:bg-gray-200 dark:hover:bg-dark-border text-gray-600 dark:text-dark-text-secondary ">
                                Add
                              </button>
                            </div>
                            <p class="text-xs text-gray-400 dark:text-dark-text-muted">Use <code class="font-mono">{"{{var:key}}"}</code> to reference a variable value</p>
                            </div>
                          </label>
                        </div>
                        {#if editingId && mayUse}
                          {@const inspection = (upstreamInspections[editingId] || []).find((item) => item.index === i)}
                          <div class="flex items-start gap-2 pt-2 border-t border-gray-100 dark:border-dark-border text-xs">
                            <div class="min-w-0 flex-1">
                              {#if inspection?.error}
                                <div class="text-red-600 dark:text-red-400 break-words">Connection failed: {inspection.error}</div>
                              {:else if inspection}
                                <div class="text-green-600 dark:text-green-400">
                                  Connected · {inspection.tool_count} tool{inspection.tool_count === 1 ? '' : 's'} · {inspection.response_mode === 'sse' ? 'SSE response' : 'JSON response'} · {inspection.duration_ms} ms
                                </div>
                                {#if inspection.server_name || inspection.protocol_version}
                                  <div class="mt-1 text-gray-400 dark:text-dark-text-muted">
                                    {inspection.server_name || 'MCP server'}{inspection.server_version ? ` ${inspection.server_version}` : ''}{inspection.protocol_version ? ` · protocol ${inspection.protocol_version}` : ''}
                                  </div>
                                {/if}
                                {#if inspection.tools.length > 0}
                                  <div class="mt-1 font-mono text-gray-500 dark:text-dark-text-muted break-words" title={inspection.tools.map((tool) => tool.name).join(', ')}>
                                    {inspection.tools.map((tool) => tool.name).join(', ')}
                                  </div>
                                {/if}
                              {:else}
                                <div class="text-gray-400 dark:text-dark-text-muted">Not checked. The test uses the saved URL and headers.</div>
                              {/if}
                            </div>
                            <button
                              type="button"
                              onclick={() => handleInspectUpstreams(editingId!, i)}
                              disabled={inspectionBusy[editingId]}
                              class="shrink-0 flex items-center gap-1 px-2 py-1 border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
                            >
                              <RefreshCw size={10} class={inspectionBusy[editingId] ? 'animate-spin' : ''} />
                              Test saved endpoint
                            </button>
                          </div>
                        {/if}
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
                      class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
                    />
                    <button
                      type="button"
                      onclick={addNpmPackage}
                      class="px-3 py-1.5 text-xs font-medium bg-gray-800 dark:bg-accent text-white hover:bg-gray-700 dark:hover:bg-accent-hover shrink-0"
                    >
                      Add NPM
                    </button>
                  </div>
                  <p class="text-xs text-gray-400 dark:text-dark-text-muted">Add an npm package as <code class="font-mono">npx &lt;package&gt;</code> MCP server. Append flags after the package name.</p>

                  <div class="flex gap-2">
                    <button
                      type="button"
                      onclick={() => { formMCPUpstreams = [...formMCPUpstreams, { url: '', headers: {} }]; }}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary justify-center"
                    >
                      <Globe size={12} />
                      Add HTTP Server
                    </button>
                    <button
                      type="button"
                      onclick={() => { formMCPUpstreams = [...formMCPUpstreams, { command: '', args: [], env: {} }]; }}
                      class="flex-1 flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dashed border-gray-300 dark:border-dark-border hover:border-gray-400 dark:hover:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary justify-center"
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
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary "
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
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
          items={filteredSets}
          {loading}
          total={mySelectedCategory ? filteredSets.length : total}
          bind:limit
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
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{set.name}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-64 truncate" title={set.description}>
                {set.description || '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                <div class="flex flex-wrap gap-1">
                  {#if (set.config?.http_tools ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border border-green-200 dark:border-green-800 font-mono">{(set.config.http_tools ?? []).length} HTTP</span>
                  {/if}
                  {#if (set.config?.enabled_skills ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-amber-50 dark:bg-amber-900/20 text-amber-600 dark:text-amber-400 border border-amber-200 dark:border-amber-800 font-mono">{(set.config.enabled_skills ?? []).length} skills</span>
                  {/if}
                  {#if (set.config?.mcp_upstreams ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400 border border-purple-200 dark:border-purple-800 font-mono">{(set.config.mcp_upstreams ?? []).length} external</span>
                    {@const checked = inspectionSummary(set.id)}
                    {#if checked}
                      <span
                        class={["px-1.5 py-0.5 border font-mono", checked.connected === checked.total
                          ? 'bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border-green-200 dark:border-green-800'
                          : 'bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 border-red-200 dark:border-red-800']}
                        title={`${checked.connected}/${checked.total} upstreams connected`}
                      >{checked.tools} discovered tools</span>
                    {/if}
                  {/if}
                  {#if platformAdmin && hasStdioUpstreams(set)}
                    {@const sum = stdioSummary(set.id)}
                    {#if sum}
                      <span
                        class={["px-1.5 py-0.5 border font-mono", sum.running === sum.total && sum.total > 0
                          ? 'bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border-green-200 dark:border-green-800'
                          : sum.running > 0
                            ? 'bg-amber-50 dark:bg-amber-900/20 text-amber-600 dark:text-amber-400 border-amber-200 dark:border-amber-800'
                            : 'bg-gray-50 dark:bg-dark-base text-gray-500 dark:text-dark-text-muted border-gray-200 dark:border-dark-border']}
                        title="Local MCP processes running / configured"
                      >{sum.running}/{sum.total} running</span>
                    {/if}
                  {/if}
                  {#if (set.config?.enabled_builtin_tools ?? []).length > 0}
                    <span class="px-1.5 py-0.5 bg-slate-50 dark:bg-slate-900/20 text-slate-600 dark:text-slate-400 border border-slate-200 dark:border-slate-800 font-mono">{(set.config.enabled_builtin_tools ?? []).length} builtin</span>
                  {/if}
                  {#if !(set.config?.http_tools?.length) && !(set.config?.enabled_skills?.length) && !(set.config?.mcp_upstreams?.length) && !(set.config?.enabled_builtin_tools?.length)}
                    <span class="text-gray-400 dark:text-dark-text-muted">-</span>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  {#if mayUse && (set.config?.mcp_upstreams ?? []).length > 0}
                    <button
                      onclick={() => handleInspectUpstreams(set.id)}
                      disabled={inspectionBusy[set.id]}
                      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text disabled:opacity-50"
                      title="Connect to saved upstreams and discover tools"
                      aria-label={`Test ${set.name} upstreams and discover tools`}
                    >
                      <Network size={14} class={inspectionBusy[set.id] ? 'animate-pulse' : ''} />
                    </button>
                  {/if}
                  {#if platformAdmin && hasStdioUpstreams(set)}
                    <button
                      onclick={() => handleRestartStdio(set.id)}
                      disabled={stdioBusy[set.id]}
                      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text disabled:opacity-50"
                      title="Restart local MCP processes (picks up env/config changes)"
                    >
                      <RotateCw size={14} class={stdioBusy[set.id] ? 'animate-spin' : ''} />
                    </button>
                  {/if}
                  <button
                    onclick={() => handleExportMCPSet(set)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Export as JSON"
                  >
                    <Download size={14} />
                  </button>
                  {#if mayWrite}
                    <button
                      onclick={() => openEdit(set)}
                      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                      title="Edit"
                    >
                      <Pencil size={14} />
                    </button>
                    {#if deleteConfirm === set.id}
                      <button
                        onclick={() => handleDelete(set.id)}
                        class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 "
                      >
                        Confirm
                      </button>
                      <button
                        onclick={() => (deleteConfirm = null)}
                        class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary "
                      >
                        Cancel
                      </button>
                    {:else}
                      <button
                        onclick={() => (deleteConfirm = set.id)}
                        class="p-1.5 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
                        title="Delete"
                      >
                        <Trash2 size={14} />
                      </button>
                    {/if}
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
                class="px-2.5 py-1 text-xs font-medium {selectedCategory === cat ? 'bg-gray-900 dark:bg-accent text-white' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-200 dark:hover:bg-dark-border'}"
              >
                {cat}
              </button>
            {/each}
            {#if selectedCategory}
              <button
                onclick={() => selectCategory('')}
                class="px-2 py-1 text-xs text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
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
                  {#if (tmpl.mcp_server.config.mcp_upstreams ?? []).length > 0}
                    <div><span class="font-mono px-1 py-0.5 bg-purple-50 dark:bg-purple-900/20 text-purple-600 dark:text-purple-400">External</span> {tmpl.mcp_server.config.mcp_upstreams?.map(u => u.command ? `${u.command} ${(u.args ?? []).join(' ')}` : new URL(u.url!).hostname).join(', ')}</div>
                  {/if}
                  {#if (tmpl.mcp_server.config.http_tools ?? []).length > 0}
                    <div><span class="font-mono px-1 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400">HTTP</span> {tmpl.mcp_server.config.http_tools?.length} tool{(tmpl.mcp_server.config.http_tools?.length ?? 0) !== 1 ? 's' : ''}</div>
                  {/if}
                </div>

                <!-- Install button -->
                {#if !installedSlugs.has(tmpl.slug) && mayWrite}
                  <button
                    onclick={() => handleInstallTemplate(tmpl.slug)}
                    class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
                  >
                    <Download size={12} />
                    Install
                  </button>
                {:else if !installedSlugs.has(tmpl.slug)}
                  <p class="border border-gray-200 dark:border-dark-border px-3 py-1.5 text-center text-xs text-gray-500 dark:text-dark-text-muted">Requires mcp.write</p>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {/if}

      <!-- Binaries Tab (persistent MCP program library) -->
      {#if activeTab === 'binaries' && platformAdmin}
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <HardDrive size={16} class="text-gray-500 dark:text-dark-text-muted" />
            <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Binaries &amp; Files</h2>
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">({binFiles.length})</span>
          </div>
          <div class="flex items-center gap-2">
            <button
              onclick={loadBinaries}
              class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
              title="Refresh"
            >
              <RefreshCw size={14} class={binLoading ? 'animate-spin' : ''} />
            </button>
            <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-dark-text-secondary cursor-pointer">
              <input type="checkbox" bind:checked={binExecutable} class="w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent" />
              Executable
            </label>
            <button
              onclick={() => binFileInput?.click()}
              disabled={binUploading}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
            >
              <Upload size={12} />
              {binUploading ? 'Uploading…' : 'Upload'}
            </button>
            <input bind:this={binFileInput} type="file" multiple onchange={handleBinaryUpload} class="hidden" />
          </div>
        </div>

        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface mb-4">
          <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 flex items-center gap-2">
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">Library directory</span>
            <code class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary truncate">{binDir || '…'}</code>
            {#if binDir}
              <button
                onclick={() => navigator.clipboard?.writeText(binDir).then(() => addToast('Path copied'))}
                class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
                title="Copy directory path"
              >
                <Copy size={12} />
              </button>
            {/if}
          </div>
          <div class="p-4 text-xs text-gray-500 dark:text-dark-text-muted space-y-1">
            <p>Files uploaded here survive restarts (mount <code class="font-mono">server.workspace.root</code> on a persistent volume). Reference them from a Local command upstream: <code class="font-mono">{binDir ? `${binDir}/my-mcp` : '<dir>/my-mcp'}</code>, or point an env var at a config file, e.g. <code class="font-mono">MY_TOOL_CONFIG={binDir ? `${binDir}/config.json` : '<dir>/config.json'}</code>.</p>
            <p>Archives (<code class="font-mono">.tar.gz</code> / <code class="font-mono">.tgz</code> / <code class="font-mono">.tar</code>) are extracted automatically into a folder named after the archive — exec bits are preserved from the archive. Re-uploading the same archive replaces the folder (upgrade).</p>
          </div>
        </div>

        {#if binLoading && binFiles.length === 0}
          <div class="flex items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted">
            <RefreshCw size={16} class="animate-spin mr-2" />
            Loading…
          </div>
        {:else if binFiles.length === 0}
          <div class="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-dark-text-muted border border-dashed border-gray-200 dark:border-dark-border">
            <HardDrive size={24} class="mb-2" />
            <p class="text-sm">No files uploaded</p>
            <p class="text-xs mt-1">Upload MCP binaries, config files, or release tarballs</p>
          </div>
        {:else}
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
                  <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Name</th>
                  <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Type</th>
                  <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Size</th>
                  <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Mode</th>
                  <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Modified</th>
                  <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
                </tr>
              </thead>
              <tbody>
                {#each binFiles as file (file.name)}
                  <tr class="border-b border-gray-100 dark:border-dark-border last:border-b-0 hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
                    <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{file.name}</td>
                    <td class="px-4 py-2.5 text-xs">
                      {#if file.dir}
                        <span class="px-1.5 py-0.5 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-800 font-mono">folder · {file.entries ?? 0} entries</span>
                      {:else if file.executable}
                        <span class="px-1.5 py-0.5 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400 border border-green-200 dark:border-green-800 font-mono">executable</span>
                      {:else}
                        <span class="px-1.5 py-0.5 bg-gray-50 dark:bg-dark-base text-gray-500 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border font-mono">file</span>
                      {/if}
                    </td>
                    <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{file.dir ? '-' : fmtSize(file.size)}</td>
                    <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-muted">{file.mode}</td>
                    <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{new Date(file.modified_at).toLocaleString()}</td>
                    <td class="px-4 py-2.5 text-right">
                      <div class="flex justify-end gap-1">
                        <button
                          onclick={() => copyBinaryPath(file.name)}
                          class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                          title="Copy full path"
                        >
                          <Copy size={14} />
                        </button>
                        {#if binDeleteConfirm === file.name}
                          <button
                            onclick={() => handleBinaryDelete(file.name)}
                            class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 "
                          >
                            Confirm
                          </button>
                          <button
                            onclick={() => (binDeleteConfirm = null)}
                            class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary "
                          >
                            Cancel
                          </button>
                        {:else}
                          <button
                            onclick={() => (binDeleteConfirm = file.name)}
                            class="p-1.5 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
                            title="Delete"
                          >
                            <Trash2 size={14} />
                          </button>
                        {/if}
                      </div>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}

        <!-- Running local MCP processes -->
        <div class="mt-6">
          <div class="flex items-center gap-2 mb-2">
            <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Running local MCP processes</h3>
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">({stdioProcesses.length})</span>
          </div>
          {#if stdioProcesses.length === 0}
            <p class="text-xs text-gray-400 dark:text-dark-text-muted">No local MCP processes are running. They start lazily on the first tool call.</p>
          {:else}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
                    <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Command</th>
                    <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">PID</th>
                    <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
                    <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Uptime</th>
                  </tr>
                </thead>
                <tbody>
                  {#each stdioProcesses as proc}
                    <tr class="border-b border-gray-100 dark:border-dark-border last:border-b-0">
                      <td class="px-4 py-2.5 font-mono text-xs text-gray-900 dark:text-dark-text">{proc.command}</td>
                      <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{proc.pid}</td>
                      <td class="px-4 py-2.5 text-xs">
                        {#if proc.alive}
                          <span class="flex items-center gap-1 text-green-600 dark:text-green-400"><span class="w-1.5 h-1.5 bg-green-500 inline-block"></span>running</span>
                        {:else}
                          <span class="text-red-500 dark:text-red-400" title={proc.exit_error}>exited{proc.exit_error ? ` (${proc.exit_error})` : ''}</span>
                        {/if}
                      </td>
                      <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{proc.alive ? fmtUptime(proc.uptime_seconds) : '-'}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </div>
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
