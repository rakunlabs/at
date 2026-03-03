<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listTokens, createToken, deleteToken, updateToken, getTokenUsage, resetTokenUsage, type APIToken, type CreateTokenResponse, type TokenUsage } from '@/lib/api/tokens';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import { listWorkflows, type Workflow } from '@/lib/api/workflows';
  import { listTriggers, type Trigger } from '@/lib/api/triggers';
  import { Key, Plus, Trash2, RefreshCw, Copy, X, ChevronDown, Pencil, FileCode, Check, BarChart3, RotateCcw } from 'lucide-svelte';
  import { generateAuthTokenYamlSnippet, generateAuthTokenJsonSnippet } from '@/lib/helper/config-snippet';
  import { formatDateTime } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'API Tokens';

  // ─── State ───
  let tokens = $state<APIToken[]>([]);
  let loading = $state(true);
  
  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

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
  let formTotalTokenLimit = $state('');
  let formLimitResetInterval = $state('');
  let formResetPreset = $state('');
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
  let editTotalTokenLimit = $state('');
  let editLimitResetInterval = $state('');
  let editResetPreset = $state('');
  let saving = $state(false);

  // Usage state
  let expandedUsageTokenId = $state<string | null>(null);
  let tokenUsageMap = $state<Record<string, TokenUsage[]>>({});
  let loadingUsage = $state<Record<string, boolean>>({});
  let resettingUsage = $state<Record<string, boolean>>({});

  // Config viewer state
  let configViewToken = $state<APIToken | null>(null);
  let configFormat = $state<'yaml' | 'json'>('yaml');
  let configCopied = $state(false);

  // ─── Data Loading ───
  async function loadTokens() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['name[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listTokens(params);
      tokens = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load tokens', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    loadTokens();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    loadTokens();
  }

  async function loadProviders() {
    try {
      const info = await getInfo();
      providers = info.providers;
    } catch (_) {}
  }

  async function loadWebhooks() {
    try {
      const wfsResult = await listWorkflows();
      workflows = wfsResult.data || [];
      const results: { trigger: Trigger; workflowName: string }[] = [];
      for (const wf of workflows) {
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
    formTotalTokenLimit = '';
    formLimitResetInterval = '';
    formResetPreset = '';
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
      if (formTotalTokenLimit) {
        const limit = parseInt(formTotalTokenLimit, 10);
        if (!isNaN(limit) && limit > 0) req.total_token_limit = limit;
      }
      if (formLimitResetInterval) {
        req.limit_reset_interval = formLimitResetInterval;
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
    editTotalTokenLimit = token.total_token_limit != null ? String(token.total_token_limit) : '';
    editLimitResetInterval = token.limit_reset_interval || '';
    // Determine if the interval matches a preset or is custom.
    const presets = ['', '1h', '12h', '24h', '7d', '30d'];
    editResetPreset = presets.includes(editLimitResetInterval) ? editLimitResetInterval : 'custom';
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
    editTotalTokenLimit = '';
    editLimitResetInterval = '';
    editResetPreset = '';
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
      if (editTotalTokenLimit) {
        const limit = parseInt(editTotalTokenLimit, 10);
        if (!isNaN(limit) && limit > 0) req.total_token_limit = limit;
      }
      if (editLimitResetInterval) {
        req.limit_reset_interval = editLimitResetInterval;
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

  // ─── Usage Actions ───

  async function toggleUsage(tokenId: string) {
    if (expandedUsageTokenId === tokenId) {
      expandedUsageTokenId = null;
      return;
    }
    expandedUsageTokenId = tokenId;
    await loadUsage(tokenId);
  }

  async function loadUsage(tokenId: string) {
    loadingUsage = { ...loadingUsage, [tokenId]: true };
    try {
      const usage = await getTokenUsage(tokenId);
      tokenUsageMap = { ...tokenUsageMap, [tokenId]: usage || [] };
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load usage', 'alert');
    } finally {
      loadingUsage = { ...loadingUsage, [tokenId]: false };
    }
  }

  async function handleResetUsage(tokenId: string) {
    resettingUsage = { ...resettingUsage, [tokenId]: true };
    try {
      await resetTokenUsage(tokenId);
      tokenUsageMap = { ...tokenUsageMap, [tokenId]: [] };
      addToast('Usage counters reset', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to reset usage', 'alert');
    } finally {
      resettingUsage = { ...resettingUsage, [tokenId]: false };
    }
  }

  function getTotalUsage(tokenId: string): { totalTokens: number; requestCount: number } {
    const usage = tokenUsageMap[tokenId];
    if (!usage || usage.length === 0) return { totalTokens: 0, requestCount: 0 };
    return usage.reduce(
      (acc, u) => ({ totalTokens: acc.totalTokens + u.total_tokens, requestCount: acc.requestCount + u.request_count }),
      { totalTokens: 0, requestCount: 0 }
    );
  }

  function formatNumber(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
    return String(n);
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
      <Key size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">API Tokens</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={loadTokens}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={() => { showCreate = !showCreate; if (!showCreate) resetForm(); }}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New Token
      </button>
    </div>
  </div>

  <!-- Created token modal -->
  {#if createdToken}
    <div class="mb-4 border border-green-200 dark:border-green-800 bg-green-50 dark:bg-green-900/20 p-4">
      <div class="flex items-center justify-between mb-2">
        <span class="text-sm font-medium text-green-800 dark:text-green-300">Token Created</span>
        <button onclick={() => (createdToken = null)} class="text-green-600 dark:text-green-400 hover:text-green-800 dark:hover:text-green-300">
          <X size={14} />
        </button>
      </div>
      <p class="text-xs text-green-700 dark:text-green-400 mb-2">Copy this token now. It won't be shown again.</p>
      <div class="flex items-center gap-2">
        <code class="flex-1 bg-white dark:bg-dark-elevated border border-green-200 dark:border-green-800 px-3 py-2 text-xs font-mono text-green-900 dark:text-green-200 break-all select-all">{createdToken}</code>
        <button
          onclick={() => copyToClipboard(createdToken!)}
          class="shrink-0 p-2 bg-white dark:bg-dark-elevated border border-green-200 dark:border-green-800 hover:bg-green-100 transition-colors"
          title="Copy"
        >
          <Copy size={14} class={copied ? 'text-green-600' : 'text-green-500'} />
        </button>
      </div>
      {#if copied}
        <span class="text-xs text-green-600 dark:text-green-400 mt-1 block">Copied!</span>
      {/if}
    </div>
  {/if}

  <!-- Create form -->
  {#if showCreate}
    <div class="mb-4 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4">
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text mb-3">Create API Token</h3>

      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="create-token-name" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Name</label>
        <input
          id="create-token-name"
          type="text"
          bind:value={formName}
          placeholder="e.g. my-app-token"
          class="col-span-3 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
        />
      </div>

      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="create-token-expires" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Expires At</label>
        <div class="col-span-3 flex items-center gap-2">
          <input
            id="create-token-expires"
            type="datetime-local"
            bind:value={formExpiresAt}
            class="border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
          />
          {#if formExpiresAt}
            <button
              onclick={() => (formExpiresAt = '')}
              class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary"
            >
              Clear (no expiry)
            </button>
          {:else}
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">No expiry</span>
          {/if}
        </div>
      </div>

      <!-- Provider restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Providers</span>
        <div class="col-span-3">
          {#if allProviderKeys.length > 0}
            <div class="flex flex-wrap gap-1.5">
              {#each allProviderKeys as key}
                <button
                  onclick={() => toggleProvider(key)}
                  class={[
                    'px-2 py-1 text-xs border transition-colors',
                    formSelectedProviders.includes(key)
                      ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                      : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                  ]}
                >
                  {key}
                </button>
              {/each}
            </div>
            <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all providers allowed</p>
          {:else}
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">No providers available</span>
          {/if}
        </div>
      </div>

      <!-- Model restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Models</span>
        <div class="col-span-3">
          {#if allModels.length > 0}
            <div class="max-h-32 overflow-y-auto border border-gray-200 dark:border-dark-border p-2">
              <div class="flex flex-wrap gap-1.5">
                {#each allModels as model}
                  <button
                    onclick={() => toggleModel(model)}
                    class={[
                      'px-2 py-0.5 text-xs border font-mono transition-colors',
                      formSelectedModels.includes(model)
                        ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                        : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                    ]}
                  >
                    {model}
                  </button>
                {/each}
              </div>
            </div>
            <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all models allowed</p>
          {:else}
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">No models available</span>
          {/if}
        </div>
      </div>

      <!-- Webhook restrictions -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Webhooks</span>
        <div class="col-span-3">
          {#if webhookTriggers.length > 0}
            <div class="max-h-40 overflow-y-auto border border-gray-200 dark:border-dark-border p-2 space-y-2">
              {#each Object.entries(webhooksByWorkflow) as [wfName, items]}
                <div>
                  <div class="text-xs text-gray-400 dark:text-dark-text-muted mb-1">{wfName}</div>
                  <div class="flex flex-wrap gap-1.5">
                    {#each items as { trigger }}
                      <button
                        onclick={() => toggleWebhook(trigger.id)}
                        class={[
                          'px-2 py-0.5 text-xs border font-mono transition-colors',
                          formSelectedWebhooks.includes(trigger.id)
                            ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                            : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                        ]}
                      >
                        {trigger.alias || trigger.id}
                      </button>
                    {/each}
                  </div>
                </div>
              {/each}
            </div>
            <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all webhooks allowed</p>
          {:else}
            <span class="text-xs text-gray-400 dark:text-dark-text-muted">No webhooks available</span>
          {/if}
        </div>
      </div>

      <!-- Token limit -->
      <div class="grid grid-cols-4 gap-3 mb-3">
        <label for="create-token-limit" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Token Limit</label>
        <div class="col-span-3 flex items-center gap-2">
          <input
            id="create-token-limit"
            type="number"
            bind:value={formTotalTokenLimit}
            placeholder="e.g. 1000000"
            min="1"
            class="w-40 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
          />
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">Total tokens across all models. Empty = unlimited</span>
        </div>
      </div>

      <!-- Limit reset interval -->
      <div class="grid grid-cols-4 gap-3 mb-4">
        <label for="create-reset-interval" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Auto Reset</label>
        <div class="col-span-3 flex items-center gap-2">
          <select
            id="create-reset-interval"
            bind:value={formResetPreset}
            onchange={() => { if (formResetPreset !== 'custom') formLimitResetInterval = formResetPreset; else formLimitResetInterval = ''; }}
            class="border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
          >
            <option value="">None</option>
            <option value="1h">Every hour</option>
            <option value="12h">Every 12 hours</option>
            <option value="24h">Daily (24h)</option>
            <option value="7d">Weekly (7d)</option>
            <option value="30d">Monthly (30d)</option>
            <option value="custom">Custom</option>
          </select>
          {#if formResetPreset === 'custom'}
            <input
              type="text"
              bind:value={formLimitResetInterval}
              placeholder="e.g. 2w3d, 48h, 90d"
              class="w-36 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
            />
          {/if}
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">Periodically reset usage counters</span>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          onclick={handleCreate}
          disabled={creating}
          class="px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
        >
          {creating ? 'Creating...' : 'Create Token'}
        </button>
        <button
          onclick={() => { showCreate = false; resetForm(); }}
          class="px-3 py-1.5 text-xs text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
        >
          Cancel
        </button>
      </div>
    </div>
  {/if}

  <!-- Edit form (standalone panel above table) -->
  {#if editingTokenId}
    {@const editingToken = tokens.find(t => t.id === editingTokenId)}
    {#if editingToken}
      <div class="mb-4 border border-red-200 dark:border-red-800 bg-red-50/30 dark:bg-red-900/10 p-4">
        <div class="space-y-3">
          <div class="flex items-center gap-2 mb-1">
            <Pencil size={12} class="text-gray-400 dark:text-dark-text-muted" />
            <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Editing token</span>
            <code class="text-xs font-mono text-gray-400 dark:text-dark-text-muted bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5">{editingToken.token_prefix}...</code>
          </div>

          <div class="grid grid-cols-4 gap-3">
            <label for="edit-token-name" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Name</label>
            <input
              id="edit-token-name"
              type="text"
              bind:value={editName}
              class="col-span-3 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
            />
          </div>

          <div class="grid grid-cols-4 gap-3">
            <label for="edit-token-expires" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Expires At</label>
            <div class="col-span-3 flex items-center gap-2">
              <input
                id="edit-token-expires"
                type="datetime-local"
                bind:value={editExpiresAt}
                class="border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
              />
              {#if editExpiresAt}
                <button
                  onclick={() => (editExpiresAt = '')}
                  class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary"
                >
                  Clear (no expiry)
                </button>
              {:else}
                <span class="text-xs text-gray-400 dark:text-dark-text-muted">No expiry</span>
              {/if}
            </div>
          </div>

          <div class="grid grid-cols-4 gap-3">
            <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Providers</span>
            <div class="col-span-3">
              {#if allProviderKeys.length > 0}
                <div class="flex flex-wrap gap-1.5">
                  {#each allProviderKeys as key}
                    <button
                      onclick={() => toggleEditProvider(key)}
                      class={[
                        'px-2 py-1 text-xs border transition-colors',
                        editSelectedProviders.includes(key)
                          ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                          : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                      ]}
                    >
                      {key}
                    </button>
                  {/each}
                </div>
                <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all providers allowed</p>
              {:else}
                <span class="text-xs text-gray-400 dark:text-dark-text-muted">No providers available</span>
              {/if}
            </div>
          </div>

          <div class="grid grid-cols-4 gap-3">
            <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Models</span>
            <div class="col-span-3">
              {#if allModels.length > 0}
                <div class="max-h-32 overflow-y-auto border border-gray-200 dark:border-dark-border p-2">
                  <div class="flex flex-wrap gap-1.5">
                    {#each allModels as model}
                      <button
                        onclick={() => toggleEditModel(model)}
                        class={[
                          'px-2 py-0.5 text-xs border font-mono transition-colors',
                          editSelectedModels.includes(model)
                            ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                            : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                        ]}
                      >
                        {model}
                      </button>
                    {/each}
                  </div>
                </div>
                <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all models allowed</p>
              {:else}
                <span class="text-xs text-gray-400 dark:text-dark-text-muted">No models available</span>
              {/if}
            </div>
          </div>

          <div class="grid grid-cols-4 gap-3">
            <span class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Allowed Webhooks</span>
            <div class="col-span-3">
              {#if webhookTriggers.length > 0}
                <div class="max-h-40 overflow-y-auto border border-gray-200 dark:border-dark-border p-2 space-y-2">
                  {#each Object.entries(webhooksByWorkflow) as [wfName, items]}
                    <div>
                      <div class="text-xs text-gray-400 dark:text-dark-text-muted mb-1">{wfName}</div>
                      <div class="flex flex-wrap gap-1.5">
                        {#each items as { trigger }}
                          <button
                            onclick={() => toggleEditWebhook(trigger.id)}
                            class={[
                              'px-2 py-0.5 text-xs border font-mono transition-colors',
                              editSelectedWebhooks.includes(trigger.id)
                                ? 'bg-gray-900 text-white border-gray-900 dark:bg-accent dark:text-white dark:border-accent'
                                : 'bg-white text-gray-600 border-gray-200 hover:border-gray-300 dark:bg-dark-elevated dark:text-dark-text-secondary dark:border-dark-border dark:hover:border-dark-border-subtle'
                            ]}
                          >
                            {trigger.alias || trigger.id}
                          </button>
                        {/each}
                      </div>
                    </div>
                  {/each}
                </div>
                <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">None selected = all webhooks allowed</p>
              {:else}
                <span class="text-xs text-gray-400 dark:text-dark-text-muted">No webhooks available</span>
              {/if}
            </div>
          </div>

          <!-- Token limit -->
          <div class="grid grid-cols-4 gap-3">
            <label for="edit-token-limit" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Token Limit</label>
            <div class="col-span-3 flex items-center gap-2">
              <input
                id="edit-token-limit"
                type="number"
                bind:value={editTotalTokenLimit}
                placeholder="e.g. 1000000"
                min="1"
                class="w-40 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
              />
              <span class="text-xs text-gray-400 dark:text-dark-text-muted">Empty = unlimited</span>
            </div>
          </div>

          <!-- Limit reset interval -->
          <div class="grid grid-cols-4 gap-3">
            <label for="edit-reset-interval" class="text-xs text-gray-600 dark:text-dark-text-secondary py-2">Auto Reset</label>
            <div class="col-span-3 flex items-center gap-2">
              <select
                id="edit-reset-interval"
                bind:value={editResetPreset}
                onchange={() => { if (editResetPreset !== 'custom') editLimitResetInterval = editResetPreset; else editLimitResetInterval = ''; }}
                class="border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
              >
                <option value="">None</option>
                <option value="1h">Every hour</option>
                <option value="12h">Every 12 hours</option>
                <option value="24h">Daily (24h)</option>
                <option value="7d">Weekly (7d)</option>
                <option value="30d">Monthly (30d)</option>
                <option value="custom">Custom</option>
              </select>
              {#if editResetPreset === 'custom'}
                <input
                  type="text"
                  bind:value={editLimitResetInterval}
                  placeholder="e.g. 2w3d, 48h, 90d"
                  class="w-36 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400 dark:focus:border-dark-border-subtle"
                />
              {/if}
              <span class="text-xs text-gray-400 dark:text-dark-text-muted">Periodically reset usage counters</span>
            </div>
          </div>

          <div class="flex items-center gap-2 pt-1">
            <button
              onclick={handleSaveEdit}
              disabled={saving}
              class="px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              {saving ? 'Saving...' : 'Save'}
            </button>
            <button
              onclick={cancelEditing}
              class="px-3 py-1.5 text-xs text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      </div>
    {/if}
  {/if}

  <!-- Token list -->
  <DataTable
    items={tokens}
    {loading}
    {total}
    {limit}
    bind:offset
    onchange={loadTokens}
    onsearch={handleSearch}
    searchPlaceholder="Search by name..."
    emptyIcon={Key}
    emptyTitle="No API tokens"
    emptyDescription="Create a token to authenticate API requests"
  >
    {#snippet header()}
      <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Token</th>
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Scope</th>
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Usage</th>
      <SortableHeader field="expires_at" label="Expires" {sorts} onsort={handleSort} />
      <SortableHeader field="created_by" label="Created By" {sorts} onsort={handleSort} />
      <SortableHeader field="last_used_at" label="Last Used" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-16"></th>
    {/snippet}

    {#snippet row(token)}
        <tr class={editingTokenId === token.id ? 'bg-red-50/30 dark:bg-red-900/10' : 'hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors'}>
          <td class="px-4 py-2.5 font-medium text-gray-900 dark:text-dark-text text-sm">{token.name}</td>
          <td class="px-4 py-2.5">
            <code class="text-xs font-mono text-gray-500 dark:text-dark-text-muted bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5">{token.token_prefix}...</code>
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
            {#if !token.allowed_providers && !token.allowed_models && !token.allowed_webhooks}
              <span class="text-gray-400 dark:text-dark-text-muted">All access</span>
            {:else}
              <div class="space-y-0.5">
                {#if token.allowed_providers && token.allowed_providers.length > 0}
                  <div>
                    <span class="text-gray-400 dark:text-dark-text-muted">Providers:</span>
                    {token.allowed_providers.join(', ')}
                  </div>
                {/if}
                {#if token.allowed_models && token.allowed_models.length > 0}
                  <div>
                    <span class="text-gray-400 dark:text-dark-text-muted">Models:</span>
                    {token.allowed_models.slice(0, 3).join(', ')}{token.allowed_models.length > 3 ? ` +${token.allowed_models.length - 3}` : ''}
                  </div>
                {/if}
                {#if token.allowed_webhooks && token.allowed_webhooks.length > 0}
                  <div>
                    <span class="text-gray-400 dark:text-dark-text-muted">Webhooks:</span>
                    {token.allowed_webhooks.slice(0, 3).join(', ')}{token.allowed_webhooks.length > 3 ? ` +${token.allowed_webhooks.length - 3}` : ''}
                  </div>
                {/if}
              </div>
            {/if}
          </td>
          <td class="px-4 py-2.5 text-xs">
            <button
              onclick={() => toggleUsage(token.id)}
              class="flex items-center gap-1 text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
              title="View usage"
            >
              <BarChart3 size={12} />
              {#if tokenUsageMap[token.id]}
                {@const usage = getTotalUsage(token.id)}
                <span>{formatNumber(usage.totalTokens)} tokens</span>
                {#if token.total_token_limit}
                  <span class="text-gray-400 dark:text-dark-text-muted">/ {formatNumber(token.total_token_limit)}</span>
                {/if}
              {:else}
                <span class="text-gray-400 dark:text-dark-text-muted">View</span>
              {/if}
            </button>
          </td>
          <td class="px-4 py-2.5 text-xs">
            {#if token.expires_at}
              <span class={isExpired(token.expires_at) ? 'text-red-500' : 'text-gray-500 dark:text-dark-text-muted'}>
                {isExpired(token.expires_at) ? 'Expired' : formatDateTime(token.expires_at)}
              </span>
            {:else}
              <span class="text-gray-400 dark:text-dark-text-muted">Never</span>
            {/if}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-[150px] truncate" title={token.created_by}>
            {token.created_by || '-'}
          </td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
            {formatDateTime(token.last_used_at)}
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
                  class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                >
                  Cancel
                </button>
              </div>
            {:else}
              <div class="flex items-center gap-1 justify-end">
                <button
                  onclick={() => openConfigView(token)}
                  class="p-1 text-gray-300 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
                  title="View Config"
                >
                  <FileCode size={14} />
                </button>
                <button
                  onclick={() => startEditing(token)}
                  class="p-1 text-gray-300 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
                  title="Edit"
                >
                  <Pencil size={14} />
                </button>
                <button
                  onclick={() => (deleteConfirmId = token.id)}
                  class="p-1 text-gray-300 dark:text-dark-text-faint hover:text-red-500 dark:hover:text-red-400 transition-colors"
                  title="Delete"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            {/if}
          </td>
        </tr>
        <!-- Expanded usage row -->
        {#if expandedUsageTokenId === token.id}
          <tr class="bg-gray-50/50 dark:bg-dark-elevated/30">
            <td colspan="8" class="px-4 py-3">
              <div class="flex items-center justify-between mb-2">
                <div class="flex items-center gap-2">
                  <BarChart3 size={12} class="text-gray-400 dark:text-dark-text-muted" />
                  <span class="text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Usage Breakdown</span>
                  {#if token.total_token_limit}
                    {@const usage = getTotalUsage(token.id)}
                    {@const pct = Math.min(100, Math.round((usage.totalTokens / token.total_token_limit) * 100))}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted">
                      {formatNumber(usage.totalTokens)} / {formatNumber(token.total_token_limit)} ({pct}%)
                    </span>
                    <div class="w-24 h-1.5 bg-gray-200 dark:bg-dark-border rounded-full overflow-hidden">
                      <div
                        class="h-full rounded-full transition-all {pct >= 90 ? 'bg-red-500' : pct >= 70 ? 'bg-yellow-500' : 'bg-green-500'}"
                        style="width: {pct}%"
                      ></div>
                    </div>
                  {/if}
                  {#if token.limit_reset_interval}
                    <span class="text-xs text-gray-400 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border px-1.5 py-0.5 rounded">
                      resets {token.limit_reset_interval}
                    </span>
                  {/if}
                </div>
                <button
                  onclick={() => handleResetUsage(token.id)}
                  disabled={resettingUsage[token.id]}
                  class="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 border border-gray-200 dark:border-dark-border hover:border-red-300 dark:hover:border-red-800 transition-colors disabled:opacity-50"
                  title="Reset all usage counters"
                >
                  <RotateCcw size={10} />
                  {resettingUsage[token.id] ? 'Resetting...' : 'Reset'}
                </button>
              </div>
              {#if loadingUsage[token.id]}
                <div class="text-xs text-gray-400 dark:text-dark-text-muted py-2">Loading...</div>
              {:else if !tokenUsageMap[token.id] || tokenUsageMap[token.id].length === 0}
                <div class="text-xs text-gray-400 dark:text-dark-text-muted py-2">No usage recorded yet</div>
              {:else}
                <div class="border border-gray-200 dark:border-dark-border overflow-hidden">
                  <table class="w-full">
                    <thead>
                      <tr class="bg-gray-100/50 dark:bg-dark-base/50">
                        <th class="text-left px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Model</th>
                        <th class="text-right px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Prompt</th>
                        <th class="text-right px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Completion</th>
                        <th class="text-right px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Total</th>
                        <th class="text-right px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Requests</th>
                        <th class="text-right px-3 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Last Used</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each tokenUsageMap[token.id] as usage}
                        <tr class="border-t border-gray-100 dark:border-dark-border">
                          <td class="px-3 py-1.5 text-xs font-mono text-gray-700 dark:text-dark-text-secondary">{usage.model}</td>
                          <td class="px-3 py-1.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatNumber(usage.prompt_tokens)}</td>
                          <td class="px-3 py-1.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatNumber(usage.completion_tokens)}</td>
                          <td class="px-3 py-1.5 text-xs text-gray-700 dark:text-dark-text-secondary text-right font-medium">{formatNumber(usage.total_tokens)}</td>
                          <td class="px-3 py-1.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{usage.request_count}</td>
                          <td class="px-3 py-1.5 text-xs text-gray-400 dark:text-dark-text-muted text-right">{formatDateTime(usage.last_request_at)}</td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
            </td>
          </tr>
        {/if}
    {/snippet}
  </DataTable>

  <!-- Config Viewer Modal -->
  {#if configViewToken}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="fixed inset-0 bg-black/40 z-50 flex items-center justify-center p-4"
      onkeydown={(e) => { if (e.key === 'Escape') closeConfigView(); }}
      onclick={(e) => { if (e.target === e.currentTarget) closeConfigView(); }}
    >
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div class="bg-white dark:bg-dark-surface shadow-xl dark:border dark:border-dark-border w-full max-w-xl overflow-hidden" onclick={(e) => e.stopPropagation()}>
        <!-- Header -->
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
            Config: <span class="font-mono">{configViewToken.name}</span>
          </span>
          <button onclick={closeConfigView} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
            <X size={14} />
          </button>
        </div>

        <!-- Format Toggle + Copy -->
        <div class="flex items-center justify-between px-4 py-2 border-b border-gray-100 dark:border-dark-border">
          <div class="flex gap-1">
            <button
              onclick={() => { configFormat = 'yaml'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'yaml' ? 'bg-gray-900 text-white dark:bg-accent' : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-elevated dark:text-dark-text-secondary'}"
            >
              YAML
            </button>
            <button
              onclick={() => { configFormat = 'json'; configCopied = false; }}
              class="px-2.5 py-1 text-xs font-medium transition-colors {configFormat === 'json' ? 'bg-gray-900 text-white dark:bg-accent' : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-elevated dark:text-dark-text-secondary'}"
            >
              JSON
            </button>
          </div>
          <button
            onclick={copyConfigSnippet}
            class="flex items-center gap-1.5 px-2.5 py-1 text-xs border border-gray-200 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text transition-colors"
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
        <div class="p-4 bg-gray-50 dark:bg-dark-base max-h-96 overflow-auto">
          <pre class="text-xs font-mono text-gray-800 dark:text-dark-text whitespace-pre leading-relaxed">{getConfigSnippet()}</pre>
        </div>

        <!-- Hint -->
        <div class="px-4 py-2.5 border-t border-gray-100 dark:border-dark-border bg-white dark:bg-dark-surface">
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">
            Add this to your <span class="font-mono font-medium">at.yaml</span> configuration file under the <span class="font-mono font-medium">gateway.auth_tokens</span> section.
          </p>
        </div>
      </div>
    </div>
  {/if}
</div>
