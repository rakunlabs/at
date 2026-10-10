<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    /** Small label above the title — the group the page belongs to. */
    eyebrow?: string;
    /** Rendered next to the title — badges, status pills. */
    badge?: Snippet;
    /** Rendered on the trailing edge — copy link, edit, delete. */
    actions?: Snippet;
  }

  let { title, description = '', eyebrow = '', badge, actions }: Props = $props();
</script>

<header class="mb-6 border-b border-dark-border pb-5">
  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
    <div class="min-w-0">
      {#if eyebrow}
        <p class="mb-1.5 text-[11px] text-oc-peach">{eyebrow}</p>
      {/if}
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="text-xl font-semibold text-dark-text">{title}</h1>
        {#if badge}{@render badge()}{/if}
      </div>
      {#if description}
        <p class="mt-1.5 max-w-2xl text-sm leading-relaxed text-dark-text-muted">{description}</p>
      {/if}
    </div>
    {#if actions}
      <div class="flex flex-wrap items-center gap-1.5 sm:shrink-0">{@render actions()}</div>
    {/if}
  </div>
</header>
