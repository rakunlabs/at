<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listAuditEntries,
    type AuditEntry,
  } from '@/lib/api/audit';
  import { formatDateTime } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import { ScrollText, RefreshCw, ChevronRight } from 'lucide-svelte';

  storeNavbar.title = 'Audit Trail';

  // ─── State ───

  let entries = $state<AuditEntry[]>([]);
  let loading = $state(true);
  let expandedIds = $state<Set<string>>(new Set());

  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Search & Sort
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['actor_id[like]'] = `%${searchQuery}%`;
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listAuditEntries(params);
      entries = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load audit entries', 'alert');
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

  // ─── Helpers ───

  function actionBadgeClass(action: string): string {
    switch (action) {
      case 'tool_call':
        return 'bg-blue-100 dark:bg-blue-900/50 text-blue-700 dark:text-blue-300';
      case 'task_checkout':
        return 'bg-yellow-100 dark:bg-yellow-900/50 text-yellow-700 dark:text-yellow-300';
      case 'status_change':
        return 'bg-purple-100 dark:bg-purple-900/50 text-purple-700 dark:text-purple-300';
      case 'config_update':
        return 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300';
      default:
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  function truncate(text: string, max: number): string {
    return text.length > max ? text.slice(0, max) + '…' : text;
  }

  function toggleExpand(id: string) {
    const next = new Set(expandedIds);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expandedIds = next;
  }

  function formatDetailValue(value: any): string {
    if (value === null || value === undefined) return 'null';
    if (typeof value === 'object') return JSON.stringify(value, null, 2);
    return String(value);
  }
</script>

<svelte:head>
  <title>AT | Audit Trail</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <ScrollText size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Audit Trail</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={load}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Audit list -->
  <DataTable
    items={entries}
    {loading}
    {total}
    {limit}
    bind:offset
    onchange={load}
    onsearch={handleSearch}
    searchPlaceholder="Search by actor ID..."
    emptyIcon={ScrollText}
    emptyTitle="No audit entries"
    emptyDescription="Audit entries are recorded automatically when agents perform actions"
  >
    {#snippet header()}
      <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Actor</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Action</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Resource</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Details</th>
    {/snippet}

    {#snippet row(entry)}
      {@const isExpanded = expandedIds.has(entry.id)}
      {@const detailEntries = entry.details ? Object.entries(entry.details) : []}
      <tr
        class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer select-none"
        onclick={() => toggleExpand(entry.id)}
      >
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-secondary whitespace-nowrap">
          <div class="flex items-center gap-1.5">
            <ChevronRight
              size={12}
              class="text-gray-400 dark:text-dark-text-secondary transition-transform flex-shrink-0 {isExpanded ? 'rotate-90' : ''}"
            />
            {formatDateTime(entry.created_at)}
          </div>
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text">
          <span class="font-medium text-gray-500 dark:text-dark-text-secondary">{entry.actor_type}:</span> {entry.actor_id}
        </td>
        <td class="px-4 py-2.5">
          <span class="inline-block px-2 py-0.5 text-xs font-medium rounded {actionBadgeClass(entry.action)}">
            {entry.action}
          </span>
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text">
          <span class="font-medium text-gray-500 dark:text-dark-text-secondary">{entry.resource_type}:</span> {entry.resource_id}
        </td>
        <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-secondary max-w-48 truncate">
          {detailEntries.length > 0 ? truncate(JSON.stringify(entry.details), 60) : '—'}
        </td>
      </tr>
      {#if isExpanded && detailEntries.length > 0}
        <tr>
          <td colspan="5" class="px-0 py-0">
            <div class="mx-4 mb-3 mt-0 overflow-hidden">
              <div class="divide-y divide-gray-100 dark:divide-dark-border">
                {#each detailEntries as [key, value]}
                  <div class="px-4 py-2 flex gap-4">
                    <span class="text-xs font-medium text-gray-500 dark:text-accent-text min-w-[120px] flex-shrink-0">{key}</span>
                    {#if typeof value === 'object' && value !== null}
                      <pre class="text-xs font-mono text-gray-700 dark:text-dark-text whitespace-pre-wrap break-all flex-1">{JSON.stringify(value, null, 2)}</pre>
                    {:else if typeof value === 'boolean'}
                      <span class="inline-block px-1.5 py-0.5 text-xs font-medium rounded {value ? 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300' : 'bg-red-100 dark:bg-red-900/50 text-red-700 dark:text-red-300'}">
                        {value}
                      </span>
                    {:else}
                      <span class="text-xs font-mono text-gray-700 dark:text-dark-text break-all flex-1">{formatDetailValue(value)}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          </td>
        </tr>
      {/if}
    {/snippet}
  </DataTable>
</div>
