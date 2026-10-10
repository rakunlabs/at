<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Info, TriangleAlert } from 'lucide-svelte';

  interface Props {
    tone?: 'note' | 'warning';
    title?: string;
    children: Snippet;
  }

  let { tone = 'note', title = '', children }: Props = $props();
</script>

<aside
  class={[
    'flex gap-2.5 border-l-2 bg-dark-surface px-3 py-2.5 text-sm leading-relaxed text-dark-text-secondary',
    tone === 'warning' ? 'border-oc-peach' : 'border-accent',
  ]}
>
  {#if tone === 'warning'}
    <TriangleAlert size={14} class="mt-0.5 shrink-0 text-oc-peach" aria-hidden="true" />
  {:else}
    <Info size={14} class="mt-0.5 shrink-0 text-accent-text" aria-hidden="true" />
  {/if}
  <div class="docs-callout min-w-0 flex-1 space-y-1.5">
    {#if title}<p class="font-semibold text-dark-text">{title}</p>{/if}
    {@render children()}
  </div>
</aside>
