<script lang="ts">
  import { untrack, onDestroy } from 'svelte';
  import { router, push, replace } from 'svelte-spa-router';
  import { Activity, RefreshCw, Search, MessagesSquare, ListTree, Sparkles } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import FilterBar from '@/lib/components/traces/FilterBar.svelte';
  import TraceTable from '@/lib/components/traces/TraceTable.svelte';
  import TraceDetailView from '@/lib/components/traces/TraceDetailView.svelte';
  import SessionView from '@/lib/components/traces/SessionView.svelte';
  import ObservationInspector from '@/lib/components/traces/ObservationInspector.svelte';
  import ObservationIcon from '@/lib/components/traces/ObservationIcon.svelte';
  import Pagination from '@/lib/components/Pagination.svelte';
  import { listTraces, listTraceSessions, getTraceFacets, type TraceSummary, type TraceSession, type TraceFacets } from '@/lib/api/traces';
  import { listLLMCalls, type LLMCall } from '@/lib/api/llm-calls';
  import {
    type TraceFilters, filtersFromQuery, filtersToQuery, filtersToParams, timePresets, presetFrom,
    formatDurationMs, formatTokens, formatCost, observationType, observationLabel, isErrorObservation, formatTraceTime } from '@/lib/helper/trace-view';

  storeNavbar.title = 'Traces';

  // Route: /traces, /traces/:id (trace detail), /traces/sessions/:id.
  let { params = {} }: { params?: { id?: string | null; session?: string | null } } = $props();

  type Tab = 'traces' | 'sessions' | 'generations';
  const query = $derived(new URLSearchParams(router.querystring || ''));
  const traceID = $derived(params.id || '');
  const sessionID = $derived(params.session || '');
  const tab = $derived<Tab>((query.get('tab') as Tab) || 'traces');
  const observationID = $derived(query.get('obs') || '');
  const filters = $derived(filtersFromQuery(query));
  const range = $derived(query.get('range') || (filters.from ? 'custom' : '24h'));
  const offset = $derived(Math.max(Number(query.get('offset')) || 0, 0));
  let limit = $state(Number(localStorage.getItem('at.traces.limit')) || 50);
  let pageLimit = $state(untrack(() => limit));
  let pageOffset = $state(0);
  $effect(() => {
    pageOffset = offset;
  });

  let traces = $state<TraceSummary[]>([]);
  let sessions = $state<TraceSession[]>([]);
  let generations = $state<LLMCall[]>([]);
  let total = $state(0);
  let loading = $state(false);
  let error = $state('');
  let facets = $state<TraceFacets | null>(null);
  let search = $state('');
  let live = $state(false);
  let selectedGeneration = $state<LLMCall | null>(null);
  let request = 0;

  // Effective window: a preset is relative to now, re-evaluated on each load.
  function windowFrom(): string {
    return range === 'custom' ? filters.from : presetFrom(range);
  }

  function setQuery(values: Record<string, string | null>, replaceEntry = false) {
    const next = new URLSearchParams(router.querystring || '');
    for (const [k, v] of Object.entries(values)) {
      if (v === null || v === '') next.delete(k);
      else next.set(k, v);
    }
    const path = traceID ? `/traces/${encodeURIComponent(traceID)}` : sessionID ? `/traces/sessions/${encodeURIComponent(sessionID)}` : '/traces';
    const search = next.toString();
    (replaceEntry ? replace : push)(`${path}${search ? `?${search}` : ''}`);
  }
  function applyFilters(f: TraceFilters) {
    setQuery({ ...filtersToQuery(f), offset: null });
  }

  async function load() {
    if (traceID || sessionID) return;
    const current = ++request;
    loading = true;
    error = '';
    const from = windowFrom();
    try {
      if (tab === 'sessions') {
        const res = await listTraceSessions({ from, to: filters.to, q: filters.q, user_id: filters.user_id, source: filters.source, offset, limit });
        if (current !== request) return;
        sessions = res.data;
        total = res.meta.total;
      } else if (tab === 'generations') {
        const p: Record<string, any> = { _offset: offset, _limit: limit, _sort: '-created_at', 'observation_type[in]': 'generation,embedding' };
        if (from) p['started_at[gte]'] = from;
        if (filters.to) p['started_at[lt]'] = filters.to;
        if (filters.model.length) p['model[in]'] = filters.model.join(',');
        if (filters.source.length) p['source[in]'] = filters.source.join(',');
        if (filters.task_id.length) p['task_id[in]'] = filters.task_id.join(',');
        if (filters.status) p.status = filters.status;
        if (filters.min_latency_ms) p['latency_ms[gte]'] = filters.min_latency_ms;
        // Usage drill-downs: one provider / error code (Generations only).
        if (query.get('provider')) p.provider = query.get('provider');
        if (query.get('error_code')) p.error_code = query.get('error_code');
        if (filters.q) p['trace_id[like]'] = `%${filters.q}%`;
        const res = await listLLMCalls(p);
        if (current !== request) return;
        generations = res.data || [];
        total = res.meta?.total || 0;
      } else {
        const res = await listTraces({ ...filtersToParams(filters), from, offset, limit });
        if (current !== request) return;
        traces = res.data;
        total = res.meta.total;
      }
    } catch (e: any) {
      if (current === request) error = e?.response?.data?.message || 'Could not load traces.';
    } finally {
      if (current === request) loading = false;
    }
  }

  async function loadFacets() {
    try {
      facets = await getTraceFacets(presetFrom('30d'));
    } catch {
      facets = null;
    }
  }

  // Reload whenever the URL-held state changes.
  $effect(() => {
    void [tab, router.querystring, traceID, sessionID, limit];
    untrack(() => {
      search = filters.q;
      void load();
    });
  });
  loadFacets();

  // Live refresh polls the list while the tab is visible.
  let timer: ReturnType<typeof setInterval> | null = null;
  $effect(() => {
    if (timer) clearInterval(timer);
    timer = null;
    if (live && !traceID && !sessionID) {
      timer = setInterval(() => {
        if (document.visibilityState === 'visible') void load();
      }, 10_000);
    }
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  function setTab(next: Tab) {
    setQuery({ tab: next === 'traces' ? null : next, offset: null });
  }
  function setRange(key: string) {
    setQuery({ range: key === '24h' ? null : key, from: null, to: null, offset: null });
  }
  function onSearch(e: Event) {
    e.preventDefault();
    setQuery({ q: search.trim() || null, offset: null });
  }
  function onSort(field: string) {
    const same = (filters.sort || 'started_at') === field;
    const order = same && filters.order === 'desc' ? 'asc' : 'desc';
    setQuery({ sort: field === 'started_at' ? null : field, order: order === 'desc' ? null : 'asc', offset: null });
  }
  function setOffset(next: number) {
    setQuery({ offset: next ? String(next) : null });
  }
  function setLimit(next: number) {
    limit = next;
    localStorage.setItem('at.traces.limit', String(next));
  }

  function listQuery(): string {
    const keep = new URLSearchParams(router.querystring || '');
    keep.delete('obs');
    const s = keep.toString();
    return s ? `?${s}` : '';
  }
  function openTrace(id: string) {
    push(`/traces/${encodeURIComponent(id)}${listQuery()}`);
  }
  function openSession(id: string, token: string) {
    const q = new URLSearchParams(listQuery().slice(1));
    q.delete('obs');
    if (token) q.set('token_id', token); else q.delete('token_id');
    q.set('tab', 'sessions');
    push(`/traces/sessions/${encodeURIComponent(id)}?${q}`);
  }
  function backToList() {
    const q = new URLSearchParams(listQuery().slice(1));
    q.delete('token_id');
    const s = q.toString();
    push(`/traces${s ? `?${s}` : ''}`);
  }
  function selectObservation(id: string) {
    setQuery({ obs: id }, true);
  }
  function traceLink(id: string, obs?: string): string {
    return `#/traces/${encodeURIComponent(id)}${obs ? `?obs=${encodeURIComponent(obs)}` : ''}`;
  }
  const scoreNames = $derived(facets?.score_names || []);
  const tabs: { key: Tab; label: string; icon: typeof ListTree }[] = [
    { key: 'traces', label: 'Traces', icon: ListTree },
    { key: 'sessions', label: 'Sessions', icon: MessagesSquare },
    { key: 'generations', label: 'Generations', icon: Sparkles },
  ];
</script>

<svelte:head>
  <title>AT | Traces</title>
</svelte:head>

{#if traceID}
  <div class="h-[calc(100dvh-3rem)] min-h-0">
    <TraceDetailView
      {traceID}
      {observationID}
      {scoreNames}
      onback={backToList}
      onselect={selectObservation}
      onopentrace={openTrace}
      onopensession={openSession}
      linkTo={traceLink}
    />
  </div>
{:else if sessionID}
  <div class="h-[calc(100dvh-3rem)] min-h-0">
    <SessionView {sessionID} tokenID={query.get('token_id') || ''} onback={backToList} onopentrace={openTrace} />
  </div>
{:else}
  <div class="flex h-[calc(100dvh-3rem)] min-h-0 flex-col p-3 sm:p-4">
    <!-- Toolbar -->
    <div class="mb-2 flex flex-wrap items-center gap-2">
      <Activity size={16} class="text-dark-text-muted" />
      <h2 class="text-sm font-semibold text-dark-text">Traces</h2>
      <div class="ml-2 flex border border-dark-border" role="tablist">
        {#each tabs as { key, label, icon: Icon } (key)}
          <button role="tab" aria-selected={tab === key} onclick={() => setTab(key)} class={['inline-flex items-center gap-1 px-2.5 py-1 text-xs', tab === key ? 'bg-dark-text text-dark-base' : 'text-dark-text-secondary hover:bg-dark-elevated']}><Icon size={12} />{label}</button>
        {/each}
      </div>
      <div class="ml-auto flex flex-wrap items-center gap-2">
        <form onsubmit={onSearch} class="flex items-center border px-2 border-dark-border-subtle bg-dark-elevated">
          <Search size={12} class="text-dark-text-muted" />
          <input bind:value={search} placeholder={tab === 'sessions' ? 'Search sessions…' : tab === 'generations' ? 'Trace ID…' : 'Search ID, name, input…'} class="w-52 bg-transparent px-1.5 py-1 text-xs outline-none text-dark-text" aria-label="Search" />
        </form>
        <select value={range} onchange={(e) => setRange((e.target as HTMLSelectElement).value)} class="border px-2 py-1 text-xs border-dark-border-subtle bg-dark-elevated text-dark-text-secondary" aria-label="Time range">
          {#each timePresets as p}<option value={p.key}>{p.label}</option>{/each}
          <option value="all">All time</option>
          {#if range === 'custom'}<option value="custom">Custom</option>{/if}
        </select>
        <label class="flex items-center gap-1 text-xs text-dark-text-secondary" title="Refresh every 10 seconds">
          <input type="checkbox" bind:checked={live} /> Live
        </label>
        <button onclick={() => load()} class="border p-1 border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated" title="Refresh"><RefreshCw size={13} class={loading ? 'animate-spin motion-reduce:animate-none' : ''} /></button>
      </div>
    </div>

    {#if tab !== 'sessions'}
      <div class="mb-2 flex flex-wrap items-center gap-1.5">
        {#if tab === 'generations'}
          {#each ['provider', 'error_code'] as key}
            {#if query.get(key)}
              <button onclick={() => setQuery({ [key]: null, offset: null })} class="border px-2 py-0.5 text-[11px] hover:line-through border-dark-border-subtle bg-dark-elevated text-dark-text-secondary" title="Remove filter">{key === 'provider' ? 'Provider' : 'Error'}: {query.get(key)} ×</button>
            {/if}
          {/each}
        {/if}
        <FilterBar {filters} {facets} onchange={applyFilters} />
      </div>
    {/if}

    <div class="min-h-0 flex-1">
      {#if tab === 'traces'}
        <TraceTable {traces} {loading} {error} sort={filters.sort || 'started_at'} order={filters.order} onsort={onSort} onopen={(t) => openTrace(t.trace_id)} onretry={load} />
      {:else if tab === 'sessions'}
        <div class="max-h-[calc(100vh-13rem)] overflow-auto border border-dark-border bg-dark-surface">
          <table class="w-full text-xs">
            <thead class="sticky top-0 bg-dark-base">
              <tr class="border-b text-left text-[10px] uppercase tracking-wider border-dark-border text-dark-text-muted">
                <th class="px-2.5 py-1.5 font-medium">Last activity</th><th class="px-2.5 py-1.5 font-medium">Session</th><th class="px-2.5 py-1.5 font-medium">User</th>
                <th class="px-2.5 py-1.5 font-medium">Sources</th><th class="px-2.5 py-1.5 text-right font-medium">Traces</th><th class="px-2.5 py-1.5 text-right font-medium">Duration</th>
                <th class="px-2.5 py-1.5 text-right font-medium">Tokens</th><th class="px-2.5 py-1.5 text-right font-medium">Cost</th><th class="px-2.5 py-1.5 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {#if error}
                <tr><td colspan="9" class="py-8 text-center text-red-400">{error} <button class="underline" onclick={load}>Retry</button></td></tr>
              {:else if !loading && sessions.length === 0}
                <tr><td colspan="9" class="py-10 text-center text-dark-text-muted">No sessions in this window. Gateway clients group traces with <code class="font-mono">x-at-session-id</code>; Sessions and organization runs group automatically.</td></tr>
              {/if}
              {#each sessions as s (s.session_id + s.token_id)}
                <tr tabindex="0" class="cursor-pointer border-b focus:bg-dark-elevated focus:outline-none border-dark-border/60 hover:bg-dark-elevated/60" onclick={() => openSession(s.session_id, s.token_id || '')} onkeydown={(e) => e.key === 'Enter' && openSession(s.session_id, s.token_id || '')}>
                  <td class="whitespace-nowrap px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{formatTraceTime(s.ended_at)}</td>
                  <td class="max-w-64 px-2.5 py-1.5"><div class="truncate font-mono text-dark-text" title={s.session_id}>{s.session_id}</div>{#if s.name}<div class="truncate text-[11px] text-dark-text-muted">{s.name}</div>{/if}</td>
                  <td class="max-w-32 truncate px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{s.end_user || s.user_id || '–'}</td>
                  <td class="px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{s.sources.join(', ')}</td>
                  <td class="px-2.5 py-1.5 text-right font-mono">{s.trace_count}</td>
                  <td class="whitespace-nowrap px-2.5 py-1.5 text-right font-mono">{formatDurationMs(Date.parse(s.ended_at) - Date.parse(s.started_at))}</td>
                  <td class="px-2.5 py-1.5 text-right font-mono">{formatTokens(s.input_tokens + s.output_tokens)}</td>
                  <td class="px-2.5 py-1.5 text-right font-mono text-dark-text">{formatCost(s.cost_cents)}</td>
                  <td class="px-2.5 py-1.5">{#if s.error_count}<span class="px-1.5 py-0.5 text-[10px] bg-red-900/30 text-red-300">{s.error_count} errors</span>{:else}<span class="px-1.5 py-0.5 text-[10px] bg-green-900/30 text-green-300">ok</span>{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <div class="flex h-full min-h-0 gap-0 border border-dark-border">
          <div class="min-w-0 flex-1 overflow-auto bg-dark-surface">
            <table class="w-full text-xs">
              <thead class="sticky top-0 bg-dark-base">
                <tr class="border-b text-left text-[10px] uppercase tracking-wider border-dark-border text-dark-text-muted">
                  <th class="px-2.5 py-1.5 font-medium">Time</th><th class="px-2.5 py-1.5 font-medium">Model</th><th class="px-2.5 py-1.5 font-medium">Source</th>
                  <th class="px-2.5 py-1.5 text-right font-medium">In → out</th><th class="px-2.5 py-1.5 text-right font-medium">Latency</th><th class="px-2.5 py-1.5 text-right font-medium">TTFT</th><th class="px-2.5 py-1.5 text-right font-medium">Cost</th>
                </tr>
              </thead>
              <tbody>
                {#if error}
                  <tr><td colspan="7" class="py-8 text-center text-red-400">{error}</td></tr>
                {:else if !loading && generations.length === 0}
                  <tr><td colspan="7" class="py-10 text-center text-dark-text-muted">No generations match these filters.</td></tr>
                {/if}
                {#each generations as g (g.id)}
                  <tr tabindex="0" class={['cursor-pointer border-b focus:bg-dark-elevated focus:outline-none border-dark-border/60 hover:bg-dark-elevated/60', selectedGeneration?.id === g.id ? 'bg-dark-highest' : '']} onclick={() => (selectedGeneration = g)} onkeydown={(e) => e.key === 'Enter' && (selectedGeneration = g)}>
                    <td class="whitespace-nowrap px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{formatTraceTime(g.started_at || g.created_at)}</td>
                    <td class="max-w-56 px-2.5 py-1.5"><span class="flex items-center gap-1"><ObservationIcon type={observationType(g)} error={isErrorObservation(g)} /><span class="truncate font-mono">{observationLabel(g)}</span></span></td>
                    <td class="px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{g.source}</td>
                    <td class="whitespace-nowrap px-2.5 py-1.5 text-right font-mono">{formatTokens(g.input_tokens)} → {formatTokens(g.output_tokens)}</td>
                    <td class="px-2.5 py-1.5 text-right font-mono">{formatDurationMs(g.latency_ms)}</td>
                    <td class="px-2.5 py-1.5 text-right font-mono text-dark-text-muted">{g.time_to_first_token_ms ? formatDurationMs(g.time_to_first_token_ms) : '–'}</td>
                    <td class="px-2.5 py-1.5 text-right font-mono text-dark-text">{formatCost(g.cost_cents)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          {#if selectedGeneration}
            <div class="w-[45%] shrink-0 border-l border-dark-border">
              <div class="flex items-center justify-end border-b px-2 py-0.5 border-dark-border bg-dark-base">
                <button class="text-[11px] hover:underline text-blue-400" onclick={() => push(`/traces/${encodeURIComponent(selectedGeneration!.trace_id)}?obs=${encodeURIComponent(selectedGeneration!.id)}`)}>Open in trace →</button>
              </div>
              <div class="h-[calc(100%-1.5rem)]">
                <ObservationInspector observation={selectedGeneration} scores={[]} {scoreNames} onscoreschange={() => {}} onclose={() => (selectedGeneration = null)} onopentrace={openTrace} />
              </div>
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <div class="mt-2">
      <Pagination {total} bind:limit={pageLimit} bind:offset={pageOffset} onchange={(next) => { if (pageLimit !== limit) setLimit(pageLimit); setOffset(next); }} />
    </div>
  </div>
{/if}
