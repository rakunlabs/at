<script lang="ts">
  // Syntax-highlighted code panel with a copy button and an optional caption.
  // Every API reference snippet goes through here so the copy affordance,
  // focus ring and colours stay identical across sections.
  import { Copy, Check } from 'lucide-svelte';
  import { highlightCode } from '@/lib/helper/markdown';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    code: string;
    /** highlight.js language id. Unknown/empty renders escaped plain text. */
    lang?: string;
    /** Caption shown on the left of the toolbar (e.g. a file path). */
    label?: string;
    /** Accessible name for the copy button. */
    copyLabel?: string;
  }

  let { code, lang = '', label = '', copyLabel = 'Copy snippet' }: Props = $props();

  let copied = $state(false);
  let timer: ReturnType<typeof setTimeout> | null = null;

  const html = $derived(highlightCode(code, lang));

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      copied = true;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => (copied = false), 2000);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }
</script>

<figure class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
  <figcaption
    class="flex items-center justify-between gap-3 px-3 py-1.5 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated"
  >
    <span class="min-w-0 truncate font-mono text-[11px] text-gray-600 dark:text-dark-text-secondary">
      {label || lang || 'code'}
    </span>
    <button
      type="button"
      onclick={copy}
      aria-label={copyLabel}
      class="inline-flex shrink-0 items-center gap-1.5 border border-gray-300 px-2 py-1 text-[11px] font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-highest dark:focus-visible:outline-accent"
    >
      {#if copied}
        <Check size={12} aria-hidden="true" />
        Copied
      {:else}
        <Copy size={12} aria-hidden="true" />
        Copy
      {/if}
    </button>
  </figcaption>
  <pre class="overflow-x-auto p-3 text-[12px] leading-relaxed"><code class="hljs">{@html html}</code></pre>
</figure>

<style>
  /* highlight.js themes paint their own background/padding on `.hljs`; the
     surrounding <figure> already owns those, so neutralise them here. Svelte's
     scoping raises specificity above the imported `.hljs` rule. */
  pre code {
    display: block;
    padding: 0;
    background: transparent;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }
</style>
