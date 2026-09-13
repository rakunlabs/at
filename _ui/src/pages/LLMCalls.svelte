<script lang="ts">
  import { untrack } from 'svelte';
  import { querystring, push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listLLMCalls,
    listLLMCallTraces,
    listLLMCallConversations,
    getLLMCall,
    type LLMCall,
    type LLMCallTrace,
    type LLMCallConversation,
  } from '@/lib/api/llm-calls';
  import { formatDateTime } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import {
    Activity,
    RefreshCw,
    X,
    Copy,
    ArrowLeft,
    Sparkles,
    Wrench,
    Flag,
    ExternalLink,
    ListTree,
    List,
    MessagesSquare,
  } from 'lucide-svelte';

  storeNavbar.title = 'Traces';

  // ─── State ───

  type ViewMode = 'conversations' | 'traces' | 'calls';
  let view = $state<ViewMode>('conversations');
  let conversations = $state<LLMCallConversation[]>([]);
  let conversationsLoading = $state(true);
  let conversationsOffset = $state(0);
  let conversationsLimit = $state(25);
  let conversationsTotal = $state(0);
  let selectedConversation = $state<LLMCallConversation | null>(null);
  let conversationRequest = 0;
  let observationRequest = 0;
  let traceListRequest = 0;

  // Trace list
  let traces = $state<LLMCallTrace[]>([]);
  let tracesLoading = $state(true);
  let tracesOffset = $state(0);
  let tracesLimit = $state(25);
  let tracesTotal = $state(0);

  // Trace detail (observation tree)
  let selectedTrace = $state<LLMCallTrace | null>(null);
  let traceObservations = $state<LLMCall[]>([]);
  let traceObsLoading = $state(false);

  // Flat observation list
  let calls = $state<LLMCall[]>([]);
  let loading = $state(true);
  let offset = $state(0);
  let limit = $state(25);
  let total = $state(0);
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // Filters (shared between views where applicable)
  let providerFilter = $state('');
  let modelFilter = $state('');
  let statusFilter = $state('');
  let sourceFilter = $state('');
  let typeFilter = $state('');

  // Detail drawer
  let selected = $state<LLMCall | null>(null);
  let detailLoading = $state(false);

  // ?task_ids=A,B,C — when present, scope both views to that task set so
  // the page can be deep-linked from TaskDetail's cost/trace button.
  let taskFilter = $state<string[]>([]);

  // Watch URL changes and re-derive the task filter. Clearing the filter
  // pushes back to the bare /llm-calls URL.
  $effect(() => {
    const qs = new URLSearchParams($querystring || '');
    const ids = (qs.get('task_ids') || '').split(',').map((s) => s.trim()).filter(Boolean);
    if (ids.join(',') !== untrack(() => taskFilter).join(',')) {
      taskFilter = ids;
      tracesOffset = 0;
      conversationsOffset = 0;
      offset = 0;
      if (untrack(() => view) === 'conversations') loadConversations();
      else if (untrack(() => view) === 'traces') loadTraces();
      else load();
    }
  });

  function clearTaskFilter() {
    push('/llm-calls');
  }

  // ─── Load: traces ───

  async function loadConversations() {
    const request = ++conversationRequest;
    conversationsLoading = true;
    try {
      const params: any = { _offset: conversationsOffset, _limit: conversationsLimit };
      if (sourceFilter) params.source = sourceFilter;
      if (statusFilter) params.status = statusFilter;
      if (taskFilter.length) params['task_id[in]'] = taskFilter.join(',');
      const result = await listLLMCallConversations(params);
      if (request !== conversationRequest) return;
      conversations = result.data || [];
      conversationsTotal = result.meta?.total || 0;
    } catch (e: any) {
      if (request === conversationRequest) addToast(e?.response?.data?.message || 'Could not load conversations', 'alert');
    } finally { if (request === conversationRequest) conversationsLoading = false; }
  }

  function conversationFilters(params: Record<string, any>) {
    const conversation = selectedConversation;
    if (!conversation) return params;
    params.token_id = conversation.token_id;
    if (conversation.session_id) params.session_id = conversation.session_id;
    else params.trace_id = conversation.trace_id;
    if (conversation.source === 'gateway') params['source[in]'] = 'gateway,gateway_stream,responses';
    else params.source = conversation.source;
    return params;
  }

  async function openConversation(conversation: LLMCallConversation) {
    selectedConversation = conversation;
    selectedTrace = null;
    tracesOffset = 0;
    if (!conversation.session_id) await openTrace(conversation);
    else await loadTraces();
  }

  function closeConversation() {
    selectedConversation = null;
    selectedTrace = null;
    ++observationRequest;
    ++traceListRequest;
    void loadConversations();
  }

  function refreshView() {
    if (selectedTrace) return loadTraceObservations(selectedTrace.trace_id);
    if (view === 'conversations' && !selectedConversation) return loadConversations();
    if (view !== 'calls') return loadTraces();
    return load();
  }

  async function loadTraces() {
    const request = ++traceListRequest;
    tracesLoading = true;
    try {
      const params: any = { _offset: tracesOffset, _limit: tracesLimit };
      if (sourceFilter) params['source'] = sourceFilter;
      if (statusFilter) params['status'] = statusFilter;
      if (taskFilter.length > 0) params['task_id[in]'] = taskFilter.join(',');
      const res = await listLLMCallTraces(conversationFilters(params));
      if (request !== traceListRequest) return;
      traces = res.data || [];
      tracesTotal = res.meta?.total || 0;
    } catch (e: any) {
      if (request === traceListRequest) addToast(e?.response?.data?.message || 'Failed to load traces', 'alert');
    } finally {
      if (request === traceListRequest) tracesLoading = false;
    }
  }

  async function openTrace(trace: LLMCallTrace) {
    selectedTrace = trace;
    await loadTraceObservations(trace.trace_id);
  }

  async function openTraceByID(traceID: string) {
    selectedConversation = null; // A delegation cross-link can enter another session.
    // Cross-link navigation: synthesize a minimal trace row, the header
    // fills in from the loaded observations.
    selectedTrace = { trace_id: traceID } as LLMCallTrace;
    await loadTraceObservations(traceID);
  }

  async function loadTraceObservations(traceID: string) {
    const request = ++observationRequest;
    traceObsLoading = true;
    traceObservations = [];
    try {
      const res = await listLLMCalls(conversationFilters({
        trace_id: traceID,
        _limit: 500,
        _sort: 'created_at',
      }) as any);
      if (request !== observationRequest) return;
      traceObservations = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load trace observations', 'alert');
    } finally {
      if (request === observationRequest) traceObsLoading = false;
    }
  }

  function closeTrace() {
    ++observationRequest;
    selectedTrace = null;
    traceObservations = [];
    if (selectedConversation && !selectedConversation.session_id) closeConversation();
  }

  // Observation tree: chronological roots with tool observations nested
  // under their parent generation.
  interface ObsNode {
    obs: LLMCall;
    children: LLMCall[];
  }

  const obsTree = $derived.by<ObsNode[]>(() => {
    const byId = new Set(traceObservations.map((o) => o.id));
    const roots: ObsNode[] = [];
    const nodeById = new Map<string, ObsNode>();
    for (const o of traceObservations) {
      if (o.parent_observation_id && byId.has(o.parent_observation_id)) continue;
      const node = { obs: o, children: [] };
      nodeById.set(o.id, node);
      roots.push(node);
    }
    for (const o of traceObservations) {
      if (!o.parent_observation_id) continue;
      const parent = nodeById.get(o.parent_observation_id);
      if (parent) parent.children.push(o);
    }
    return roots;
  });

  // ─── Load: flat observation list ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['trace_id[like]'] = `%${searchQuery}%`;
      if (providerFilter) params['provider'] = providerFilter;
      if (modelFilter) params['model[like]'] = `%${modelFilter}%`;
      if (statusFilter) params['status'] = statusFilter;
      if (sourceFilter) params['source'] = sourceFilter;
      if (typeFilter) params['observation_type'] = typeFilter;
      if (taskFilter.length > 0) params['task_id[in]'] = taskFilter.join(',');
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listLLMCalls(params as any);
      calls = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load traces', 'alert');
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
    if (view === 'conversations' && !selectedConversation) {
      conversationsOffset = 0;
      loadConversations();
    } else if (view !== 'calls') {
      tracesOffset = 0;
      loadTraces();
    } else {
      offset = 0;
      load();
    }
  }

  function clearFilters() {
    providerFilter = '';
    modelFilter = '';
    statusFilter = '';
    sourceFilter = '';
    typeFilter = '';
    applyFilters();
  }

  function switchView(v: ViewMode) {
    view = v;
    selectedTrace = null;
    selectedConversation = null;
    ++observationRequest;
    if (v === 'conversations') loadConversations();
    else if (v === 'traces') loadTraces();
    else load();
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

  loadConversations();

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

  function formatDuration(start: string, end: string): string {
    if (!start || !end) return '-';
    const ms = new Date(end).getTime() - new Date(start).getTime();
    if (ms <= 0) return '-';
    if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)}m`;
    if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`;
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

  function obsType(o: LLMCall): string {
    return o.observation_type || 'generation';
  }

  function obsLabel(o: LLMCall): string {
    if (obsType(o) === 'generation') {
      return o.model ? `${o.provider}/${o.model}` : 'generation';
    }
    return o.name || obsType(o);
  }

  function childTraceID(o: LLMCall | null): string {
    const v = o?.metadata?.['child_trace_id'];
    return typeof v === 'string' ? v : '';
  }

  function isError(o: LLMCall): boolean {
    return o.status === 'error' || o.level === 'error';
  }

  // A generation whose bodies were expired by the retention janitor (or
  // never captured because the llm_audit feature is off).
  const bodiesMissing = $derived(
    selected != null &&
      obsType(selected) === 'generation' &&
      !selected.request_body &&
      !selected.response_body &&
      !detailLoading
  );
</script>

<svelte:head>
  <title>AT | Traces</title>
</svelte:head>

<div class="p-3 sm:p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
    <div class="flex items-center gap-2">
      <Activity size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Traces</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">
        ({view === 'calls' ? total : view === 'conversations' && !selectedConversation ? conversationsTotal : tracesTotal})
      </span>
    </div>
    <div class="flex items-center gap-1">
      <button onclick={() => switchView('conversations')} aria-pressed={view === 'conversations'} class={['flex h-9 items-center gap-1 px-2 text-xs rounded border focus-visible:outline-2 focus-visible:outline-accent', view === 'conversations' ? 'bg-gray-100 dark:bg-dark-elevated border-gray-300 dark:border-dark-border text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}><MessagesSquare size={14} />Conversations</button>
      <button
        onclick={() => switchView('traces')}
        class={[
          'flex items-center gap-1 px-2 py-1 text-xs rounded border',
          view === 'traces'
            ? 'bg-gray-100 dark:bg-dark-elevated border-gray-300 dark:border-dark-border text-gray-900 dark:text-dark-text'
            : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary',
        ]}
      ><ListTree size={12} /> Traces</button>
      <button
        onclick={() => switchView('calls')}
        class={[
          'flex items-center gap-1 px-2 py-1 text-xs rounded border',
          view === 'calls'
            ? 'bg-gray-100 dark:bg-dark-elevated border-gray-300 dark:border-dark-border text-gray-900 dark:text-dark-text'
            : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary',
        ]}
      ><List size={12} /> Observations</button>
      <button
        onclick={refreshView}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} class={(view === 'calls' ? loading : view === 'conversations' && !selectedConversation ? conversationsLoading : selectedTrace ? traceObsLoading : tracesLoading) ? 'animate-spin motion-reduce:animate-none' : ''} />
      </button>
    </div>
  </div>

  {#if taskFilter.length > 0}
    <div class="mb-3 flex items-center gap-2 px-2.5 py-1.5 text-xs rounded border border-blue-200 dark:border-blue-900/40 bg-blue-50 dark:bg-blue-900/10 text-blue-800 dark:text-blue-300">
      <span>Filtered by task tree ({taskFilter.length} task{taskFilter.length === 1 ? '' : 's'})</span>
      <button
        onclick={clearTaskFilter}
        class="ml-auto flex items-center gap-1 hover:underline"
        title="Clear task filter"
      ><X size={12} /> Clear</button>
    </div>
  {/if}

  {#if view === 'conversations' && selectedConversation}
    <div class="mb-4 space-y-2">
      <button onclick={closeConversation} class="inline-flex min-h-9 items-center gap-2 text-sm text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text"><ArrowLeft size={16} />All conversations</button>
      <h3 class="break-all text-sm font-medium text-gray-900 dark:text-dark-text">{selectedConversation.session_id || 'Single request without a session ID'}</h3>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-text-secondary">{selectedConversation.trace_count} traces · {selectedConversation.generation_count} model calls · {formatCost(selectedConversation.cost_cents)} · {formatTokens(selectedConversation.cache_read_tokens)} cache-read tokens</p>
    </div>
  {/if}

  {#if view !== 'calls' && selectedTrace}
    <!-- ─── Trace detail: observation tree ─── -->
    <div class="mb-3 flex items-center gap-2">
      <button
        onclick={closeTrace}
        class="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary"
      ><ArrowLeft size={12} /> {selectedConversation ? 'Conversation traces' : 'All traces'}</button>
      <span class="text-xs font-mono text-gray-400 dark:text-dark-text-muted truncate" title={selectedTrace.trace_id}>
        {selectedTrace.trace_id}
      </span>
      {#if selectedTrace.session_id}
        <span class="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted font-mono truncate" title="Session">
          session: {selectedTrace.session_id}
        </span>
      {/if}
    </div>

    {#if traceObsLoading}
      <div class="text-xs text-gray-400 dark:text-dark-text-muted py-8 text-center">Loading observations…</div>
    {:else if traceObservations.length === 0}
      <div class="text-xs text-gray-400 dark:text-dark-text-muted py-8 text-center">No observations in this trace.</div>
    {:else}
      <div class="border border-gray-200 dark:border-dark-border rounded divide-y divide-gray-100 dark:divide-dark-border bg-white dark:bg-dark-surface">
        {#each obsTree as node (node.obs.id)}
          {@render obsRow(node.obs, false)}
          {#each node.children as child (child.id)}
            {@render obsRow(child, true)}
          {/each}
        {/each}
      </div>
    {/if}
  {:else}
    <!-- ─── Filters ─── -->
    <div class="mb-3 flex flex-wrap items-center gap-2">
      {#if view === 'calls'}
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
          bind:value={typeFilter}
          onchange={applyFilters}
          class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text rounded"
        >
          <option value="">All types</option>
          <option value="generation">generation</option>
          <option value="tool">tool</option>
          <option value="event">event</option>
        </select>
      {/if}
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
        <option value="agent">agent</option>
        <option value="workflow">workflow</option>
      </select>
      <button
        onclick={applyFilters}
        class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary rounded"
      >Apply</button>
      {#if providerFilter || modelFilter || statusFilter || sourceFilter || typeFilter}
        <button
          onclick={clearFilters}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary"
        ><X size={12} /> Clear</button>
      {/if}
    </div>

    {#if view === 'conversations' && !selectedConversation}
      <details class="mb-4 rounded-md border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 text-sm">
        <summary class="cursor-pointer font-medium text-gray-700 dark:text-dark-text">How gateway conversations are grouped</summary>
        <p class="mt-2 leading-6 text-gray-600 dark:text-dark-text-secondary">Send the same <code class="font-mono">x-at-session-id</code> for each request in a conversation. <code>X-Session-Id</code>, <code>x-opencode-session</code>, and request <code>metadata.session_id</code> or <code>metadata.conversation_id</code> are also recognized. Each API token is grouped separately. Cache reuse alone does not identify a conversation; requests without an ID stay separate.</p>
      </details>
      <DataTable items={conversations} loading={conversationsLoading} total={conversationsTotal} bind:limit={conversationsLimit} bind:offset={conversationsOffset} onchange={loadConversations} emptyIcon={MessagesSquare} emptyTitle="No conversations" emptyDescription="Send gateway requests with a session ID or chat with an agent to see related traces together.">
        {#snippet header()}
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Last activity</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Conversation</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Source</th>
          <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Model calls</th>
          <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Tokens in / out</th>
          <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Cache read / write</th>
          <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Cost</th>
          <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-dark-text-secondary">Errors</th>
        {/snippet}
        {#snippet row(conversation)}
          <tr class="hover:bg-gray-50 dark:hover:bg-dark-elevated">
            <td class="whitespace-nowrap px-4 py-3 text-xs tabular-nums text-gray-600 dark:text-dark-text-secondary">{formatDateTime(conversation.ended_at)}</td>
            <td class="px-4 py-3 text-sm"><button onclick={() => openConversation(conversation)} class="max-w-60 text-left rounded focus-visible:outline-2 focus-visible:outline-accent"><span class="block truncate font-medium text-gray-900 dark:text-dark-text" title={conversation.session_id || conversation.trace_id}>{conversation.session_id || conversation.name || 'Single request'}</span><span class="text-xs text-gray-500 dark:text-dark-text-secondary">{conversation.session_id ? `${conversation.trace_count} traces` : 'No session ID'}{conversation.token_id ? ` · token ${conversation.token_id.slice(0, 8)}…` : ''}</span></button></td>
            <td class="px-4 py-3 text-xs text-gray-600 dark:text-dark-text-secondary">{conversation.source}</td>
            <td class="px-4 py-3 text-right text-xs tabular-nums">{conversation.generation_count}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-xs tabular-nums">{formatTokens(conversation.input_tokens)} / {formatTokens(conversation.output_tokens)}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-xs tabular-nums">{formatTokens(conversation.cache_read_tokens)} / {formatTokens(conversation.cache_write_tokens)}</td>
            <td class="px-4 py-3 text-right text-xs font-medium tabular-nums">{formatCost(conversation.cost_cents)}</td>
            <td class="px-4 py-3 text-right text-xs tabular-nums">{conversation.error_count}</td>
          </tr>
        {/snippet}
      </DataTable>
    {:else if view !== 'calls'}
      <!-- ─── Trace list ─── -->
      <DataTable
        items={traces}
        loading={tracesLoading}
        total={tracesTotal}
        bind:limit={tracesLimit}
        bind:offset={tracesOffset}
        onchange={loadTraces}
        emptyIcon={Activity}
        emptyTitle="No traces"
        emptyDescription="Run a task, chat with an agent, or send a request through the gateway to see traces here."
      >
        {#snippet header()}
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Started</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Name</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Source</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Obs</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">In</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Out</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Cache R/W</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Cost</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Duration</th>
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Errors</th>
        {/snippet}

        {#snippet row(trace)}
          <tr
            class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer"
            onclick={() => openTrace(trace)}
          >
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted whitespace-nowrap font-mono">{formatDateTime(trace.started_at)}</td>
            <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text-secondary max-w-56">
              <button class="max-w-full truncate rounded text-left hover:underline focus-visible:outline-2 focus-visible:outline-accent" title={trace.name || trace.trace_id} onclick={(e) => { e.stopPropagation(); void openTrace(trace); }}>{trace.name || trace.trace_id}</button>
              {#if trace.task_id}
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted font-mono truncate" title={trace.task_id}>task: {trace.task_id}</div>
              {/if}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-mono">{trace.source}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{trace.observation_count}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(trace.input_tokens)}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(trace.output_tokens)}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right whitespace-nowrap" title="Cache read / cache write tokens">{formatTokens(trace.cache_read_tokens)} / {formatTokens(trace.cache_write_tokens)}</td>
            <td class="px-4 py-2.5 text-xs font-medium text-gray-900 dark:text-dark-text text-right">{formatCost(trace.cost_cents)}</td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right whitespace-nowrap">
              <div title="Wall-clock duration">{formatDuration(trace.started_at, trace.ended_at)}</div>
              <div class="text-[10px] text-gray-400 dark:text-dark-text-muted" title="Summed LLM and tool execution time">active {formatLatency(trace.latency_ms_total)}</div>
            </td>
            <td class="px-4 py-2.5 text-xs text-right">
              {#if trace.error_count > 0}
                <span class="px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300">{trace.error_count}</span>
              {:else}
                <span class="text-gray-400 dark:text-dark-text-muted">0</span>
              {/if}
            </td>
          </tr>
        {/snippet}
      </DataTable>
    {:else}
      <!-- ─── Flat observation list ─── -->
      <DataTable
        items={calls}
        {loading}
        {total}
        bind:limit
        bind:offset
        onchange={load}
        onsearch={handleSearch}
        searchPlaceholder="Search by trace ID..."
        emptyIcon={Activity}
        emptyTitle="No observations"
        emptyDescription="Run a task, chat with an agent, or send a request through the gateway to see observations here."
      >
        {#snippet header()}
          <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Type</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Name / Model</th>
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
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted whitespace-nowrap font-mono">{formatDateTime(call.created_at)}</td>
            <td class="px-4 py-2.5 text-xs">{@render typeBadge(call)}</td>
            <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text-secondary max-w-56 truncate" title={obsLabel(call)}>
              {obsLabel(call)}
              {#if call.streamed}<span class="ml-1 text-[10px] px-1 rounded bg-gray-100 dark:bg-dark-elevated text-gray-500">stream</span>{/if}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-mono">{call.source}</td>
            <td class="px-4 py-2.5 text-xs">
              {#if isError(call)}
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
    {/if}
  {/if}
</div>

{#snippet typeBadge(o: LLMCall)}
  {#if obsType(o) === 'generation'}
    <span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300"><Sparkles size={10} /> gen</span>
  {:else if obsType(o) === 'tool'}
    <span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300"><Wrench size={10} /> tool</span>
  {:else}
    <span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted"><Flag size={10} /> event</span>
  {/if}
{/snippet}

{#snippet obsRow(o: LLMCall, nested: boolean)}
  <button
    class={[
      'w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-gray-50 dark:hover:bg-dark-elevated/50 transition-colors',
      nested ? 'pl-10' : '',
      isError(o) ? 'bg-red-50/50 dark:bg-red-900/10' : '',
    ]}
    onclick={() => openDetail(o)}
  >
    {@render typeBadge(o)}
    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate min-w-0 flex-1" title={obsLabel(o)}>
      {obsLabel(o)}
      {#if isError(o)}
        <span class="ml-1 text-[10px] text-red-600 dark:text-red-400">{o.error_code || 'error'}</span>
      {/if}
    </span>
    {#if childTraceID(o)}
      <span
        role="link"
        tabindex="0"
        class="flex items-center gap-0.5 text-[10px] text-blue-600 dark:text-blue-400 hover:underline shrink-0"
        onclick={(e) => { e.stopPropagation(); openTraceByID(childTraceID(o)); }}
        onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); openTraceByID(childTraceID(o)); } }}
      ><ExternalLink size={10} /> child trace</span>
    {/if}
    {#if obsType(o) === 'generation'}
      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted whitespace-nowrap shrink-0">
        {formatTokens(o.input_tokens)} → {formatTokens(o.output_tokens)} · {formatCost(o.cost_cents)}
      </span>
    {/if}
    {#if o.latency_ms}
      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted whitespace-nowrap shrink-0">{formatLatency(o.latency_ms)}</span>
    {/if}
    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted whitespace-nowrap shrink-0 font-mono">{formatDateTime(o.created_at)}</span>
  </button>
{/snippet}

<!-- Detail drawer -->
{#if selected}
  <div class="fixed inset-0 z-40 flex justify-end">
    <button class="absolute inset-0 bg-black/30" onclick={closeDetail} aria-label="Close"></button>
    <div class="relative z-50 w-full max-w-2xl h-full overflow-y-auto bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shadow-xl">
      <div class="sticky top-0 flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-2 min-w-0">
          {@render typeBadge(selected)}
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">{obsLabel(selected)}</span>
          {#if isError(selected)}
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
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono">{formatDateTime(selected.created_at)}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Trace ID</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate" title={selected.trace_id}>{selected.trace_id || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Session ID</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate" title={selected.session_id}>{selected.session_id || '-'}</div>
          <div class="text-gray-400 dark:text-dark-text-muted">Source / Endpoint</div>
          <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate">{selected.source} · {selected.endpoint || '-'}</div>
          {#if selected.task_id}
            <div class="text-gray-400 dark:text-dark-text-muted">Task</div>
            <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate">
              <a class="hover:underline" href={`#/tasks/${selected.task_id}`}>{selected.task_id}</a>
            </div>
          {/if}
          {#if obsType(selected) === 'generation'}
            <div class="text-gray-400 dark:text-dark-text-muted">Requested model</div>
            <div class="text-gray-700 dark:text-dark-text-secondary font-mono truncate">{selected.requested_model || '-'}</div>
            <div class="text-gray-400 dark:text-dark-text-muted">Tokens (in / out)</div>
            <div class="text-gray-700 dark:text-dark-text-secondary">{formatTokens(selected.input_tokens)} / {formatTokens(selected.output_tokens)}
              {#if selected.reasoning_tokens} · {formatTokens(selected.reasoning_tokens)} reasoning{/if}
            </div>
            <div class="text-gray-400 dark:text-dark-text-muted">Cache (read / write)</div>
            <div class="text-gray-700 dark:text-dark-text-secondary">{formatTokens(selected.cache_read_tokens)} / {formatTokens(selected.cache_write_tokens)}</div>
            <div class="text-gray-400 dark:text-dark-text-muted">Cost</div>
            <div class="text-gray-700 dark:text-dark-text-secondary">{formatCost(selected.cost_cents)}</div>
            <div class="text-gray-400 dark:text-dark-text-muted">Finish reason</div>
            <div class="text-gray-700 dark:text-dark-text-secondary">{selected.finish_reason || '-'}</div>
          {/if}
          {#if selected.latency_ms}
            <div class="text-gray-400 dark:text-dark-text-muted">Latency</div>
            <div class="text-gray-700 dark:text-dark-text-secondary">{formatLatency(selected.latency_ms)}
              {#if selected.time_to_first_token_ms} · TTFT {formatLatency(selected.time_to_first_token_ms)}{/if}
            </div>
          {/if}
          {#if isError(selected)}
            <div class="text-gray-400 dark:text-dark-text-muted">Error</div>
            <div class="text-red-600 dark:text-red-400 break-words">{selected.error_message || selected.error_code}</div>
          {/if}
        </div>

        {#if selected.metadata && Object.keys(selected.metadata).length > 0}
          <div>
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Metadata</span>
              {#if childTraceID(selected)}
                <button
                  onclick={() => { const id = childTraceID(selected); closeDetail(); openTraceByID(id); }}
                  class="flex items-center gap-1 text-xs text-blue-600 dark:text-blue-400 hover:underline"
                ><ExternalLink size={12} /> Open child trace</button>
              {/if}
            </div>
            <pre class="text-[11px] leading-relaxed p-3 rounded bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border overflow-x-auto max-h-40 text-gray-800 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{JSON.stringify(selected.metadata, null, 2)}</pre>
          </div>
        {/if}

        {#if detailLoading}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted">Loading full payloads…</div>
        {/if}

        {#if obsType(selected) === 'generation'}
          {#if bodiesMissing}
            <div class="text-xs text-amber-600 dark:text-amber-400 p-3 rounded bg-amber-50 dark:bg-amber-900/10 border border-amber-200 dark:border-amber-900/30">
              Request/response bodies are not available — either body capture (the <code>llm_audit</code> feature) was off when this call ran, or the bodies passed the retention window and were expired.
            </div>
          {:else}
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
          {/if}
        {:else}
          <!-- Tool / event input & output -->
          {#if selected.input}
            <div>
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Input</span>
                <button onclick={() => copyText(selected!.input || '')} class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Copy">
                  <Copy size={12} /> Copy
                </button>
              </div>
              <pre class="text-[11px] leading-relaxed p-3 rounded bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border overflow-x-auto max-h-80 text-gray-800 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{prettyJSON(selected.input || '')}</pre>
            </div>
          {/if}
          {#if selected.output}
            <div>
              <div class="flex items-center justify-between mb-1">
                <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Output</span>
                <button onclick={() => copyText(selected!.output || '')} class="flex items-center gap-1 text-xs text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Copy">
                  <Copy size={12} /> Copy
                </button>
              </div>
              <pre class="text-[11px] leading-relaxed p-3 rounded bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border overflow-x-auto max-h-80 text-gray-800 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{prettyJSON(selected.output || '')}</pre>
            </div>
          {/if}
        {/if}
      </div>
    </div>
  </div>
{/if}
