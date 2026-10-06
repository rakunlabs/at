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
    'inline-flex items-center gap-1.5 border px-2 py-1 text-xs font-medium focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-50 border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated focus-visible:outline-accent';
</script>

<DocsPaneHeader title={guide.title} description={guide.description}>
  {#snippet badge()}
    {#if guide.builtin}
      <span
        class="inline-flex items-center gap-1 border px-1.5 py-0.5 text-[11px] font-medium border-dark-border-subtle bg-dark-elevated text-dark-text-secondary"
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
          ? 'border-accent bg-accent text-gray-950 hover:bg-accent-hover'
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
          class="inline-flex items-center gap-1.5 border border-red-700 bg-red-700 px-2 py-1 text-xs font-medium text-white hover:bg-red-800 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-red-700 disabled:opacity-50"
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
          class="inline-flex items-center gap-1.5 border px-2 py-1 text-xs font-medium focus-visible:outline-2 focus-visible:outline-offset-2 border-red-800 text-red-300 hover:bg-red-900/25 focus-visible:outline-red-400"
        >
          <Trash2 size={12} aria-hidden="true" />
          Delete
        </button>
      {/if}
    {/if}
  {/snippet}
</DocsPaneHeader>

{#if !content}
  <p class="text-sm leading-relaxed text-dark-text-secondary">
    This guide has no content yet.
  </p>
{:else if viewMode === 'source'}
  <article class="markdown-body max-w-none text-[13.5px] leading-[1.7]" use:renderMarkdown>
    <pre><code class="hljs language-markdown">{@html highlightCode(content, 'markdown')}</code></pre>
  </article>
{:else}
  <Markdown source={content} as="article" class="max-w-none text-[13.5px] leading-[1.7]" enhance />
{/if}
