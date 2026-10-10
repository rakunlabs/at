<script lang="ts">
  import { ArrowLeft, ArrowRight } from 'lucide-svelte';
  import { docsPath, type DocsSelection } from '@/lib/helper/docs-nav';

  interface Entry {
    selection: DocsSelection;
    title: string;
  }

  interface Props {
    prev: Entry | null;
    next: Entry | null;
  }

  let { prev, next }: Props = $props();

  const card =
    'flex min-w-0 flex-1 flex-col gap-1 border border-dark-border px-3 py-2.5 hover:bg-dark-surface focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent';
</script>

{#if prev || next}
  <nav aria-label="Previous and next page" class="mt-10 flex flex-col gap-2 sm:flex-row">
    {#if prev}
      <a href={`#${docsPath(prev.selection)}`} class={card}>
        <span class="flex items-center gap-1 text-[11px] text-dark-text-muted">
          <ArrowLeft size={11} aria-hidden="true" /> Previous
        </span>
        <span class="truncate text-sm text-dark-text">{prev.title}</span>
      </a>
    {:else}
      <span class="hidden flex-1 sm:block"></span>
    {/if}
    {#if next}
      <a href={`#${docsPath(next.selection)}`} class={[card, 'sm:items-end sm:text-right']}>
        <span class="flex items-center gap-1 text-[11px] text-dark-text-muted">
          Next <ArrowRight size={11} aria-hidden="true" />
        </span>
        <span class="max-w-full truncate text-sm text-dark-text">{next.title}</span>
      </a>
    {/if}
  </nav>
{/if}
