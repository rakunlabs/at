<script lang="ts">
  import { ArrowUp, ArrowDown, ArrowUpDown } from 'lucide-svelte';

  interface Props {
    field: string;
    label: string;
    sorts: SortEntry[];
    onsort: (field: string, multiSort: boolean) => void;
    align?: 'left' | 'right';
    class?: string;
  }

  export interface SortEntry {
    field: string;
    desc: boolean;
  }

  let { field, label, sorts, onsort, align = 'left', class: className = '' }: Props = $props();

  let currentSort = $derived(sorts.find(s => s.field === field));
  let sortIndex = $derived(sorts.length > 1 ? sorts.findIndex(s => s.field === field) : -1);
</script>

<th
  class={`px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider select-none cursor-pointer hover:text-gray-700 dark:hover:text-dark-text-secondary hover:bg-gray-100/50 dark:hover:bg-dark-elevated/50 transition-colors ${align === 'right' ? 'text-right' : 'text-left'} ${className}`}
  onclick={(e) => onsort(field, e.shiftKey)}
>
  <div class={`flex items-center gap-1 ${align === 'right' ? 'justify-end' : ''}`}>
    <span>{label}</span>
    {#if currentSort}
      <span class="flex items-center text-gray-700 dark:text-dark-text-secondary">
        {#if currentSort.desc}
          <ArrowDown size={12} />
        {:else}
          <ArrowUp size={12} />
        {/if}
        {#if sortIndex >= 0}
          <span class="text-[9px] ml-px font-bold">{sortIndex + 1}</span>
        {/if}
      </span>
    {:else}
      <span class="text-gray-300 dark:text-dark-text-faint opacity-0 group-hover:opacity-100">
        <ArrowUpDown size={10} />
      </span>
    {/if}
  </div>
</th>
