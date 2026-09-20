<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listRoutingProfiles,
    createRoutingProfile,
    updateRoutingProfile,
    deleteRoutingProfile,
    ROUTING_PROFILE_MAX_TARGETS,
    type RoutingProfile,
  } from '@/lib/api/routing-profiles';
  import { listProviders } from '@/lib/api/providers';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Route, Plus, Pencil, Trash2, X, Save, RefreshCw, ArrowUp, ArrowDown } from 'lucide-svelte';

  storeNavbar.title = 'Routing Profiles';

  // ─── State ───

  let profiles = $state<RoutingProfile[]>([]);
  let loading = $state(true);

  let offset = $state(0);
  let limit = $state(25);
  let total = $state(0);

  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  let formName = $state('');
  let formDescription = $state('');
  let formTargets = $state<string[]>(['']);
  let saving = $state(false);

  // Every provider/model pair the caller can configure, used as a datalist so a
  // target can be picked rather than typed. It is a suggestion list, not a
  // constraint: a profile may legitimately name a provider added later.
  let knownModels = $state<string[]>([]);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['name[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listRoutingProfiles(params);
      profiles = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load routing profiles', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadModels() {
    try {
      const res = await listProviders({ _limit: 200 } as any);
      const out: string[] = [];
      for (const p of res.data || []) {
        const models = p.config?.models?.length ? p.config.models : p.config?.model ? [p.config.model] : [];
        for (const m of models) {
          if (m) out.push(`${p.key}/${m}`);
        }
      }
      knownModels = [...new Set(out)].sort();
    } catch {
      // Suggestions are optional; the field stays free text.
      knownModels = [];
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
  loadModels();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formTargets = [''];
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(profile: RoutingProfile) {
    resetForm();
    editingId = profile.id;
    formName = profile.name;
    formDescription = profile.description;
    formTargets = profile.targets.length ? [...profile.targets] : [''];
    showForm = true;
  }

  function addTarget() {
    if (formTargets.length >= ROUTING_PROFILE_MAX_TARGETS) {
      addToast(`At most ${ROUTING_PROFILE_MAX_TARGETS} targets are allowed`, 'warn');
      return;
    }
    formTargets = [...formTargets, ''];
  }

  function removeTarget(index: number) {
    formTargets = formTargets.filter((_, i) => i !== index);
    if (formTargets.length === 0) formTargets = [''];
  }

  function moveTarget(index: number, delta: number) {
    const next = index + delta;
    if (next < 0 || next >= formTargets.length) return;
    const copy = [...formTargets];
    [copy[index], copy[next]] = [copy[next], copy[index]];
    formTargets = copy;
  }

  async function handleSubmit() {
    const name = formName.trim();
    if (!name) {
      addToast('Profile name is required', 'warn');
      return;
    }
    if (name.includes('/')) {
      addToast('Profile names may not contain "/" — that is how a direct provider/model reference is recognised', 'warn');
      return;
    }

    const targets = formTargets.map((t) => t.trim()).filter(Boolean);
    if (targets.length === 0) {
      addToast('At least one target is required', 'warn');
      return;
    }
    const malformed = targets.find((t) => !/^[^/]+\/.+$/.test(t));
    if (malformed) {
      addToast(`Target "${malformed}" must be in provider/model form`, 'warn');
      return;
    }
    if (new Set(targets).size !== targets.length) {
      addToast('Targets must be unique', 'warn');
      return;
    }

    saving = true;
    try {
      const body = { name, description: formDescription.trim(), targets };
      if (editingId) {
        await updateRoutingProfile(editingId, body);
        addToast('Routing profile updated', 'info');
      } else {
        await createRoutingProfile(body);
        addToast('Routing profile created', 'info');
      }
      resetForm();
      load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save routing profile', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteRoutingProfile(id);
      addToast('Routing profile deleted', 'info');
      deleteConfirm = null;
      load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete routing profile', 'alert');
    }
  }
</script>

<svelte:head>
  <title>AT | Routing Profiles</title>
</svelte:head>

<datalist id="routing-profile-models">
  {#each knownModels as model}
    <option value={model}></option>
  {/each}
</datalist>

<div class="p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Route size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Routing Profiles</h2>
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
        New Profile
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formName}` : 'New Routing Profile'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary ">
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
            placeholder="e.g., my-stack"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
          />
        </div>

        <!-- Description -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
          <input
            id="form-description"
            type="text"
            bind:value={formDescription}
            placeholder="What this chain is for (optional)"
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
          />
        </div>

        <!-- Targets -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <label for="form-target-0" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Targets</label>
          <div class="col-span-3 space-y-2">
            {#each formTargets as _, i}
              <div class="flex items-center gap-2">
                <span class="text-xs text-gray-400 dark:text-dark-text-muted font-mono w-5 text-right">{i + 1}</span>
                <input
                  id={`form-target-${i}`}
                  type="text"
                  list="routing-profile-models"
                  bind:value={formTargets[i]}
                  placeholder="provider/model"
                  class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted "
                />
                <button
                  type="button"
                  onclick={() => moveTarget(i, -1)}
                  disabled={i === 0}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-30"
                  title="Move up"
                >
                  <ArrowUp size={14} />
                </button>
                <button
                  type="button"
                  onclick={() => moveTarget(i, 1)}
                  disabled={i === formTargets.length - 1}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-30"
                  title="Move down"
                >
                  <ArrowDown size={14} />
                </button>
                <button
                  type="button"
                  onclick={() => removeTarget(i)}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 "
                  title="Remove"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            {/each}
            <button
              type="button"
              onclick={addTarget}
              class="flex items-center gap-1.5 px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary "
            >
              <Plus size={12} />
              Add target
            </button>
          </div>
        </div>

        <!-- Usage hint -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <div></div>
          <div class="col-span-3 text-xs text-gray-400 dark:text-dark-text-muted bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border px-3 py-2 space-y-1">
            <div>
              Send <code class="font-mono">"model": "{formName || 'name'}"</code> to the gateway and the request is tried against each target in order, falling back on rate limits and upstream errors.
            </div>
            <div>A profile only routes: every target still has to be permitted by the calling token.</div>
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

  <!-- Profile list -->
  {#if loading || profiles.length > 0 || !showForm}
    <DataTable
      items={profiles}
      {loading}
      {total}
      bind:limit
      bind:offset
      onchange={load}
      onsearch={handleSearch}
      searchPlaceholder="Search by name..."
      emptyIcon={Route}
      emptyTitle="No routing profiles configured"
      emptyDescription="A routing profile lets a client send one model name and have the gateway try several provider models in order"
    >
      {#snippet header()}
        <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Chain</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
        <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
        <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
      {/snippet}

      {#snippet row(profile)}
        <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
          <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{profile.name}</td>
          <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-muted">
            <div class="flex flex-wrap items-center gap-1">
              {#each profile.targets as target, i}
                {#if i > 0}
                  <span class="text-gray-300 dark:text-dark-text-muted">→</span>
                {/if}
                <span class="border border-gray-200 dark:border-dark-border px-1.5 py-0.5 text-gray-700 dark:text-dark-text-secondary">{target}</span>
              {/each}
            </div>
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-48 truncate" title={profile.description}>
            {profile.description || '-'}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(profile.updated_at)}</td>
          <td class="px-4 py-2.5 text-right">
            <div class="flex justify-end gap-1">
              <button
                onclick={() => openEdit(profile)}
                class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text "
                title="Edit"
              >
                <Pencil size={14} />
              </button>
              {#if deleteConfirm === profile.id}
                <button
                  onclick={() => handleDelete(profile.id)}
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
                  onclick={() => (deleteConfirm = profile.id)}
                  class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 "
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
