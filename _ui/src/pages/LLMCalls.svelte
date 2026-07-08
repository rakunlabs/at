<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listLLMCalls, getLLMCall, type LLMCall } from '@/lib/api/llm-calls';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Activity, RefreshCw, X, Copy } from 'lucide-svelte';

  storeNavbar.title = 'LLM Traces';

  // ─── State ───

  let calls = $state<LLMCall[]>([]);
  let loading = $state(true);

  let offset = $state(0);
  let limit = $state(20);
  let total = $state(0);

  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // Filters
  let providerFilter = $state('');
  let modelFilter = $state('');
  let statusFilter = $state('');
  let sourceFilter = $state('');

  // Detail drawer
  let selected = $state<LLMCall | null>(null);
  let detailLoading = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['trace_id[like]'] = `%${searchQuery}%`;
      if (providerFilter) params['provider'] = providerFilter;
      if (modelFilter) params['model[like]'] = `%${modelFilter}%`;
      if (statusFilter) params['status'] = statusFilter;
      if (sourceFilter) params['source'] = sourceFilter;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listLLMCalls(params as any);
      calls = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load LLM traces', 'alert');
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

  function applyFilters() {
    offset = 0;
    load();
  }

  function clearFilters() {
    providerFilter = '';
    modelFilter = '';
    statusFilter = '';
    sourceFilter = '';
    offset = 0;
    load();
  }

  async function openDetail(call: LLMCall) {
    selected = call;
    detailLoading = true;
    try {
      // Fetch the full record (list responses clip bodies to a preview).
      selected = await getLLMCall(call.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load trace detail', 'alert');
    } finally {
      detailLoading = false;
    }
  }

  function closeDetail() {
    selected = null;
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Copied to clipboard', 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }

  load();

  // ─── Formatting ───

  function formatCost(cents: number): string {
    if (!cents) return '$0';
    return `$${(cents / 100).toFixed(4)}`;
  }

  function formatTokens(n: number): string {
    if (!n) return '0';
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  }

  function formatLatency(ms: number): string {
    if (!ms) return '-';
    if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
    return `${ms}ms`;
  }

  function prettyJSON(raw: string): string {
    if (!raw) return '';
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }
</script>

<svelte:head>
  <title>AT | LLM Traces</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Activity size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">LLM Traces</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <button onclick={() => load()}
      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Refresh">
      <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
    </button>
  </div>

  <!-- Filters -->
  <div class="mb-3 flex flex-wrap items-center gap-2">
    <input
      bind:value={providerFilter}
      onkeydown={(e) => e.key === 'Enter' && applyFilters()}
      placeholder="Provider"
      class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text rounded w-28"
    />
    <input
      bind:value={modelFilter}
      onkeydown={(e) => e.key === 'Enter' && applyFilters()}
      placeholder="Model"
      class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text rounded w-36"
    />
    <select
      bind:value={statusFilter}
      onchange={applyFilters}
      class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text rounded"
    >
      <option value="">All status</option>
      <option value="ok">ok</option>
      <option value="error">error</option>
    </select>
    <select
      bind:value={sourceFilter}
      onchange={applyFilters}
      class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text rounded"
    >
      <option value="">All sources</option>
      <option value="gateway">gateway</option>
      <option value="gateway_stream">gateway_stream</option>
      <option value="responses">responses</option>
      <option value="chat">chat</option>
    </select>
    <button
      onclick={applyFilters}
      class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary rounded"
    >Apply</button>
    {#if providerFilter || modelFilter || statusFilter || sourceFilter}
      <button
        onclick={clearFilters}
        class="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary"
      ><X size={12} /> Clear</button>
    {/if}
  </div>

  <!-- Call List -->
  <DataTable
    items={calls}
    {loading}
    {total}
    {limit}
    bind:offset
    onchange={load}
    onsearch={handleSearch}
    searchPlaceholder="Search by trace ID..."
    emptyIcon={Activity}
    emptyTitle="No LLM traces"
    emptyDescription="Enable the LLM Call Audit feature and send a request through the gateway to see traces here."
  >
    {#snippet header()}
      <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Provider / Model</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Source</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
      <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">In</th>
      <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Out</th>
      <SortableHeader field="latency_ms" label="Latency" {sorts} onsort={handleSort} />
      <SortableHeader field="cost_cents" label="Cost" {sorts} onsort={handleSort} />
    {/snippet}

    {#snippet row(call)}
      <tr
        class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer"
        onclick={() => openDetail(call)}
      >
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted whitespace-nowrap">{formatDate(call.created_at)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text-secondary">
          <span class="text-gray-400">{call.provider}/</span>{call.model}
          {#if call.streamed}<span class="ml-1 text-[10px] px-1 rounded bg-gray-100 dark:bg-dark-elevated text-gray-500">stream</span>{/if}
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-mono">{call.source}</td>
        <td class="px-4 py-2.5 text-xs">
          {#if call.status === 'error'}
            <span class="px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300" title={call.error_message}>{call.error_code || 'error'}</span>
          {:else}
            <span class="px-1.5 py-0.5 rounded bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300">ok</span>
          {/if}
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(call.input_tokens)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(call.output_tokens)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatLatency(call.latency_ms)}</td>
        <td class="px-4 py-2.5 text-xs font-medium text-gray-900 dark:text-dark-text text-right">{formatCost(call.cost_cents)}</td>
      </tr>
    {/snippet}
  </DataTable>
</div>

<!-- Detail drawer -->
{#if selected}
  <div class="fixed inset-0 z-40 flex justify-end">
    <button class="absolute inset-0 bg-black/30" onclick={closeDetail} aria-label="Close"></button>
    <div class="relative z-50 w-full max-w-2xl h-full overflow-y-auto bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shadow-xl">
      <div class="sticky top-0 flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-2 min-w-0">
          <Activity size={14} class="text-gray-500 shrink-0" />
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">{selected.provider}/{selected.model}</span>
          {#if selected.status === 'error'}
            <span class="text-[10px] px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300">{selected.error_code || 'error'}</span>
          {/if}
        </div>
        <button onclick={closeDetail} class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Close">
          <X size={16} />
        </button>
      </div>

      <div class="p-4 space-y-4">
        <!-- Meta grid -->
        <div class="grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs">
          <div class="text-gray-400 dark:text-dark-text-muted">Time</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{formatDate(selected.created_at)}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Trace ID</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate" title={selected.trace_id}>{selected.trace_id || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Session ID</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate" title={selected.session_id}>{selected.session_id || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Source / Endpoint</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate">{selected.source} · {selected.endpoint || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Requested model</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate">{selected.requested_model || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Tokens (in / out)</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{formatTokens(selected.input_tokens)} / {formatTokens(selected.output_tokens)}
            {#if selected.reasoning_tokens} · {formatTokens(selected.reasoning_tokens)} reasoning{/if}
          </div>
          <div class="text-gray-400 dark:text-dark-text-muted">Cache (read / write)</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{formatTokens(selected.cache_read_tokens)} / {formatTokens(selected.cache_write_tokens)}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Latency</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{formatLatency(selected.latency_ms)}
            {#if selected.time_to_first_token_ms} · TTFT {formatLatency(selected.time_to_first_token_ms)}{/if}
          </div>
          <div class="text-gray-400 dark:text-dark-text-muted">Cost</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{formatCost(selected.cost_cents)}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Finish reason</div>
          <div class="text-gray-700 dark:text-dark-text-secondary">{selected.finish_reason || '-'}</div>
          {#if selected.status === 'error'}
            <div class="text-gray-400 dark:text-dark-text-muted">Error</div>
            <div class="text-red-600 dark:text-red-400 break-words">{selected.error_message || selected.error_code}</div>
          {/if}
        </div>

        {#if detailLoading}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted">Loading full bodies…</div>
        {/if}

        <!-- Request body -->
        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">
              Request{#if selected.request_truncated} <span class="normal-case text-amber-600 dark:text-amber-400">(truncated · full in spill file)</span>{/if}
            </span>
            <button onclick={() => copyText(selected!.request_body)} class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Copy">
              <Copy size={12} /> Copy
            </button>
          </div>
          <pre class="text-[11px] leading-relaxed p-3 rounded bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border overflow-x-auto max-h-80 text-gray-800 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{prettyJSON(selected.request_body)}</pre>
        </div>

        <!-- Response body -->
        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">
              Response{#if selected.response_truncated} <span class="normal-case text-amber-600 dark:text-amber-400">(truncated · full in spill file)</span>{/if}
            </span>
            <button onclick={() => copyText(selected!.response_body)} class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Copy">
              <Copy size={12} /> Copy
            </button>
          </div>
          <pre class="text-[11px] leading-relaxed p-3 rounded bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border overflow-x-auto max-h-80 text-gray-800 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{prettyJSON(selected.response_body)}</pre>
        </div>
      </div>
    </div>
  </div>
{/if}
