<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listTokens, createToken, deleteToken, updateToken, type APIToken, type CreateTokenResponse } from '@/lib/api/tokens';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { listWorkflows, type Workflow } from '@/lib/api/workflows';
  import { listTriggers, type Trigger } from '@/lib/api/triggers';
  import { Key, Plus, Trash2, RefreshCw, Copy, X, ChevronDown, Pencil, FileCode, Check } from 'lucide-svelte';
  import { generateAuthTokenYamlSnippet, generateAuthTokenJsonSnippet } from '@/lib/helper/config-snippet';

  storeNavbar.title = 'API Tokens';

  // ─── State ───
  let tokens = $state<APIToken[]>([]);
  let loading = $state(true);
  let providers = $state<InfoProvider[]>([]);

  // Webhook data
  let workflows = $state<Workflow[]>([]);
  let webhookTriggers = $state<{ trigger: Trigger; workflowName: string }[]>([]);

  // Create form
  let showCreate = $state(false);
  let formName = $state('');
  let formExpiresAt = $state('');
  let formSelectedProviders = $state<string[]>([]);
  let formSelectedModels = $state<string[]>([]);
  let formSelectedWebhooks = $state<string[]>([]);
  let creating = $state(false);

  // Created token modal
  let createdToken = $state<string | null>(null);
  let copied = $state(false);

  // Delete confirmation
  let deleteConfirmId = $state<string | null>(null);

  // Edit state
  let editingTokenId = $state<string | null>(null);
  let editName = $state('');
  let editExpiresAt = $state('');
  let editSelectedProviders = $state<string[]>([]);
  let editSelectedModels = $state<string[]>([]);
  let editSelectedWebhooks = $state<string[]>([]);
  let saving = $state(false);

  // Config viewer state
  let configViewToken = $state<APIToken | null>(null);
  let configFormat = $state<'yaml' | 'json'>('yaml');
  let configCopied = $state(false);

  // ─── Data Loading ───
  async function loadTokens() {
    loading = true;
    try {
      tokens = await listTokens();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load tokens', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadProviders() {
    try {
      const info = await getInfo();
      providers = info.providers;
    } catch (_) {}
  }

  async function loadWebhooks() {
    try {
      const wfs = await listWorkflows();
      workflows = wfs;
      const results: { trigger: Trigger; workflowName: string }[] = [];
      for (const wf of wfs) {
        try {
          const triggers = await listTriggers(wf.id);
          for (const t of triggers) {
            if (t.type === 'http') {
              results.push({ trigger: t, workflowName: wf.name });
            }
          }
        } catch (_) {}
      }
      webhookTriggers = results;
    } catch (_) {}
  }

  loadTokens();
  loadProviders();
  loadWebhooks();

  // ─── Computed ───
  let allModels = $derived(
    providers.flatMap((p) => {
      if (p.models && p.models.length > 0) {
        return p.models.map((m) => `${p.key}/${m}`);
      }
      return [`${p.key}/${p.default_model}`];
    })
  );

  let allProviderKeys = $derived(providers.map((p) => p.key));

  // Group webhooks by workflow name for the picker UI.
  let webhooksByWorkflow = $derived(
    webhookTriggers.reduce<Record<string, { trigger: Trigger; workflowName: string }[]>>((acc, item) => {
      if (!acc[item.workflowName]) acc[item.workflowName] = [];
      acc[item.workflowName].push(item);
      return acc;
    }, {})
  );

  // ─── Actions ───
  function resetForm() {
    formName = '';
    formExpiresAt = '';
    formSelectedProviders = [];
    formSelectedModels = [];
    formSelectedWebhooks = [];
  }

  async function handleCreate() {
    if (!formName.trim()) {
      addToast('Token name is required', 'alert');
      return;
    }

    creating = true;
    try {
      const req: any = { name: formName.trim() };

      if (formSelectedProviders.length > 0) {
        req.allowed_providers = formSelectedProviders;
      }
      if (formSelectedModels.length > 0) {
        req.allowed_models = formSelectedModels;
      }
      if (formSelectedWebhooks.length > 0) {
        req.allowed_webhooks = formSelectedWebhooks;
      }
      if (formExpiresAt) {
        req.expires_at = new Date(formExpiresAt).toISOString();
      }

      const resp: CreateTokenResponse = await createToken(req);
      createdToken = resp.token;
      copied = false;
      showCreate = false;
      resetForm();
      await loadTokens();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create token', 'alert');
    } finally {
      creating = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteToken(id);
      deleteConfirmId = null;
      addToast('Token deleted', 'info');
      await loadTokens();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete token', 'alert');
    }
  }

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text);
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }

  function formatDate(dateStr: string | null): string {
    if (!dateStr) return '-';
    const d = new Date(dateStr);
    return d.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  function isExpired(expiresAt: string | null): boolean {
    if (!expiresAt) return false;
    return new Date(expiresAt) < new Date();
  }

  function toggleProvider(key: string) {
    if (formSelectedProviders.includes(key)) {
      formSelectedProviders = formSelectedProviders.filter((p) => p !== key);
    } else {
      formSelectedProviders = [...formSelectedProviders, key];
    }
  }

  function toggleModel(model: string) {
    if (formSelectedModels.includes(model)) {
      formSelectedModels = formSelectedModels.filter((m) => m !== model);
    } else {
      formSelectedModels = [...formSelectedModels, model];
    }
  }

  function toggleWebhook(id: string) {
    if (formSelectedWebhooks.includes(id)) {
      formSelectedWebhooks = formSelectedWebhooks.filter((w) => w !== id);
    } else {
      formSelectedWebhooks = [...formSelectedWebhooks, id];
    }
  }

  // ─── Edit Actions ───
  function startEditing(token: APIToken) {
    editingTokenId = token.id;
    editName = token.name;
    editSelectedProviders = token.allowed_providers ? [...token.allowed_providers] : [];
    editSelectedModels = token.allowed_models ? [...token.allowed_models] : [];
    editSelectedWebhooks = token.allowed_webhooks ? [...token.allowed_webhooks] : [];
    // Convert expires_at to datetime-local format for the input
    if (token.expires_at) {
      const d = new Date(token.expires_at);
      editExpiresAt = d.toISOString().slice(0, 16); // "YYYY-MM-DDTHH:MM"
    } else {
      editExpiresAt = '';
    }
  }

  function cancelEditing() {
    editingTokenId = null;
    editName = '';
    editExpiresAt = '';
    editSelectedProviders = [];
    editSelectedModels = [];
    editSelectedWebhooks = [];
  }

  function toggleEditProvider(key: string) {
    if (editSelectedProviders.includes(key)) {
      editSelectedProviders = editSelectedProviders.filter((p) => p !== key);
    } else {
      editSelectedProviders = [...editSelectedProviders, key];
    }
  }

  function toggleEditModel(model: string) {
    if (editSelectedModels.includes(model)) {
      editSelectedModels = editSelectedModels.filter((m) => m !== model);
    } else {
      editSelectedModels = [...editSelectedModels, model];
    }
  }

  function toggleEditWebhook(id: string) {
    if (editSelectedWebhooks.includes(id)) {
      editSelectedWebhooks = editSelectedWebhooks.filter((w) => w !== id);
    } else {
      editSelectedWebhooks = [...editSelectedWebhooks, id];
    }
  }

  async function handleSaveEdit() {
    if (!editingTokenId) return;
    if (!editName.trim()) {
      addToast('Token name is required', 'alert');
      return;
    }

    saving = true;
    try {
      const req: any = { name: editName.trim() };

      if (editSelectedProviders.length > 0) {
        req.allowed_providers = editSelectedProviders;
      }
      if (editSelectedModels.length > 0) {
        req.allowed_models = editSelectedModels;
      }
      if (editSelectedWebhooks.length > 0) {
        req.allowed_webhooks = editSelectedWebhooks;
      }
      if (editExpiresAt) {
        req.expires_at = new Date(editExpiresAt).toISOString();
      }

      await updateToken(editingTokenId, req);
      addToast('Token updated', 'info');
      cancelEditing();
      await loadTokens();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update token', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Config Viewer ───

  function openConfigView(token: APIToken) {
    configViewToken = token;
    configFormat = 'yaml';
    configCopied = false;
  }

  function closeConfigView() {
    configViewToken = null;
    configCopied = false;
  }

  function getConfigSnippet(): string {
    if (!configViewToken) return '';
    if (configFormat === 'yaml') {
      return generateAuthTokenYamlSnippet(configViewToken);
    }
    return generateAuthTokenJsonSnippet(configViewToken);
  }

  function copyConfigSnippet() {
    const snippet = getConfigSnippet();
    navigator.clipboard.writeText(snippet);
    configCopied = true;
    addToast('Config copied to clipboard');
    setTimeout(() => { configCopied = false; }, 2000);
  }
</script>

<svelte:head>
  <title>AT | API Tokens</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Key size={16} class="text-gray-500" />
      <h2 class="text-sm font-medium text-gray-900">API Tokens</h2>
      <span class="text-xs text-gray-400">({tokens.length})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={loadTokens}
        class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={() => { showCreate = !showCreate; if (!showCreate) resetForm(); }}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors"
      >
        <Plus size={12} />
        New Token
      </button>
    </div>
  </div>

  <!-- Created token modal -->
  {#if createdToken}
    <div class="mb-4 border border-green-200 bg-green-50 p-4">
      <div class="flex items-center justify-between mb-2">
        <span class="text-sm font-medium text-green-800">Token Created</span>
        <button onclick={() => (createdToken = null)} class="text-green-600 hover:text-green-800">
          <X size={14} />
        </button>
      </div>
      <p class="text-xs text-green-700 mb-2">Copy this token now. It won't be shown again.</p>
      <div class="flex items-center gap-2">
        <code class="flex-1 bg-white border border-green-200 px-3 py-2 text-xs font-mono text-green-900 break-all select-all">{createdToken}</code>
        <button
          onclick={() => copyToClipboard(createdToken!)}
          class="shrink-0 p-2 bg-white border border-green-200 hover:bg-green-100 transition-colors"
          title="Copy"
        >
          <Copy size={14} class={copied ? 'text-green-600' : 'text-green-500'} />
        </button>
      </div>
      {#if copied}
        <span class="text-xs text-green-600 mt-1 block">Copied!</span>
      {/if}
    </div>
  {/if}

  <!-- Create form -->
  {#if showCreate}
    <div class="mb-4 border border-gray-200 bg-white p-4">
      <h3 class="text-sm font-medium text-gray-900 mb-3">Create API Token</h3>

      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="create-token-name" class="text-xs text-gray-600 py-2">Name</label>
        <input
          id="create-token-name"
          type="text"
          bind:value={formName}
          placeholder="e.g. my-app-token"
          class="col-span-3 border border-gray-200 px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"
        />
      </div>

      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="create-token-expires" class="text-xs text-gray-600 py-2">Expires At</label>
        <div class="col-span-3 flex items-center gap-2">
          <input
            id="create-token-expires"
            type="datetime-local"
            bind:value={formExpiresAt}
            class="border border-gray-200 px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"
          />
          {#if formExpiresAt}
            <button
              onclick={() => (formExpiresAt = '')}
              class="text-xs text-gray-400 hover:text-gray-600"
            >
              Clear (no expiry)
            </button>
          {:else}
            <span class="text-xs text-gray-400">No expiry</span>
          {/if}
        </div>
      </div>

      <!-- Provider restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 py-2">Allowed Providers</span>
        <div class="col-span-3">
          {#if allProviderKeys.length > 0}
            <div class="flex flex-wrap gap-1.5">
              {#each allProviderKeys as key}
                <button
                  onclick={() => toggleProvider(key)}
                  class={[
                    'px-2 py-1 text-xs border transition-colors',
                    formSelectedProviders.includes(key)
                      ? 'bg-gray-900 text-white border-gray-900'
                      : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                  ]}
                >
                  {key}
                </button>
              {/each}
            </div>
            <p class="text-xs text-gray-400 mt-1">None selected = all providers allowed</p>
          {:else}
            <span class="text-xs text-gray-400">No providers available</span>
          {/if}
        </div>
      </div>

      <!-- Model restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 py-2">Allowed Models</span>
        <div class="col-span-3">
          {#if allModels.length > 0}
            <div class="max-h-32 overflow-y-auto border border-gray-200 p-2">
              <div class="flex flex-wrap gap-1.5">
                {#each allModels as model}
                  <button
                    onclick={() => toggleModel(model)}
                    class={[
                      'px-2 py-0.5 text-xs border font-mono transition-colors',
                      formSelectedModels.includes(model)
                        ? 'bg-gray-900 text-white border-gray-900'
                        : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                    ]}
                  >
                    {model}
                  </button>
                {/each}
              </div>
            </div>
            <p class="text-xs text-gray-400 mt-1">None selected = all models allowed</p>
          {:else}
            <span class="text-xs text-gray-400">No models available</span>
          {/if}
        </div>
      </div>

      <!-- Webhook restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-4">
        <span class="text-xs text-gray-600 py-2">Allowed Webhooks</span>
        <div class="col-span-3">
          {#if webhookTriggers.length > 0}
            <div class="max-h-40 overflow-y-auto border border-gray-200 p-2 space-y-2">
              {#each Object.entries(webhooksByWorkflow) as [wfName, items]}
                <div>
                  <div class="text-xs text-gray-400 mb-1">{wfName}</div>
                  <div class="flex flex-wrap gap-1.5">
                    {#each items as { trigger }}
                      <button
                        onclick={() => toggleWebhook(trigger.id)}
                        class={[
                          'px-2 py-0.5 text-xs border font-mono transition-colors',
                          formSelectedWebhooks.includes(trigger.id)
                            ? 'bg-gray-900 text-white border-gray-900'
                            : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                        ]}
                      >
                        {trigger.alias || trigger.id}
                      </button>
                    {/each}
                  </div>
                </div>
              {/each}
            </div>
            <p class="text-xs text-gray-400 mt-1">None selected = all webhooks allowed</p>
          {:else}
            <span class="text-xs text-gray-400">No webhooks available</span>
          {/if}
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          onclick={handleCreate}
          disabled={creating}
          class="px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors disabled:opacity-50"
        >
          {creating ? 'Creating...' : 'Create Token'}
        </button>
        <button
          onclick={() => { showCreate = false; resetForm(); }}
          class="px-3 py-1.5 text-xs text-gray-600 hover:text-gray-900 transition-colors"
        >
          Cancel
        </button>
      </div>
    </div>
  {/if}

  <!-- Token list -->
  <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
    {#if loading}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
    {:else if tokens.length === 0}
      <div class="px-4 py-10 text-center">
        <Key size={24} class="mx-auto text-gray-300 mb-2" />
        <div class="text-gray-400 mb-1">No API tokens</div>
        <div class="text-xs text-gray-400">Create a token to authenticate API requests</div>
      </div>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-100 bg-gray-50/50">
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Name</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Token</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Scope</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Expires</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Created By</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Last Used</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider w-16"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-50">
          {#each tokens as token}
            {#if editingTokenId === token.id}
              <!-- Edit form row -->
              <tr class="bg-blue-50/30">
                <td colspan="7" class="px-4 py-3">
                  <div class="space-y-3">
                    <div class="flex items-center gap-2 mb-1">
                      <Pencil size={12} class="text-gray-400" />
                      <span class="text-xs font-medium text-gray-600">Editing token</span>
                      <code class="text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5">{token.token_prefix}...</code>
                    </div>

                    <div class="grid grid-cols-4 gap-3">
                      <label for="edit-token-name" class="text-xs text-gray-600 py-2">Name</label>
                      <input
                        id="edit-token-name"
                        type="text"
                        bind:value={editName}
                        class="col-span-3 border border-gray-200 px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"
                      />
                    </div>

                    <div class="grid grid-cols-4 gap-3">
                      <label for="edit-token-expires" class="text-xs text-gray-600 py-2">Expires At</label>
                      <div class="col-span-3 flex items-center gap-2">
                        <input
                          id="edit-token-expires"
                          type="datetime-local"
                          bind:value={editExpiresAt}
                          class="border border-gray-200 px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"
                        />
                        {#if editExpiresAt}
                          <button
                            onclick={() => (editExpiresAt = '')}
                            class="text-xs text-gray-400 hover:text-gray-600"
                          >
                            Clear (no expiry)
                          </button>
                        {:else}
                          <span class="text-xs text-gray-400">No expiry</span>
                        {/if}
                      </div>
                    </div>

                    <div class="grid grid-cols-4 gap-3">
                      <span class="text-xs text-gray-600 py-2">Allowed Providers</span>
                      <div class="col-span-3">
                        {#if allProviderKeys.length > 0}
                          <div class="flex flex-wrap gap-1.5">
                            {#each allProviderKeys as key}
                              <button
                                onclick={() => toggleEditProvider(key)}
                                class={[
                                  'px-2 py-1 text-xs border transition-colors',
                                  editSelectedProviders.includes(key)
                                    ? 'bg-gray-900 text-white border-gray-900'
                                    : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                                ]}
                              >
                                {key}
                              </button>
                            {/each}
                          </div>
                          <p class="text-xs text-gray-400 mt-1">None selected = all providers allowed</p>
                        {:else}
                          <span class="text-xs text-gray-400">No providers available</span>
                        {/if}
                      </div>
                    </div>

                    <div class="grid grid-cols-4 gap-3">
                      <span class="text-xs text-gray-600 py-2">Allowed Models</span>
                      <div class="col-span-3">
                        {#if allModels.length > 0}
                          <div class="max-h-32 overflow-y-auto border border-gray-200 p-2">
                            <div class="flex flex-wrap gap-1.5">
                              {#each allModels as model}
                                <button
                                  onclick={() => toggleEditModel(model)}
                                  class={[
                                    'px-2 py-0.5 text-xs border font-mono transition-colors',
                                    editSelectedModels.includes(model)
                                      ? 'bg-gray-900 text-white border-gray-900'
                                      : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                                  ]}
                                >
                                  {model}
                                </button>
                              {/each}
                            </div>
                          </div>
                          <p class="text-xs text-gray-400 mt-1">None selected = all models allowed</p>
                        {:else}
                          <span class="text-xs text-gray-400">No models available</span>
                        {/if}
                      </div>
                    </div>

                    <div class="grid grid-cols-4 gap-3">
                      <span class="text-xs text-gray-600 py-2">Allowed Webhooks</span>
                      <div class="col-span-3">
                        {#if webhookTriggers.length > 0}
                          <div class="max-h-40 overflow-y-auto border border-gray-200 p-2 space-y-2">
                            {#each Object.entries(webhooksByWorkflow) as [wfName, items]}
                              <div>
                                <div class="text-xs text-gray-400 mb-1">{wfName}</div>
                                <div class="flex flex-wrap gap-1.5">
                                  {#each items as { trigger }}
                                    <button
                                      onclick={() => toggleEditWebhook(trigger.id)}
                                      class={[
                                        'px-2 py-0.5 text-xs border font-mono transition-colors',
                                        editSelectedWebhooks.includes(trigger.id)
                                          ? 'bg-gray-900 text-white border-gray-900'
                                          : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300'
                                      ]}
                                    >
                                      {trigger.alias || trigger.id}
                                    </button>
                                  {/each}
                                </div>
                              </div>
                            {/each}
                          </div>
                          <p class="text-xs text-gray-400 mt-1">None selected = all webhooks allowed</p>
                        {:else}
                          <span class="text-xs text-gray-400">No webhooks available</span>
                        {/if}
                      </div>
                    </div>

                    <div class="flex items-center gap-2 pt-1">
                      <button
                        onclick={handleSaveEdit}
                        disabled={saving}
                        class="px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 transition-colors disabled:opacity-50"
                      >
                        {saving ? 'Saving...' : 'Save'}
                      </button>
                      <button
                        onclick={cancelEditing}
                        class="px-3 py-1.5 text-xs text-gray-600 hover:text-gray-900 transition-colors"
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                </td>
              </tr>
            {:else}
            <tr class="hover:bg-gray-50/50 transition-colors">
              <td class="px-4 py-2.5 font-medium text-gray-900 text-sm">{token.name}</td>
              <td class="px-4 py-2.5">
                <code class="text-xs font-mono text-gray-500 bg-gray-100 px-1.5 py-0.5">{token.token_prefix}...</code>
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                {#if !token.allowed_providers && !token.allowed_models && !token.allowed_webhooks}
                  <span class="text-gray-400">All access</span>
                {:else}
                  <div class="space-y-0.5">
                    {#if token.allowed_providers && token.allowed_providers.length > 0}
                      <div>
                        <span class="text-gray-400">Providers:</span>
                        {token.allowed_providers.join(', ')}
                      </div>
                    {/if}
                    {#if token.allowed_models && token.allowed_models.length > 0}
                      <div>
                        <span class="text-gray-400">Models:</span>
                        {token.allowed_models.slice(0, 3).join(', ')}{token.allowed_models.length > 3 ? ` +${token.allowed_models.length - 3}` : ''}
                      </div>
                    {/if}
                    {#if token.allowed_webhooks && token.allowed_webhooks.length > 0}
                      <div>
                        <span class="text-gray-400">Webhooks:</span>
                        {token.allowed_webhooks.slice(0, 3).join(', ')}{token.allowed_webhooks.length > 3 ? ` +${token.allowed_webhooks.length - 3}` : ''}
                      </div>
                    {/if}
                  </div>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-xs">
                {#if token.expires_at}
                  <span class={isExpired(token.expires_at) ? 'text-red-500' : 'text-gray-500'}>
                    {isExpired(token.expires_at) ? 'Expired' : formatDate(token.expires_at)}
                  </span>
                {:else}
                  <span class="text-gray-400">Never</span>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 max-w-[150px] truncate" title={token.created_by}>
                {token.created_by || '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                {formatDate(token.last_used_at)}
              </td>
              <td class="px-4 py-2.5 text-right">
                {#if deleteConfirmId === token.id}
                  <div class="flex items-center gap-1 justify-end">
                    <button
                      onclick={() => handleDelete(token.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirmId = null)}
                      class="px-2 py-1 text-xs text-gray-500 hover:text-gray-700 transition-colors"
                    >
                      Cancel
                    </button>
                  </div>
                {:else}
                  <div class="flex items-center gap-1 justify-end">
                    <button
                      onclick={() => openConfigView(token)}
                      class="p-1 text-gray-300 hover:text-gray-600 transition-colors"
                      title="View Config"
                    >
                      <FileCode size={14} />
                    </button>
                    <button
                      onclick={() => startEditing(token)}
                      class="p-1 text-gray-300 hover:text-gray-600 transition-colors"
                      title="Edit"
                    >
                      <Pencil size={14} />
                    </button>
                    <button
                      onclick={() => (deleteConfirmId = token.id)}
                      class="p-1 text-gray-300 hover:text-red-500 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                {/if}
              </td>
            </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {/if}
  </div>

  <!-- Config Viewer Modal -->
  {#if configViewToken}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="fixed inset-0 bg-black/40 z-50 flex items-center justify-center p-4"
      onkeydown={(e) => { if (e.key === 'Escape') closeConfigView(); }}
      onclick={(e) => { if (e.target === e.currentTarget) closeConfigView(); }}
    >
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div class="bg-white shadow-xl w-full max-w-xl overflow-hidden" onclick={(e) => e.stopPropagation()}>
        <!-- Header -->
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 bg-gray-50">
          <span class="text-sm font-medium text-gray-900">
            Config: <span class="font-mono">{configViewToken.name}</span>
          </span>
          <button onclick={closeConfigView} class="p-1 hover:bg-gray-200 text-gray-400 hover:text-gray-600 transition-colors">
            <X size={14} />
          </button>
        </div>

        <!-- Format Toggle + Copy -->
        <div class="flex items-center justify-between px-4 py-2 border-b border-gray-100">
          <div class="flex gap-1">
            <button
              onclick={() => { configFormat = 'yaml'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'yaml' ? 'bg-gray-900 text-white' : 'bg-gray-100 text-gray-600 hover:bg-gray-200'}"
            >
              YAML
            </button>
            <button
              onclick={() => { configFormat = 'json'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'json' ? 'bg-gray-900 text-white' : 'bg-gray-100 text-gray-600 hover:bg-gray-200'}"
            >
              JSON
            </button>
          </div>
          <button
            onclick={copyConfigSnippet}
            class="flex items-center gap-1.5 px-2.5 py-1 text-xs border border-gray-200 hover:bg-gray-50 text-gray-600 hover:text-gray-900 transition-colors"
          >
            {#if configCopied}
              <Check size={12} class="text-green-600" />
              <span class="text-green-600">Copied</span>
            {:else}
              <Copy size={12} />
              Copy
            {/if}
          </button>
        </div>

        <!-- Code Block -->
        <div class="p-4 bg-gray-50 max-h-96 overflow-auto">
          <pre class="text-xs font-mono text-gray-800 whitespace-pre leading-relaxed">{getConfigSnippet()}</pre>
        </div>

        <!-- Hint -->
        <div class="px-4 py-2.5 border-t border-gray-100 bg-white">
          <p class="text-xs text-gray-500">
            Add this to your <span class="font-mono font-medium">at.yaml</span> configuration file under the <span class="font-mono font-medium">gateway.auth_tokens</span> section.
          </p>
        </div>
      </div>
    </div>
  {/if}
</div>
