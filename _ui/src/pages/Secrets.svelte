<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listVariables,
    createVariable,
    updateVariable,
    deleteVariable,
    type Variable,
  } from '@/lib/api/secrets';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Braces, Plus, Pencil, Trash2, X, Save, RefreshCw, Eye, EyeOff } from 'lucide-svelte';

  storeNavbar.title = 'Variables';

  // ─── State ───

  let variables = $state<Variable[]>([]);
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
  let formKey = $state('');
  let formValue = $state('');
  let formDescription = $state('');
  let formSecret = $state(true);
  let formShowValue = $state(false);
  let formHasStoredValue = $state(false);
  let saving = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['key[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listVariables(params);
      variables = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load variables', 'alert');
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
    formKey = '';
    formValue = '';
    formDescription = '';
    formSecret = true;
    formShowValue = false;
    formHasStoredValue = false;
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(variable: Variable) {
    resetForm();
    editingId = variable.id;
    formKey = variable.key;
    formDescription = variable.description;
    formSecret = variable.secret;
    formValue = '';
    formShowValue = false;
    formHasStoredValue = true;
    showForm = true;
  }

  async function handleSubmit() {
    if (!formKey.trim()) {
      addToast('Variable key is required', 'warn');
      return;
    }

    if (!editingId && !formValue) {
      addToast('Variable value is required', 'warn');
      return;
    }

    saving = true;
    try {
      if (editingId) {
        const payload: { key: string; value?: string; description?: string; secret?: boolean } = {
          key: formKey.trim(),
          description: formDescription.trim(),
          secret: formSecret,
        };
        if (formValue) {
          payload.value = formValue;
        }
        await updateVariable(editingId, payload);
        addToast(`Variable "${formKey}" updated`);
      } else {
        await createVariable({
          key: formKey.trim(),
          value: formValue,
          description: formDescription.trim(),
          secret: formSecret,
        });
        addToast(`Variable "${formKey}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save variable', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteVariable(id);
      addToast('Variable deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete variable', 'alert');
    }
  }
</script>

<svelte:head>
  <title>AT | Variables</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Braces size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Variables</h2>
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
        New Variable
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formKey}` : 'New Variable'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Key -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-key" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Key</label>
          <input
            id="form-key"
            type="text"
            bind:value={formKey}
            placeholder="e.g., github_token, base_url"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Value -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-value" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Value</label>
          <div class="col-span-3 flex gap-2">
            <input
              id="form-value"
              type={formShowValue ? 'text' : 'password'}
              bind:value={formValue}
              placeholder={editingId && formHasStoredValue ? '(stored - leave blank to keep)' : 'Variable value'}
              class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
            />
            <button
              type="button"
              onclick={() => { formShowValue = !formShowValue; }}
              class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
              title={formShowValue ? 'Hide value' : 'Show value'}
            >
              {#if formShowValue}
                <EyeOff size={14} />
              {:else}
                <Eye size={14} />
              {/if}
            </button>
          </div>
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
          <input
            id="form-description"
            type="text"
            bind:value={formDescription}
            placeholder="What this variable is for (optional)"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
          />
        </div>

        <!-- Secret toggle -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-secret" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Secret</label>
          <div class="col-span-3 flex items-center gap-2">
            <input
              id="form-secret"
              type="checkbox"
              bind:checked={formSecret}
              class="w-4 h-4 text-gray-900 border-gray-300 focus:ring-gray-900/10 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
            />
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">
              {formSecret ? 'Encrypted at rest, value hidden in list view' : 'Stored as plaintext, value shown in list view'}
            </span>
          </div>
        </div>

        <!-- Usage hint -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <div></div>
          <div class="col-span-3 text-xs text-gray-400 dark:text-dark-text-muted bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border px-3 py-2 space-y-1">
            <div><span class="font-medium text-gray-500 dark:text-dark-text-muted">JS handler:</span> <code class="font-mono">getVar("{formKey || 'key'}")</code></div>
            <div><span class="font-medium text-gray-500 dark:text-dark-text-muted">Bash handler:</span> <code class="font-mono">$VAR_{(formKey || 'KEY').toUpperCase().replace(/[.\-]/g, '_')}</code></div>
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

  <!-- Variable list -->
  {#if loading || variables.length > 0 || !showForm}
    <DataTable
      items={variables}
      {loading}
      {total}
      {limit}
      bind:offset
      onchange={load}
      onsearch={handleSearch}
      searchPlaceholder="Search by key..."
      emptyIcon={Braces}
      emptyTitle="No variables configured"
      emptyDescription="Variables store configuration values and credentials for use in skill handlers"
    >
      {#snippet header()}
        <SortableHeader field="key" label="Key" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Value</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
        <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
      {/snippet}

      {#snippet row(variable)}
        <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
          <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{variable.key}</td>
          <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-muted max-w-48 truncate">
            {#if variable.secret}
              <span class="text-gray-400 dark:text-dark-text-muted">***</span>
            {:else}
              <span class="text-gray-700 dark:text-dark-text-secondary">{variable.value}</span>
            {/if}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-48 truncate" title={variable.description}>
            {variable.description || '-'}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(variable.updated_at)}</td>
          <td class="px-4 py-2.5 text-right">
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openEdit(variable)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                title="Edit"
              >
                <Pencil size={14} />
              </button>
              {#if deleteConfirm === variable.id}
                <button
                  onclick={() => handleDelete(variable.id)}
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
                  onclick={() => (deleteConfirm = variable.id)}
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
