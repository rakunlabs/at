<script lang="ts">
  import type { NodeRunState } from '@/lib/store/workflow-run.svelte';
  import { CheckCircle2, XCircle, Loader2, Clock, AlertTriangle } from 'lucide-svelte';
  import { Handle, getFlow } from 'kaykay';

  interface Props {
    state?: NodeRunState;
    nodeId?: string;
  }

  let { state, nodeId = '' }: Props = $props();
  const flow = getFlow();
  let node = $derived(flow?.getNode(nodeId));
  let errorOutput = $derived((node?.data.execution as { on_error?: string } | undefined)?.on_error === 'error_output');
</script>

{#if errorOutput}
  <Handle id="__error" type="output" port="data" position={node?.type.endsWith('_config') ? 'bottom' : 'top'} label="failure" />
{/if}

{#if state && state.status !== 'idle'}
  <div class="border-t border-gray-200 dark:border-gray-700">
    <div class="flex items-center gap-1.5 px-2 py-1 bg-gray-50 dark:bg-gray-800/30">
      {#if state.status === 'running'}
        <Loader2 size={10} class="text-blue-500 animate-spin" />
        <span class="text-[9px] text-blue-600 dark:text-blue-400 font-medium">{state.retry_delay_ms != null ? `Retry wait · ${state.retry_delay_ms}ms` : 'Running...'}{(state.max_attempts ?? 1) > 1 ? ` · ${state.attempt ?? 1}/${state.max_attempts}` : ''}</span>
      {:else if state.status === 'completed'}
        <CheckCircle2 size={10} class="text-green-500" />
        <span class="text-[9px] text-green-600 dark:text-green-400 font-medium">{state.pinned ? 'Pinned' : 'Done'}</span>
        {#if state.duration_ms != null}
          <span class="text-[9px] text-gray-400 ml-auto flex items-center gap-0.5">
            <Clock size={8} />
            {state.duration_ms < 1000 ? `${state.duration_ms}ms` : `${(state.duration_ms / 1000).toFixed(1)}s`}
          </span>
        {/if}
      {:else if state.status === 'error'}
        {#if state.error_policy}<AlertTriangle size={10} class="text-amber-600" />{:else}<XCircle size={10} class="text-red-500" />{/if}
        <span class="text-[9px] text-red-600 dark:text-red-400 font-medium">{state.error_policy ? 'Handled failure' : 'Error'}</span>
        {#if state.duration_ms != null}
          <span class="text-[9px] text-gray-400 ml-auto flex items-center gap-0.5">
            <Clock size={8} />
            {state.duration_ms < 1000 ? `${state.duration_ms}ms` : `${(state.duration_ms / 1000).toFixed(1)}s`}
          </span>
        {/if}
      {/if}
    </div>
  </div>
{/if}
