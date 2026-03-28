<script lang="ts">
  import type { NodeRunState } from '@/lib/store/workflow-run.svelte';
  import { CheckCircle2, XCircle, Loader2, Clock } from 'lucide-svelte';

  interface Props {
    state?: NodeRunState;
  }

  let { state }: Props = $props();
</script>

{#if state && state.status !== 'idle'}
  <div class="border-t border-gray-200 dark:border-gray-700">
    <div class="flex items-center gap-1.5 px-2 py-1 bg-gray-50 dark:bg-gray-800/30">
      {#if state.status === 'running'}
        <Loader2 size={10} class="text-blue-500 animate-spin" />
        <span class="text-[9px] text-blue-600 dark:text-blue-400 font-medium">Running...</span>
      {:else if state.status === 'completed'}
        <CheckCircle2 size={10} class="text-green-500" />
        <span class="text-[9px] text-green-600 dark:text-green-400 font-medium">Done</span>
        {#if state.duration_ms != null}
          <span class="text-[9px] text-gray-400 ml-auto flex items-center gap-0.5">
            <Clock size={8} />
            {state.duration_ms < 1000 ? `${state.duration_ms}ms` : `${(state.duration_ms / 1000).toFixed(1)}s`}
          </span>
        {/if}
      {:else if state.status === 'error'}
        <XCircle size={10} class="text-red-500" />
        <span class="text-[9px] text-red-600 dark:text-red-400 font-medium">Error</span>
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
