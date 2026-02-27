<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listWorkflows, createWorkflow, deleteWorkflow, type Workflow } from '@/lib/api/workflows';
  import { Plus, RefreshCw, Trash2, Pencil, Play, Workflow as WorkflowIcon } from 'lucide-svelte';

  storeNavbar.title = 'Workflows';

  let workflows = $state<Workflow[]>([]);
  let loading = $state(true);

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
      workflows = await listWorkflows();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load workflows', 'alert');
    } finally {
      loading = false;
    }
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

  function formatDate(dateStr: string): string {
    if (!dateStr) return '-';
    const d = new Date(dateStr);
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  loadWorkflows();
</script>

<div class="p-4 max-w-5xl">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <h2 class="text-base font-semibold text-gray-900">Workflows</h2>
      {#if workflows.length > 0}
        <span class="text-xs bg-gray-100 text-gray-600 px-1.5 py-0.5 rounded">{workflows.length}</span>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={() => loadWorkflows()}
        class="flex items-center gap-1 px-2 py-1 text-xs text-gray-600 bg-white border border-gray-300 rounded hover:bg-gray-50 transition-colors"
      >
        <RefreshCw size={12} />
        Refresh
      </button>
      <button
        onclick={() => (showCreateForm = !showCreateForm)}
        class="flex items-center gap-1 px-2 py-1 text-xs text-white bg-gray-900 rounded hover:bg-gray-800 transition-colors"
      >
        <Plus size={12} />
        New Workflow
      </button>
    </div>
  </div>

  <!-- Create Form -->
  {#if showCreateForm}
    <div class="mb-4 p-3 bg-white border border-gray-200 rounded">
      <div class="text-sm font-medium text-gray-700 mb-2">Create Workflow</div>
      <div class="flex flex-col gap-2">
        <input
          type="text"
          bind:value={newName}
          placeholder="Workflow name"
          class="px-2 py-1 text-sm border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
        />
        <input
          type="text"
          bind:value={newDescription}
          placeholder="Description (optional)"
          class="px-2 py-1 text-sm border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
        />
        <div class="flex items-center gap-2">
          <button
            onclick={handleCreate}
            disabled={creating}
            class="px-3 py-1 text-xs text-white bg-gray-900 rounded hover:bg-gray-800 disabled:opacity-50 transition-colors"
          >
            {creating ? 'Creating...' : 'Create'}
          </button>
          <button
            onclick={() => (showCreateForm = false)}
            class="px-3 py-1 text-xs text-gray-600 bg-white border border-gray-300 rounded hover:bg-gray-50 transition-colors"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Table -->
  <div class="bg-white border border-gray-200 rounded overflow-hidden">
    {#if loading}
      <div class="p-8 text-center text-sm text-gray-500">Loading...</div>
    {:else if workflows.length === 0}
      <div class="p-8 text-center">
        <WorkflowIcon size={32} class="mx-auto text-gray-300 mb-2" />
        <div class="text-sm text-gray-500">No workflows yet</div>
        <div class="text-xs text-gray-400 mt-1">Create a workflow to start building visual agent pipelines</div>
      </div>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 bg-gray-50">
            <th class="text-left px-3 py-2 font-medium text-gray-600 text-xs">Name</th>
            <th class="text-left px-3 py-2 font-medium text-gray-600 text-xs">Description</th>
            <th class="text-left px-3 py-2 font-medium text-gray-600 text-xs">Version</th>
            <th class="text-left px-3 py-2 font-medium text-gray-600 text-xs">Nodes</th>
            <th class="text-left px-3 py-2 font-medium text-gray-600 text-xs">Updated</th>
            <th class="text-right px-3 py-2 font-medium text-gray-600 text-xs">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each workflows as wf (wf.id)}
            <tr class="border-b border-gray-100 hover:bg-gray-50 transition-colors">
              <td class="px-3 py-2">
                <button
                  onclick={() => push(`/workflows/${wf.id}`)}
                  class="text-left text-sm font-medium text-blue-600 hover:text-blue-800 hover:underline"
                >
                  {wf.name}
                </button>
              </td>
              <td class="px-3 py-2 text-gray-500 text-xs">{wf.description || '-'}</td>
              <td class="px-3 py-2 text-gray-500 text-xs">
                {#if wf.active_version != null}
                  <span class="px-1.5 py-0.5 text-[10px] font-medium text-green-700 bg-green-50 border border-green-200 rounded">v{wf.active_version}</span>
                {:else}
                  <span class="text-gray-400">-</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-gray-500 text-xs">{wf.graph?.nodes?.length ?? 0}</td>
              <td class="px-3 py-2 text-gray-500 text-xs">
                <div>{formatDate(wf.updated_at)}</div>
                {#if wf.updated_by}
                  <div class="text-[10px] text-gray-400">by {wf.updated_by}</div>
                {/if}
              </td>
              <td class="px-3 py-2">
                <div class="flex items-center justify-end gap-1">
                  <button
                    onclick={() => push(`/workflows/${wf.id}`)}
                    class="p-1 text-gray-400 hover:text-gray-700 transition-colors"
                    title="Edit"
                  >
                    <Pencil size={13} />
                  </button>
                  {#if deletingId === wf.id}
                    <span class="text-xs text-red-600 mr-1">Delete?</span>
                    <button
                      onclick={() => handleDelete(wf.id)}
                      class="px-1.5 py-0.5 text-xs text-white bg-red-600 rounded hover:bg-red-700 transition-colors"
                    >
                      Yes
                    </button>
                    <button
                      onclick={() => (deletingId = null)}
                      class="px-1.5 py-0.5 text-xs text-gray-600 bg-white border border-gray-300 rounded hover:bg-gray-50 transition-colors"
                    >
                      No
                    </button>
                  {:else}
                    <button
                      onclick={() => (deletingId = wf.id)}
                      class="p-1 text-gray-400 hover:text-red-600 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={13} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
