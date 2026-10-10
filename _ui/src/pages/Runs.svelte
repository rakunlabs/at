<script lang="ts">
  import { onDestroy } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listActiveRuns, cancelRun, type ActiveRun } from '@/lib/api/runs';
  import { Activity, RefreshCw, Square, Clock } from 'lucide-svelte';
  import { formatTime } from '@/lib/helper/format';
  import DataTable from '@/lib/components/DataTable.svelte';
  import { routeFeatureEnabled } from '@/lib/helper/feature-routes';

  storeNavbar.title = 'Workflows';

  // ─── State ───
  let runs = $state<ActiveRun[]>([]);
  let loading = $state(true);
  let cancellingId = $state<string | null>(null);
  let cancelConfirmId = $state<string | null>(null);
  let autoRefresh = $state(true);

  // Non-reactive interval handle — must not be $state to avoid
  // retriggering effects when set.
  let refreshTimer: ReturnType<typeof setInterval> | undefined;

  // ─── Data Loading ───
  async function loadRuns() {
    try {
      runs = (await listActiveRuns()) || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load runs', 'alert');
    } finally {
      loading = false;
    }
  }

  loadRuns();

  // ─── Auto-refresh ───
  function startAutoRefresh() {
    stopAutoRefresh();
    refreshTimer = setInterval(loadRuns, 3000);
  }

  function stopAutoRefresh() {
    if (refreshTimer !== undefined) {
      clearInterval(refreshTimer);
      refreshTimer = undefined;
    }
  }

  $effect(() => {
    if (autoRefresh) {
      startAutoRefresh();
    } else {
      stopAutoRefresh();
    }
  });

  onDestroy(() => stopAutoRefresh());

  // ─── Actions ───
  async function handleCancel(runId: string) {
    cancellingId = runId;
    try {
      await cancelRun(runId);
      addToast('Cancel signal sent');
      cancelConfirmId = null;
      // Refresh after a short delay to let the run finish
      setTimeout(loadRuns, 500);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to cancel run', 'alert');
    } finally {
      cancellingId = null;
    }
  }

  function sourceLabel(source: string): string {
    switch (source) {
      case 'api': return 'API';
      case 'webhook': return 'Webhook';
      case 'cron': return 'Cron';
      default: return source;
    }
  }

  function sourceBadgeClass(source: string): string {
    switch (source) {
      case 'api': return 'bg-blue-900/20 text-blue-300';
      case 'webhook': return 'bg-purple-900/20 text-purple-300';
      case 'cron': return 'bg-amber-900/20 text-amber-300';
      default: return 'bg-dark-elevated text-dark-text-secondary';
    }
  }
</script>

<svelte:head>
  <title>AT | Workflows · Runs</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
    <div class="flex items-center gap-2">
      <Activity size={16} class="text-dark-text-muted" />
      <h2 class="text-sm font-medium text-dark-text">Active runs</h2>
      <span class="text-xs text-dark-text-muted">({runs.length})</span>
    </div>
    <div class="flex items-center gap-3">
      <label class="flex items-center gap-1.5 text-xs text-dark-text-muted cursor-pointer">
        <input
          type="checkbox"
          bind:checked={autoRefresh}
          class="accent-accent"
        />
        Auto-refresh
      </label>
      <button
        onclick={loadRuns}
        class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Info banner -->
  <div class="mb-4 border border-dark-border px-4 py-2.5 text-xs text-dark-text-muted">
    Shows workflows currently running. Cancelled runs may take a moment to stop at the next cancellation checkpoint.
  </div>

  <!-- Runs list -->
  <DataTable
    items={runs}
    {loading}
    emptyIcon={Activity}
    emptyTitle="No active runs"
    emptyDescription="Workflows will appear here while executing"
  >
    {#snippet header()}
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Run ID</th>
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Workflow</th>
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Source</th>
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Started</th>
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs">Duration</th>
      <th class="text-left px-4 py-2 font-medium text-dark-text-muted text-xs w-24"></th>
    {/snippet}

    {#snippet row(run)}
      <tr class="hover:bg-dark-surface">
        <td class="px-4 py-2.5">
          <code class="text-xs font-mono text-dark-text-secondary">{run.id}</code>
        </td>
        <td class="px-4 py-2.5">
          {#if routeFeatureEnabled('/workflows')}
            <a href={`#/workflows/${run.workflow_id}`} class="text-xs text-accent hover:underline focus-visible:outline-2 focus-visible:outline-accent">{run.workflow_id}</a>
          {:else}
            <code class="text-xs text-dark-text-secondary">{run.workflow_id}</code>
          {/if}
        </td>
        <td class="px-4 py-2.5">
          <span class="px-2 py-0.5 text-xs font-medium {sourceBadgeClass(run.source)}">
            {sourceLabel(run.source)}
          </span>
        </td>
        <td class="px-4 py-2.5 text-xs text-dark-text-muted">
          {formatTime(run.started_at)}
        </td>
        <td class="px-4 py-2.5 text-xs text-dark-text-muted">
          <span class="flex items-center gap-1">
            <Clock size={11} class="text-dark-text-muted" />
            {run.duration}
          </span>
        </td>
        <td class="px-4 py-2.5 text-right">
          {#if cancelConfirmId === run.id}
            <div class="flex items-center gap-1 justify-end">
              <button
                onclick={() => handleCancel(run.id)}
                disabled={cancellingId === run.id}
                class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 disabled:opacity-50"
              >
                {cancellingId === run.id ? 'Cancelling...' : 'Confirm'}
              </button>
              <button
                onclick={() => (cancelConfirmId = null)}
                class="px-2 py-1 text-xs text-dark-text-muted hover:text-dark-text-secondary"
              >
                No
              </button>
            </div>
          {:else}
            <button
              onclick={() => (cancelConfirmId = run.id)}
              class="flex items-center gap-1 px-2 py-1 text-xs text-red-400 hover:text-red-300 hover:bg-red-900/20"
              title="Cancel run"
            >
              <Square size={11} />
              Cancel
            </button>
          {/if}
        </td>
      </tr>
    {/snippet}
  </DataTable>
</div>
