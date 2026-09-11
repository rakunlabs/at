<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    /** Rendered next to the title — badges, status pills. */
    badge?: Snippet;
    /** Rendered on the trailing edge — copy link, edit, delete. */
    actions?: Snippet;
  }

  let { title, description = '', badge, actions }: Props = $props();
</script>

<header class="mb-5 border-b border-gray-200 pb-4 dark:border-dark-border">
  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">{title}</h1>
        {#if badge}{@render badge()}{/if}
      </div>
      {#if description}
        <p class="mt-1 text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
          {description}
        </p>
      {/if}
    </div>
    {#if actions}
      <div class="flex flex-wrap items-center gap-1.5 sm:shrink-0">{@render actions()}</div>
    {/if}
  </div>
</header>
