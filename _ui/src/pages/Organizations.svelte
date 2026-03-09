<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listOrganizations,
    createOrganization,
    updateOrganization,
    deleteOrganization,
    type Organization,
  } from '@/lib/api/organizations';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Building2, Plus, Pencil, Trash2, X, Save, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Organizations';

  // ─── State ───

  let organizations = $state<Organization[]>([]);
  let loading = $state(true);

  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let saving = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['name[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listOrganizations(params);
      organizations = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load organizations', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    load();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    load();
  }

  load();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(organization: Organization) {
    resetForm();
    editingId = organization.id;
    formName = organization.name;
    formDescription = organization.description || '';
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Organization name is required', 'warn');
      return;
    }

    saving = true;
    try {
      if (editingId) {
        await updateOrganization(editingId, {
          name: formName.trim(),
          description: formDescription.trim(),
        });
        addToast(`Organization "${formName}" updated`);
      } else {
        await createOrganization({
          name: formName.trim(),
          description: formDescription.trim(),
        });
        addToast(`Organization "${formName}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save organization', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteOrganization(id);
      addToast('Organization deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete organization', 'alert');
    }
  }
</script>

<svelte:head>
  <title>AT | Organizations</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Building2 size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Organizations</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={load}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New Organization
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formName}` : 'New Organization'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
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
            placeholder="e.g., Acme Corp, Engineering Team"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Description</label>
          <textarea
            id="form-description"
            bind:value={formDescription}
            placeholder="What this organization is for (optional)"
            rows="3"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors resize-none"
          ></textarea>
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

  <!-- Organization list -->
  {#if loading || organizations.length > 0 || !showForm}
    <DataTable
      items={organizations}
      {loading}
      {total}
      {limit}
      bind:offset
      onchange={load}
      onsearch={handleSearch}
      searchPlaceholder="Search by name..."
      emptyIcon={Building2}
      emptyTitle="No organizations"
      emptyDescription="Organizations provide multi-tenant isolation for agents, goals, and tasks"
    >
      {#snippet header()}
        <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
        <SortableHeader field="created_at" label="Created" {sorts} onsort={handleSort} />
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
      {/snippet}

      {#snippet row(organization)}
        <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer" onclick={() => push(`/organizations/${organization.id}`)}>
          <td class="px-4 py-2.5 font-medium text-gray-900 dark:text-dark-text hover:underline">{organization.name}</td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-48 truncate" title={organization.description}>
            {organization.description || '-'}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(organization.created_at)}</td>
          <td class="px-4 py-2.5 text-right" onclick={(e) => e.stopPropagation()}>
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openEdit(organization)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                title="Edit"
              >
                <Pencil size={14} />
              </button>
              {#if deleteConfirm === organization.id}
                <button
                  onclick={() => handleDelete(organization.id)}
                  class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                >
                  Confirm
                </button>
                <button
                  onclick={() => (deleteConfirm = null)}
                  class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                >
                  Cancel
                </button>
              {:else}
                <button
                  onclick={() => (deleteConfirm = organization.id)}
                  class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors"
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
</div>
