<script lang="ts">
  import { onMount, onDestroy, untrack } from 'svelte';
  import { listWorkflowRuns, type WorkflowRunRecord } from '@/lib/api/workflows';

  let { workflowId, refreshKey = 0, onselectnode }: { workflowId: string; refreshKey?: number; onselectnode?: (nodeId: string) => void } = $props();

  let items = $state<WorkflowRunRecord[]>([]);
  let error = $state('');
  let loading = $state(false);
  let failedOnly = $state(false);
  let expanded = $state<Record<string, boolean>>({});
  let alive = true;
  let sequence = 0;

  async function load(id = workflowId, onlyFailed = failedOnly) {
    if (!alive) return;
    const seq = ++sequence;
    loading = true;
    try {
      const data = await listWorkflowRuns(id, onlyFailed ? { status: 'failed', limit: 100 } : { limit: 100 });
      if (alive && seq === sequence) { items = data; error = ''; }
    } catch (e: any) {
      if (alive && seq === sequence) error = e?.response?.data?.message || 'Could not load run history.';
    } finally {
      if (alive && seq === sequence) loading = false;
    }
  }

  export function refresh() { return load(); }

  $effect(() => { const id = workflowId; const only = failedOnly; void refreshKey; untrack(() => load(id, only)); });
  onMount(() => { const timer = setInterval(() => load(), 10000); return () => clearInterval(timer); });
  onDestroy(() => { alive = false; sequence++; });

  const sourceLabel: Record<string, string> = { cron: 'Schedule', webhook: 'Webhook', api: 'API', tool: 'Agent tool' };
  const statusClass: Record<string, string> = {
    completed: 'text-green-400',
    running: 'text-blue-400',
    failed: 'text-red-400',
    interrupted: 'text-red-400',
    cancelled: 'text-dark-text-secondary',
  };

  function duration(run: WorkflowRunRecord): string {
    if (!run.finished_at) return '';
    const ms = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
    if (ms < 1000) return `${ms} ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
    return `${Math.floor(ms / 60000)} m ${Math.round((ms % 60000) / 1000)} s`;
  }
</script>

<div class="space-y-3">
  <div class="flex items-center justify-between gap-2">
    <p class="text-xs leading-relaxed text-dark-text-secondary">Schedule, webhook, API and agent-tool runs. Completed runs are kept 7 days, failed ones 30 days.</p>
  </div>
  <label class="flex items-center gap-2 text-xs text-dark-text">
    <input type="checkbox" bind:checked={failedOnly} class="border-dark-border-subtle" />
    Failed only
  </label>
  {#if error}<div role="alert" class="border p-3 text-xs border-red-900 text-red-400">{error}<button onclick={() => load()} class="ml-2 underline">Retry</button></div>{/if}
  {#each items as run (run.id)}
    <section class="border p-3 border-dark-border">
      <div class="flex items-start justify-between gap-2 text-xs">
        <span class="text-dark-text-secondary">{new Date(run.started_at).toLocaleString()}</span>
        <strong class={statusClass[run.status] ?? 'text-dark-text'}>{run.status}</strong>
      </div>
      <div class="mt-1 flex flex-wrap gap-x-3 text-xs text-dark-text-secondary">
        <span>{sourceLabel[run.source] ?? run.source}</span>
        {#if duration(run)}<span>{duration(run)}</span>{/if}
      </div>
      {#if run.failed_node_id}
        <p class="mt-2 text-xs text-dark-text">Failed at
          {#if onselectnode}
            <button onclick={() => onselectnode?.(run.failed_node_id)} class="font-mono underline">{run.failed_node_id}</button>
          {:else}
            <span class="font-mono">{run.failed_node_id}</span>
          {/if}
          <span class="text-dark-text-secondary">({run.failed_node_type})</span>
        </p>
      {/if}
      {#if run.error}<p class="mt-1 line-clamp-4 break-words text-xs text-red-400" title={run.error}>{run.error}</p>{/if}
      {#if run.handled_errors?.length}
        <button onclick={() => expanded[run.id] = !expanded[run.id]} class="mt-2 text-xs underline text-amber-400">
          {run.handled_errors.length} handled step failure{run.handled_errors.length === 1 ? '' : 's'}
        </button>
        {#if expanded[run.id]}
          <ul class="mt-1 space-y-1">
            {#each run.handled_errors as h}
              <li class="break-words text-xs text-dark-text"><span class="font-mono">{h.node_id}</span> ({h.node_type}): {h.error}</li>
            {/each}
          </ul>
        {/if}
      {/if}
      <p class="mt-2 break-all font-mono text-[10px] text-dark-text-secondary">{run.id}</p>
    </section>
  {:else}
    {#if !error}<p class="py-4 text-sm text-dark-text-secondary">{loading ? 'Loading run history…' : failedOnly ? 'No failed runs.' : 'No runs yet. Scheduled, webhook and API runs appear here.'}</p>{/if}
  {/each}
</div>
