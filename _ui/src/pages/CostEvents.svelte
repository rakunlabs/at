<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listCostEvents,
    type CostEvent,
  } from '@/lib/api/cost-events';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Receipt, RefreshCw, X } from 'lucide-svelte';
  import { querystring, push } from 'svelte-spa-router';
  import { untrack } from 'svelte';

  storeNavbar.title = 'Cost Events';

  // ─── State ───

  let costEvents = $state<CostEvent[]>([]);
  let loading = $state(true);

  // Pagination
  let offset = $state(0);
  let limit = $state(20);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // ?task_ids=A,B,C — when present, scope the event list to that set so
  // the page can be linked from TaskDetail's "View cost events" button.
  // Stored as an array because the backend's query parser supports
  // ?task_id=A&task_id=B style repeated parameters.
  let taskFilter = $state<string[]>([]);

  // Watch URL changes and re-derive task filter. When the user clicks the
  // X to clear the filter we also push() back to the bare /cost-events URL.
  $effect(() => {
    const qs = new URLSearchParams($querystring || '');
    const ids = (qs.get('task_ids') || '').split(',').map((s) => s.trim()).filter(Boolean);
    // Only fire if it actually changed; otherwise we'd loop with the
    // load() call below.
    if (ids.join(',') !== untrack(() => taskFilter).join(',')) {
      taskFilter = ids;
      offset = 0;
      load();
    }
  });

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['billing_code[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      // The rakunlabs/query parser supports `field[in]=a,b,c` for an
      // explicit IN-list. Use that even for a single ID so we don't
      // depend on the equality fallback. ULIDs are hex-only so they
      // can't contain commas.
      if (taskFilter.length > 0) {
        params['task_id[in]'] = taskFilter.join(',');
      }
      const res = await listCostEvents(params as any);
      costEvents = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load cost events', 'alert');
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

  function clearTaskFilter() {
    push('/cost-events');
  }

  load();

  function formatCost(cents: number): string {
    return `$${(cents / 100).toFixed(4)}`;
  }

  function formatTokens(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  }

  // Aggregate the visible page so the user sees a quick total even when
  // scoped to a task. Server already returns `total` for paging count;
  // this is just the sum on the current page (cheap, no extra round-trip).
  let pageTotalCents = $derived(
    costEvents.reduce((acc, e) => acc + (e.cost_cents || 0), 0),
  );
  let pageTotalTokens = $derived(
    costEvents.reduce((acc, e) => acc + (e.input_tokens || 0) + (e.output_tokens || 0), 0),
  );
</script>

<svelte:head>
  <title>AT | Cost Events</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Receipt size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Cost Events</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button onclick={() => load()}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Refresh">
        <RefreshCw size={14} class={loading ? 'animate-spin' : ''} />
      </button>
    </div>
  </div>

  <!-- Active task filter banner. Only rendered when ?task_ids= is set, e.g.
       when navigated from a TaskDetail "View cost" button. -->
  {#if taskFilter.length > 0}
    <div class="mb-3 flex items-center justify-between gap-2 px-3 py-2 border border-blue-200 dark:border-blue-900/50 bg-blue-50 dark:bg-blue-900/10 rounded text-xs">
      <div class="flex items-center gap-2 min-w-0">
        <span class="text-blue-700 dark:text-blue-300 font-medium shrink-0">Filtered by task tree</span>
        <span class="text-blue-600 dark:text-blue-400 font-mono truncate" title={taskFilter.join(', ')}>
          {taskFilter.length === 1 ? taskFilter[0] : `${taskFilter.length} tasks`}
        </span>
        <span class="text-blue-500 dark:text-blue-300/70 shrink-0">
          · page total: <span class="font-medium">{formatCost(pageTotalCents)}</span>
          · tokens on page: <span class="font-medium">{formatTokens(pageTotalTokens)}</span>
        </span>
      </div>
      <button
        onclick={clearTaskFilter}
        class="flex items-center gap-1 text-blue-700 dark:text-blue-300 hover:text-blue-900 dark:hover:text-blue-100 transition-colors"
        title="Clear task filter"
      >
        <X size={12} /> Clear
      </button>
    </div>
  {/if}

  <!-- Event List -->
  <DataTable
    items={costEvents}
    {loading}
    {total}
    {limit}
    bind:offset
    onchange={load}
    onsearch={handleSearch}
    searchPlaceholder="Search by billing code..."
    emptyIcon={Receipt}
    emptyTitle="No cost events"
    emptyDescription={taskFilter.length > 0
      ? 'No cost events recorded for this task tree yet.'
      : 'Cost events track per-call LLM usage and spending'}
  >
    {#snippet header()}
      <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Agent</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Provider / Model</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Task</th>
      <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Input</th>
      <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Output</th>
      <SortableHeader field="cost_cents" label="Cost" {sorts} onsort={handleSort} />
    {/snippet}

    {#snippet row(event)}
      <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(event.created_at)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-mono truncate max-w-32" title={event.agent_id}>{event.agent_id?.slice(0, 12) || '-'}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
          <span class="text-gray-400">{event.provider}/</span>{event.model}
        </td>
        <td class="px-4 py-2.5 text-xs font-mono">
          {#if event.task_id}
            <a href="#/tasks/{event.task_id}" class="text-blue-600 dark:text-blue-400 hover:underline" title={event.task_id}>
              {event.task_id.slice(0, 12)}
            </a>
          {:else}
            <span class="text-gray-400 dark:text-dark-text-muted">-</span>
          {/if}
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(event.input_tokens)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(event.output_tokens)}</td>
        <td class="px-4 py-2.5 text-xs font-medium text-gray-900 dark:text-dark-text text-right">{formatCost(event.cost_cents)}</td>
      </tr>
    {/snippet}
  </DataTable>
</div>
