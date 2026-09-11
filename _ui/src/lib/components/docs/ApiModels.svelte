<script lang="ts">
  import { Copy, Check } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    models: string[];
    loading?: boolean;
    error?: string;
    onretry?: () => void;
  }

  let { models, loading = false, error = '', onretry }: Props = $props();

  let copiedId = $state('');
  let timer: ReturnType<typeof setTimeout> | null = null;

  async function copy(model: string) {
    try {
      await navigator.clipboard.writeText(model);
      copiedId = model;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => (copiedId = ''), 2000);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }
</script>

{#if loading}
  <p class="text-sm text-gray-600 dark:text-dark-text-secondary">Loading models…</p>
{:else if error}
  <div
    role="alert"
    class="border border-amber-300 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-900/20"
  >
    <p class="text-sm text-amber-900 dark:text-amber-200">{error}</p>
    {#if onretry}
      <button
        type="button"
        onclick={onretry}
        class="mt-2 border border-amber-400 px-2 py-1 text-xs font-medium text-amber-900 hover:bg-amber-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-amber-700 dark:border-amber-700 dark:text-amber-200 dark:hover:bg-amber-900/40"
      >
        Retry
      </button>
    {/if}
  </div>
{:else if models.length === 0}
  <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    No models are available. Configure a provider on the
    <a
      href="#/providers"
      class="text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent"
      >Providers</a
    > page.
  </p>
{:else}
  <p class="text-sm text-gray-600 dark:text-dark-text-secondary">
    {models.length} model{models.length === 1 ? '' : 's'} available on this instance.
  </p>
  <ul class="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
    {#each models as model (model)}
      <li class="flex items-center gap-1 border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface">
        <code class="min-w-0 flex-1 truncate px-2 py-1.5 font-mono text-[12px] text-gray-800 dark:text-dark-text">
          {model}
        </code>
        <button
          type="button"
          onclick={() => copy(model)}
          aria-label={`Copy model id ${model}`}
          class="shrink-0 border-l border-gray-200 p-1.5 text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:hover:text-dark-text dark:focus-visible:outline-accent"
        >
          {#if copiedId === model}
            <Check size={12} aria-hidden="true" />
          {:else}
            <Copy size={12} aria-hidden="true" />
          {/if}
        </button>
      </li>
    {/each}
  </ul>
{/if}
