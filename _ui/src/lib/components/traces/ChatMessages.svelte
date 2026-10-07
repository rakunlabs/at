<script lang="ts">
  import { ChevronDown, ChevronRight, Wrench, Paperclip } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import JsonView from './JsonView.svelte';
  import type { ChatMessage } from '@/lib/helper/trace-view';

  interface Props {
    messages: ChatMessage[];
    system?: string;
    /** Tool results keyed by call ID, so calls render with their result. */
    pairResults?: boolean;
    /** Messages shown before the rest collapse behind "show earlier". */
    tail?: number;
  }
  let { messages, system = '', pairResults = true, tail = 12 }: Props = $props();

  let showAll = $state(false);
  let showSystem = $state(false);
  let rendered = $state<Record<number, boolean>>({});

  // Results answered by a later tool message are shown inside the call card
  // and the standalone result message is hidden.
  const resultsByCall = $derived.by(() => {
    const map = new Map<string, { content: string; isError?: boolean }>();
    if (!pairResults) return map;
    for (const m of messages) {
      if (m.role === 'tool' && m.toolCallID) map.set(m.toolCallID, { content: m.text });
      for (const r of m.toolResults) if (r.toolCallID) map.set(r.toolCallID, { content: r.content, isError: r.isError });
    }
    return map;
  });
  const callIDs = $derived(new Set(messages.flatMap((m) => m.toolCalls.map((c) => c.id).filter(Boolean) as string[])));
  const visible = $derived(messages.map((m, i) => ({ m, i })).filter(({ m }) => {
    if (!pairResults) return true;
    if (m.role === 'tool' && m.toolCallID && callIDs.has(m.toolCallID)) return false;
    // A user message that only carries paired tool results.
    if (m.role === 'user' && !m.text && !m.attachments.length && m.toolResults.length && m.toolResults.every((r) => r.toolCallID && callIDs.has(r.toolCallID))) return false;
    return true;
  }));
  const shown = $derived(showAll || visible.length <= tail ? visible : visible.slice(-tail));

  const roleStyle: Record<string, string> = {
    system: 'border-l-dark-border-subtle bg-dark-base',
    user: 'border-l-sky-500 bg-sky-950/20',
    assistant: 'border-l-emerald-500 bg-dark-surface',
    tool: 'border-l-amber-500 bg-amber-950/20',
  };
</script>

<div class="space-y-2">
  {#if system}
    <div class={['border border-l-2 border-dark-border', roleStyle.system]}>
      <button class="flex w-full items-center gap-1 px-3 py-1.5 text-left text-[11px] font-semibold text-dark-text-muted" onclick={() => (showSystem = !showSystem)} aria-expanded={showSystem}>
        {#if showSystem}<ChevronDown size={11} />{:else}<ChevronRight size={11} />{/if} System
        {#if !showSystem}<span class="ml-2 truncate font-normal normal-case tracking-normal text-dark-text-muted">{system.slice(0, 140)}</span>{/if}
      </button>
      {#if showSystem}
        <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-sans text-xs text-dark-text-secondary">{system}</pre>
      {/if}
    </div>
  {/if}

  {#if shown.length < visible.length}
    <button class="w-full border border-dashed py-1 text-[11px] border-dark-border text-dark-text-muted hover:bg-dark-elevated" onclick={() => (showAll = true)}>
      Show {visible.length - shown.length} earlier messages
    </button>
  {/if}

  {#each shown as { m, i } (i)}
    <div class={['border border-l-2 border-dark-border', roleStyle[m.role] || roleStyle.user]}>
      <div class="flex items-center justify-between px-3 pt-1.5">
        <span class="text-[11px] font-semibold text-dark-text-muted">{m.role}{#if m.toolCallID}<span class="ml-1 font-mono normal-case tracking-normal text-dark-text-muted">· {m.toolCallID}</span>{/if}</span>
        {#if m.text && m.role === 'assistant'}
          <button class="text-[10px] text-dark-text-muted hover:text-dark-text-secondary" onclick={() => (rendered[i] = !rendered[i])}>{rendered[i] ? 'Raw' : 'Markdown'}</button>
        {/if}
      </div>
      <div class="space-y-2 px-3 pb-2 pt-1">
        {#if m.reasoning}
          <details class="text-xs">
            <summary class="cursor-pointer text-[11px] text-dark-text-muted">Reasoning</summary>
            <pre class="mt-1 max-h-60 overflow-auto whitespace-pre-wrap break-words font-sans italic text-dark-text-muted">{m.reasoning}</pre>
          </details>
        {/if}
        {#if m.text}
          {#if rendered[i]}
            <Markdown source={m.text} safe class="text-xs" />
          {:else}
            <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-words font-sans text-xs text-dark-text">{m.text}</pre>
          {/if}
        {/if}
        {#each m.attachments as attachment}
          <span class="mr-1 inline-flex items-center gap-1 border px-1.5 py-0.5 text-[10px] border-dark-border text-dark-text-muted"><Paperclip size={10} />{attachment}</span>
        {/each}
        {#each m.toolCalls as call (call.id || call.name)}
          {@const result = call.id ? resultsByCall.get(call.id) : undefined}
          <details class="border border-dark-border bg-dark-base">
            <summary class="flex cursor-pointer items-center gap-1.5 px-2 py-1 text-xs">
              <Wrench size={11} class="text-purple-400" />
              <span class="font-mono font-medium text-dark-text">{call.name}</span>
              {#if call.id}<span class="font-mono text-[10px] text-dark-text-muted">{call.id}</span>{/if}
              {#if result?.isError}<span class="ml-auto text-[10px] text-red-400">error</span>{:else if result}<span class="ml-auto text-[10px] text-dark-text-muted">result</span>{/if}
            </summary>
            <div class="space-y-1.5 border-t p-2 border-dark-border">
              <div class="text-[11px] font-semibold text-dark-text-muted">Arguments</div>
              <JsonView raw={call.arguments} depth={3} maxHeight="max-h-48" searchable={false} />
              {#if result}
                <div class="text-[11px] font-semibold text-dark-text-muted">Result</div>
                <JsonView raw={result.content} depth={2} maxHeight="max-h-60" searchable={false} />
              {/if}
            </div>
          </details>
        {/each}
        {#each m.toolResults.filter((r) => !(pairResults && r.toolCallID && callIDs.has(r.toolCallID))) as result}
          <div class="border p-2 border-amber-900/40 bg-dark-base">
            <div class="mb-1 text-[11px] font-semibold text-amber-400">Tool result{#if result.toolCallID} <span class="font-mono normal-case">{result.toolCallID}</span>{/if}</div>
            <JsonView raw={result.content} depth={2} maxHeight="max-h-48" searchable={false} />
          </div>
        {/each}
        {#if !m.text && !m.reasoning && !m.toolCalls.length && !m.toolResults.length && !m.attachments.length}
          <span class="text-xs italic text-dark-text-muted">(empty)</span>
        {/if}
      </div>
    </div>
  {/each}
</div>
