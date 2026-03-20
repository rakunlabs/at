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
    <!-- Status bar -->
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

    <!-- Output preview -->
    {#if state.status === 'completed' && state.data}
      <div class="px-2 py-1 max-h-24 overflow-y-auto">
        {#each Object.entries(state.data) as [key, value]}
          <div class="flex gap-1 items-start mb-0.5">
            <span class="text-[9px] text-gray-400 shrink-0 font-mono">{key}:</span>
            {#if typeof value === 'string'}
              <span class="text-[10px] text-gray-600 dark:text-gray-300 break-all leading-snug line-clamp-3">{value}</span>
            {:else if typeof value === 'number' || typeof value === 'boolean'}
              <span class="text-[10px] text-gray-600 dark:text-gray-300 font-mono">{String(value)}</span>
            {:else if Array.isArray(value)}
              <span class="text-[10px] text-gray-500 font-mono">[{value.length} items]</span>
            {:else if value && typeof value === 'object'}
              <span class="text-[10px] text-gray-500 font-mono">{JSON.stringify(value).slice(0, 80)}{JSON.stringify(value).length > 80 ? '...' : ''}</span>
            {:else}
              <span class="text-[10px] text-gray-400">null</span>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    <!-- Error message -->
    {#if state.status === 'error' && state.error}
      <div class="px-2 py-1">
        <span class="text-[10px] text-red-500 break-all leading-snug line-clamp-2">{state.error}</span>
      </div>
    {/if}
  </div>
{/if}
