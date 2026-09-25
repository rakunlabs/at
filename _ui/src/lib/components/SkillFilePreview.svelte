<script lang="ts">
  import { enhanceMarkdown, highlightCode } from '@/lib/helper/markdown';
  import { codeLanguage, isMarkdownPath, splitFrontmatter } from '@/lib/helper/skill-files';
  import { renderSkillMarkdown } from '@/lib/helper/skill-markdown';

  interface Props {
    path: string;
    content: string;
    /** Paths in the skill folder, for resolving relative links. */
    paths: string[];
    onopen: (path: string) => void;
  }

  let { path, content, paths, onopen }: Props = $props();

  let pathSet = $derived(new Set(paths));
  let markdown = $derived(isMarkdownPath(path));
  let parts = $derived(markdown ? splitFrontmatter(content) : null);
  let html = $derived(parts ? renderSkillMarkdown(parts.body, path, (candidate) => pathSet.has(candidate)) : '');
  let language = $derived(markdown ? '' : codeLanguage(path));
  let lineCount = $derived(content ? content.split('\n').length : 0);

  function handleClick(event: MouseEvent) {
    const link = (event.target as Element | null)?.closest?.('a[data-skill-path]');
    if (!link) return;
    event.preventDefault();
    onopen(link.getAttribute('data-skill-path') || '');
  }
</script>

{#if !content.trim()}
  <div class="flex flex-1 items-center justify-center p-6 text-sm text-gray-400 dark:text-dark-text-muted">This file is empty.</div>
{:else if parts}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="min-h-0 flex-1 overflow-y-auto" onclick={handleClick}>
    <div class="mx-auto max-w-3xl px-6 py-5">
      {#if parts.frontmatter}
        <div class="mb-5 border border-gray-200 dark:border-dark-border">
          <div class="border-b border-gray-200 bg-gray-50 px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-gray-500 dark:border-dark-border dark:bg-dark-base dark:text-dark-text-muted">Frontmatter</div>
          {#if parts.frontmatter.fields}
            <dl class="grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)] gap-x-4 gap-y-1.5 px-3 py-2.5 text-xs">
              {#each parts.frontmatter.fields as [key, value] (key)}
                <dt class="truncate font-mono text-gray-500 dark:text-dark-text-muted" title={key}>{key}</dt>
                <dd class="whitespace-pre-wrap break-words text-gray-900 dark:text-dark-text">{value}</dd>
              {/each}
            </dl>
          {:else}
            <pre class="overflow-x-auto px-3 py-2.5 text-xs leading-5"><code class="hljs language-yaml">{@html highlightCode(parts.frontmatter.raw, 'yaml')}</code></pre>
          {/if}
        </div>
      {/if}
      <div class="markdown-body skill-markdown text-sm" use:enhanceMarkdown>{@html html}</div>
    </div>
  </div>
{:else}
  <div class="min-h-0 flex-1 overflow-auto">
    <div class="skill-code flex min-h-full min-w-max font-mono text-xs leading-5">
      <div aria-hidden="true" class="select-none border-r border-gray-200 bg-gray-50 px-3 py-4 text-right text-gray-400 dark:border-dark-border dark:bg-dark-base dark:text-dark-text-faint">
        {#each { length: lineCount } as _, index}<div>{index + 1}</div>{/each}
      </div>
      <pre class="flex-1 px-4 py-4"><code class="hljs{language ? ` language-${language}` : ''}">{@html highlightCode(content, language)}</code></pre>
    </div>
  </div>
{/if}

<style>
  /* Line numbers and code share one line box; the global .hljs block padding
     and background would offset one against the other. */
  .skill-code :global(code.hljs) {
    display: block;
    padding: 0;
    background: transparent;
  }

  /* Document-style heading scale for long-form skill instructions (chat
     bubbles keep the compact global .markdown-body scale). */
  .skill-markdown :global(h1) {
    font-size: 1.5rem;
    padding-bottom: 0.3em;
    border-bottom: 1px solid var(--color-gray-200);
  }

  .skill-markdown :global(h2) {
    font-size: 1.2rem;
    margin-top: 1.6em;
    padding-bottom: 0.25em;
    border-bottom: 1px solid var(--color-gray-200);
  }

  .skill-markdown :global(h3) {
    font-size: 1.05rem;
  }

  :global(.dark) .skill-markdown :global(h1),
  :global(.dark) .skill-markdown :global(h2) {
    border-bottom-color: var(--color-dark-border);
  }

  .skill-markdown :global(li:has(> input[type='checkbox'])) {
    list-style: none;
    margin-left: -1.1em;
  }

  .skill-markdown :global(a[data-skill-path]) {
    cursor: pointer;
  }

  .skill-markdown :global(.skill-md-dead-link) {
    text-decoration: underline dotted;
    text-underline-offset: 2px;
  }

  .skill-markdown :global(.skill-md-image) {
    font-style: italic;
    opacity: 0.7;
  }
</style>
