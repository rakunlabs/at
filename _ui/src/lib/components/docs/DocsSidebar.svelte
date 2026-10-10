<script lang="ts">
  // The single navigation surface for /docs: one search box and collapsible
  // groups (the reference sections plus the guide library). Implemented as an
  // ARIA tree with roving tabindex — arrow keys walk the visible rows,
  // Enter/Space activate (native button behaviour), ArrowLeft/Right
  // collapse/expand and move between levels.
  import { Search, X, Plus, ChevronRight, Loader2, AlertTriangle } from 'lucide-svelte';
  import {
    buildNavRows,
    groupExpanded,
    groupRowKey,
    navRowKey,
    stepIndex,
    type DocsGroupState,
    type DocsKind,
    type DocsSelection,
  } from '@/lib/helper/docs-nav';
  import type { DisplayGuide } from './builtin-guides';
  import type { SidebarGroup } from './docs-types';
  import { iconFor } from './guide-icons';

  interface Props {
    groups: SidebarGroup[];
    selection: DocsSelection;
    query: string;
    expanded: DocsGroupState;
    guidesLoading?: boolean;
    guidesError?: string;
    onselect: (selection: DocsSelection) => void;
    onnewguide: () => void;
    onretryguides: () => void;
  }

  let {
    groups,
    selection,
    query = $bindable(''),
    expanded = $bindable(),
    guidesLoading = false,
    guidesError = '',
    onselect,
    onnewguide,
    onretryguides,
  }: Props = $props();

  let treeEl = $state<HTMLElement | null>(null);
  let focusKey = $state('');

  const searching = $derived(query.trim().length > 0);
  const rows = $derived(buildNavRows(groups, expanded));
  const activeKey = $derived(navRowKey(selection.kind, selection.id));
  const noResults = $derived(searching && groups.every((g) => g.entries.length === 0));

  // Exactly one row is in the tab order. Prefer the row the user last moved
  // focus to, then the selected row, then the first row.
  const tabKey = $derived.by(() => {
    if (focusKey && rows.some((r) => r.key === focusKey)) return focusKey;
    if (rows.some((r) => r.key === activeKey)) return activeKey;
    return rows[0]?.key ?? '';
  });

  function rowEl(key: string): HTMLElement | null {
    return treeEl?.querySelector<HTMLElement>(`[data-nav-key="${CSS.escape(key)}"]`) ?? null;
  }

  function focusRow(key: string) {
    focusKey = key;
    rowEl(key)?.focus();
  }

  function focusAt(index: number) {
    const row = rows[index];
    if (row) focusRow(row.key);
  }

  // Keep the selected entry visible when a deep link, a search jump or the
  // owning group being expanded puts it outside the scroll viewport.
  $effect(() => {
    const key = activeKey;
    void rows.length;
    rowEl(key)?.scrollIntoView({ block: 'nearest' });
  });

  function setGroup(id: string, open: boolean) {
    expanded = { ...expanded, [id]: open };
  }

  function onTreeKeydown(e: KeyboardEvent) {
    const target = (e.target as HTMLElement)?.closest<HTMLElement>('[data-nav-key]');
    const key = target?.dataset.navKey;
    if (!key) return;
    const index = rows.findIndex((r) => r.key === key);
    if (index < 0) return;
    const row = rows[index];

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        focusAt(stepIndex(rows, index, 1));
        break;
      case 'ArrowUp':
        e.preventDefault();
        focusAt(stepIndex(rows, index, -1));
        break;
      case 'Home':
        e.preventDefault();
        focusAt(0);
        break;
      case 'End':
        e.preventDefault();
        focusAt(rows.length - 1);
        break;
      case 'ArrowRight':
        e.preventDefault();
        if (row.kind === 'group') {
          if (!groupExpanded(expanded, row.group)) setGroup(row.group, true);
          else focusAt(stepIndex(rows, index, 1));
        }
        break;
      case 'ArrowLeft':
        e.preventDefault();
        if (row.kind === 'group') {
          if (groupExpanded(expanded, row.group)) setGroup(row.group, false);
        } else {
          focusRow(groupRowKey(row.group));
        }
        break;
    }
  }

  function onSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      focusAt(0);
    } else if (e.key === 'Enter') {
      // Jump straight to the first hit.
      const first = rows.find((r) => r.kind === 'entry');
      if (first) {
        e.preventDefault();
        onselect({ kind: first.entryKind, id: first.id });
      }
    } else if (e.key === 'Escape' && query) {
      e.preventDefault();
      query = '';
    }
  }

  function isGuide(entry: unknown): entry is DisplayGuide {
    return typeof (entry as DisplayGuide).iconName === 'string';
  }

  /** The user-authored guide group owns loading state and the + button. */
  const LIBRARY_GROUP = 'my-guides';

  const focusRing =
    'focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent';
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="shrink-0 border-b border-dark-border p-3">
    <label for="docs-search" class="sr-only">Search documentation</label>
    <div class="relative">
      <Search
        size={13}
        class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-dark-text-muted"
        aria-hidden="true"
      />
      <input
        id="docs-search"
        type="search"
        bind:value={query}
        onkeydown={onSearchKeydown}
        placeholder="Search docs…"
        autocomplete="off"
        class={[
          'h-8 w-full border border-dark-border-subtle bg-dark-base pl-7 pr-12 text-xs text-dark-text placeholder:text-dark-text-muted',
          focusRing,
        ]}
      />
      {#if query}
        <button
          type="button"
          onclick={() => (query = '')}
          aria-label="Clear search"
          class="absolute right-1 top-1/2 -translate-y-1/2 p-1 text-dark-text-muted hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
        >
          <X size={12} aria-hidden="true" />
        </button>
      {:else}
        <kbd
          class="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 border border-dark-border-subtle px-1 text-[10px] text-dark-text-faint"
          aria-hidden="true">/</kbd
        >
      {/if}
    </div>
  </div>

  <nav aria-label="Documentation" class="min-h-0 flex-1 overflow-y-auto py-2">
    <ul bind:this={treeEl} role="tree" aria-label="Documentation sections" onkeydown={onTreeKeydown}>
      {#each groups as group (group.id)}
        {@const open = groupExpanded(expanded, group.id)}
        {@const gKey = groupRowKey(group.id)}
        {@const isLibrary = group.id === LIBRARY_GROUP}
        <li role="none" class="mb-2">
          <div class="flex items-center pr-2">
            <button
              type="button"
              role="treeitem"
              data-nav-key={gKey}
              aria-expanded={open}
              aria-selected="false"
              aria-owns={open ? `docs-group-${group.id}` : undefined}
              tabindex={tabKey === gKey ? 0 : -1}
              onclick={() => {
                focusKey = gKey;
                setGroup(group.id, !open);
              }}
              class={[
                'flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1 text-left text-[11px] font-medium text-dark-text-muted hover:text-dark-text',
                focusRing,
              ]}
            >
              <ChevronRight
                size={12}
                class={`shrink-0 text-dark-text-faint ${open ? 'rotate-90' : ''}`}
                aria-hidden="true"
              />
              <span class="flex-1 truncate">{group.title}</span>
              {#if isLibrary && guidesLoading}
                <span class="sr-only">Loading guides</span>
                <Loader2 size={11} class="animate-spin motion-reduce:animate-none" aria-hidden="true" />
              {:else if isLibrary && guidesError}
                <AlertTriangle size={11} class="text-oc-peach" aria-hidden="true" />
              {:else if searching}
                <span class="tabular-nums text-dark-text-faint">{group.entries.length}/{group.total}</span>
              {/if}
            </button>
            {#if isLibrary}
              <button
                type="button"
                onclick={onnewguide}
                aria-label="New guide"
                title="New guide"
                class="shrink-0 p-1 text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
              >
                <Plus size={12} aria-hidden="true" />
              </button>
            {/if}
          </div>

          {#if open}
            <ul role="group" id={`docs-group-${group.id}`}>
              {#if isLibrary && guidesError}
                <li role="none" class="px-4 py-1.5">
                  <p class="text-[11px] leading-relaxed text-oc-peach">{guidesError}</p>
                  <button type="button" onclick={onretryguides} class="settings-button mt-1.5">Retry</button>
                </li>
              {/if}

              {#each group.entries as entry (entry.id)}
                {@const key = navRowKey(group.kind as DocsKind, entry.id)}
                {@const active = key === activeKey}
                {@const Icon = isGuide(entry) ? iconFor(entry.iconName) : null}
                <li role="none">
                  <button
                    type="button"
                    role="treeitem"
                    data-nav-key={key}
                    aria-selected={active}
                    aria-current={active ? 'page' : undefined}
                    tabindex={key === tabKey ? 0 : -1}
                    title={entry.description}
                    onclick={() => {
                      focusKey = key;
                      onselect({ kind: group.kind, id: entry.id });
                    }}
                    class={[
                      'flex w-full items-center gap-2 border-l-2 py-1 pl-6 pr-3 text-left text-xs',
                      focusRing,
                      active
                        ? 'border-oc-peach bg-dark-elevated text-dark-text'
                        : 'border-transparent text-dark-text-secondary hover:bg-dark-surface hover:text-dark-text',
                    ]}
                  >
                    {#if Icon}
                      <Icon
                        size={12}
                        class={`shrink-0 ${active ? 'text-oc-peach' : 'text-dark-text-faint'}`}
                        aria-hidden="true"
                      />
                    {/if}
                    <span class="min-w-0 flex-1 truncate">{entry.title}</span>
                  </button>
                </li>
              {:else}
                {#if !(isLibrary && (guidesLoading || guidesError))}
                  <li role="none" class="py-1 pl-6 pr-3 text-[11px] text-dark-text-faint">
                    {searching ? 'No match.' : isLibrary ? 'None yet — use + to write one.' : 'Empty.'}
                  </li>
                {/if}
              {/each}
            </ul>
          {/if}
        </li>
      {/each}
    </ul>

    {#if noResults}
      <div class="mx-3 mt-2 border border-dark-border px-3 py-4 text-center">
        <p class="text-xs leading-relaxed text-dark-text-secondary">
          Nothing matches <span class="text-dark-text">“{query}”</span>.
        </p>
        <button type="button" onclick={() => (query = '')} class="settings-button mt-3">
          Clear search
        </button>
      </div>
    {/if}
  </nav>
</div>
