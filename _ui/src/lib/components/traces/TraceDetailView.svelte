<script lang="ts">
  import { untrack } from 'svelte';
  import { ArrowLeft, Copy, Bookmark, BookmarkCheck, RefreshCw, MessagesSquare, Star, Link2, ListTree, ChartGantt } from 'lucide-svelte';
  import TraceTree from './TraceTree.svelte';
  import ObservationInspector from './ObservationInspector.svelte';
  import ScorePanel from './ScorePanel.svelte';
  import ChatMessages from './ChatMessages.svelte';
  import { getTrace, setTraceBookmark, type TraceDetail } from '@/lib/api/traces';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    buildObservationTree, timelineOf, ancestorIDs, flattenTree, formatDurationMs, formatTokens, formatCost, formatScore, formatTraceTime } from '@/lib/helper/trace-view';

  interface Props {
    traceID: string;
    observationID: string;
    scoreNames: string[];
    onback: () => void;
    onselect: (observationID: string) => void;
    onopentrace: (traceID: string) => void;
    onopensession: (sessionID: string, tokenID: string) => void;
    linkTo: (traceID: string, observationID?: string) => string;
  }
  let { traceID, observationID, scoreNames, onback, onselect, onopentrace, onopensession, linkTo }: Props = $props();

  let detail = $state<TraceDetail | null>(null);
  let loading = $state(true);
  let failed = $state('');
  let collapsed = $state(new Set<string>());
  let view = $state<'waterfall' | 'tree'>((localStorage.getItem('at.traces.detail-view') as any) || 'waterfall');
  let inspectorWidth = $state(Number(localStorage.getItem('at.traces.inspector-width')) || 46);
  let panel = $state<'observation' | 'trace-scores' | 'trace-io'>('observation');
  let request = 0;

  $effect(() => {
    const id = traceID;
    untrack(() => load(id, true));
  });

  async function load(id: string, reset = false) {
    const current = ++request;
    loading = true;
    failed = '';
    if (reset) {
      detail = null;
      collapsed = new Set();
    }
    try {
      const d = await getTrace(id);
      if (current !== request) return;
      detail = d;
      // Deep link: reveal the requested observation; otherwise pick the root.
      const tree = buildObservationTree(d.observations);
      if (observationID && d.observations.some((o) => o.id === observationID)) {
        const open = new Set(collapsed);
        for (const ancestor of ancestorIDs(tree, observationID)) open.delete(ancestor);
        collapsed = open;
      } else if (reset && tree.length) {
        onselect(tree[0].obs.id);
      }
    } catch (e: any) {
      if (current === request) failed = e?.response?.status === 404 ? 'This trace does not exist or has been removed by retention.' : e?.response?.data?.message || 'Could not load the trace.';
    } finally {
      if (current === request) loading = false;
    }
  }

  const roots = $derived(detail ? buildObservationTree(detail.observations) : []);
  const timeline = $derived(timelineOf(roots));
  const selected = $derived(detail?.observations.find((o) => o.id === observationID) ?? null);
  const t = $derived(detail?.trace);
  const traceScores = $derived(detail ? detail.scores.filter((s) => !s.observation_id) : []);

  function toggle(id: string) {
    const next = new Set(collapsed);
    if (next.has(id)) next.delete(id); else next.add(id);
    collapsed = next;
  }
  function expandAll(open: boolean) {
    collapsed = open ? new Set() : new Set(flattenTree(roots).filter((n) => n.children.length && n.depth > 0).map((n) => n.obs.id));
  }
  function setView(v: 'waterfall' | 'tree') {
    view = v;
    localStorage.setItem('at.traces.detail-view', v);
  }
  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast(what, 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }
  async function toggleBookmark() {
    if (!detail) return;
    const next = !detail.trace.bookmarked;
    try {
      await setTraceBookmark(traceID, next);
      detail.trace.bookmarked = next;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not update bookmark', 'alert');
    }
  }

  // Panel resize: drag the divider; width persists per browser.
  let dragging = false;
  let host = $state<HTMLDivElement | null>(null);
  function startDrag(e: PointerEvent) {
    dragging = true;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }
  function drag(e: PointerEvent) {
    if (!dragging || !host) return;
    const rect = host.getBoundingClientRect();
    inspectorWidth = Math.min(Math.max(((rect.right - e.clientX) / rect.width) * 100, 25), 75);
  }
  function endDrag() {
    if (!dragging) return;
    dragging = false;
    localStorage.setItem('at.traces.inspector-width', String(Math.round(inspectorWidth)));
  }

  function onkeydown(e: KeyboardEvent) {
    const target = e.target as HTMLElement;
    if (target.closest('input, textarea, select, [contenteditable]')) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      onback();
    } else if (e.key === 'r' && !e.metaKey && !e.ctrlKey) {
      load(traceID);
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="flex h-full min-h-0 flex-col">
  <!-- Header -->
  <div class="border-b border-gray-200 bg-white px-4 py-2.5 dark:border-dark-border dark:bg-dark-surface">
    <div class="flex flex-wrap items-center gap-2">
      <button onclick={onback} class="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text" title="Back (Esc)"><ArrowLeft size={14} /> Traces</button>
      <span class="text-gray-300 dark:text-dark-border">/</span>
      <span class="text-[10px] font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-text-muted">Trace</span>
      <h2 class="min-w-0 truncate text-sm font-semibold text-gray-900 dark:text-dark-text">{t?.name || traceID}</h2>
      {#if t && t.error_count > 0}<span class="bg-red-100 px-1.5 py-0.5 text-[10px] text-red-700 dark:bg-red-900/30 dark:text-red-300">{t.error_count} error{t.error_count === 1 ? '' : 's'}</span>{/if}
      {#each t?.tags || [] as tag}<span class="border border-gray-200 px-1.5 py-0.5 text-[10px] text-gray-600 dark:border-dark-border dark:text-dark-text-secondary">{tag}</span>{/each}
      <div class="ml-auto flex items-center gap-1">
        <button onclick={() => copy(traceID, 'Trace ID copied')} class="inline-flex items-center gap-1 border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated" title="Copy trace ID"><Copy size={12} /> ID</button>
        <button onclick={() => copy(new URL(linkTo(traceID, observationID), document.baseURI).href, 'Link copied')} class="inline-flex items-center gap-1 border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated" title="Copy link"><Link2 size={12} /> Share</button>
        {#if t?.session_id}
          <button onclick={() => onopensession(t!.session_id, t!.token_id || '')} class="inline-flex items-center gap-1 border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated"><MessagesSquare size={12} /> Session</button>
        {/if}
        <button onclick={() => (panel = panel === 'trace-scores' ? 'observation' : 'trace-scores')} class={['inline-flex items-center gap-1 border px-2 py-1 text-xs', panel === 'trace-scores' ? 'border-gray-900 text-gray-900 dark:border-dark-text dark:text-dark-text' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}><Star size={12} /> Score</button>
        <button onclick={toggleBookmark} class="border border-gray-200 p-1 text-gray-600 hover:bg-gray-50 dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated" title={t?.bookmarked ? 'Remove bookmark' : 'Bookmark'}>
          {#if t?.bookmarked}<BookmarkCheck size={14} class="text-amber-500" />{:else}<Bookmark size={14} />{/if}
        </button>
        <button onclick={() => load(traceID)} class="p-1 text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text" title="Refresh (r)"><RefreshCw size={14} class={loading ? 'animate-spin motion-reduce:animate-none' : ''} /></button>
      </div>
    </div>
    {#if t}
      <dl class="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-[11px]">
        {#each [
          ['Trace ID', traceID.length > 14 ? `${traceID.slice(0, 6)}…${traceID.slice(-4)}` : traceID],
          ['Started', formatTraceTime(t.started_at)],
          ['Duration', formatDurationMs(t.duration_ms)],
          ['Tokens', `${formatTokens(t.input_tokens)} → ${formatTokens(t.output_tokens)}`],
          ['Cost', formatCost(t.cost_cents)],
          ['Observations', String(t.observation_count)],
          ...(t.user_id ? [['User', t.user_id]] : []),
          ...(t.end_user ? [['End user', t.end_user]] : []),
          ...(t.session_id ? [['Session', t.session_id]] : []),
          ...(t.environment ? [['Environment', t.environment]] : []),
          ...(t.release ? [['Release', t.release]] : []),
          ['Source', t.source],
        ] as [label, value]}
          <div class="min-w-0">
            <dt class="text-gray-400 dark:text-dark-text-muted">{label}</dt>
            <dd class="max-w-56 truncate font-mono text-gray-800 dark:text-dark-text-secondary" title={value}>{value}</dd>
          </div>
        {/each}
        {#each t.scores as score}
          <div><dt class="text-gray-400 dark:text-dark-text-muted">{score.name}</dt><dd class="font-mono font-semibold text-gray-900 dark:text-dark-text">{formatScore(score)}</dd></div>
        {/each}
      </dl>
      {#if t.input || t.output}
        <button class="mt-1.5 text-[11px] text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text" onclick={() => (panel = panel === 'trace-io' ? 'observation' : 'trace-io')}>{panel === 'trace-io' ? 'Hide' : 'Show'} trace input / output</button>
      {/if}
    {/if}
  </div>

  {#if failed}
    <div class="m-4 border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-900/40 dark:bg-red-950/20 dark:text-red-300">{failed}</div>
  {:else if !detail}
    <div class="p-8 text-center text-xs text-gray-400 dark:text-dark-text-muted">Loading trace…</div>
  {:else}
    {#if detail.truncated}
      <div class="border-b border-amber-200 bg-amber-50 px-4 py-1.5 text-[11px] text-amber-700 dark:border-amber-900/30 dark:bg-amber-900/10 dark:text-amber-400">This trace is very large; only the first {detail.observations.length} observations are shown.</div>
    {/if}
    <div bind:this={host} class="flex min-h-0 flex-1" role="presentation" onpointermove={drag} onpointerup={endDrag} onpointercancel={endDrag}>
      <div class="flex min-w-0 flex-1 flex-col">
        <div class="flex items-center gap-1 border-b border-gray-200 bg-gray-50 px-2 py-1 dark:border-dark-border dark:bg-dark-base">
          <button onclick={() => setView('waterfall')} class={['inline-flex items-center gap-1 px-2 py-0.5 text-[11px]', view === 'waterfall' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-elevated dark:text-dark-text' : 'text-gray-500 dark:text-dark-text-muted']}><ChartGantt size={12} /> Timeline</button>
          <button onclick={() => setView('tree')} class={['inline-flex items-center gap-1 px-2 py-0.5 text-[11px]', view === 'tree' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-elevated dark:text-dark-text' : 'text-gray-500 dark:text-dark-text-muted']}><ListTree size={12} /> Tree</button>
          <span class="ml-2 font-mono text-[10px] text-gray-400 dark:text-dark-text-muted">{formatDurationMs(timeline.duration)}</span>
          <span class="ml-auto flex gap-2 text-[11px] text-gray-500 dark:text-dark-text-muted">
            <button class="hover:text-gray-900 dark:hover:text-dark-text" onclick={() => expandAll(true)}>Expand all</button>
            <button class="hover:text-gray-900 dark:hover:text-dark-text" onclick={() => expandAll(false)}>Collapse</button>
          </span>
        </div>
        <div class="min-h-0 flex-1 overflow-auto bg-white dark:bg-dark-surface">
          {#if detail.observations.length === 0}
            <p class="p-6 text-center text-xs text-gray-400 dark:text-dark-text-muted">No observations in this trace.</p>
          {:else}
            <TraceTree {roots} {timeline} selectedID={observationID} {collapsed} waterfall={view === 'waterfall'} onselect={(id) => { panel = 'observation'; onselect(id); }} ontoggle={toggle} {onopentrace} />
          {/if}
        </div>
        <p class="border-t border-gray-200 bg-gray-50 px-2 py-0.5 text-[10px] text-gray-400 dark:border-dark-border dark:bg-dark-base dark:text-dark-text-muted">↑↓ / j k select · ← → collapse · Esc back · r refresh</p>
      </div>
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="w-1 shrink-0 cursor-col-resize bg-gray-200 hover:bg-gray-400 dark:bg-dark-border dark:hover:bg-dark-border-subtle" onpointerdown={startDrag} title="Drag to resize"></div>
      <div class="min-h-0 shrink-0 border-l border-gray-200 dark:border-dark-border" style:width={`${inspectorWidth}%`}>
        {#if panel === 'trace-scores'}
          <div class="h-full overflow-y-auto bg-white p-3 dark:bg-dark-surface">
            <h3 class="mb-2 text-xs font-semibold text-gray-900 dark:text-dark-text">Trace scores</h3>
            <ScorePanel traceID={traceID} scores={traceScores} names={scoreNames} onchange={() => load(traceID)} />
          </div>
        {:else if panel === 'trace-io'}
          <div class="h-full space-y-3 overflow-y-auto bg-white p-3 dark:bg-dark-surface">
            <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Trace input / output</h3>
            <ChatMessages messages={[
              ...(t?.input ? [{ role: 'user', text: t.input, toolCalls: [], toolResults: [], attachments: [] }] : []),
              ...(t?.output ? [{ role: 'assistant', text: t.output, toolCalls: [], toolResults: [], attachments: [] }] : []),
            ]} />
          </div>
        {:else if selected}
          <ObservationInspector observation={selected} scores={detail.scores} {scoreNames} onscoreschange={() => load(traceID)} {onopentrace} linkTo={(id) => linkTo(traceID, id)} />
        {:else}
          <div class="flex h-full items-center justify-center bg-white p-6 text-xs text-gray-400 dark:bg-dark-surface dark:text-dark-text-muted">Select an observation to inspect it.</div>
        {/if}
      </div>
    </div>
  {/if}
</div>
