<script lang="ts" module>
  export interface PaletteItem {
    label: string;
    /** Keyboard hint or a marker such as the current selection. */
    hint?: string;
    current?: boolean;
    disabled?: boolean;
    run: () => void;
  }
  export interface PaletteGroup {
    label: string;
    items: PaletteItem[];
  }
</script>

<script lang="ts">
  import { tick } from 'svelte';

  interface Props {
    title: string;
    groups: PaletteGroup[];
    placeholder?: string;
    onclose: () => void;
  }
  let { title, groups, placeholder = 'Search', onclose }: Props = $props();

  let query = $state('');
  let selected = $state(0);
  let input: HTMLInputElement | undefined = $state();
  let list: HTMLDivElement | undefined = $state();
  const listId = `palette-${Math.random().toString(36).slice(2)}`;

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return groups
      .map(g => ({ label: g.label, items: g.items.filter(i => !q || i.label.toLowerCase().includes(q) || g.label.toLowerCase().includes(q)) }))
      .filter(g => g.items.length > 0);
  });
  let flat = $derived(filtered.flatMap(g => g.items));

  $effect(() => {
    void query;
    selected = Math.max(0, flat.findIndex(i => i.current && !i.disabled));
  });

  $effect(() => {
    void selected;
    tick().then(() => list?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' }));
  });

  $effect(() => { input?.focus(); });

  function choose(item: PaletteItem | undefined) {
    if (!item || item.disabled) return;
    onclose();
    item.run();
  }

  function move(step: number) {
    if (flat.length === 0) return;
    let next = selected;
    for (let n = 0; n < flat.length; n++) {
      next = (next + step + flat.length) % flat.length;
      if (!flat[next].disabled) break;
    }
    selected = next;
  }

  function keydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || (e.key === 'n' && e.ctrlKey)) { e.preventDefault(); move(1); }
    else if (e.key === 'ArrowUp' || (e.key === 'p' && e.ctrlKey)) { e.preventDefault(); move(-1); }
    else if (e.key === 'Enter') { e.preventDefault(); choose(flat[selected]); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onclose(); }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="fixed inset-0 z-[70] flex items-start justify-center bg-black/60 px-4 pt-[14vh]" onclick={e => { if (e.target === e.currentTarget) onclose(); }}>
  <div role="dialog" aria-modal="true" aria-label={title} class="w-full max-w-xl bg-dark-surface shadow-[0_24px_60px_-12px_rgb(0_0_0/0.8)]">
    <div class="flex items-baseline justify-between px-[18px] pt-3.5">
      <h2 class="font-bold text-dark-text">{title}</h2>
      <button onclick={onclose} class="text-dark-text-muted hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent">esc</button>
    </div>
    <input
      bind:this={input}
      bind:value={query}
      onkeydown={keydown}
      {placeholder}
      role="combobox"
      aria-expanded="true"
      aria-controls={listId}
      aria-activedescendant={flat.length ? `${listId}-${selected}` : undefined}
      aria-label={title}
      class="mx-[18px] mt-2.5 mb-1.5 block w-[calc(100%-36px)] border-b border-dark-border bg-transparent py-1.5 text-dark-text placeholder:text-dark-text-muted focus:outline-none"
    />
    <div bind:this={list} id={listId} role="listbox" class="max-h-[46vh] overflow-y-auto pt-1 pb-2.5">
      {#if flat.length === 0}
        <p class="px-[18px] py-2.5 text-dark-text-muted">no match</p>
      {/if}
      {#each filtered as group (group.label)}
        <div class="px-[18px] pt-2.5 pb-0.5 text-[var(--oc-violet)]">{group.label}</div>
        {#each group.items as item (item.label)}
          {@const index = flat.indexOf(item)}
          <button
            id={`${listId}-${index}`}
            role="option"
            aria-selected={index === selected}
            aria-disabled={item.disabled}
            tabindex="-1"
            onmouseenter={() => { if (!item.disabled) selected = index; }}
            onclick={() => choose(item)}
            class={['flex w-full justify-between gap-4 px-[18px] py-[3px] text-left', index === selected ? 'bg-[var(--oc-peach)] text-[#1b1414]' : item.disabled ? 'text-dark-text-faint' : 'text-dark-text-secondary']}
          >
            <span class="min-w-0 truncate">{item.label}</span>
            {#if item.current}
              <span class={index === selected ? '' : 'text-[var(--oc-green)]'}>■</span>
            {:else if item.hint}
              <span class={['shrink-0', index === selected ? '' : 'text-dark-text-muted']}>{item.hint}</span>
            {/if}
          </button>
        {/each}
      {/each}
    </div>
  </div>
</div>
