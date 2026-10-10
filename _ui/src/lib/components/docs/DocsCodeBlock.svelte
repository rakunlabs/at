<script lang="ts">
  // Syntax-highlighted code panel with a copy button and an optional caption.
  // Every reference snippet goes through here so the copy affordance, focus
  // ring and colours stay identical across sections.
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

<figure class="border border-dark-border">
  <figcaption class="flex items-center justify-between gap-3 border-b border-dark-border bg-dark-surface py-1 pl-3 pr-1">
    <span class="min-w-0 truncate font-mono text-[11px] text-dark-text-muted">
      {label || lang || 'code'}
    </span>
    <button
      type="button"
      onclick={copy}
      aria-label={copyLabel}
      class="inline-flex shrink-0 items-center gap-1.5 px-2 py-1 text-[11px] text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
    >
      {#if copied}
        <Check size={12} class="text-oc-green" aria-hidden="true" />
        Copied
      {:else}
        <Copy size={12} aria-hidden="true" />
        Copy
      {/if}
    </button>
  </figcaption>
  <pre class="overflow-x-auto px-4 py-3 text-[12.5px] leading-relaxed"><code class="hljs">{@html html}</code></pre>
</figure>

<style>
  /* highlight.js themes paint their own background/padding on `.hljs`; the
     surrounding <figure> already owns those, so neutralise them here. Svelte's
     scoping raises specificity above the imported `.hljs` rule. */
  pre code {
    display: block;
    padding: 0;
    background: transparent;
    font-family: inherit;
  }
</style>
