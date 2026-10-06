<script lang="ts">
  import { untrack } from 'svelte';
  import { Copy, X, ExternalLink, Link2 } from 'lucide-svelte';
  import ObservationIcon from './ObservationIcon.svelte';
  import ChatMessages from './ChatMessages.svelte';
  import JsonView from './JsonView.svelte';
  import ScorePanel from './ScorePanel.svelte';
  import { getLLMCall, type LLMCall } from '@/lib/api/llm-calls';
  import type { TraceScore } from '@/lib/api/traces';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    observationType, observationLabel, isErrorObservation, observationTimes,
    parseRequestConversation, parseResponseMessage, requestParameters,
    formatDurationMs, formatTokens, formatCost, formatTraceTime } from '@/lib/helper/trace-view';

  interface Props {
    /** The selected observation as listed (bodies clipped); the full record is fetched. */
    observation: LLMCall;
    scores: TraceScore[];
    scoreNames: string[];
    onscoreschange: () => void;
    onclose?: () => void;
    onopentrace?: (traceID: string) => void;
    /** Builds a deep link to this observation. */
    linkTo?: (observationID: string) => string;
  }
  let { observation, scores, scoreNames, onscoreschange, onclose, onopentrace, linkTo }: Props = $props();

  type Tab = 'io' | 'metadata' | 'model' | 'scores' | 'raw';
  let tab = $state<Tab>('io');
  let full = $state<LLMCall | null>(null);
  let loading = $state(false);
  let request = 0;

  $effect(() => {
    const id = observation.id;
    untrack(() => load(id));
  });

  async function load(id: string) {
    const current = ++request;
    full = null;
    loading = true;
    try {
      const record = await getLLMCall(id);
      if (current === request) full = record;
    } catch (e: any) {
      if (current === request) addToast(e?.response?.data?.message || 'Could not load observation', 'alert');
    } finally {
      if (current === request) loading = false;
    }
  }

  const o = $derived(full ?? observation);
  const type = $derived(observationType(o));
  const isModelCall = $derived(type === 'generation' || type === 'embedding');
  const error = $derived(isErrorObservation(o));
  const times = $derived(observationTimes(o));
  const conversation = $derived(isModelCall ? parseRequestConversation(o.request_body) : null);
  const response = $derived(isModelCall ? parseResponseMessage(o.response_body) : null);
  const params = $derived(isModelCall ? requestParameters(o.request_body) : {});
  const bodiesMissing = $derived(isModelCall && !loading && !o.request_body && !o.response_body);
  const metadata = $derived(o.metadata && Object.keys(o.metadata).length ? o.metadata : null);
  const childTrace = $derived(typeof o.metadata?.['child_trace_id'] === 'string' ? (o.metadata['child_trace_id'] as string) : '');
  const tabs = $derived<{ key: Tab; label: string }[]>([
    { key: 'io', label: 'Input / Output' },
    { key: 'metadata', label: 'Metadata' },
    ...(isModelCall ? [{ key: 'model' as Tab, label: 'Model & usage' }] : []),
    { key: 'scores', label: `Scores${scores.filter((s) => s.observation_id === o.id).length ? ` (${scores.filter((s) => s.observation_id === o.id).length})` : ''}` },
    { key: 'raw', label: 'Raw JSON' },
  ]);
  $effect(() => {
    if (tab === 'model' && !isModelCall) tab = 'io';
  });

  async function copy(text: string, what = 'Copied') {
    try {
      await navigator.clipboard.writeText(text);
      addToast(what, 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }

  function kv(label: string, value: string | number | undefined | null, mono = true) {
    return { label, value: value === undefined || value === null || value === '' ? '–' : String(value), mono };
  }
  const modelRows = $derived([
    kv('Provider', o.provider),
    kv('Model', o.model),
    kv('Requested', o.requested_model),
    ...Object.entries(params).map(([k, v]) => kv(k, typeof v === 'object' ? JSON.stringify(v) : String(v))),
    kv('Input tokens', formatTokens(o.input_tokens)),
    kv('Output tokens', formatTokens(o.output_tokens)),
    kv('Cache read', formatTokens(o.cache_read_tokens)),
    kv('Cache write', formatTokens(o.cache_write_tokens)),
    kv('Reasoning tokens', formatTokens(o.reasoning_tokens)),
    kv('Cost', formatCost(o.cost_cents)),
    kv('Latency', formatDurationMs(o.latency_ms)),
    kv('Time to first token', o.time_to_first_token_ms ? formatDurationMs(o.time_to_first_token_ms) : ''),
    kv('Finish reason', o.finish_reason),
    kv('Streamed', o.streamed ? 'yes' : 'no'),
  ]);
  const contextRows = $derived([
    kv('Observation ID', o.id),
    kv('Started', o.started_at ? formatTraceTime(o.started_at) : formatTraceTime(o.created_at), false),
    kv('Duration', type === 'event' ? '' : formatDurationMs(times.end - times.start)),
    kv('Source', o.source),
    kv('Endpoint', o.endpoint),
    kv('Session', o.session_id),
    kv('User', o.user_id),
    kv('End user', o.user_field),
    kv('Environment', o.environment),
    kv('Release', o.release),
    kv('Agent', o.agent_id),
    kv('Task', o.task_id),
    kv('Token', o.token_id),
  ].filter((r) => r.value !== '–'));
</script>

<div class="flex h-full min-h-0 flex-col bg-dark-surface">
  <div class="sticky top-0 z-10 border-b border-dark-border bg-dark-surface">
    <div class="flex items-center gap-2 px-3 pt-2">
      <ObservationIcon {type} {error} label />
      <span class="min-w-0 truncate text-sm font-medium text-dark-text" title={observationLabel(o)}>{observationLabel(o)}</span>
      {#if error}<span class="shrink-0 px-1.5 py-0.5 text-[10px] bg-red-900/30 text-red-300">{o.error_code || 'error'}</span>{/if}
      <span class="ml-auto flex shrink-0 items-center gap-1">
        <button onclick={() => copy(o.id, 'Observation ID copied')} class="p-1 text-dark-text-muted hover:text-dark-text" title="Copy observation ID"><Copy size={13} /></button>
        {#if linkTo}<button onclick={() => copy(new URL(linkTo(o.id), document.baseURI).href, 'Link copied')} class="p-1 text-dark-text-muted hover:text-dark-text" title="Copy link to this observation"><Link2 size={13} /></button>{/if}
        {#if onclose}<button onclick={onclose} class="p-1 text-dark-text-muted hover:text-dark-text" title="Close (Esc)"><X size={14} /></button>{/if}
      </span>
    </div>
    <div class="flex flex-wrap gap-x-3 gap-y-0.5 px-3 pb-1.5 pt-1 font-mono text-[11px] text-dark-text-muted">
      {#if type !== 'event'}<span>{formatDurationMs(times.end - times.start)}</span>{/if}
      {#if isModelCall}<span>{formatTokens(o.input_tokens)} → {formatTokens(o.output_tokens)} tok</span><span>{formatCost(o.cost_cents)}</span>{/if}
      {#if o.model}<span>{o.model}</span>{/if}
      {#if childTrace && onopentrace}
        <button class="inline-flex items-center gap-0.5 font-sans hover:underline text-blue-400" onclick={() => onopentrace(childTrace)}><ExternalLink size={11} /> Child trace</button>
      {/if}
    </div>
    <div class="flex gap-0 overflow-x-auto px-1" role="tablist">
      {#each tabs as t (t.key)}
        <button role="tab" aria-selected={tab === t.key} onclick={() => (tab = t.key)} class={['whitespace-nowrap border-b-2 px-2.5 py-1.5 text-xs', tab === t.key ? 'font-medium border-dark-text text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}>{t.label}</button>
      {/each}
    </div>
  </div>

  <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
    {#if error && o.error_message}
      <div class="border p-2 text-xs border-red-900/40 bg-red-950/20 text-red-300"><span class="font-medium">{o.error_code || 'Error'}:</span> <span class="whitespace-pre-wrap break-words">{o.error_message}</span></div>
    {/if}

    {#if tab === 'io'}
      {#if loading}
        <div class="text-xs text-dark-text-muted">Loading payloads…</div>
      {:else if isModelCall}
        {#if bodiesMissing}
          <div class="border p-3 text-xs border-amber-900/30 bg-amber-900/10 text-amber-400">
            Request and response bodies are not available. Body capture (Settings → Features → Trace body capture) was off when this call ran, or the bodies passed the retention window.
          </div>
        {:else}
          <section>
            <h4 class="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-dark-text-muted">Input{#if o.request_truncated} <span class="normal-case text-amber-400">(truncated)</span>{/if}</h4>
            {#if conversation}
              <ChatMessages messages={conversation.messages} system={conversation.system} />
              {#if conversation.tools.length}
                <details class="mt-2 text-xs">
                  <summary class="cursor-pointer text-dark-text-muted">{conversation.tools.length} tools available</summary>
                  <div class="mt-1 flex flex-wrap gap-1">{#each conversation.tools as tool}<span class="border px-1.5 py-0.5 font-mono text-[10px] border-dark-border text-dark-text-secondary" title={tool.description}>{tool.name}</span>{/each}</div>
                </details>
              {/if}
            {:else}
              <JsonView raw={o.request_body} />
            {/if}
          </section>
          <section>
            <h4 class="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-dark-text-muted">Output{#if o.response_truncated} <span class="normal-case text-amber-400">(truncated)</span>{/if}</h4>
            {#if response}
              <ChatMessages messages={[response]} pairResults={false} />
            {:else if o.response_body}
              <JsonView raw={o.response_body} />
            {:else}
              <p class="text-xs text-dark-text-muted">No response.</p>
            {/if}
          </section>
        {/if}
      {:else}
        {#if o.input}
          <section>
            <h4 class="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-dark-text-muted">Input</h4>
            <JsonView raw={o.input} depth={3} />
          </section>
        {/if}
        {#if o.output}
          <section>
            <h4 class="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-dark-text-muted">Output</h4>
            <JsonView raw={o.output} depth={3} />
          </section>
        {/if}
        {#if !o.input && !o.output}
          <p class="text-xs text-dark-text-muted">This {type} has no input or output{type === 'event' ? '; see Metadata' : ''}.</p>
        {/if}
      {/if}
    {:else if tab === 'metadata'}
      <dl class="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
        {#each contextRows as row}
          <dt class="text-dark-text-muted">{row.label}</dt>
          <dd class={['truncate text-dark-text-secondary', row.mono ? 'font-mono' : '']} title={row.value}>
            {#if row.label === 'Task'}<a class="hover:underline" href={`#/tasks/${row.value}`}>{row.value}</a>{:else}{row.value}{/if}
          </dd>
        {/each}
      </dl>
      {#if metadata}
        <JsonView value={metadata} depth={2} />
      {:else}
        <p class="text-xs text-dark-text-muted">No metadata.</p>
      {/if}
    {:else if tab === 'model'}
      <dl class="grid grid-cols-[9rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
        {#each modelRows as row}
          <dt class="text-dark-text-muted">{row.label}</dt>
          <dd class="truncate font-mono text-dark-text-secondary" title={row.value}>{row.value}</dd>
        {/each}
      </dl>
    {:else if tab === 'scores'}
      <ScorePanel traceID={o.trace_id} observationID={o.id} {scores} names={scoreNames} onchange={onscoreschange} />
    {:else if tab === 'raw'}
      <JsonView value={o} depth={1} maxHeight="max-h-[70vh]" />
    {/if}
  </div>
</div>
