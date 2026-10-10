<script lang="ts">
  import { tick } from 'svelte';
  import { FileCode, Lock, Pencil, Trash2 } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import { highlightCode } from '@/lib/helper/markdown';
  import type { DocsSelection } from '@/lib/helper/docs-nav';
  import type { DisplayGuide } from './builtin-guides';
  import DocsPaneHeader from './DocsPaneHeader.svelte';
  import DocsCopyLinkButton from './DocsCopyLinkButton.svelte';

  interface Props {
    guide: DisplayGuide;
    eyebrow?: string;
    onedit: (guide: DisplayGuide) => void;
    ondelete: (id: string) => void;
    deleting?: boolean;
  }

  let { guide, eyebrow = '', onedit, ondelete, deleting = false }: Props = $props();

  let viewMode = $state<'rendered' | 'source'>('rendered');
  let confirming = $state(false);
  let bodyEl = $state<HTMLElement | null>(null);
  let outline = $state<{ el: HTMLElement; text: string; depth: number }[]>([]);

  // A different guide means a fresh viewing context: drop any pending
  // delete confirmation and go back to the rendered view.
  $effect(() => {
    void guide.id;
    viewMode = 'rendered';
    confirming = false;
  });

  // Headings of the rendered guide, for the "On this page" list. Rebuilt after
  // the markdown renders; scrolling uses the element directly because the
  // hash already carries the router path.
  $effect(() => {
    void guide.content;
    void viewMode;
    const el = bodyEl;
    void tick().then(() => {
      if (!el || viewMode !== 'rendered') {
        outline = [];
        return;
      }
      outline = Array.from(el.querySelectorAll<HTMLElement>('h2, h3'))
        .map((h) => ({ el: h, text: h.textContent?.trim() ?? '', depth: h.tagName === 'H3' ? 1 : 0 }))
        .filter((h) => h.text);
    });
  });

  const selection = $derived<DocsSelection>({ kind: 'guides', id: guide.id });
  const content = $derived(guide.content.trim());
</script>

<DocsPaneHeader title={guide.title} description={guide.description} {eyebrow}>
  {#snippet badge()}
    {#if guide.builtin}
      <span class="inline-flex items-center gap-1 border border-dark-border-subtle px-1.5 py-0.5 text-[11px] text-dark-text-muted">
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
      class={['settings-button', viewMode === 'source' ? 'border-accent text-accent-text' : '']}
    >
      <FileCode size={12} aria-hidden="true" />
      Source
    </button>

    <DocsCopyLinkButton {selection} />

    {#if !guide.builtin}
      <button type="button" onclick={() => onedit(guide)} class="settings-button">
        <Pencil size={12} aria-hidden="true" />
        Edit
      </button>
      {#if confirming}
        <button type="button" onclick={() => ondelete(guide.id)} disabled={deleting} class="settings-danger">
          <Trash2 size={12} aria-hidden="true" />
          {deleting ? 'Deleting…' : 'Confirm delete'}
        </button>
        <button type="button" onclick={() => (confirming = false)} disabled={deleting} class="settings-button">
          Cancel
        </button>
      {:else}
        <button
          type="button"
          onclick={() => (confirming = true)}
          class="settings-button border-oc-red/40 text-oc-red hover:bg-oc-red/10"
        >
          <Trash2 size={12} aria-hidden="true" />
          Delete
        </button>
      {/if}
    {/if}
  {/snippet}
</DocsPaneHeader>

{#if !content}
  <p class="text-sm leading-relaxed text-dark-text-muted">This guide has no content yet.</p>
{:else}
  <div class="flex gap-10">
    <div bind:this={bodyEl} class="min-w-0 flex-1">
      {#if viewMode === 'source'}
        <figure class="border border-dark-border">
          <pre class="overflow-x-auto px-4 py-3 text-[12.5px] leading-relaxed"><code class="hljs language-markdown">{@html highlightCode(content, 'markdown')}</code></pre>
        </figure>
      {:else}
        <Markdown source={content} as="article" class="max-w-none text-[13.5px] leading-[1.75]" enhance />
      {/if}
    </div>

    {#if outline.length >= 3}
      <nav aria-label="On this page" class="sticky top-6 hidden w-48 shrink-0 self-start xl:block">
        <p class="mb-2 text-[11px] text-dark-text-muted">On this page</p>
        <ul class="space-y-1 border-l border-dark-border">
          {#each outline as h, i (i)}
            <li>
              <button
                type="button"
                onclick={() => h.el.scrollIntoView({ block: 'start' })}
                class={[
                  '-ml-px block w-full truncate border-l border-transparent text-left text-xs text-dark-text-muted hover:border-dark-text-muted hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent',
                  h.depth ? 'pl-5' : 'pl-3',
                ]}
              >
                {h.text}
              </button>
            </li>
          {/each}
        </ul>
      </nav>
    {/if}
  </div>
{/if}
