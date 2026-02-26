<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listNodeConfigs,
    createNodeConfig,
    updateNodeConfig,
    deleteNodeConfig,
    type NodeConfig,
  } from '@/lib/api/node-configs';
  import { Settings, Plus, Pencil, Trash2, X, Save, RefreshCw, Eye, EyeOff } from 'lucide-svelte';

  storeNavbar.title = 'Node Configs';

  // ─── State ───

  let configs = $state<NodeConfig[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  // Form fields
  let formName = $state('');
  let formType = $state('email');

  // Email config fields
  let formHost = $state('');
  let formPort = $state(587);
  let formUsername = $state('');
  let formPassword = $state('');
  let formFrom = $state('');
  let formTls = $state(false);
  let formShowPassword = $state(false);
  let formHasStoredPassword = $state(false);
  let saving = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      configs = await listNodeConfigs();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load node configs', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formType = 'email';
    formHost = '';
    formPort = 587;
    formUsername = '';
    formPassword = '';
    formFrom = '';
    formTls = false;
    formShowPassword = false;
    formHasStoredPassword = false;
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(config: NodeConfig) {
    resetForm();
    editingId = config.id;
    formName = config.name;
    formType = config.type;

    try {
      const data = JSON.parse(config.data);
      if (config.type === 'email') {
        formHost = data.host || '';
        formPort = data.port || 587;
        formUsername = data.username || '';
        formFrom = data.from || '';
        formTls = data.tls || false;
        formPassword = '';
        formHasStoredPassword = !!(data.username || data.password);
      }
    } catch {
      // ignore parse errors
    }

    showForm = true;
  }

  function buildDataJSON(): string {
    if (formType === 'email') {
      const data: Record<string, any> = {
        host: formHost.trim(),
        port: formPort,
        username: formUsername.trim(),
        from: formFrom.trim(),
        tls: formTls,
      };
      if (formPassword) {
        data.password = formPassword;
      }
      return JSON.stringify(data);
    }
    return '{}';
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Config name is required', 'warn');
      return;
    }

    if (formType === 'email' && !formHost.trim()) {
      addToast('SMTP host is required', 'warn');
      return;
    }

    saving = true;
    try {
      const payload = {
        name: formName.trim(),
        type: formType,
        data: buildDataJSON(),
      };

      if (editingId) {
        await updateNodeConfig(editingId, payload);
        addToast(`Config "${formName}" updated`);
      } else {
        await createNodeConfig(payload);
        addToast(`Config "${formName}" created`);
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save config', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteNodeConfig(id);
      addToast('Config deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete config', 'alert');
    }
  }

  function formatDate(dateStr: string): string {
    if (!dateStr) return '-';
    const d = new Date(dateStr);
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
  }

  function parseConfigSummary(config: NodeConfig): string {
    try {
      const data = JSON.parse(config.data);
      if (config.type === 'email') {
        return `${data.host || '?'}:${data.port || '?'}`;
      }
    } catch {
      // ignore
    }
    return '-';
  }
</script>

<svelte:head>
  <title>AT | Node Configs</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Settings size={16} class="text-gray-500" />
      <h2 class="text-sm font-medium text-gray-900">Node Configs</h2>
      <span class="text-xs text-gray-400">({configs.length})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={load}
        class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors"
      >
        <Plus size={12} />
        New Config
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 mb-6 bg-white shadow-sm overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 bg-gray-50">
        <span class="text-sm font-medium text-gray-900">
          {editingId ? `Edit: ${formName}` : 'New Config'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 text-gray-400 hover:text-gray-600 transition-colors">
          <X size={14} />
        </button>
      </div>

      <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Name -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-name" class="text-sm font-medium text-gray-700">Name</label>
          <input
            id="form-name"
            type="text"
            bind:value={formName}
            placeholder="e.g., Production SMTP"
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          />
        </div>

        <!-- Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-type" class="text-sm font-medium text-gray-700">Type</label>
          <select
            id="form-type"
            bind:value={formType}
            class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm bg-white focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
          >
            <option value="email">Email (SMTP)</option>
          </select>
        </div>

        <!-- Email-specific fields -->
        {#if formType === 'email'}
          <!-- Host -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-host" class="text-sm font-medium text-gray-700">Host</label>
            <input
              id="form-host"
              type="text"
              bind:value={formHost}
              placeholder="smtp.example.com"
              class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
          </div>

          <!-- Port -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-port" class="text-sm font-medium text-gray-700">Port</label>
            <input
              id="form-port"
              type="number"
              bind:value={formPort}
              placeholder="587"
              class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
          </div>

          <!-- Username -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-username" class="text-sm font-medium text-gray-700">Username</label>
            <input
              id="form-username"
              type="text"
              bind:value={formUsername}
              placeholder="(optional) user@example.com"
              class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
          </div>

          <!-- Password -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-password" class="text-sm font-medium text-gray-700">Password</label>
            <div class="col-span-3 flex gap-2">
              <input
                id="form-password"
                type={formShowPassword ? 'text' : 'password'}
                bind:value={formPassword}
                placeholder={editingId && formHasStoredPassword ? '(stored - leave blank to keep)' : '(optional) SMTP password'}
                class="flex-1 border border-gray-300 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
              />
              <button
                type="button"
                onclick={() => { formShowPassword = !formShowPassword; }}
                class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-400 hover:text-gray-600 transition-colors"
                title={formShowPassword ? 'Hide password' : 'Show password'}
              >
                {#if formShowPassword}
                  <EyeOff size={14} />
                {:else}
                  <Eye size={14} />
                {/if}
              </button>
            </div>
          </div>

          <!-- From -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-from" class="text-sm font-medium text-gray-700">Default From</label>
            <input
              id="form-from"
              type="text"
              bind:value={formFrom}
              placeholder="noreply@example.com"
              class="col-span-3 border border-gray-300 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
          </div>

          <!-- TLS toggle -->
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-tls" class="text-sm font-medium text-gray-700">Implicit TLS</label>
            <div class="col-span-3 flex items-center gap-2">
              <input
                id="form-tls"
                type="checkbox"
                bind:checked={formTls}
                class="w-4 h-4 text-gray-900 border-gray-300 focus:ring-gray-900/10"
              />
              <span class="text-xs text-gray-500">
                {formTls ? 'TLS from start (port 465)' : 'STARTTLS upgrade (port 587/25)'}
              </span>
            </div>
          </div>
        {/if}

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-gray-100">
          <button
            type="button"
            onclick={resetForm}
            class="px-3 py-1.5 text-sm border border-gray-300 hover:bg-gray-50 text-gray-700 transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={saving}
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 transition-colors disabled:opacity-50"
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

  <!-- Config list -->
  <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
    {#if loading}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
    {:else if configs.length === 0 && !showForm}
      <div class="px-4 py-10 text-center">
        <Settings size={24} class="mx-auto text-gray-300 mb-2" />
        <div class="text-gray-400 mb-1">No node configs configured</div>
        <div class="text-xs text-gray-400 mb-3">Node configs store reusable settings like SMTP servers for workflow nodes</div>
        <button
          onclick={openCreate}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors mx-auto"
        >
          <Plus size={12} />
          New Config
        </button>
      </div>
    {:else if configs.length > 0}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 bg-gray-50">
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Name</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Type</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Details</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider">Updated</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 text-xs uppercase tracking-wider w-24"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          {#each configs as config}
            <tr class="hover:bg-gray-50/50 transition-colors">
              <td class="px-4 py-2.5 font-medium text-gray-900">{config.name}</td>
              <td class="px-4 py-2.5 text-xs font-mono text-gray-500">{config.type}</td>
              <td class="px-4 py-2.5 text-xs font-mono text-gray-500">{parseConfigSummary(config)}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500">{formatDate(config.updated_at)}</td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => openEdit(config)}
                    class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-700 transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === config.id}
                    <button
                      onclick={() => handleDelete(config.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 hover:bg-gray-50 transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = config.id)}
                      class="p-1.5 hover:bg-red-50 text-gray-400 hover:text-red-600 transition-colors"
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
    {/if}
  </div>
</div>
