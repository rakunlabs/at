<script lang="ts">
  import { Plus, X } from 'lucide-svelte';
  import type { TraceFacets } from '@/lib/api/traces';
  import { type TraceFilters, type FilterChip, filterChips, removeChip } from '@/lib/helper/trace-view';

  interface Props {
    filters: TraceFilters;
    facets: TraceFacets | null;
    onchange: (filters: TraceFilters) => void;
  }
  let { filters, facets, onchange }: Props = $props();

  type Field = { key: string; label: string; kind: 'list' | 'range' | 'status' | 'score' | 'text'; options?: () => string[] };
  const fields: Field[] = [
    { key: 'name', label: 'Trace name', kind: 'list', options: () => facets?.names || [] },
    { key: 'model', label: 'Model', kind: 'list', options: () => facets?.models || [] },
    { key: 'source', label: 'Source', kind: 'list', options: () => facets?.sources || [] },
    { key: 'tag', label: 'Tag', kind: 'list', options: () => facets?.tags || [] },
    { key: 'environment', label: 'Environment', kind: 'list', options: () => facets?.environments || [] },
    { key: 'release', label: 'Release', kind: 'list', options: () => facets?.releases || [] },
    { key: 'user_id', label: 'User', kind: 'list', options: () => facets?.user_ids || [] },
    { key: 'end_user', label: 'End user', kind: 'list', options: () => facets?.end_users || [] },
    { key: 'session_id', label: 'Session', kind: 'text' },
    { key: 'task_id', label: 'Task', kind: 'text' },
    { key: 'latency', label: 'Latency (ms)', kind: 'range' },
    { key: 'cost', label: 'Cost (cents)', kind: 'range' },
    { key: 'tokens', label: 'Token count', kind: 'range' },
    { key: 'status', label: 'Error status', kind: 'status' },
    { key: 'score', label: 'Score', kind: 'score', options: () => facets?.score_names || [] },
  ];

  let open = $state(false);
  let field = $state<Field | null>(null);
  let value = $state('');
  let min = $state('');
  let max = $state('');
  let status = $state<'error' | 'ok'>('error');
  let menu = $state<HTMLDivElement | null>(null);

  const chips = $derived(filterChips(filters));
  const suggestions = $derived.by(() => {
    if (!field?.options) return [];
    const q = value.trim().toLowerCase();
    const current = (filters as any)[field.key] as string[] | undefined;
    return field.options().filter((o) => (!q || o.toLowerCase().includes(q)) && !(current || []).includes(o)).slice(0, 40);
  });

  function pick(f: Field) {
    field = f;
    value = min = max = '';
    status = 'error';
  }
  function close() {
    open = false;
    field = null;
  }
  // Number inputs bind numbers (or null when cleared), not strings.
  const text = (v: unknown) => (v === null || v === undefined ? '' : String(v).trim());
  function apply(v = value) {
    if (!field) return;
    const lo = text(min);
    const hi = text(max);
    const next: TraceFilters = { ...filters };
    const range = (loKey: keyof TraceFilters, hiKey: keyof TraceFilters) => {
      (next as any)[loKey] = lo;
      (next as any)[hiKey] = hi;
    };
    switch (field.kind) {
      case 'list':
      case 'text': {
        const values = v.split(',').map((s) => s.trim()).filter(Boolean);
        if (!values.length) return;
        const key = field.key as 'name';
        next[key] = Array.from(new Set([...filters[key], ...values]));
        break;
      }
      case 'range':
        if (!lo && !hi) return;
        if (field.key === 'latency') range('min_latency_ms', 'max_latency_ms');
        if (field.key === 'cost') range('min_cost_cents', 'max_cost_cents');
        if (field.key === 'tokens') range('min_tokens', 'max_tokens');
        break;
      case 'status':
        next.status = status;
        break;
      case 'score':
        if (!v.trim()) return;
        next.score_name = v.trim();
        next.min_score = lo;
        next.max_score = hi;
        break;
    }
    onchange(next);
    close();
  }
  function remove(chip: FilterChip) {
    onchange(removeChip(filters, chip));
  }
  // composedPath is captured at dispatch, so a click on an option that the
  // menu re-renders away still counts as inside.
  function onwindowclick(e: MouseEvent) {
    if (open && menu && !e.composedPath().includes(menu)) close();
  }
</script>

<svelte:window onclick={onwindowclick} onkeydown={(e) => { if (open && e.key === 'Escape') { e.stopPropagation(); close(); } }} />

<div class="flex flex-wrap items-center gap-1.5">
  {#each chips as chip (chip.key + (chip.value || ''))}
    <span class="inline-flex items-center gap-1 border py-0.5 pl-2 pr-1 text-[11px] border-dark-border-subtle bg-dark-elevated text-dark-text-secondary">
      <span class="max-w-60 truncate" title={chip.label}>{chip.label}</span>
      <button onclick={() => remove(chip)} class="text-dark-text-muted hover:text-dark-text" aria-label={`Remove ${chip.label}`}><X size={11} /></button>
    </span>
  {/each}
  <div class="relative" bind:this={menu}>
    <button onclick={(e) => { e.stopPropagation(); open = !open; field = null; }} class="inline-flex items-center gap-1 border border-dashed px-2 py-0.5 text-[11px] hover:border-dark-border-subtle border-dark-border-subtle text-dark-text-muted hover:text-dark-text">
      <Plus size={11} /> Add filter
    </button>
    {#if open}
      <div class="absolute left-0 top-full z-30 mt-1 w-72 border shadow-lg border-dark-border bg-dark-surface">
        {#if !field}
          <ul class="max-h-80 overflow-y-auto py-1">
            {#each fields as f (f.key)}
              <li><button onclick={() => pick(f)} class="w-full px-3 py-1.5 text-left text-xs text-dark-text-secondary hover:bg-dark-elevated">{f.label}</button></li>
            {/each}
          </ul>
        {:else}
          <form class="space-y-2 p-2" onsubmit={(e) => { e.preventDefault(); apply(); }}>
            <div class="flex items-center justify-between text-[11px] font-medium text-dark-text-secondary">
              {field.label}
              <button type="button" class="text-dark-text-muted hover:text-dark-text-secondary" onclick={() => (field = null)}>Back</button>
            </div>
            {#if field.kind === 'list' || field.kind === 'text' || field.kind === 'score'}
              <!-- svelte-ignore a11y_autofocus -->
              <input bind:value autofocus placeholder={field.kind === 'score' ? 'Score name' : 'Value (comma separated)'} class="w-full border px-2 py-1 text-xs border-dark-border-subtle bg-dark-elevated text-dark-text" />
              {#if suggestions.length}
                <ul class="max-h-40 overflow-y-auto border border-dark-border">
                  {#each suggestions as s}
                    <li><button type="button" onclick={() => (field?.kind === 'score' ? (value = s) : apply(s))} class="w-full truncate px-2 py-1 text-left font-mono text-[11px] text-dark-text-secondary hover:bg-dark-elevated">{s}</button></li>
                  {/each}
                </ul>
              {/if}
            {/if}
            {#if field.kind === 'range' || field.kind === 'score'}
              <div class="flex items-center gap-1.5 text-xs">
                <input bind:value={min} type="number" step="any" min={field.kind === 'range' ? 0 : undefined} placeholder="min" class="w-full border px-2 py-1 border-dark-border-subtle bg-dark-elevated text-dark-text" />
                <span class="text-dark-text-muted">–</span>
                <input bind:value={max} type="number" step="any" min={field.kind === 'range' ? 0 : undefined} placeholder="max" class="w-full border px-2 py-1 border-dark-border-subtle bg-dark-elevated text-dark-text" />
              </div>
            {/if}
            {#if field.kind === 'status'}
              <select bind:value={status} class="w-full border px-2 py-1 text-xs border-dark-border-subtle bg-dark-elevated text-dark-text">
                <option value="error">Has errors</option>
                <option value="ok">No errors</option>
              </select>
            {/if}
            <button type="submit" class="w-full border py-1 text-xs hover:bg-dark-highest border-dark-text bg-dark-text text-dark-base">Apply</button>
          </form>
        {/if}
      </div>
    {/if}
  </div>
</div>
