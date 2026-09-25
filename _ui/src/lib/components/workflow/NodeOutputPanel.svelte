<script lang="ts">
  import type { NodeRunState } from '@/lib/workflow/run-events';
  import { pinUnavailableReason, type PinnedNode } from '@/lib/workflow/test-runs';
  import NodeDataView from './NodeDataView.svelte';
  let { state: nodeState, pinned, running = false, runError = '', onpin, onunpin }: {
    state?: NodeRunState;
    pinned?: PinnedNode;
    running?: boolean;
    /** Run-level failure of a step test targeting this node (e.g. invalid run inputs). */
    runError?: string;
    onpin: () => void;
    onunpin: () => void;
  } = $props();
  let pinReason = $derived(pinUnavailableReason(nodeState));
</script>

<div class="space-y-3">
  {#if runError}<p role="alert" class="break-words border border-red-300 bg-red-50 p-3 text-xs text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300">{runError}</p>{/if}
  {#if nodeState}
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Status: {nodeState.pinned ? 'Pinned output used (node not executed)' : nodeState.skipped ? 'Skipped — inactive branch' : nodeState.status}{nodeState.duration_ms != null ? ` · ${nodeState.duration_ms} ms` : ''}</p>
    {#if (nodeState.invocations ?? 0) > 1}
      <p class="text-xs text-gray-600 dark:text-dark-text-secondary">{nodeState.invocations} invocations — showing the latest-started invocation only.</p>
    {/if}
  {/if}
  {#if nodeState?.error}<p role="alert" class="break-words border border-red-300 bg-red-50 p-3 text-xs text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300">{nodeState.error}</p>{/if}
  {#if nodeState?.error_policy}<p class="text-xs text-amber-800 dark:text-amber-300">{nodeState.error_policy === 'error_output' ? 'Failure handled: sent to the failure output.' : 'Failure handled: skipped this branch; independent branches continue.'}</p>{/if}
  {#if nodeState?.attempt_history?.length}
    <details class="border border-gray-200 p-2 text-xs dark:border-dark-border" open={(nodeState.max_attempts ?? 1) > 1}>
      <summary class="cursor-pointer text-gray-700 dark:text-dark-text-secondary">Attempts ({nodeState.attempt_history.length}/{nodeState.max_attempts ?? 1})</summary>
      <ol class="mt-2 space-y-2">
        {#each nodeState.attempt_history as attempt (attempt.attempt)}
          <li class="break-words text-gray-700 dark:text-dark-text-secondary">Attempt {attempt.attempt}: {attempt.status}{attempt.duration_ms != null ? ` · ${attempt.duration_ms} ms` : ''}{#if attempt.error}<p class="mt-1 text-red-700 dark:text-red-400">{attempt.error}</p>{/if}</li>
        {/each}
      </ol>
      {#if nodeState.retry_delay_ms != null}<p class="mt-2 text-gray-600 dark:text-dark-text-secondary">Waiting {nodeState.retry_delay_ms} ms before retry.</p>{/if}
    </details>
  {/if}
  {#if pinned}
    <div class="space-y-2 border border-blue-200 bg-blue-50 p-3 text-xs text-blue-900 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-200">
      <p>Output pinned for this editor session. Production runs ignore pins.</p>
      <details><summary class="cursor-pointer py-1">View pinned data</summary><pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(pinned.data, null, 2)}</pre></details>
      <button onclick={onunpin} disabled={running} class="border border-blue-300 px-3 py-1.5 disabled:opacity-50 dark:border-blue-700">Unpin output</button>
    </div>
  {:else if nodeState}
    <button onclick={onpin} disabled={running || !!pinReason} class="border border-gray-300 px-3 py-1.5 text-xs text-gray-700 disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text">Pin output for tests</button>
    {#if pinReason}<p class="text-xs text-gray-600 dark:text-dark-text-secondary">{pinReason}</p>{/if}
  {/if}
  {#if nodeState?.status === 'running' && nodeState.data === undefined}
    <p class="text-sm text-gray-600 dark:text-dark-text-secondary">Running…</p>
  {:else if !nodeState}
    <p class="text-sm text-gray-600 dark:text-dark-text-secondary">No output yet. Use <strong>Execute step</strong> to run this step and the upstream steps it needs.</p>
  {:else}
    <NodeDataView value={nodeState.data} omitted={nodeState.data_omitted} />
  {/if}
</div>
