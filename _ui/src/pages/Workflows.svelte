<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listWorkflows, createWorkflow, deleteWorkflow, type Workflow } from '@/lib/api/workflows';
  import { Plus, RefreshCw, Trash2, Pencil, Play, Workflow as WorkflowIcon, Copy } from 'lucide-svelte';
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
              <label class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                type="text"
                bind:value={newName}
                placeholder="Workflow name"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>
            <div class="grid grid-cols-4 gap-3 items-center">
              <label class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                type="text"
                bind:value={newDescription}
                placeholder="Description (optional)"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
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
</div>