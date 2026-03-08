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
  import { ScrollText, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Audit Trail';

  // ─── State ───

  let entries = $state<AuditEntry[]>([]);
  let loading = $state(true);

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
        return 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400';
      case 'task_checkout':
        return 'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400';
      case 'status_change':
        return 'bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-400';
      case 'config_update':
        return 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400';
      default:
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  function truncate(text: string, max: number): string {
    return text.length > max ? text.slice(0, max) + '…' : text;
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
      <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted whitespace-nowrap">{formatDateTime(entry.created_at)}</td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text-secondary">
          <span class="font-medium text-gray-500 dark:text-dark-text-muted">{entry.actor_type}:</span> {entry.actor_id}
        </td>
        <td class="px-4 py-2.5">
          <span class="inline-block px-2 py-0.5 text-xs font-medium rounded {actionBadgeClass(entry.action)}">
            {entry.action}
          </span>
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text-secondary">
          <span class="font-medium text-gray-500 dark:text-dark-text-muted">{entry.resource_type}:</span> {entry.resource_id}
        </td>
        <td class="px-4 py-2.5 text-xs font-mono text-gray-500 dark:text-dark-text-muted max-w-48 truncate" title={JSON.stringify(entry.details)}>
          {truncate(JSON.stringify(entry.details), 80)}
        </td>
      </tr>
    {/snippet}
  </DataTable>
</div>
