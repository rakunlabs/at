<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listVariables,
    createVariable,
    updateVariable,
    deleteVariable,
    type Variable,
    type VariableInput,
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
  let limit = $state(25);
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
  let formAllowHttp = $state(false);
  let formAllowedHosts = $state('');
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
    formAllowHttp = false;
    formAllowedHosts = '';
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
    formAllowHttp = (variable.allowed_tools || []).includes('http_request');
    formAllowedHosts = (variable.allowed_hosts || []).join('\n');
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

    const allowedHosts = formAllowedHosts
      .split(/[\n,]/)
      .map((host) => host.trim())
      .filter(Boolean);
    if (formSecret && formAllowHttp && allowedHosts.length === 0) {
      addToast('Add at least one HTTPS destination host for secret references', 'warn');
      return;
    }

    saving = true;
    try {
      if (editingId) {
        const payload: VariableInput = {
          key: formKey.trim(),
          description: formDescription.trim(),
          secret: formSecret,
          allowed_tools: formSecret && formAllowHttp ? ['http_request'] : [],
          allowed_hosts: formSecret && formAllowHttp ? allowedHosts : [],
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
          allowed_tools: formSecret && formAllowHttp ? ['http_request'] : [],
          allowed_hosts: formSecret && formAllowHttp ? allowedHosts : [],
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

<div class="p-6 max-w-6xl mx-auto">
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
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary "
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover "
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
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary ">
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
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
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
              class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
            />
            <button
              type="button"
              onclick={() => { formShowValue = !formShowValue; }}
              class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary "
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
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
          />
        </div>

        <!-- Secret toggle -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-secret" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Secret</label>
          <div class="col-span-3 flex items-center gap-2">
            <input
              id="form-secret"
              type="checkbox"
              checked={formSecret}
              onchange={(event) => {
                formSecret = (event.currentTarget as HTMLInputElement).checked;
                if (!formSecret) {
                  formAllowHttp = false;
                  formAllowedHosts = '';
                }
              }}
              class="w-4 h-4 text-gray-900 border-gray-300 focus:ring-gray-900/10 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
            />
            <span class="text-xs text-gray-500 dark:text-dark-text-muted">
              {formSecret ? 'Encrypted at rest, value hidden in list view' : 'Stored as plaintext, value shown in list view'}
            </span>
          </div>
        </div>

        {#if formSecret}
          <div class="grid grid-cols-4 gap-3 items-start">
            <div class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Tool references</div>
            <div class="col-span-3 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-3 py-3 space-y-3">
              <label class="flex items-start gap-2">
                <input
                  type="checkbox"
                  bind:checked={formAllowHttp}
                  class="mt-0.5 w-4 h-4 text-gray-900 border-gray-300 focus:ring-gray-900/10 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                />
                <span>
                  <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Allow use by HTTP Request</span>
                  <span class="block text-xs text-gray-500 dark:text-dark-text-muted">The model can reference this variable by name, but the value is resolved only inside the request executor.</span>
                </span>
              </label>

              {#if formAllowHttp}
                <div>
                  <label for="form-allowed-hosts" class="block text-xs font-medium text-gray-600 dark:text-dark-text-secondary mb-1">Allowed HTTPS hosts</label>
                  <textarea
                    id="form-allowed-hosts"
                    bind:value={formAllowedHosts}
                    rows="3"
                    placeholder={'api.example.com\n*.service.example.com'}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-2 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  ></textarea>
                  <p class="mt-1 text-xs text-gray-500 dark:text-dark-text-muted">One host per line. URLs, paths, and broad <code class="font-mono">*</code> wildcards are refused.</p>
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- Usage hint -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <div></div>
          <div class="col-span-3 text-xs text-gray-400 dark:text-dark-text-muted bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border px-3 py-2 space-y-1">
            <div><span class="font-medium text-gray-500 dark:text-dark-text-muted">Reference:</span> <code class="font-mono">{'{"$ref":"variable://' + (formKey || 'key') + '","prefix":"Bearer "}'}</code></div>
            <div>Secret references are accepted only by explicitly allowed tools and destination hosts. Arbitrary Bash does not receive them.</div>
          </div>
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
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover disabled:opacity-50"
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
      bind:limit
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
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Tool use</th>
        <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
      {/snippet}

      {#snippet row(variable)}
        <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
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
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-48">
            {#if (variable.allowed_tools || []).includes('http_request')}
              <span class="font-mono text-gray-700 dark:text-dark-text-secondary">http_request</span>
              <span class="block truncate" title={(variable.allowed_hosts || []).join(', ')}>{(variable.allowed_hosts || []).join(', ')}</span>
            {:else}
              <span class="text-gray-400 dark:text-dark-text-faint">Not allowed</span>
            {/if}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(variable.updated_at)}</td>
          <td class="px-4 py-2.5 text-right">
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openEdit(variable)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text "
                title="Edit"
              >
                <Pencil size={14} />
              </button>
              {#if deleteConfirm === variable.id}
                <button
                  onclick={() => handleDelete(variable.id)}
                  class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 "
                >
                  Confirm
                </button>
                <button
                  onclick={() => (deleteConfirm = null)}
                  class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated "
                >
                  Cancel
                </button>
              {:else}
                <button
                  onclick={() => (deleteConfirm = variable.id)}
                  class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-red-500 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300 "
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
