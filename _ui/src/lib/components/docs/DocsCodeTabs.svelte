<script lang="ts">
  // Language-tabbed code samples. Each tab carries its own code; the selected
  // tab id is bindable so the page can remember the reader's language.
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import type { DocsCodeTab } from './docs-types';

  interface Props {
    tabs: DocsCodeTab[];
    active?: string;
    /** Distinguishes several tab sets on one page for ARIA ids. */
    name?: string;
  }

  let { tabs, active = $bindable(''), name = 'code' }: Props = $props();

  let tablistEl = $state<HTMLElement | null>(null);

  const current = $derived(tabs.find((t) => t.id === active) ?? tabs[0]);

  function focusTab(index: number) {
    const wrapped = (index + tabs.length) % tabs.length;
    active = tabs[wrapped].id;
    tablistEl?.querySelector<HTMLElement>(`[data-tab="${tabs[wrapped].id}"]`)?.focus();
  }

  function onKeydown(e: KeyboardEvent) {
    const index = tabs.findIndex((t) => t.id === current?.id);
    const moves: Record<string, number> = {
      ArrowRight: index + 1,
      ArrowLeft: index - 1,
      Home: 0,
      End: tabs.length - 1,
    };
    if (e.key in moves) {
      e.preventDefault();
      focusTab(moves[e.key]);
    }
  }
</script>

{#if current}
  <div>
    <div
      bind:this={tablistEl}
      role="tablist"
      aria-label="Example language"
      class="flex flex-wrap border border-b-0 border-dark-border bg-dark-surface"
    >
      {#each tabs as tab (tab.id)}
        {@const selected = tab.id === current.id}
        <button
          type="button"
          role="tab"
          data-tab={tab.id}
          id={`docs-${name}-tab-${tab.id}`}
          aria-selected={selected}
          aria-controls={`docs-${name}-panel`}
          tabindex={selected ? 0 : -1}
          onclick={() => (active = tab.id)}
          onkeydown={onKeydown}
          class={[
            '-mb-px border-b-2 px-3 py-1.5 text-xs focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent',
            selected
              ? 'border-accent text-dark-text'
              : 'border-transparent text-dark-text-muted hover:text-dark-text',
          ]}
        >
          {tab.label}
        </button>
      {/each}
    </div>
    <div id={`docs-${name}-panel`} role="tabpanel" aria-labelledby={`docs-${name}-tab-${current.id}`}>
      <DocsCodeBlock
        code={current.code}
        lang={current.lang}
        label={current.label}
        copyLabel={`Copy ${current.label} example`}
      />
    </div>
  </div>
{/if}
