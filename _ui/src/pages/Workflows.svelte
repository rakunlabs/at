<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listWorkflows, createWorkflow, deleteWorkflow, getWorkflow, runWorkflowStream, type Workflow, type WorkflowNode, type WorkflowStreamEvent } from '@/lib/api/workflows';
  import { Plus, RefreshCw, Trash2, Pencil, Play, Workflow as WorkflowIcon, Copy, X, Loader2, CheckCircle2, AlertTriangle } from 'lucide-svelte';
  import { formatDateTime } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Workflows';

  let workflows = $state<Workflow[]>([]);
  let loading = $state(true);

  // Pagination state
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([{ field: 'updated_at', desc: true }]);

  // Create form state
  let showCreateForm = $state(false);
  let newName = $state('');
  let newDescription = $state('');
  let creating = $state(false);

  // Delete confirmation state
  let deletingId = $state<string | null>(null);

  async function loadWorkflows() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['name[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listWorkflows(params);
      workflows = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load workflows', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    loadWorkflows();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    loadWorkflows();
  }

  function copyID(id: string, e: Event) {
    e.stopPropagation();
    navigator.clipboard.writeText(id);
    addToast('Workflow ID copied', 'info');
  }

  async function handleCreate() {
    if (!newName.trim()) {
      addToast('Name is required', 'warn');
      return;
    }
    creating = true;
    try {
      const wf = await createWorkflow({
        name: newName.trim(),
        description: newDescription.trim(),
        graph: { nodes: [], edges: [] },
      });
      addToast(`Workflow "${wf.name}" created`, 'info');
      newName = '';
      newDescription = '';
      showCreateForm = false;
      push(`/workflows/${wf.id}`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create workflow', 'alert');
    } finally {
      creating = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteWorkflow(id);
      workflows = workflows.filter((w) => w.id !== id);
      addToast('Workflow deleted', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete workflow', 'alert');
    } finally {
      deletingId = null;
    }
  }

  // ─── Run Panel State ───

  // ─── Input Field Schema Types ───

  interface InputField {
    name: string;
    type: 'string' | 'number' | 'boolean' | 'select' | 'textarea';
    description?: string;
    default?: any;
    options?: string[]; // for select type
  }

  let runPanelWorkflow = $state<Workflow | null>(null);
  let runInputNodes = $state<WorkflowNode[]>([]);
  let runSelectedEntry = $state('');
  let runInputsJson = $state('{}');
  let runFormValues = $state<Record<string, any>>({});
  let runUseForm = $state(false); // true if selected node has fields
  let runRunning = $state(false);
  let runEvents = $state<WorkflowStreamEvent[]>([]);
  let runStatus = $state<'idle' | 'running' | 'completed' | 'error'>('idle');
  let runAbort = $state<AbortController | null>(null);
  let runOutputEl: HTMLElement | undefined = $state();

  async function openRunPanel(wf: Workflow) {
    try {
      const full = await getWorkflow(wf.id);
      runPanelWorkflow = full;
      runInputNodes = full.graph.nodes.filter(n => n.type === 'input');
      runSelectedEntry = runInputNodes.length > 0 ? runInputNodes[0].id : '';
      runInputsJson = '{\n  \n}';
      runEvents = [];
      runStatus = 'idle';
      syncFormFromEntry();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load workflow', 'alert');
    }
  }

  function closeRunPanel() {
    if (runAbort) {
      runAbort.abort();
      runAbort = null;
    }
    runPanelWorkflow = null;
    runRunning = false;
    runStatus = 'idle';
  }

  function getInputNodeLabel(node: WorkflowNode): string {
    return (node.data?.label as string) || node.id;
  }

  function getInputNodeFields(node: WorkflowNode): InputField[] {
    const fields = node.data?.fields;
    if (Array.isArray(fields)) return fields as InputField[];
    return [];
  }

  function syncFormFromEntry() {
    const node = runInputNodes.find(n => n.id === runSelectedEntry);
    if (!node) { runUseForm = false; return; }
    const fields = getInputNodeFields(node);
    if (fields.length > 0) {
      runUseForm = true;
      const values: Record<string, any> = {};
      for (const f of fields) {
        values[f.name] = f.default ?? (f.type === 'number' ? 0 : f.type === 'boolean' ? false : '');
      }
      runFormValues = values;
    } else {
      runUseForm = false;
    }
  }

  function buildInputsFromForm(): Record<string, any> {
    return { ...runFormValues };
  }

  function handleRun() {
    if (!runPanelWorkflow || runRunning) return;

    let inputs: Record<string, any>;
    if (runUseForm) {
      inputs = buildInputsFromForm();
    } else {
      try {
        inputs = JSON.parse(runInputsJson);
      } catch {
        addToast('Invalid JSON in inputs', 'warn');
        return;
      }
    }

    runRunning = true;
    runStatus = 'running';
    runEvents = [];

    const entryNodeIds = runSelectedEntry ? [runSelectedEntry] : undefined;

    runAbort = runWorkflowStream(
      runPanelWorkflow.id,
      inputs,
      (event) => {
        runEvents = [...runEvents, event];
        if (event.event_type === 'error') {
          runStatus = 'error';
        }
        // Auto-scroll output
        setTimeout(() => runOutputEl?.scrollTo(0, runOutputEl.scrollHeight), 50);
      },
      () => {
        runRunning = false;
        runAbort = null;
        if (runStatus !== 'error') {
          runStatus = 'completed';
        }
      },
      undefined,
      entryNodeIds,
    );
  }

  function stopRun() {
    if (runAbort) {
      runAbort.abort();
      runAbort = null;
      runRunning = false;
      runStatus = 'idle';
    }
  }

  function eventIcon(type: string) {
    switch (type) {
      case 'node_started': return '▶';
      case 'node_completed': return '✓';
      case 'node_error': return '✗';
      case 'node_skipped': return '⊘';
      case 'run_completed': return '★';
      case 'error': return '✗';
      default: return '•';
    }
  }

  function eventColor(type: string): string {
    switch (type) {
      case 'node_started': return 'text-blue-500 dark:text-blue-400';
      case 'node_completed': return 'text-green-500 dark:text-green-400';
      case 'node_error': case 'error': return 'text-red-500 dark:text-red-400';
      case 'run_completed': return 'text-green-600 dark:text-green-300';
      default: return 'text-gray-400 dark:text-dark-text-muted';
    }
  }

  loadWorkflows();
</script>

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <WorkflowIcon size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Workflows</h2>
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={() => loadWorkflows()}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={() => (showCreateForm = !showCreateForm)}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
          >
            <Plus size={12} />
            New Workflow
          </button>
        </div>
      </div>

      <!-- Create Form -->
      {#if showCreateForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">New Workflow</span>
            <button onclick={() => (showCreateForm = false)} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors">
              <Plus size={14} class="rotate-45" />
            </button>
          </div>
          <div class="p-4 space-y-4">
            <div class="grid grid-cols-4 gap-3 items-center">
              <label class="contents">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</span>
                <input
                  type="text"
                  bind:value={newName}
                  placeholder="Workflow name"
                  class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
              </label>
            </div>
            <div class="grid grid-cols-4 gap-3 items-center">
              <label class="contents">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</span>
                <input
                  type="text"
                  bind:value={newDescription}
                  placeholder="Description (optional)"
                  class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
              </label>
            </div>
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
              <button
                onclick={() => (showCreateForm = false)}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors"
              >
                Cancel
              </button>
              <button
                onclick={handleCreate}
                disabled={creating}
                class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50 transition-colors"
              >
                {creating ? 'Creating...' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      {/if}

      <!-- Table -->
      <DataTable
        items={workflows}
        {loading}
        {total}
        {limit}
        bind:offset
        onchange={loadWorkflows}
        onsearch={handleSearch}
        searchPlaceholder="Search by name..."
        emptyIcon={WorkflowIcon}
        emptyTitle="No workflows yet"
        emptyDescription="Create a workflow to start building visual agent pipelines"
      >
        {#snippet header()}
          <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
          <SortableHeader field="active_version" label="Version" {sorts} onsort={handleSort} />
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Nodes</th>
          <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Actions</th>
        {/snippet}

        {#snippet row(wf)}
          <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
            <td class="px-4 py-2.5">
              <div>
                <button
                  onclick={() => push(`/workflows/${wf.id}`)}
                  class="text-left font-medium text-blue-600 dark:text-accent-text hover:text-blue-800 dark:hover:text-accent hover:underline block"
                >
                  {wf.name}
                </button>
                <div class="flex items-center gap-1 text-[10px] text-gray-400 dark:text-dark-text-muted mt-0.5 group">
                  <span class="font-mono">{wf.id}</span>
                  <button
                    onclick={(e) => copyID(wf.id, e)}
                    class="opacity-0 group-hover:opacity-100 transition-opacity p-0.5 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted"
                    title="Copy ID"
                  >
                    <Copy size={10} />
                  </button>
                </div>
              </div>
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-64 truncate" title={wf.description}>{wf.description || '-'}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              {#if wf.active_version != null}
                <span class="px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800">v{wf.active_version}</span>
              {:else}
                <span class="text-gray-400 dark:text-dark-text-muted">-</span>
              {/if}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{wf.graph?.nodes?.length ?? 0}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              <div>{formatDateTime(wf.updated_at)}</div>
              {#if wf.updated_by}
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">by {wf.updated_by}</div>
              {/if}
            </td>
            <td class="px-4 py-2.5 text-right">
              <div class="flex items-center justify-end gap-1">
                <button
                  onclick={() => push(`/workflows/${wf.id}`)}
                  class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                  title="Edit"
                >
                  <Pencil size={14} />
                </button>
                {#if deletingId === wf.id}
                  <span class="text-xs text-red-600 dark:text-red-400 mr-1">Delete?</span>
                  <button
                    onclick={() => handleDelete(wf.id)}
                    class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                  >
                    Yes
                  </button>
                  <button
                    onclick={() => (deletingId = null)}
                    class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary transition-colors"
                  >
                    No
                  </button>
                {:else}
                  <button
                    onclick={() => (deletingId = wf.id)}
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
    </div>
  </div>

  <!-- Run Panel (slide-in from right) -->
  {#if runPanelWorkflow}
    <div class="w-[480px] border-l border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col shrink-0">
      <!-- Header -->
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50 shrink-0">
        <div class="min-w-0">
          <div class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">Run: {runPanelWorkflow.name}</div>
          <div class="text-[10px] text-gray-400 dark:text-dark-text-muted font-mono">{runPanelWorkflow.id}</div>
        </div>
        <button onclick={closeRunPanel} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors shrink-0">
          <X size={14} />
        </button>
      </div>

      <!-- Form -->
      <div class="p-4 space-y-3 border-b border-gray-200 dark:border-dark-border shrink-0">
        <!-- Entry node selector -->
        {#if runInputNodes.length > 0}
          <label class="block">
            <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-1">Entry Point</span>
            <select
              bind:value={runSelectedEntry}
              onchange={() => syncFormFromEntry()}
              disabled={runRunning}
              class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
            >
              {#each runInputNodes as node}
                <option value={node.id}>{getInputNodeLabel(node)}</option>
              {/each}
            </select>
          </label>
        {:else}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted">No input nodes found in this workflow.</div>
        {/if}

        <!-- Inputs: Form or JSON -->
        {#if runUseForm}
          {@const node = runInputNodes.find(n => n.id === runSelectedEntry)}
          {@const fields = node ? getInputNodeFields(node) : []}
          <div class="space-y-2.5">
            <div class="flex items-center justify-between">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Inputs</span>
              <button
                onclick={() => { runUseForm = false; runInputsJson = JSON.stringify(runFormValues, null, 2); }}
                class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
              >Switch to JSON</button>
            </div>
            {#each fields as field}
              <label class="block">
                <span class="text-[10px] font-medium text-gray-600 dark:text-dark-text-secondary block mb-0.5">
                  {field.name}
                  {#if field.description}
                    <span class="font-normal text-gray-400 dark:text-dark-text-muted ml-1">— {field.description}</span>
                  {/if}
                </span>
                {#if field.type === 'select' && field.options}
                  <select
                    value={runFormValues[field.name] ?? field.default ?? ''}
                    onchange={(e) => { runFormValues[field.name] = (e.target as HTMLSelectElement).value; }}
                    disabled={runRunning}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                  >
                    {#each field.options as opt}
                      <option value={opt}>{opt}</option>
                    {/each}
                  </select>
                {:else if field.type === 'number'}
                  <input
                    type="number"
                    value={runFormValues[field.name] ?? field.default ?? 0}
                    oninput={(e) => { runFormValues[field.name] = Number((e.target as HTMLInputElement).value); }}
                    disabled={runRunning}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                  />
                {:else if field.type === 'boolean'}
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={runFormValues[field.name] ?? field.default ?? false}
                      onchange={(e) => { runFormValues[field.name] = (e.target as HTMLInputElement).checked; }}
                      disabled={runRunning}
                      class="w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                    />
                    <span class="text-xs text-gray-600 dark:text-dark-text-secondary">{runFormValues[field.name] ? 'Yes' : 'No'}</span>
                  </label>
                {:else if field.type === 'textarea'}
                  <textarea
                    value={runFormValues[field.name] ?? field.default ?? ''}
                    oninput={(e) => { runFormValues[field.name] = (e.target as HTMLTextAreaElement).value; }}
                    disabled={runRunning}
                    rows={3}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs font-mono resize-y focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                  ></textarea>
                {:else}
                  <input
                    type="text"
                    value={runFormValues[field.name] ?? field.default ?? ''}
                    oninput={(e) => { runFormValues[field.name] = (e.target as HTMLInputElement).value; }}
                    disabled={runRunning}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2.5 py-1 text-xs focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                  />
                {/if}
              </label>
            {/each}
          </div>
        {:else}
          <label class="block">
            <div class="flex items-center justify-between mb-1">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Inputs (JSON)</span>
              {#if runInputNodes.find(n => n.id === runSelectedEntry)?.data?.fields}
                <button
                  onclick={() => { syncFormFromEntry(); }}
                  class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
                >Switch to Form</button>
              {/if}
            </div>
            <textarea
              bind:value={runInputsJson}
              disabled={runRunning}
              rows={6}
              class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface px-3 py-2 text-xs font-mono resize-y focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
              placeholder={'{"key": "value"}'}
            ></textarea>
          </label>
        {/if}

        <!-- Run / Stop buttons -->
        <div class="flex items-center gap-2">
          {#if runRunning}
            <button
              onclick={stopRun}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-red-600 text-white hover:bg-red-700 transition-colors"
            >
              <X size={12} />
              Stop
            </button>
            <span class="flex items-center gap-1 text-xs text-gray-400 dark:text-dark-text-muted">
              <Loader2 size={12} class="animate-spin" />
              Running...
            </span>
          {:else}
            <button
              onclick={handleRun}
              disabled={!runPanelWorkflow || runInputNodes.length === 0}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50 transition-colors"
            >
              <Play size={12} />
              Run
            </button>
            {#if runStatus === 'completed'}
              <span class="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                <CheckCircle2 size={12} />
                Completed
              </span>
            {:else if runStatus === 'error'}
              <span class="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                <AlertTriangle size={12} />
                Error
              </span>
            {/if}
            {#if runStatus === 'completed' || runStatus === 'error'}
              <button
                onclick={() => { runEvents = []; runStatus = 'idle'; }}
                class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors ml-auto"
              >Clear</button>
            {/if}
          {/if}
        </div>
      </div>

      <!-- Output -->
      <div
        bind:this={runOutputEl}
        class="flex-1 overflow-y-auto p-4 bg-gray-50 dark:bg-dark-base"
      >
        {#if runStatus === 'idle' && runEvents.length === 0}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted text-center py-8">
            Select an entry point, provide inputs, and click Run.
          </div>
        {:else if runStatus === 'running'}
          <div class="flex items-center justify-center gap-2 py-8">
            <Loader2 size={16} class="animate-spin text-gray-400 dark:text-dark-text-muted" />
            <span class="text-sm text-gray-500 dark:text-dark-text-muted">Running...</span>
          </div>
        {:else if runStatus === 'completed'}
          {@const outputEvent = runEvents.findLast(e => e.outputs)}
          {@const errorEvents = runEvents.filter(e => e.error)}
          <div class="space-y-3">
            <div class="flex items-center gap-2">
              <CheckCircle2 size={16} class="text-green-500" />
              <span class="text-sm font-medium text-green-700 dark:text-green-400">Done</span>
            </div>
            {#if outputEvent?.outputs}
              <div class="p-3 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded">
                <div class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider mb-2">Output</div>
                <pre class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap break-all max-h-80 overflow-y-auto">{JSON.stringify(outputEvent.outputs, null, 2)}</pre>
              </div>
            {/if}
            {#if errorEvents.length > 0}
              <div class="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded">
                <div class="text-[10px] font-medium text-red-600 dark:text-red-400 uppercase tracking-wider mb-1">Errors</div>
                {#each errorEvents as err}
                  <div class="text-xs text-red-600 dark:text-red-400 break-all">{err.error}</div>
                {/each}
              </div>
            {/if}
          </div>
        {:else if runStatus === 'error'}
          {@const errorEvents = runEvents.filter(e => e.error)}
          <div class="space-y-3">
            <div class="flex items-center gap-2">
              <AlertTriangle size={16} class="text-red-500" />
              <span class="text-sm font-medium text-red-700 dark:text-red-400">Failed</span>
            </div>
            {#each errorEvents as err}
              <div class="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded text-xs text-red-600 dark:text-red-400 break-all">
                {err.error}
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>