<script lang="ts" module>
  import type { NodeRunState } from '@/lib/workflow/run-events';

  export interface UpstreamSource {
    id: string;
    label: string;
    handle: string;
    state?: NodeRunState;
  }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import InputMapper from './InputMapper.svelte';
  import NodeDataView from './NodeDataView.svelte';

  let { data, ports, state: nodeState, upstream, disabled = false, runInputs }: {
    data: Record<string, any>;
    ports: { id: string; label?: string }[];
    state?: NodeRunState;
    /** Steps connected to this node's inputs, with their last-run state. */
    upstream: UpstreamSource[];
    disabled?: boolean;
    /** Workflow run inputs editor, shared with the Run panel. */
    runInputs?: Snippet;
  } = $props();

  // This node's own captured input is authoritative. Before it has run, the
  // upstream steps' outputs are the closest preview of what it will receive.
  let showUpstream = $derived(nodeState?.inputs === undefined && !nodeState?.inputs_omitted && !nodeState?.pinned);
</script>

<div class="space-y-3">
  {#if upstream.length === 0}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">This step has no incoming connection. When it is an entry point it receives the workflow run inputs below.</p>
    {#if runInputs}
      <div class="space-y-3 border border-gray-200 p-3 dark:border-dark-border">{@render runInputs()}</div>
    {/if}
  {/if}

  <InputMapper {data} {ports} state={nodeState} {disabled} />

  {#if showUpstream && upstream.length}
    {#each upstream as source (source.id + ':' + source.handle)}
      <div class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-border">
        <h3 class="break-all text-xs font-semibold text-gray-900 dark:text-dark-text">{source.label} <span class="font-normal text-gray-600 dark:text-dark-text-secondary">· output “{source.handle}” · last run</span></h3>
        {#if source.state?.data !== undefined || source.state?.data_omitted}
          <NodeDataView value={source.state.data} omitted={source.state.data_omitted} />
        {:else if source.state?.status === 'running'}
          <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Running…</p>
        {:else if source.state?.error}
          <p class="break-words text-xs text-red-700 dark:text-red-400">{source.state.error}</p>
        {:else}
          <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Not run yet. <strong>Execute step</strong> runs the upstream steps this one needs.</p>
        {/if}
      </div>
    {/each}
  {/if}

  {#if upstream.length > 0 && runInputs}
    <details class="border border-gray-200 p-3 text-xs dark:border-dark-border">
      <summary class="cursor-pointer text-gray-700 dark:text-dark-text-secondary">Workflow run inputs</summary>
      <div class="mt-3 space-y-3">{@render runInputs()}</div>
    </details>
  {/if}
</div>
