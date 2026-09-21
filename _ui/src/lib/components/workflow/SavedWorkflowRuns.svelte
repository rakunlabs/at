<script lang="ts">
  import { onMount, onDestroy, untrack } from 'svelte';
  import { X, RefreshCw } from 'lucide-svelte';
  import { listWorkflowExecutions, getWorkflowExecution, decideWorkflowExecution, type WorkflowExecutionRecord } from '@/lib/api/workflows';
  let { workflowId, refreshKey = 0, onclose }: { workflowId: string; refreshKey?: number; onclose: () => void } = $props();
  let items = $state<WorkflowExecutionRecord[]>([]);
  let details = $state<Record<string, WorkflowExecutionRecord>>({});
  let error = $state('');
  let loading = $state(false);
  let busy = $state('');
  let alive = true;
  let sequence = 0;
  async function load(id = workflowId) {
    if (!alive) return;
    const seq = ++sequence; loading = true;
    try { const data = await listWorkflowExecutions(id); if (alive && seq === sequence) { items = data; for (const item of data) { if (details[item.id] && details[item.id].revision !== item.revision) delete details[item.id]; } error = ''; } }
    catch (e: any) { if (alive && seq === sequence) error = e?.response?.data?.message || 'Could not load saved runs.'; }
    finally { if (alive && seq === sequence) loading = false; }
  }
  $effect(() => { const id = workflowId; void refreshKey; untrack(() => load(id)); });
  onMount(() => { const timer = setInterval(() => load(), 5000); return () => clearInterval(timer); });
  onDestroy(() => { alive = false; sequence++; });
  async function decide(item: WorkflowExecutionRecord, action: 'approve' | 'reject' | 'cancel') {
    busy = item.id;
    try { await decideWorkflowExecution(workflowId, item.id, item.revision, action); delete details[item.id]; await load(); }
    catch (e: any) { error = e?.response?.data?.message || 'The execution changed. Refresh and try again.'; }
    finally { if (alive) busy = ''; }
  }
  async function review(item: WorkflowExecutionRecord) {
    if (details[item.id]) { delete details[item.id]; return; }
    busy = item.id;
    try { const value = await getWorkflowExecution(workflowId, item.id); if (alive) details[item.id] = value; }
    catch (e: any) { if (alive) error = e?.response?.data?.message || 'Could not load execution data.'; }
    finally { if (alive) busy = ''; }
  }
</script>

<aside aria-label="Saved workflow runs" class="absolute inset-y-0 right-0 z-30 flex w-96 max-w-full shrink-0 flex-col border-l border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface xl:static">
  <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-border">
    <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Saved runs</h2>
    <div class="flex"><button onclick={() => load()} disabled={loading} aria-label="Refresh saved runs" class="min-h-11 min-w-11 p-2 text-gray-600 disabled:opacity-50 dark:text-dark-text-secondary sm:min-h-0 sm:min-w-0"><RefreshCw size={16} /></button><button onclick={onclose} aria-label="Close saved runs" class="min-h-11 min-w-11 p-2 text-gray-600 dark:text-dark-text-secondary sm:min-h-0 sm:min-w-0"><X size={16} /></button></div>
  </div>
  <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Up to 50 runs, pending runs first. Closing the editor does not stop them. Approvals resume under the original initiator, not the approver.</p>
    {#if error}<div role="alert" class="border border-red-300 p-3 text-xs text-red-700 dark:border-red-900 dark:text-red-400">{error}<button onclick={() => load()} class="ml-2 underline">Retry</button></div>{/if}
    {#each items as item (item.id)}
      <section class="border border-gray-200 p-3 dark:border-dark-border">
        <div class="flex items-start justify-between gap-2 text-xs"><span class="text-gray-600 dark:text-dark-text-secondary">{new Date(item.created_at).toLocaleString()}</span><strong class="text-gray-900 dark:text-dark-text">{item.status}</strong></div>
        <p class="mt-1 break-all font-mono text-xs text-gray-600 dark:text-dark-text-secondary">{item.id}</p>
        <p class="mt-1 break-all text-xs text-gray-600 dark:text-dark-text-secondary">Initiator: {item.owner_user_id}</p>
        {#if item.wait_prompt}<p class="mt-2 whitespace-pre-wrap text-sm text-gray-900 dark:text-dark-text">{item.wait_prompt}</p>{/if}
        {#if item.wait_node_id}<p class="mt-2 text-xs text-gray-600 dark:text-dark-text-secondary">Step: {item.wait_node_id}</p>{/if}
        {#if item.wake_at && item.status === 'waiting'}<p class="mt-1 text-xs text-gray-600 dark:text-dark-text-secondary">Resumes after {new Date(item.wake_at).toLocaleString()}</p>{/if}
        {#if item.expires_at && item.status === 'waiting'}<p class="mt-1 text-xs text-gray-600 dark:text-dark-text-secondary">Approval expires {new Date(item.expires_at).toLocaleString()}</p>{/if}
        {#if item.error}<p class="mt-2 break-words text-xs text-red-700 dark:text-red-400">{item.error}</p>{/if}
        <div class="mt-3 flex flex-wrap gap-2 text-xs">
          <button onclick={() => review(item)} disabled={busy === item.id} class="min-h-11 border border-gray-300 px-2 py-2 text-gray-700 dark:border-dark-border-subtle dark:text-dark-text sm:min-h-0">{details[item.id] ? 'Hide data' : 'Review data'}</button>
          {#if item.status === 'waiting' && item.wait_mode === 'approval'}
            <button onclick={() => decide(item, 'approve')} disabled={!!busy || item.can_decide === false} class="min-h-11 bg-green-700 px-3 py-2 text-white disabled:opacity-50 sm:min-h-0">Approve & resume</button>
            <button onclick={() => decide(item, 'reject')} disabled={!!busy || item.can_decide === false} class="min-h-11 border border-red-300 px-3 py-2 text-red-700 disabled:opacity-50 dark:border-red-900 dark:text-red-400 sm:min-h-0">Reject</button>
          {:else if ['queued', 'running', 'waiting'].includes(item.status)}
            <button onclick={() => decide(item, 'cancel')} disabled={!!busy || item.can_decide === false} class="min-h-11 border border-red-300 px-3 py-2 text-red-700 disabled:opacity-50 dark:border-red-900 dark:text-red-400 sm:min-h-0">Cancel run</button>
          {/if}
        </div>
        {#if details[item.id]}
          {#if details[item.id].in_flight}<p class="mt-2 text-xs text-gray-600 dark:text-dark-text-secondary">In-flight step: {details[item.id].in_flight}</p>{/if}
          <pre class="mt-3 max-h-72 overflow-auto whitespace-pre-wrap break-all bg-gray-50 p-2 text-xs text-gray-800 dark:bg-dark-base dark:text-dark-text">{JSON.stringify(details[item.id].waiting_data ?? details[item.id].outputs ?? {}, null, 2)}</pre>
        {/if}
      </section>
    {:else}
      {#if !error}<p class="py-4 text-sm text-gray-600 dark:text-dark-text-secondary">{loading ? 'Loading saved runs…' : 'No saved runs yet. Workflows with Wait are saved here when run.'}</p>{/if}
    {/each}
  </div>
</aside>
