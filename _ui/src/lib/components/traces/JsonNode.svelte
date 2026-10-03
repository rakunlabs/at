<script lang="ts">
  import JsonNode from './JsonNode.svelte';
  import { ChevronRight, ChevronDown } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    value: unknown;
    name?: string;
    depth: number;
    level: number;
    query: string;
  }
  let { value, name, depth, level, query }: Props = $props();

  const isObject = $derived(value !== null && typeof value === 'object');
  const entries = $derived(isObject ? Object.entries(value as Record<string, unknown>) : []);
  const isArray = $derived(Array.isArray(value));
  let open = $state<boolean | null>(null);
  const expanded = $derived(open ?? (level < depth || (query !== '' && matches(value, query))));

  // Bounded search so a large payload cannot stall typing.
  function matches(v: unknown, q: string, budget = { n: 2000 }): boolean {
    if (budget.n-- <= 0) return false;
    if (v === null || typeof v !== 'object') return String(v).toLowerCase().includes(q);
    for (const [k, child] of Object.entries(v as Record<string, unknown>)) {
      if (k.toLowerCase().includes(q) || matches(child, q, budget)) return true;
    }
    return false;
  }
  const visibleEntries = $derived(query === '' ? entries : entries.filter(([k, v]) => k.toLowerCase().includes(query) || matches(v, query)));

  function scalarClass(v: unknown): string {
    if (typeof v === 'string') return 'text-emerald-700 dark:text-emerald-400';
    if (typeof v === 'number') return 'text-blue-700 dark:text-blue-400';
    if (typeof v === 'boolean') return 'text-purple-700 dark:text-purple-400';
    return 'text-gray-400 dark:text-dark-text-muted';
  }
  function scalarText(v: unknown): string {
    return typeof v === 'string' ? JSON.stringify(v) : String(v);
  }
  async function copyValue() {
    try {
      await navigator.clipboard.writeText(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
      addToast('Copied value', 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }
</script>

<div class="group">
  {#if isObject}
    <button class="flex items-center gap-0.5 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated" onclick={() => (open = !expanded)} aria-expanded={expanded}>
      {#if expanded}<ChevronDown size={10} />{:else}<ChevronRight size={10} />{/if}
      {#if name !== undefined}<span class="text-gray-600 dark:text-dark-text-secondary">{name}:</span>{/if}
      <span class="text-gray-400 dark:text-dark-text-muted">{isArray ? `[${entries.length}]` : `{${entries.length}}`}</span>
    </button>
    {#if expanded}
      <div class="ml-3 border-l border-gray-200 pl-2 dark:border-dark-border">
        {#each visibleEntries.slice(0, 500) as [key, child] (key)}
          <JsonNode value={child} name={isArray ? `${key}` : key} {depth} level={level + 1} {query} />
        {/each}
        {#if visibleEntries.length > 500}
          <div class="text-gray-400 dark:text-dark-text-muted">… {visibleEntries.length - 500} more</div>
        {/if}
      </div>
    {/if}
  {:else}
    <div class="flex items-start gap-1 pl-3">
      {#if name !== undefined}<span class="shrink-0 text-gray-600 dark:text-dark-text-secondary">{name}:</span>{/if}
      <span class={['min-w-0 whitespace-pre-wrap break-words', scalarClass(value), query && String(value).toLowerCase().includes(query) ? 'bg-yellow-100 dark:bg-yellow-900/40' : '']}>{scalarText(value)}</span>
      <button onclick={copyValue} class="invisible shrink-0 text-[10px] text-gray-400 group-hover:visible hover:text-gray-700 dark:hover:text-dark-text-secondary" title="Copy value">copy</button>
    </div>
  {/if}
</div>
