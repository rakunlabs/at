<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    applyModelPricingSync,
    deleteModelPricing,
    exportModelPricingCatalog,
    importModelPricingCatalog,
    listModelPricing,
    previewModelPricingAgent,
    previewModelPricingSync,
    resetModelPricing,
    setModelPricing,
    type ModelPricing,
    type ModelPricingCatalog,
    type ModelPricingSyncPreviewItem,
  } from '@/lib/api/agent-budgets';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { Bot, Check, CircleDollarSign, Download, RefreshCw, RotateCcw, Trash2, Upload, X } from 'lucide-svelte';

  storeNavbar.title = 'Model Pricing';

  let pricing = $state<ModelPricing[]>([]);
  let providers = $state<ProviderRecord[]>([]);
  let preview = $state<ModelPricingSyncPreviewItem[]>([]);
  let selectedPreview = $state<string[]>([]);
  let previewSource = $state('pi.dev');
  let loading = $state(true);
  let previewLoading = $state(false);
  let applying = $state(false);
  let saving = $state(false);
  let exportingCatalog = $state(false);
  let importingCatalog = $state(false);
  let search = $state('');
  let statusFilter = $state<'all' | 'missing' | 'update' | 'override' | 'no_match'>('all');
  let overwriteOverrides = $state(false);
  let editingID = $state<string | null>(null);
  let deleteConfirmID = $state<string | null>(null);
  let catalogImportFileInput: HTMLInputElement;

  let form = $state({
    provider_key: '',
    model: '',
    prompt_price_per_1m: '',
    completion_price_per_1m: '',
    cache_read_price_per_1m: '',
    cache_write_price_per_1m: '',
  });

  let agent = $state({
    provider_key: '',
    model: '',
    instruction: 'Find current model pricing for the configured AT provider models. If I give you a URL or pasted source text, extract prices from that. Otherwise search the web if the selected provider supports it.',
    source_url: '',
    source_text: '',
    web_search: true,
  });

  let filteredPricing = $derived(
    pricing.filter((p) => {
      const q = search.trim().toLowerCase();
      if (!q) return true;
      return `${p.provider_key}/${p.model}`.toLowerCase().includes(q) || (p.source_model || '').toLowerCase().includes(q);
    })
  );

  let filteredPreview = $derived(
    preview.filter((p) => statusFilter === 'all' || p.status === statusFilter)
  );

  async function loadPricing() {
    loading = true;
    try {
      pricing = await listModelPricing();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load pricing', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadProviders() {
    try {
      const res = await listProviders();
      providers = res.data || [];
      if (!agent.provider_key && providers.length > 0) {
        agent.provider_key = providers[0].key;
        agent.model = providers[0].config.model || '';
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load providers', 'alert');
    }
  }

  function updateAgentProviderModel() {
    const provider = providers.find((item) => item.key === agent.provider_key);
    agent.model = provider?.config.model || '';
  }

  function resetForm() {
    editingID = null;
    form = {
      provider_key: '',
      model: '',
      prompt_price_per_1m: '',
      completion_price_per_1m: '',
      cache_read_price_per_1m: '',
      cache_write_price_per_1m: '',
    };
  }

  function startEdit(item: ModelPricing) {
    editingID = item.id;
    form = {
      provider_key: item.provider_key,
      model: item.model,
      prompt_price_per_1m: String(item.prompt_price_per_1m ?? 0),
      completion_price_per_1m: String(item.completion_price_per_1m ?? 0),
      cache_read_price_per_1m: String(item.cache_read_price_per_1m ?? 0),
      cache_write_price_per_1m: String(item.cache_write_price_per_1m ?? 0),
    };
  }

  function parsePrice(value: string): number {
    const n = parseFloat(value);
    return Number.isFinite(n) && n > 0 ? n : 0;
  }

  async function savePricing() {
    if (!form.provider_key.trim() || !form.model.trim()) {
      addToast('Provider key and model are required', 'alert');
      return;
    }
    saving = true;
    try {
      await setModelPricing({
        provider_key: form.provider_key.trim(),
        model: form.model.trim(),
        prompt_price_per_1m: parsePrice(form.prompt_price_per_1m),
        completion_price_per_1m: parsePrice(form.completion_price_per_1m),
        cache_read_price_per_1m: parsePrice(form.cache_read_price_per_1m),
        cache_write_price_per_1m: parsePrice(form.cache_write_price_per_1m),
      });
      addToast(editingID ? 'Pricing updated' : 'Pricing added', 'info');
      resetForm();
      await loadPricing();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save pricing', 'alert');
    } finally {
      saving = false;
    }
  }

  async function removePricing(id: string) {
    try {
      await deleteModelPricing(id);
      deleteConfirmID = null;
      addToast('Pricing deleted', 'info');
      await loadPricing();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete pricing', 'alert');
    }
  }

  async function resetOverride(id: string) {
    try {
      await resetModelPricing(id);
      addToast('Pricing reset to source', 'info');
      await loadPricing();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to reset pricing', 'alert');
    }
  }

  async function runPreview() {
    previewLoading = true;
    try {
      const res = await previewModelPricingSync('pi.dev');
      previewSource = res.source || 'pi.dev';
      preview = res.items || [];
      selectedPreview = preview
        .filter((item) => item.matched && ['missing', 'update'].includes(item.status))
        .map(previewKey);
      addToast(`Loaded ${preview.length} pricing candidates`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to preview pricing sync', 'alert');
    } finally {
      previewLoading = false;
    }
  }

  async function runAgentPreview() {
    if (!agent.provider_key) {
      addToast('Select a provider for the pricing agent', 'alert');
      return;
    }
    previewLoading = true;
    try {
      const res = await previewModelPricingAgent({
        provider_key: agent.provider_key,
        model: agent.model.trim() || undefined,
        instruction: agent.instruction.trim(),
        source_url: agent.source_url.trim() || undefined,
        source_text: agent.source_text.trim() || undefined,
        web_search: agent.web_search,
      });
      previewSource = res.source || 'agent';
      preview = res.items || [];
      selectedPreview = preview
        .filter((item) => item.matched && ['missing', 'update'].includes(item.status))
        .map(previewKey);
      addToast(`Pricing agent found ${preview.filter((item) => item.matched).length} matches`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to run pricing agent', 'alert');
    } finally {
      previewLoading = false;
    }
  }

  async function applySelected() {
    const items = preview
      .filter((item) => selectedPreview.includes(previewKey(item)))
      .map((item) => ({ provider_key: item.provider_key, model: item.model }));
    if (items.length === 0) {
      addToast('Select at least one matched row', 'alert');
      return;
    }
    applying = true;
    try {
      const res = await applyModelPricingSync(items, overwriteOverrides, previewSource, previewSource === 'pi.dev' ? undefined : preview);
      addToast(`Applied ${res.applied}, skipped ${res.skipped}`, res.errors?.length ? 'warn' : 'info');
      await loadPricing();
      if (previewSource === 'pi.dev') {
        await runPreview();
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to apply pricing sync', 'alert');
    } finally {
      applying = false;
    }
  }

  async function downloadCatalog() {
    exportingCatalog = true;
    try {
      const data = await exportModelPricingCatalog();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'at-model-pricing-catalog.json';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      addToast('Pricing catalog downloaded', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to download pricing catalog', 'alert');
    } finally {
      exportingCatalog = false;
    }
  }

  async function handleImportCatalogFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    importingCatalog = true;
    try {
      const parsed = JSON.parse(await file.text());
      const catalog: Pick<ModelPricingCatalog, 'items'> = Array.isArray(parsed) ? { items: parsed } : parsed;
      const res = await importModelPricingCatalog(catalog, overwriteOverrides);
      addToast(`Imported ${res.applied}, skipped ${res.skipped}`, res.errors?.length ? 'warn' : 'info');
      await loadPricing();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import pricing catalog', 'alert');
    } finally {
      importingCatalog = false;
      input.value = '';
    }
  }

  function previewKey(item: ModelPricingSyncPreviewItem): string {
    return `${item.provider_key}\x00${item.model}`;
  }

  function togglePreview(item: ModelPricingSyncPreviewItem) {
    if (!item.matched) return;
    const key = previewKey(item);
    if (selectedPreview.includes(key)) {
      selectedPreview = selectedPreview.filter((v) => v !== key);
    } else {
      selectedPreview = [...selectedPreview, key];
    }
  }

  function price(value: number | undefined): string {
    const n = value || 0;
    if (n === 0) return '$0';
    return `$${n.toFixed(n < 1 ? 4 : 3).replace(/0+$/, '').replace(/\.$/, '')}`;
  }

  function statusLabel(status: string): string {
    switch (status) {
      case 'missing': return 'Missing';
      case 'update': return 'Update';
      case 'override': return 'Override';
      case 'current': return 'Current';
      case 'no_match': return 'No match';
      default: return status;
    }
  }

  function statusClass(status: string): string {
    switch (status) {
      case 'missing': return 'text-blue-700 bg-blue-50 border-blue-200 dark:text-blue-300 dark:bg-blue-900/20 dark:border-blue-900/50';
      case 'update': return 'text-amber-700 bg-amber-50 border-amber-200 dark:text-amber-300 dark:bg-amber-900/20 dark:border-amber-900/50';
      case 'override': return 'text-purple-700 bg-purple-50 border-purple-200 dark:text-purple-300 dark:bg-purple-900/20 dark:border-purple-900/50';
      case 'current': return 'text-green-700 bg-green-50 border-green-200 dark:text-green-300 dark:bg-green-900/20 dark:border-green-900/50';
      default: return 'text-gray-500 bg-gray-50 border-gray-200 dark:text-dark-text-muted dark:bg-dark-elevated dark:border-dark-border';
    }
  }

  loadPricing();
  loadProviders();
</script>

<svelte:head>
  <title>AT | Model Pricing</title>
</svelte:head>

<div class="p-6 max-w-7xl mx-auto space-y-5">
  <div class="flex items-center justify-between gap-3">
    <div class="flex items-center gap-2">
      <CircleDollarSign size={18} class="text-gray-500 dark:text-dark-text-muted" />
      <div>
        <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Model Pricing</h2>
        <p class="text-xs text-gray-400 dark:text-dark-text-muted">Effective prices used by gateway cost tracking and token spend budgets.</p>
      </div>
    </div>
    <div class="flex flex-wrap items-center justify-end gap-2">
      <button
        onclick={downloadCatalog}
        disabled={exportingCatalog}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors disabled:opacity-50"
      >
        <Download size={12} />
        {exportingCatalog ? 'Downloading...' : 'Download Catalog'}
      </button>
      <button
        onclick={() => catalogImportFileInput.click()}
        disabled={importingCatalog}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors disabled:opacity-50"
      >
        <Upload size={12} />
        {importingCatalog ? 'Importing...' : 'Upload Catalog'}
      </button>
      <input bind:this={catalogImportFileInput} type="file" accept=".json,application/json" onchange={handleImportCatalogFile} class="hidden" />
      <button
        onclick={runPreview}
        disabled={previewLoading}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
      >
        <RefreshCw size={12} class={previewLoading ? 'animate-spin' : ''} />
        {previewLoading ? 'Loading...' : 'Fetch pi.dev'}
      </button>
    </div>
  </div>

  <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4">
    <div class="flex flex-col lg:flex-row lg:items-start lg:justify-between gap-4">
      <div class="flex items-start gap-2 max-w-2xl">
        <Bot size={18} class="text-gray-500 dark:text-dark-text-muted mt-0.5" />
        <div>
          <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">AI Pricing Agent</h3>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Tell a configured provider where to look, paste source text, or allow web search if that model supports it. The result is a preview before anything is applied.</p>
        </div>
      </div>
      <button onclick={runAgentPreview} disabled={previewLoading || !agent.provider_key} class="flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
        <Bot size={12} />
        {previewLoading ? 'Previewing...' : 'Run Agent Preview'}
      </button>
    </div>
    <div class="mt-4 grid grid-cols-1 lg:grid-cols-4 gap-3">
      <div>
        <label for="pricing-agent-provider" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Agent Provider</label>
        <select id="pricing-agent-provider" bind:value={agent.provider_key} onchange={updateAgentProviderModel} class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400">
          <option value="">Select provider</option>
          {#each providers as provider}
            <option value={provider.key}>{provider.key} · {provider.config.model}</option>
          {/each}
        </select>
      </div>
      <div>
        <label for="pricing-agent-model" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Agent Model</label>
        <input id="pricing-agent-model" bind:value={agent.model} placeholder="default provider model" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
      </div>
      <div class="lg:col-span-2">
        <label for="pricing-agent-url" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Source URL</label>
        <input id="pricing-agent-url" bind:value={agent.source_url} placeholder="https://provider.com/pricing or raw GitHub URL" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
      </div>
      <div class="lg:col-span-2">
        <label for="pricing-agent-instruction" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Instruction</label>
        <textarea id="pricing-agent-instruction" bind:value={agent.instruction} rows="4" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"></textarea>
      </div>
      <div class="lg:col-span-2">
        <div class="flex items-center justify-between mb-1">
          <label for="pricing-agent-source" class="block text-xs text-gray-500 dark:text-dark-text-muted">Pasted Source Text</label>
          <label class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted">
            <input type="checkbox" bind:checked={agent.web_search} class="h-3 w-3" /> web search if supported
          </label>
        </div>
        <textarea id="pricing-agent-source" bind:value={agent.source_text} rows="4" placeholder="Optional: paste docs, markdown, JSON, CSV, or copied pricing table text" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400"></textarea>
      </div>
    </div>
  </section>

  <div class="grid grid-cols-1 xl:grid-cols-3 gap-4">
    <section class="xl:col-span-1 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4">
      <div class="flex items-center justify-between mb-3">
        <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">
          {editingID ? 'Edit Manual Override' : 'Add Manual Price'}
        </h3>
        {#if editingID}
          <button onclick={resetForm} class="text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary">
            <X size={14} />
          </button>
        {/if}
      </div>

      <div class="space-y-3">
        <div>
          <label for="pricing-provider" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Provider Key</label>
          <input id="pricing-provider" bind:value={form.provider_key} placeholder="anthropic-prod" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
        </div>
        <div>
          <label for="pricing-model" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Model</label>
          <input id="pricing-model" bind:value={form.model} placeholder="claude-sonnet-4-5" class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
        </div>
        <div class="grid grid-cols-2 gap-2">
          <div>
            <label for="pricing-input" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Input $/M</label>
            <input id="pricing-input" type="number" step="0.000001" min="0" bind:value={form.prompt_price_per_1m} class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
          </div>
          <div>
            <label for="pricing-output" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Output $/M</label>
            <input id="pricing-output" type="number" step="0.000001" min="0" bind:value={form.completion_price_per_1m} class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
          </div>
          <div>
            <label for="pricing-cache-read" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Cache Read $/M</label>
            <input id="pricing-cache-read" type="number" step="0.000001" min="0" bind:value={form.cache_read_price_per_1m} class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
          </div>
          <div>
            <label for="pricing-cache-write" class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Cache Write $/M</label>
            <input id="pricing-cache-write" type="number" step="0.000001" min="0" bind:value={form.cache_write_price_per_1m} class="w-full border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2.5 py-1.5 text-sm focus:outline-none focus:border-gray-400" />
          </div>
        </div>
        <button onclick={savePricing} disabled={saving} class="w-full px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
          {saving ? 'Saving...' : editingID ? 'Save Override' : 'Add Price'}
        </button>
      </div>
    </section>

    <section class="xl:col-span-2 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border flex items-center justify-between gap-3 bg-gray-50 dark:bg-dark-base">
        <div>
          <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">Current Pricing</h3>
          <p class="text-xs text-gray-400 dark:text-dark-text-muted">Manual rows are protected from source sync unless overwritten.</p>
        </div>
        <input bind:value={search} placeholder="Search provider/model" class="w-56 border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-2.5 py-1.5 text-xs focus:outline-none focus:border-gray-400" />
      </div>

      {#if loading}
        <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">Loading...</div>
      {:else if filteredPricing.length === 0}
        <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">No pricing rows found</div>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
                <th class="text-left px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Model</th>
                <th class="text-right px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Input</th>
                <th class="text-right px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Output</th>
                <th class="text-right px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Cache R/W</th>
                <th class="text-left px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Source</th>
                <th class="w-24"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
              {#each filteredPricing as item}
                <tr class="hover:bg-gray-50/60 dark:hover:bg-dark-elevated/50">
                  <td class="px-3 py-2">
                    <div class="font-mono text-xs text-gray-800 dark:text-dark-text">{item.provider_key}/{item.model}</div>
                    {#if item.manual_override}
                      <span class="inline-flex mt-1 text-[10px] border px-1.5 py-0.5 text-purple-700 bg-purple-50 border-purple-200 dark:text-purple-300 dark:bg-purple-900/20 dark:border-purple-900/50">manual override</span>
                    {/if}
                  </td>
                  <td class="px-3 py-2 text-right text-xs tabular-nums text-gray-600 dark:text-dark-text-secondary">{price(item.prompt_price_per_1m)}</td>
                  <td class="px-3 py-2 text-right text-xs tabular-nums text-gray-600 dark:text-dark-text-secondary">{price(item.completion_price_per_1m)}</td>
                  <td class="px-3 py-2 text-right text-xs tabular-nums text-gray-600 dark:text-dark-text-secondary">{price(item.cache_read_price_per_1m)} / {price(item.cache_write_price_per_1m)}</td>
                  <td class="px-3 py-2 text-xs text-gray-500 dark:text-dark-text-muted">
                    {#if item.source}
                      <div>{item.source} · {item.source_provider}/{item.source_model}</div>
                      {#if item.last_synced_at}<div class="text-gray-400 dark:text-dark-text-muted">{new Date(item.last_synced_at).toLocaleString()}</div>{/if}
                    {:else}
                      <span class="text-gray-400 dark:text-dark-text-muted">manual</span>
                    {/if}
                  </td>
                  <td class="px-3 py-2">
                    <div class="flex items-center justify-end gap-1">
                      {#if item.source && item.manual_override}
                        <button onclick={() => resetOverride(item.id)} title="Reset to source" class="p-1 text-gray-300 hover:text-green-600 dark:text-dark-text-faint dark:hover:text-green-400"><RotateCcw size={13} /></button>
                      {/if}
                      <button onclick={() => startEdit(item)} title="Edit" class="p-1 text-gray-300 hover:text-gray-600 dark:text-dark-text-faint dark:hover:text-dark-text-secondary"><CircleDollarSign size={13} /></button>
                      {#if deleteConfirmID === item.id}
                        <button onclick={() => removePricing(item.id)} class="px-1.5 py-0.5 text-[10px] bg-red-600 text-white">Confirm</button>
                        <button onclick={() => (deleteConfirmID = null)} class="p-1 text-gray-300 hover:text-gray-600"><X size={13} /></button>
                      {:else}
                        <button onclick={() => (deleteConfirmID = item.id)} title="Delete" class="p-1 text-gray-300 hover:text-red-600 dark:text-dark-text-faint dark:hover:text-red-400"><Trash2 size={13} /></button>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  </div>

  <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">{previewSource === 'pi.dev' ? 'pi.dev Sync Preview' : 'AI Pricing Preview'}</h3>
        <p class="text-xs text-gray-400 dark:text-dark-text-muted">Preview compares configured AT provider models with source prices. Override rows are skipped unless explicitly overwritten.</p>
      </div>
      <div class="flex items-center gap-2">
        <select bind:value={statusFilter} class="border border-gray-200 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text px-2 py-1.5 text-xs focus:outline-none">
          <option value="all">All</option>
          <option value="missing">Missing</option>
          <option value="update">Updates</option>
          <option value="override">Overrides</option>
          <option value="no_match">No match</option>
        </select>
        <label class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted">
          <input type="checkbox" bind:checked={overwriteOverrides} class="h-3 w-3" /> overwrite overrides
        </label>
        <button onclick={applySelected} disabled={applying || selectedPreview.length === 0} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
          <Check size={12} /> {applying ? 'Applying...' : `Apply Selected (${selectedPreview.length})`}
        </button>
      </div>
    </div>

    {#if preview.length === 0}
      <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">Fetch from pi.dev or run the AI pricing agent to preview model prices.</div>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
              <th class="w-10 px-3 py-2"></th>
              <th class="text-left px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">AT Model</th>
              <th class="text-left px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Source Match</th>
              <th class="text-right px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Current</th>
              <th class="text-right px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Source</th>
              <th class="text-left px-3 py-2 text-xs font-medium text-gray-500 dark:text-dark-text-muted">Status</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
            {#each filteredPreview as item}
              {@const key = previewKey(item)}
              <tr class="hover:bg-gray-50/60 dark:hover:bg-dark-elevated/50">
                <td class="px-3 py-2 text-center">
                  <input type="checkbox" checked={selectedPreview.includes(key)} disabled={!item.matched} onchange={() => togglePreview(item)} class="h-3 w-3" />
                </td>
                <td class="px-3 py-2">
                  <div class="font-mono text-xs text-gray-800 dark:text-dark-text">{item.provider_key}/{item.model}</div>
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">{item.provider_type}</div>
                </td>
                <td class="px-3 py-2 text-xs text-gray-500 dark:text-dark-text-muted">
                  {#if item.matched}
                    <div class="font-mono">{item.source_provider}/{item.source_model}</div>
                    <div class="text-gray-400 dark:text-dark-text-muted">{item.match_type} · {Math.round((item.confidence || 0) * 100)}%</div>
                  {:else}
                    <span class="text-gray-400 dark:text-dark-text-muted">No source match</span>
                  {/if}
                </td>
                <td class="px-3 py-2 text-right text-xs tabular-nums text-gray-500 dark:text-dark-text-muted">
                  {#if item.has_current}
                    {price(item.current_prompt_price_per_1m)} / {price(item.current_completion_price_per_1m)} / {price(item.current_cache_read_price_per_1m)} / {price(item.current_cache_write_price_per_1m)}
                  {:else}
                    <span class="text-gray-400 dark:text-dark-text-muted">missing</span>
                  {/if}
                </td>
                <td class="px-3 py-2 text-right text-xs tabular-nums text-gray-700 dark:text-dark-text-secondary">
                  {#if item.matched}
                    {price(item.source_prompt_price_per_1m)} / {price(item.source_completion_price_per_1m)} / {price(item.source_cache_read_price_per_1m)} / {price(item.source_cache_write_price_per_1m)}
                  {:else}
                    -
                  {/if}
                </td>
                <td class="px-3 py-2">
                  <span class={['inline-flex text-[10px] border px-1.5 py-0.5', statusClass(item.status)]}>{statusLabel(item.status)}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>
