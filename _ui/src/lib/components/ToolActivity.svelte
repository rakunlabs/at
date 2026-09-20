<script lang="ts">
  import { Check, ChevronRight, CircleAlert, LoaderCircle, Wrench } from 'lucide-svelte';
  import { getTextContent, type ChatMessage, type ToolCall } from '../helper/chat';
  import { formatToolPayload, toolResultFailed } from '../helper/tool-activity';

  interface Props {
    call: ToolCall;
    result?: ChatMessage;
    source?: string;
    running?: boolean;
    queued?: boolean;
  }
  let { call, result, source = '', running = false, queued = false }: Props = $props();
  let open = $state(false);
  let output = $derived(result ? getTextContent(result.content) : '');
  let failed = $derived(result !== undefined && toolResultFailed(output));
  let status = $derived(result !== undefined ? failed ? 'Error' : 'Completed' : running ? 'Running' : queued ? 'Queued' : 'No result recorded');
</script>

<details class="min-w-0 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base" ontoggle={event => { open = event.currentTarget.open; }}>
  <summary class="flex min-h-11 cursor-pointer list-none flex-wrap items-center gap-2 px-3 py-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent [&::-webkit-details-marker]:hidden">
    <ChevronRight size={14} class={`shrink-0 ${open ? 'rotate-90' : ''}`} />
    <Wrench size={13} class="shrink-0 text-gray-500 dark:text-dark-text-muted" />
    <span class="min-w-0 flex-1 break-all font-mono font-medium text-gray-800 dark:text-dark-text">{call.function.name}</span>
    {#if source}<span class="text-gray-500 dark:text-dark-text-muted">{source}</span>{/if}
    <span class={['flex items-center gap-1', failed ? 'text-red-700 dark:text-red-400' : 'text-gray-600 dark:text-dark-text-secondary']}>
      {#if failed}<CircleAlert size={13} />{:else if result !== undefined}<Check size={13} />{:else if running}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{/if}
      {status}
    </span>
    <span class="basis-full pl-6 text-[11px] text-gray-500 dark:text-dark-text-muted">{open ? 'Click to collapse' : 'Click to expand · arguments and result'}</span>
  </summary>
  {#if open}
    <div class="min-w-0 space-y-3 border-t border-gray-200 dark:border-dark-border p-3">
      <section aria-label="Tool arguments">
        <h4 class="mb-1 text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Arguments</h4>
        <!-- Scrollable payloads need keyboard focus for arrow/PageDown scrolling. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <pre tabindex="0" class="max-h-64 overflow-auto overscroll-contain whitespace-pre-wrap break-words border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 text-xs text-gray-800 dark:text-dark-text focus-visible:outline-2 focus-visible:outline-accent">{formatToolPayload(call.function.arguments)}</pre>
      </section>
      <section aria-label="Tool result">
        <h4 class="mb-1 text-xs font-medium text-gray-600 dark:text-dark-text-secondary">Result</h4>
        {#if result !== undefined}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre tabindex="0" class="max-h-80 overflow-auto overscroll-contain whitespace-pre-wrap break-words border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 text-xs text-gray-800 dark:text-dark-text focus-visible:outline-2 focus-visible:outline-accent">{output ? formatToolPayload(output) : '(Empty result)'}</pre>
        {:else}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">{running ? 'Waiting for the tool to return…' : queued ? 'Waiting for earlier tools to finish.' : 'This conversation has no recorded result for this call.'}</p>
        {/if}
      </section>
    </div>
  {/if}
</details>
