<script lang="ts">
  import { Check, ChevronRight, CircleAlert, LoaderCircle, Wrench } from 'lucide-svelte';
  import { getTextContent, type ChatMessage, type ToolCall } from '../helper/chat';
  import { formatToolPayload, toolArgSummary, toolGlyph, toolResultFailed, toolResultSummary } from '../helper/tool-activity';

  interface Props {
    call: ToolCall;
    result?: ChatMessage;
    source?: string;
    /** One-line, human-readable description of what the call did. */
    summary?: string;
    running?: boolean;
    queued?: boolean;
    /** Single terminal-style line (Chats); expands to arguments and result. */
    compact?: boolean;
  }
  let { call, result, source = '', summary = '', running = false, queued = false, compact = false }: Props = $props();
  let open = $state(false);
  let output = $derived(result ? getTextContent(result.content) : '');
  let failed = $derived(result !== undefined && toolResultFailed(output));
  let status = $derived(result !== undefined ? failed ? 'Error' : 'Completed' : running ? 'Running' : queued ? 'Queued' : 'No result recorded');
  let arg = $derived(compact ? toolArgSummary(call.function.arguments) : '');
  let outcome = $derived(result !== undefined ? toolResultSummary(output, failed) : running ? 'running' : queued ? 'queued' : 'no result');
</script>

{#if compact}
  <details class="min-w-0" ontoggle={event => { open = event.currentTarget.open; }}>
    <summary
      title={`${call.function.name}${source ? ` · ${source}` : ''} — ${status}. Click for arguments and result.`}
      class="flex cursor-pointer list-none items-baseline gap-[1ch] py-px text-dark-text-muted hover:text-dark-text-secondary focus-visible:outline-1 focus-visible:outline-accent [&::-webkit-details-marker]:hidden"
    >
      <span class={['w-[1ch] shrink-0', failed ? 'text-[var(--oc-red)]' : running ? 'text-[var(--oc-peach)]' : 'text-dark-text-faint']}>{running ? '~' : toolGlyph(call.function.name)}</span>
      <span class="min-w-0 truncate">
        <span class="text-dark-text-secondary">{call.function.name}</span>
        {#if arg}<span> {arg}</span>{/if}
        <span class={failed ? 'text-[var(--oc-red)]' : running ? 'text-[var(--oc-peach)]' : 'text-dark-text-faint'}> ({outcome})</span>
      </span>
      {#if source}<span class="ml-auto hidden shrink-0 pl-2 text-dark-text-faint sm:inline">{source}</span>{/if}
    </summary>
    {#if summary}<p class="ml-[2ch] text-dark-text-muted">{summary}</p>{/if}
    {#if open}
      <div class="my-1.5 ml-[2ch] min-w-0 space-y-2 bg-dark-surface px-3 py-2.5 text-[0.93em]">
        <section aria-label="Tool arguments">
          <h4 class="text-dark-text-faint">arguments</h4>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre tabindex="0" class="max-h-64 overflow-auto overscroll-contain whitespace-pre-wrap break-words text-dark-text-secondary focus-visible:outline-1 focus-visible:outline-accent">{formatToolPayload(call.function.arguments)}</pre>
        </section>
        <section aria-label="Tool result">
          <h4 class="text-dark-text-faint">result</h4>
          {#if result !== undefined}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
            <pre tabindex="0" class={['max-h-80 overflow-auto overscroll-contain whitespace-pre-wrap break-words focus-visible:outline-1 focus-visible:outline-accent', failed ? 'text-[var(--oc-red)]' : 'text-dark-text-secondary']}>{output ? formatToolPayload(output) : '(Empty result)'}</pre>
          {:else}
            <p class="text-dark-text-muted">{running ? 'Waiting for the tool to return…' : queued ? 'Waiting for earlier tools to finish.' : 'This conversation has no recorded result for this call.'}</p>
          {/if}
        </section>
      </div>
    {/if}
  </details>
{:else}
<details class="min-w-0 border border-dark-border bg-dark-base" ontoggle={event => { open = event.currentTarget.open; }}>
  <summary class="flex min-h-11 cursor-pointer list-none flex-wrap items-center gap-2 px-3 py-2 text-xs hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent [&::-webkit-details-marker]:hidden">
    <ChevronRight size={14} class={`shrink-0 ${open ? 'rotate-90' : ''}`} />
    <Wrench size={13} class="shrink-0 text-dark-text-muted" />
    <span class="min-w-0 flex-1 break-all font-mono font-medium text-dark-text">{call.function.name}</span>
    {#if source}<span class="text-dark-text-muted">{source}</span>{/if}
    <span class={['flex items-center gap-1', failed ? 'text-red-400' : 'text-dark-text-secondary']}>
      {#if failed}<CircleAlert size={13} />{:else if result !== undefined}<Check size={13} />{:else if running}<LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" />{/if}
      {status}
    </span>
    {#if summary}<span class="basis-full pl-6 break-words text-dark-text-secondary">{summary}</span>{/if}
    <span class="basis-full pl-6 text-dark-text-muted">{open ? 'Click to collapse' : 'Click to expand · arguments and result'}</span>
  </summary>
  {#if open}
    <div class="min-w-0 space-y-3 border-t border-dark-border p-3">
      <section aria-label="Tool arguments">
        <h4 class="mb-1 text-xs font-medium text-dark-text-secondary">Arguments</h4>
        <!-- Scrollable payloads need keyboard focus for arrow/PageDown scrolling. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <pre tabindex="0" class="max-h-64 overflow-auto overscroll-contain whitespace-pre-wrap break-words border border-dark-border p-3 text-xs text-dark-text focus-visible:outline-2 focus-visible:outline-accent">{formatToolPayload(call.function.arguments)}</pre>
      </section>
      <section aria-label="Tool result">
        <h4 class="mb-1 text-xs font-medium text-dark-text-secondary">Result</h4>
        {#if result !== undefined}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre tabindex="0" class="max-h-80 overflow-auto overscroll-contain whitespace-pre-wrap break-words border border-dark-border p-3 text-xs text-dark-text focus-visible:outline-2 focus-visible:outline-accent">{output ? formatToolPayload(output) : '(Empty result)'}</pre>
        {:else}
          <p class="text-xs text-dark-text-muted">{running ? 'Waiting for the tool to return…' : queued ? 'Waiting for earlier tools to finish.' : 'This conversation has no recorded result for this call.'}</p>
        {/if}
      </section>
    </div>
  {/if}
</details>
{/if}
