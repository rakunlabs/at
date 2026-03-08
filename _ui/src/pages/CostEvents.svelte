<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listCostEvents,
    getCostByBillingCode,
    type CostEvent,
    type CostSummary,
  } from '@/lib/api/cost-events';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { Receipt, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Cost Events';

  // ─── State ───

  let costEvents = $state<CostEvent[]>([]);
  let loading = $state(true);
  let billingCodes = $state<CostSummary[]>([]);
  let showBillingView = $state(false);

  // Pagination
  let offset = $state(0);
  let limit = $state(20);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['billing_code[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listCostEvents(params);
      costEvents = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load cost events', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadBillingCodes() {
    try {
      billingCodes = await getCostByBillingCode() || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load billing summary', 'alert');
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

  function toggleView() {
    showBillingView = !showBillingView;
    if (showBillingView && billingCodes.length === 0) {
      loadBillingCodes();
    }
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
      <button onclick={toggleView}
        class="px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors">
        {showBillingView ? 'Event List' : 'By Billing Code'}
      </button>
      <button onclick={() => { load(); loadBillingCodes(); }}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Refresh">
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Billing Code Summary View -->
  {#if showBillingView}
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
      <table class="w-full text-sm">
        <thead class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
          <tr>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Billing Code</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Events</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Input Tokens</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Output Tokens</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Total Cost</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
          {#each billingCodes as bc}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5 font-medium text-gray-900 dark:text-dark-text font-mono">{bc.key || '(none)'}</td>
              <td class="px-4 py-2.5 text-right text-xs text-gray-500 dark:text-dark-text-muted">{bc.event_count}</td>
              <td class="px-4 py-2.5 text-right text-xs text-gray-500 dark:text-dark-text-muted">{formatTokens(bc.total_input_tokens)}</td>
              <td class="px-4 py-2.5 text-right text-xs text-gray-500 dark:text-dark-text-muted">{formatTokens(bc.total_output_tokens)}</td>
              <td class="px-4 py-2.5 text-right text-xs font-medium text-gray-900 dark:text-dark-text">{formatCost(bc.total_cost_cents)}</td>
            </tr>
          {:else}
            <tr>
              <td colspan="5" class="px-4 py-8 text-center text-sm text-gray-400 dark:text-dark-text-muted">No billing code data</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <!-- Event List View -->
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
      emptyDescription="Cost events track per-call LLM usage and spending"
    >
      {#snippet header()}
        <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Agent</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Provider / Model</th>
        <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Billing Code</th>
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
          <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-muted">{event.billing_code || '-'}</td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(event.input_tokens)}</td>
          <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted text-right">{formatTokens(event.output_tokens)}</td>
          <td class="px-4 py-2.5 text-xs font-medium text-gray-900 dark:text-dark-text text-right">{formatCost(event.cost_cents)}</td>
        </tr>
      {/snippet}
    </DataTable>
  {/if}
</div>
