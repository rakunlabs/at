<script lang="ts">
  // Single source of truth for rendering markdown across the app.
  //
  // Why this exists: there were ~14 call sites doing
  //   <div class="markdown-body ..." use:renderMarkdown>{@html md(text)}</div>
  // — each with its own slightly-different list of classes, some of them
  // referencing the non-installed `prose-*` typography plugin. That meant
  // tables, lists, headings, etc. rendered inconsistently (or not at all)
  // depending on which page you looked at. This component centralises:
  //   • parsing (`md()`)
  //   • mermaid post-processing (`renderMarkdown` action)
  //   • optional code-block copy buttons + table wrappers (`enhanceMarkdown`)
  //   • the `.markdown-body` typography rules from global.css
  // so a single change affects every call site.
  //
  // Usage:
  //   <Markdown source={text} />
  //   <Markdown source={text} class="text-[13.5px] leading-[1.7]" />
  //   <Markdown source={text} enhance inline />
  //   <Markdown source={text} as="article" class="guide-content" enhance />
  import { md, renderMarkdown, enhanceMarkdown } from '@/lib/helper/markdown';

  interface Props {
    /** Markdown source to render. Empty/nullish values produce nothing. */
    source: string | undefined | null;
    /** Extra classes appended after the base `markdown-body` class. */
    class?: string;
    /**
     * When true, decorates `<pre>` with copy buttons + language labels and
     * wraps tables for horizontal scrolling. Off by default to keep chat
     * bubbles minimal; enable on long-form surfaces like Guides / Tasks.
     */
    enhance?: boolean;
    /**
     * Host element tag. Defaults to `<div>`. Some surfaces want
     * `<article>` for semantics (guides) or similar.
     */
    as?: 'div' | 'article' | 'section' | 'span';
    /**
     * When true, renders using a `<span>` wrapper with no default
     * block-level margins. Useful inside inline contexts.
     */
    inline?: boolean;
  }

  let {
    source,
    class: className = '',
    enhance = false,
    as = 'div',
    inline = false,
  }: Props = $props();

  // Pre-compute HTML (md() is synchronous). Empty string short-circuits
  // so we don't render an empty container.
  const html = $derived(source ? md(source) : '');
  const tag = $derived(inline ? 'span' : as);
  const baseClass = $derived(
    `markdown-body${inline ? ' markdown-inline' : ''}${className ? ' ' + className : ''}`,
  );
</script>

{#if html}
  {#if enhance}
    <svelte:element
      this={tag}
      class={baseClass}
      use:renderMarkdown
      use:enhanceMarkdown
    >
      {@html html}
    </svelte:element>
  {:else}
    <svelte:element this={tag} class={baseClass} use:renderMarkdown>
      {@html html}
    </svelte:element>
  {/if}
{/if}
