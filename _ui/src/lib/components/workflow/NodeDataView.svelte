<script lang="ts">
  import { listDataFields } from '@/lib/workflow/data-fields';
  let { value, omitted = false, onselect }: {
    value?: Record<string, any>;
    omitted?: boolean;
    onselect?: (path: string) => void;
  } = $props();
  let mode = $state<'fields' | 'json'>('fields');
  let fields = $derived(listDataFields(value));
</script>

{#if omitted}
  <p class="border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">Preview unavailable: this value exceeds 64 KiB or cannot be represented as JSON. The node still receives the full data.</p>
{:else if value === undefined}
  <p class="text-sm text-gray-600 dark:text-dark-text-secondary">No data captured yet. Run the workflow to inspect this step.</p>
{:else}
  <div class="flex gap-1" aria-label="Data view">
    {#each ['fields', 'json'] as view}
      <button onclick={() => mode = view as typeof mode} aria-pressed={mode === view} class="border px-3 py-1.5 text-xs {mode === view ? 'border-gray-900 bg-gray-900 text-white dark:border-dark-text-secondary dark:bg-dark-elevated dark:text-dark-text' : 'border-gray-200 text-gray-600 dark:border-dark-border dark:text-dark-text-secondary'}">{view === 'fields' ? 'Fields' : 'JSON'}</button>
    {/each}
  </div>
  {#if mode === 'json'}
    <!-- Keyboard users must be able to focus and scroll captured JSON. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div role="region" tabindex="0" aria-label="Captured JSON" class="max-h-96 overflow-auto border border-gray-200 bg-gray-50 p-3 text-xs text-gray-800 dark:border-dark-border dark:bg-dark-base dark:text-dark-text"><pre>{JSON.stringify(value, null, 2)}</pre></div>
  {:else}
    <div class="divide-y divide-gray-200 border border-gray-200 dark:divide-dark-border dark:border-dark-border">
      {#each fields.fields as field (field.path)}
        <div class="p-2">
          <div class="flex min-w-0 items-start justify-between gap-2">
            <span class="min-w-0 break-all text-xs font-medium text-gray-900 dark:text-dark-text">{field.label} <span class="font-normal text-gray-600 dark:text-dark-text-secondary">{field.kind}</span></span>
            {#if onselect}<button onclick={() => onselect?.(field.path)} aria-label={`Use field ${field.path}`} class="shrink-0 border border-gray-300 px-2 py-1 text-xs text-blue-700 hover:bg-gray-50 dark:border-dark-border-subtle dark:text-blue-400 dark:hover:bg-dark-elevated">Use field</button>{/if}
          </div>
          <code class="mt-1 block break-all text-xs text-gray-600 dark:text-dark-text-secondary">{field.path}</code>
          <p class="mt-1 break-all text-xs text-gray-700 dark:text-dark-text-secondary">{field.preview}</p>
        </div>
      {:else}
        <p class="p-3 text-xs text-gray-600 dark:text-dark-text-secondary">Empty object — no fields to select.</p>
      {/each}
    </div>
    {#if fields.limited}<p class="text-xs text-gray-600 dark:text-dark-text-secondary">Field list limited to 200 entries and 12 levels. JSON view shows the captured snapshot.</p>{/if}
  {/if}
{/if}
