<script lang="ts">
  import { FileCode, Lock, Pencil, Trash2 } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import { renderMarkdown, highlightCode } from '@/lib/helper/markdown';
  import type { DocsSelection } from '@/lib/helper/docs-nav';
  import type { DisplayGuide } from './builtin-guides';
  import DocsPaneHeader from './DocsPaneHeader.svelte';
  import DocsCopyLinkButton from './DocsCopyLinkButton.svelte';

  interface Props {
    guide: DisplayGuide;
    onedit: (guide: DisplayGuide) => void;
    ondelete: (id: string) => void;
    deleting?: boolean;
  }

  let { guide, onedit, ondelete, deleting = false }: Props = $props();

  let viewMode = $state<'rendered' | 'source'>('rendered');
  let confirming = $state(false);

  // A different guide means a fresh viewing context: drop any pending
  // delete confirmation and go back to the rendered view.
  $effect(() => {
    void guide.id;
    viewMode = 'rendered';
    confirming = false;
  });

  const selection = $derived<DocsSelection>({ kind: 'guides', id: guide.id });
  const content = $derived(guide.content.trim());

  const btn =
    'inline-flex items-center gap-1.5 border border-gray-300 px-2 py-1 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent';
</script>

<DocsPaneHeader title={guide.title} description={guide.description}>
  {#snippet badge()}
    {#if guide.builtin}
      <span
        class="inline-flex items-center gap-1 border border-gray-300 bg-gray-100 px-1.5 py-0.5 text-[11px] font-medium text-gray-700 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text-secondary"
      >
        <Lock size={10} aria-hidden="true" />
        Built-in
      </span>
    {/if}
  {/snippet}

  {#snippet actions()}
    <button
      type="button"
      onclick={() => (viewMode = viewMode === 'source' ? 'rendered' : 'source')}
      aria-pressed={viewMode === 'source'}
      class={[
        btn,
        viewMode === 'source'
          ? 'border-gray-900 bg-gray-900 text-white hover:bg-gray-800 dark:border-accent dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover'
          : '',
      ]}
    >
      <FileCode size={12} aria-hidden="true" />
      Source
    </button>

    <DocsCopyLinkButton {selection} />

    {#if !guide.builtin}
      <button type="button" onclick={() => onedit(guide)} class={btn}>
        <Pencil size={12} aria-hidden="true" />
        Edit
      </button>
      {#if confirming}
        <button
          type="button"
          onclick={() => ondelete(guide.id)}
          disabled={deleting}
          class="inline-flex items-center gap-1.5 border border-red-700 bg-red-700 px-2 py-1 text-xs font-medium text-white transition-colors hover:bg-red-800 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-700 motion-reduce:transition-none disabled:opacity-50"
        >
          <Trash2 size={12} aria-hidden="true" />
          {deleting ? 'Deleting…' : 'Confirm delete'}
        </button>
        <button type="button" onclick={() => (confirming = false)} disabled={deleting} class={btn}>
          Cancel
        </button>
      {:else}
        <button
          type="button"
          onclick={() => (confirming = true)}
          class="inline-flex items-center gap-1.5 border border-red-400 px-2 py-1 text-xs font-medium text-red-700 transition-colors hover:bg-red-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-700 motion-reduce:transition-none dark:border-red-800 dark:text-red-300 dark:hover:bg-red-900/25 dark:focus-visible:outline-red-400"
        >
          <Trash2 size={12} aria-hidden="true" />
          Delete
        </button>
      {/if}
    {/if}
  {/snippet}
</DocsPaneHeader>

{#if !content}
  <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    This guide has no content yet.
  </p>
{:else if viewMode === 'source'}
  <article class="markdown-body max-w-none text-[13.5px] leading-[1.7]" use:renderMarkdown>
    <pre><code class="hljs language-markdown">{@html highlightCode(content, 'markdown')}</code></pre>
  </article>
{:else}
  <Markdown source={content} as="article" class="max-w-none text-[13.5px] leading-[1.7]" enhance />
{/if}
