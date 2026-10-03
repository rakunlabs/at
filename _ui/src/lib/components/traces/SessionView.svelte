<script lang="ts">
  import { untrack } from 'svelte';
  import { ArrowLeft, Copy, ExternalLink, RefreshCw } from 'lucide-svelte';
  import ChatMessages from './ChatMessages.svelte';
  import { getTraceSession, type TraceSessionDetail } from '@/lib/api/traces';
  import { addToast } from '@/lib/store/toast.svelte';
  import { formatDurationMs, formatTokens, formatCost, formatTraceTime } from '@/lib/helper/trace-view';

  interface Props {
    sessionID: string;
    tokenID: string;
    onback: () => void;
    onopentrace: (traceID: string) => void;
  }
  let { sessionID, tokenID, onback, onopentrace }: Props = $props();

  let detail = $state<TraceSessionDetail | null>(null);
  let loading = $state(true);
  let failed = $state('');
  let request = 0;

  $effect(() => {
    const id = sessionID;
    const token = tokenID;
    untrack(() => load(id, token));
  });

  async function load(id: string, token: string) {
    const current = ++request;
    loading = true;
    failed = '';
    try {
      const d = await getTraceSession(id, token);
      if (current === request) detail = d;
    } catch (e: any) {
      if (current === request) failed = e?.response?.status === 404 ? 'This session has no traces in this workspace.' : e?.response?.data?.message || 'Could not load the session.';
    } finally {
      if (current === request) loading = false;
    }
  }

  const s = $derived(detail?.session);
  const duration = $derived(s ? Date.parse(s.ended_at) - Date.parse(s.started_at) : 0);
  const missingIO = $derived(detail ? detail.traces.every((t) => !t.input && !t.output) : false);

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Session ID copied', 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="border-b border-gray-200 bg-white px-4 py-2.5 dark:border-dark-border dark:bg-dark-surface">
    <div class="flex flex-wrap items-center gap-2">
      <button onclick={onback} class="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text"><ArrowLeft size={14} /> Sessions</button>
      <span class="text-gray-300 dark:text-dark-border">/</span>
      <span class="text-[10px] font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-text-muted">Session</span>
      <h2 class="min-w-0 truncate font-mono text-sm font-semibold text-gray-900 dark:text-dark-text">{sessionID}</h2>
      <button onclick={() => copy(sessionID)} class="p-1 text-gray-400 hover:text-gray-700 dark:text-dark-text-muted" title="Copy session ID"><Copy size={12} /></button>
      <button onclick={() => load(sessionID, tokenID)} class="ml-auto p-1 text-gray-400 hover:text-gray-700 dark:text-dark-text-muted" title="Refresh"><RefreshCw size={14} class={loading ? 'animate-spin motion-reduce:animate-none' : ''} /></button>
    </div>
    {#if s}
      <dl class="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-[11px]">
        {#each [
          ['User', s.end_user || s.user_id || '–'],
          ['Started', formatTraceTime(s.started_at)],
          ['Duration', formatDurationMs(duration)],
          ['Traces', String(s.trace_count)],
          ['Tokens', `${formatTokens(s.input_tokens)} → ${formatTokens(s.output_tokens)}`],
          ['Cost', formatCost(s.cost_cents)],
          ['Errors', String(s.error_count)],
          ['Sources', s.sources.join(', ')],
        ] as [label, value]}
          <div><dt class="text-gray-400 dark:text-dark-text-muted">{label}</dt><dd class="max-w-56 truncate font-mono text-gray-800 dark:text-dark-text-secondary" title={value}>{value}</dd></div>
        {/each}
      </dl>
    {/if}
  </div>

  <div class="min-h-0 flex-1 overflow-y-auto bg-gray-50 p-4 dark:bg-dark-base">
    {#if failed}
      <div class="border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-900/40 dark:bg-red-950/20 dark:text-red-300">{failed}</div>
    {:else if !detail}
      <p class="text-center text-xs text-gray-400 dark:text-dark-text-muted">Loading session…</p>
    {:else}
      {#if missingIO}
        <p class="mb-3 border border-amber-200 bg-amber-50 p-2 text-[11px] text-amber-700 dark:border-amber-900/30 dark:bg-amber-900/10 dark:text-amber-400">Conversation text is captured only while trace body capture is on and is removed with bodies after the retention window. The traces below remain available.</p>
      {/if}
      <ol class="mx-auto max-w-4xl space-y-4">
        {#each detail.traces as t, i (t.trace_id)}
          <li>
            <div class="mb-1 flex items-center gap-2 text-[11px] text-gray-500 dark:text-dark-text-muted">
              <span class="font-mono">#{i + 1}</span>
              <span>{formatTraceTime(t.started_at)}</span>
              <span class="font-mono">{formatDurationMs(t.duration_ms)} · {formatTokens(t.total_tokens)} tok · {formatCost(t.cost_cents)}</span>
              {#if t.error_count}<span class="text-red-600 dark:text-red-400">{t.error_count} error{t.error_count === 1 ? '' : 's'}</span>{/if}
              <button class="ml-auto inline-flex items-center gap-1 text-blue-600 hover:underline dark:text-blue-400" onclick={() => onopentrace(t.trace_id)}><ExternalLink size={11} /> {t.name || 'Open trace'}</button>
            </div>
            {#if t.input || t.output}
              <ChatMessages messages={[
                ...(t.input ? [{ role: 'user', text: t.input, toolCalls: [], toolResults: [], attachments: [] }] : []),
                ...(t.output ? [{ role: 'assistant', text: t.output, toolCalls: [], toolResults: [], attachments: [] }] : []),
              ]} />
            {:else}
              <button class="w-full border border-dashed border-gray-300 bg-white p-2 text-left text-xs text-gray-500 hover:bg-gray-50 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text-muted" onclick={() => onopentrace(t.trace_id)}>{t.observation_count} observations — open the trace to inspect them.</button>
            {/if}
          </li>
        {/each}
      </ol>
      {#if s && s.trace_count > detail.traces.length}
        <p class="mt-4 text-center text-[11px] text-gray-400 dark:text-dark-text-muted">Showing the first {detail.traces.length} of {s.trace_count} traces.</p>
      {/if}
    {/if}
  </div>
</div>
