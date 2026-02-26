<script lang="ts">
  import { onDestroy } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listActiveRuns, cancelRun, type ActiveRun } from '@/lib/api/runs';
  import { Activity, RefreshCw, Square, Clock } from 'lucide-svelte';

  storeNavbar.title = 'Active Runs';

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
      runs = await listActiveRuns();
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
      case 'api': return 'bg-blue-100 text-blue-700';
      case 'webhook': return 'bg-purple-100 text-purple-700';
      case 'cron': return 'bg-amber-100 text-amber-700';
      default: return 'bg-gray-100 text-gray-700';
    }
  }

  function formatTime(dateStr: string): string {
    const d = new Date(dateStr);
    return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
</script>

<svelte:head>
  <title>AT | Active Runs</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Activity size={16} class="text-gray-500" />
      <h2 class="text-sm font-medium text-gray-900">Active Runs</h2>
      <span class="text-xs text-gray-400">({runs.length})</span>
    </div>
    <div class="flex items-center gap-3">
      <label class="flex items-center gap-1.5 text-xs text-gray-500 cursor-pointer">
        <input
          type="checkbox"
          bind:checked={autoRefresh}
          class="accent-gray-900"
        />
        Auto-refresh
      </label>
      <button
        onclick={loadRuns}
        class="p-1.5 hover:bg-gray-100 text-gray-400 hover:text-gray-600 transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Info banner -->
  <div class="mb-4 border border-gray-200 bg-gray-50 px-4 py-2.5 text-xs text-gray-500">
    Shows workflows currently running. Cancelled runs may take a moment to stop at the next cancellation checkpoint.
  </div>

  <!-- Runs list -->
  <div class="border border-gray-200 bg-white shadow-sm overflow-hidden">
    {#if loading}
      <div class="px-4 py-10 text-center text-gray-400 text-sm">Loading...</div>
    {:else if runs.length === 0}
      <div class="px-4 py-10 text-center">
        <Activity size={24} class="mx-auto text-gray-300 mb-2" />
        <div class="text-gray-400 mb-1">No active runs</div>
        <div class="text-xs text-gray-400">Workflows will appear here while executing</div>
      </div>
    {:else}
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-100 bg-gray-50/50">
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Run ID</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Workflow</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Source</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Started</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider">Duration</th>
            <th class="text-left px-4 py-2 font-medium text-gray-500 text-xs uppercase tracking-wider w-24"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-50">
          {#each runs as run}
            <tr class="hover:bg-gray-50/50 transition-colors">
              <td class="px-4 py-2.5">
                <code class="text-xs font-mono text-gray-600">{run.id}</code>
              </td>
              <td class="px-4 py-2.5">
                <code class="text-xs font-mono text-gray-500 bg-gray-100 px-1.5 py-0.5">{run.workflow_id}</code>
              </td>
              <td class="px-4 py-2.5">
                <span class="px-2 py-0.5 text-xs font-medium {sourceBadgeClass(run.source)}">
                  {sourceLabel(run.source)}
                </span>
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                {formatTime(run.started_at)}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500">
                <span class="flex items-center gap-1">
                  <Clock size={11} class="text-gray-400" />
                  {run.duration}
                </span>
              </td>
              <td class="px-4 py-2.5 text-right">
                {#if cancelConfirmId === run.id}
                  <div class="flex items-center gap-1 justify-end">
                    <button
                      onclick={() => handleCancel(run.id)}
                      disabled={cancellingId === run.id}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors disabled:opacity-50"
                    >
                      {cancellingId === run.id ? 'Cancelling...' : 'Confirm'}
                    </button>
                    <button
                      onclick={() => (cancelConfirmId = null)}
                      class="px-2 py-1 text-xs text-gray-500 hover:text-gray-700 transition-colors"
                    >
                      No
                    </button>
                  </div>
                {:else}
                  <button
                    onclick={() => (cancelConfirmId = run.id)}
                    class="flex items-center gap-1 px-2 py-1 text-xs text-red-500 hover:text-red-700 hover:bg-red-50 transition-colors"
                    title="Cancel run"
                  >
                    <Square size={11} />
                    Cancel
                  </button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
