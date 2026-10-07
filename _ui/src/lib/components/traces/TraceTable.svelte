<script lang="ts">
  import { ArrowDown, ArrowUp, Bookmark, Columns3 } from 'lucide-svelte';
  import type { TraceSummary } from '@/lib/api/traces';
  import { formatDurationMs, formatTokens, formatCost, formatScore, formatTraceTime } from '@/lib/helper/trace-view';

  interface Props {
    traces: TraceSummary[];
    loading: boolean;
    error: string;
    sort: string;
    order: 'asc' | 'desc';
    onsort: (field: string) => void;
    onopen: (trace: TraceSummary) => void;
    onretry: () => void;
  }
  let { traces, loading, error, sort, order, onsort, onopen, onretry }: Props = $props();

  type Column = { key: string; label: string; sort?: string; align?: 'right'; default: boolean };
  const columns: Column[] = [
    { key: 'time', label: 'Timestamp', sort: 'started_at', default: true },
    { key: 'name', label: 'Name', default: true },
    { key: 'input', label: 'Input', default: true },
    { key: 'output', label: 'Output', default: true },
    { key: 'user', label: 'User', default: true },
    { key: 'session', label: 'Session', default: true },
    { key: 'source', label: 'Source', default: false },
    { key: 'model', label: 'Model', default: false },
    { key: 'tags', label: 'Tags', default: false },
    { key: 'env', label: 'Env', default: false },
    { key: 'latency', label: 'Latency', sort: 'duration', align: 'right', default: true },
    { key: 'tokens', label: 'Tokens', sort: 'tokens', align: 'right', default: true },
    { key: 'cost', label: 'Cost', sort: 'cost', align: 'right', default: true },
    { key: 'obs', label: 'Obs', sort: 'observations', align: 'right', default: false },
    { key: 'scores', label: 'Scores', default: true },
    { key: 'status', label: 'Status', sort: 'errors', default: true },
  ];
  const storageKey = 'at.traces.columns';
  function initialColumns(): string[] {
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || 'null');
      if (Array.isArray(saved) && saved.every((k) => typeof k === 'string')) return saved;
    } catch { /* ignore */ }
    return columns.filter((c) => c.default).map((c) => c.key);
  }
  let visible = $state<string[]>(initialColumns());
  let pickerOpen = $state(false);
  let picker = $state<HTMLDivElement | null>(null);
  const shown = $derived(columns.filter((c) => visible.includes(c.key)));

  function toggleColumn(key: string) {
    visible = visible.includes(key) ? visible.filter((k) => k !== key) : columns.filter((c) => c.key === key || visible.includes(c.key)).map((c) => c.key);
    localStorage.setItem(storageKey, JSON.stringify(visible));
  }
  function clip(text: string | undefined, n = 80): string {
    if (!text) return '';
    const flat = text.replace(/\s+/g, ' ').trim();
    return flat.length > n ? flat.slice(0, n) + '…' : flat;
  }
  function shortID(id: string | undefined): string {
    if (!id) return '';
    return id.length > 14 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
  }
  // Keyboard: Enter on a focused row opens it.
  function rowKey(e: KeyboardEvent, t: TraceSummary) {
    if (e.key === 'Enter') onopen(t);
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const row = (e.currentTarget as HTMLElement)[e.key === 'ArrowDown' ? 'nextElementSibling' : 'previousElementSibling'] as HTMLElement | null;
      row?.focus();
    }
  }
</script>

<svelte:window onclick={(e) => { if (pickerOpen && picker && !e.composedPath().includes(picker)) pickerOpen = false; }} />

<div class="relative border border-dark-border">
  <div bind:this={picker} class="absolute right-0.5 top-0.5 z-20 bg-dark-base">
    <button onclick={() => (pickerOpen = !pickerOpen)} class="p-1 text-dark-text-muted hover:text-dark-text" title="Columns" aria-expanded={pickerOpen}><Columns3 size={13} /></button>
    {#if pickerOpen}
      <div class="absolute right-0 top-full mt-1 w-44 border py-1 shadow-lg border-dark-border bg-dark-surface">
        {#each columns as c (c.key)}
          <label class="flex cursor-pointer items-center gap-2 px-3 py-1 text-xs text-dark-text-secondary hover:bg-dark-elevated">
            <input type="checkbox" checked={visible.includes(c.key)} onchange={() => toggleColumn(c.key)} /> {c.label}
          </label>
        {/each}
      </div>
    {/if}
  </div>
  <div class="max-h-[calc(100vh-15rem)] overflow-auto">
    <table class="w-full border-collapse text-xs" aria-label="Traces">
      <thead class="sticky top-0 z-10 bg-dark-base">
        <tr class="border-b border-dark-border">
          {#each shown as c, i (c.key)}
            <th class={['whitespace-nowrap px-2.5 py-1.5', i === shown.length - 1 ? 'pr-8' : '', 'text-[11px] font-medium text-dark-text-muted', c.align === 'right' ? 'text-right' : 'text-left']}>
              {#if c.sort}
                <button onclick={() => onsort(c.sort!)} class={['inline-flex items-center gap-0.5 hover:text-dark-text', sort === c.sort || (!sort && c.sort === 'started_at') ? 'text-dark-text' : '']}>
                  {c.label}
                  {#if sort === c.sort || (!sort && c.sort === 'started_at')}{#if order === 'asc'}<ArrowUp size={10} />{:else}<ArrowDown size={10} />{/if}{/if}
                </button>
              {:else}{c.label}{/if}
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#if error}
          <tr><td colspan={shown.length} class="px-3 py-8 text-center text-xs text-red-400">{error} <button class="ml-2 underline" onclick={onretry}>Retry</button></td></tr>
        {:else if loading && traces.length === 0}
          <tr><td colspan={shown.length} class="px-3 py-8 text-center text-xs text-dark-text-muted">Loading traces…</td></tr>
        {:else if traces.length === 0}
          <tr><td colspan={shown.length} class="px-3 py-10 text-center text-xs text-dark-text-muted">No traces match these filters. Run an agent, a workflow or send a gateway request to see traces here.</td></tr>
        {/if}
        {#each traces as t (t.trace_id)}
          <tr tabindex="0" class={['cursor-pointer border-b align-top focus:outline-none border-dark-border/60 hover:bg-dark-elevated/60 focus:bg-dark-elevated', loading ? 'opacity-60' : '']} onclick={() => onopen(t)} onkeydown={(e) => rowKey(e, t)}>
            {#each shown as c (c.key)}
              {#if c.key === 'time'}
                <td class="whitespace-nowrap px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{formatTraceTime(t.started_at)}</td>
              {:else if c.key === 'name'}
                <td class="max-w-48 px-2.5 py-1.5"><span class="flex items-center gap-1">{#if t.bookmarked}<Bookmark size={10} class="shrink-0 fill-amber-400 text-amber-500" />{/if}<span class="truncate font-medium text-dark-text" title={t.name || t.trace_id}>{t.name || shortID(t.trace_id)}</span></span></td>
              {:else if c.key === 'input'}
                <td class="max-w-56 px-2.5 py-1.5 text-dark-text-secondary"><span class="line-clamp-1 break-all" title={t.input}>{clip(t.input) || '–'}</span></td>
              {:else if c.key === 'output'}
                <td class="max-w-56 px-2.5 py-1.5 text-dark-text-secondary"><span class="line-clamp-1 break-all" title={t.output}>{clip(t.output) || '–'}</span></td>
              {:else if c.key === 'user'}
                <td class="max-w-32 truncate px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted" title={t.end_user || t.user_id}>{t.end_user || shortID(t.user_id) || '–'}</td>
              {:else if c.key === 'session'}
                <td class="max-w-32 truncate px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted" title={t.session_id}>{shortID(t.session_id) || '–'}</td>
              {:else if c.key === 'source'}
                <td class="px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{t.source}</td>
              {:else if c.key === 'model'}
                <td class="max-w-40 truncate px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted" title={t.models.join(', ')}>{t.models.join(', ') || '–'}</td>
              {:else if c.key === 'tags'}
                <td class="px-2.5 py-1.5">{#each t.tags.slice(0, 3) as tag}<span class="mr-1 border px-1 text-[10px] border-dark-border text-dark-text-secondary">{tag}</span>{/each}</td>
              {:else if c.key === 'env'}
                <td class="px-2.5 py-1.5 font-mono text-[11px] text-dark-text-muted">{t.environment || '–'}</td>
              {:else if c.key === 'latency'}
                <td class="whitespace-nowrap px-2.5 py-1.5 text-right font-mono text-[11px] text-dark-text-secondary">{formatDurationMs(t.duration_ms)}</td>
              {:else if c.key === 'tokens'}
                <td class="whitespace-nowrap px-2.5 py-1.5 text-right font-mono text-[11px] text-dark-text-secondary" title={`${t.input_tokens} in / ${t.output_tokens} out`}>{formatTokens(t.total_tokens)}</td>
              {:else if c.key === 'cost'}
                <td class="whitespace-nowrap px-2.5 py-1.5 text-right font-mono text-[11px] text-dark-text">{formatCost(t.cost_cents)}</td>
              {:else if c.key === 'obs'}
                <td class="px-2.5 py-1.5 text-right font-mono text-[11px] text-dark-text-muted">{t.observation_count}</td>
              {:else if c.key === 'scores'}
                <td class="px-2.5 py-1.5">{#each t.scores.slice(0, 3) as s}<span class="mr-1 whitespace-nowrap font-mono text-[10px] text-dark-text-secondary" title={`${s.name} (${s.count})`}>{s.name.slice(0, 10)} <b class="text-dark-text">{formatScore(s)}</b></span>{/each}</td>
              {:else if c.key === 'status'}
                <td class="px-2.5 py-1.5">
                  {#if t.error_count > 0}
                    <span class="px-1.5 py-0.5 text-[10px] bg-red-900/30 text-red-300">{t.error_count} error{t.error_count === 1 ? '' : 's'}</span>
                  {:else}
                    <span class="px-1.5 py-0.5 text-[10px] bg-green-900/30 text-green-300">ok</span>
                  {/if}
                </td>
              {/if}
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
