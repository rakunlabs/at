<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import { codeExampleTabs, codeExampleFor } from './snippets';

  interface Props {
    baseUrl: string;
    model: string;
    /** Lifted so the chosen language survives navigating away and back. */
    activeTab?: string;
  }

  let { baseUrl, model, activeTab = $bindable('python') }: Props = $props();

  let tablistEl = $state<HTMLElement | null>(null);

  const current = $derived(
    codeExampleTabs.find((t) => t.id === activeTab) ?? codeExampleTabs[0],
  );
  const code = $derived(codeExampleFor(current.id, model, baseUrl));

  function focusTab(index: number) {
    const wrapped = (index + codeExampleTabs.length) % codeExampleTabs.length;
    activeTab = codeExampleTabs[wrapped].id;
    tablistEl
      ?.querySelector<HTMLElement>(`[data-tab="${codeExampleTabs[wrapped].id}"]`)
      ?.focus();
  }

  function onKeydown(e: KeyboardEvent) {
    const index = codeExampleTabs.findIndex((t) => t.id === activeTab);
    if (e.key === 'ArrowRight') {
      e.preventDefault();
      focusTab(index + 1);
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault();
      focusTab(index - 1);
    } else if (e.key === 'Home') {
      e.preventDefault();
      focusTab(0);
    } else if (e.key === 'End') {
      e.preventDefault();
      focusTab(codeExampleTabs.length - 1);
    }
  }
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Point any OpenAI client at the gateway base URL. These snippets use
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >{model}</code
  > — swap in any model from Available models.
</p>

<div>
  <div
    bind:this={tablistEl}
    role="tablist"
    aria-label="Example language"
    class="flex flex-wrap gap-1 border-b border-gray-200 dark:border-dark-border"
  >
    {#each codeExampleTabs as tab (tab.id)}
      {@const selected = tab.id === current.id}
      <button
        type="button"
        role="tab"
        data-tab={tab.id}
        id={`docs-tab-${tab.id}`}
        aria-selected={selected}
        aria-controls="docs-tabpanel-code"
        tabindex={selected ? 0 : -1}
        onclick={() => (activeTab = tab.id)}
        onkeydown={onKeydown}
        class={[
          '-mb-px border-b-2 px-3 py-2 text-xs font-medium transition-colors motion-reduce:transition-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:focus-visible:outline-accent',
          selected
            ? 'border-gray-900 text-gray-900 dark:border-accent dark:text-accent-text'
            : 'border-transparent text-gray-600 hover:text-gray-900 dark:text-dark-text-secondary dark:hover:text-dark-text',
        ]}
      >
        {tab.label}
      </button>
    {/each}
  </div>

  <div
    id="docs-tabpanel-code"
    role="tabpanel"
    aria-labelledby={`docs-tab-${current.id}`}
    tabindex="-1"
    class="pt-3"
  >
    <DocsCodeBlock
      {code}
      lang={current.lang}
      label={current.label}
      copyLabel={`Copy ${current.label} example`}
    />
  </div>
</div>
