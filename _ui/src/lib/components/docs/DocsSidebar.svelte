<script lang="ts">
  // The single navigation surface for /docs: one search box and two
  // collapsible groups. Implemented as an ARIA tree with roving tabindex —
  // arrow keys walk the visible rows, Enter/Space activate (native button
  // behaviour), ArrowLeft/Right collapse/expand and move between levels.
  import {
    Search,
    X,
    Plus,
    ChevronRight,
    Lock,
    Pencil,
    Loader2,
    AlertTriangle,
  } from 'lucide-svelte';
  import {
    buildNavRows,
    navRowKey,
    stepIndex,
    type DocsGroupId,
    type DocsGroupState,
    type DocsSelection,
  } from '@/lib/helper/docs-nav';
  import type { ApiSectionMeta } from './api-sections';
  import type { DisplayGuide } from './builtin-guides';
  import { iconFor } from './guide-icons';

  interface Props {
    apiEntries: ApiSectionMeta[];
    guideEntries: DisplayGuide[];
    apiTotal: number;
    guideTotal: number;
    selection: DocsSelection;
    query: string;
    groups: DocsGroupState;
    guidesLoading?: boolean;
    guidesError?: string;
    onselect: (selection: DocsSelection) => void;
    onnewguide: () => void;
    onretryguides: () => void;
  }

  let {
    apiEntries,
    guideEntries,
    apiTotal,
    guideTotal,
    selection,
    query = $bindable(''),
    groups = $bindable(),
    guidesLoading = false,
    guidesError = '',
    onselect,
    onnewguide,
    onretryguides,
  }: Props = $props();

  let treeEl = $state<HTMLElement | null>(null);
  let focusKey = $state('');

  const searching = $derived(query.trim().length > 0);
  const rows = $derived(buildNavRows(apiEntries, guideEntries, groups));
  const activeKey = $derived(navRowKey(selection.kind, selection.id));
  const noResults = $derived(searching && apiEntries.length === 0 && guideEntries.length === 0);

  // Exactly one row is in the tab order. Prefer the row the user last moved
  // focus to, then the selected row, then the first row.
  const tabKey = $derived.by(() => {
    if (focusKey && rows.some((r) => r.key === focusKey)) return focusKey;
    if (rows.some((r) => r.key === activeKey)) return activeKey;
    return rows[0]?.key ?? '';
  });

  // The first user (non-builtin) guide starts the "My guides" run. Headings are
  // decorative, so arrow-key order still matches buildNavRows exactly.
  const firstUserIndex = $derived(guideEntries.findIndex((g) => !g.builtin));

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
    if (!groups[selection.kind]) return;
    rowEl(key)?.scrollIntoView({ block: 'nearest' });
  });

  function toggleGroup(id: DocsGroupId) {
    groups = { ...groups, [id]: !groups[id] };
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
          if (!groups[row.group]) groups = { ...groups, [row.group]: true };
          else focusAt(stepIndex(rows, index, 1));
        }
        break;
      case 'ArrowLeft':
        e.preventDefault();
        if (row.kind === 'group') {
          if (groups[row.group]) groups = { ...groups, [row.group]: false };
        } else {
          focusRow(`group:${row.group}`);
        }
        break;
    }
  }

  function onSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      focusAt(0);
    } else if (e.key === 'Escape' && query) {
      e.preventDefault();
      query = '';
    }
  }

  const rowBase =
    'flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors motion-reduce:transition-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-gray-900 dark:focus-visible:outline-accent';
  const groupRow = `${rowBase} font-semibold text-gray-900 dark:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated`;
  const subHeading =
    'flex items-center gap-1.5 px-3 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary';
</script>

{#snippet entryRow(group: DocsGroupId, id: string, title: string, description: string, IconCmp: any)}
  {@const key = navRowKey(group, id)}
  {@const active = key === activeKey}
  <li role="none">
    <button
      type="button"
      role="treeitem"
      data-nav-key={key}
      aria-selected={active}
      tabindex={key === tabKey ? 0 : -1}
      onclick={() => {
        focusKey = key;
        onselect({ kind: group, id });
      }}
      class={[
        rowBase,
        'items-start border-l-2 pl-4',
        active
          ? 'border-gray-900 bg-gray-100 font-medium text-gray-900 dark:border-accent dark:bg-dark-elevated dark:text-dark-text'
          : 'border-transparent text-gray-700 hover:bg-gray-50 hover:text-gray-900 dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:hover:text-dark-text',
      ]}
    >
      {#if IconCmp}
        <IconCmp size={13} class="mt-0.5 shrink-0" aria-hidden="true" />
      {/if}
      <span class="min-w-0 flex-1">
        <span class="block truncate">{title}</span>
        {#if description}
          <span class="mt-0.5 block truncate text-[11px] text-gray-600 dark:text-dark-text-secondary">
            {description}
          </span>
        {/if}
      </span>
    </button>
  </li>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-white dark:bg-dark-surface">
  <div class="shrink-0 border-b border-gray-200 p-3 dark:border-dark-border">
    <label for="docs-search" class="sr-only">Search documentation</label>
    <div class="relative">
      <Search
        size={13}
        class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-600 dark:text-dark-text-secondary"
        aria-hidden="true"
      />
      <input
        id="docs-search"
        type="search"
        bind:value={query}
        onkeydown={onSearchKeydown}
        placeholder="Search docs and guides…"
        class="h-8 w-full border border-gray-300 bg-white pl-7 pr-7 text-xs text-gray-900 placeholder:text-gray-600 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-gray-900 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-secondary dark:focus-visible:outline-accent"
      />
      {#if query}
        <button
          type="button"
          onclick={() => (query = '')}
          aria-label="Clear search"
          class="absolute right-1 top-1/2 -translate-y-1/2 p-1 text-gray-600 hover:text-gray-900 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-gray-900 dark:text-dark-text-secondary dark:hover:text-dark-text dark:focus-visible:outline-accent"
        >
          <X size={12} aria-hidden="true" />
        </button>
      {/if}
    </div>
  </div>

  <nav aria-label="Documentation" class="min-h-0 flex-1 overflow-y-auto">
    <ul
      bind:this={treeEl}
      role="tree"
      aria-label="Documentation sections"
      onkeydown={onTreeKeydown}
      class="py-1"
    >
      <!-- ─── API docs ─── -->
      <li role="none">
        <button
          type="button"
          role="treeitem"
          data-nav-key="group:api"
          aria-expanded={groups.api}
          aria-selected="false"
          aria-owns={groups.api ? 'docs-group-api' : undefined}
          tabindex={tabKey === 'group:api' ? 0 : -1}
          onclick={() => {
            focusKey = 'group:api';
            toggleGroup('api');
          }}
          class={groupRow}
        >
          <ChevronRight
            size={13}
            class="shrink-0 transition-transform motion-reduce:transition-none {groups.api
              ? 'rotate-90'
              : ''}"
            aria-hidden="true"
          />
          <span class="flex-1">API docs</span>
          <span class="text-[11px] font-normal tabular-nums text-gray-600 dark:text-dark-text-secondary">
            {searching ? `${apiEntries.length}/${apiTotal}` : apiTotal}
          </span>
        </button>
        {#if groups.api}
          <ul role="group" id="docs-group-api">
            {#each apiEntries as section (section.id)}
              {@render entryRow('api', section.id, section.title, section.description, null)}
            {:else}
              <li role="none" class="px-4 py-2 text-[11px] text-gray-600 dark:text-dark-text-secondary">
                No API section matches.
              </li>
            {/each}
          </ul>
        {/if}
      </li>

      <!-- ─── Guides ─── -->
      <li role="none">
        <div class="mt-1 flex items-center border-t border-gray-200 pt-1 dark:border-dark-border">
          <button
            type="button"
            role="treeitem"
            data-nav-key="group:guides"
            aria-expanded={groups.guides}
            aria-selected="false"
            aria-owns={groups.guides ? 'docs-group-guides' : undefined}
            tabindex={tabKey === 'group:guides' ? 0 : -1}
            onclick={() => {
              focusKey = 'group:guides';
              toggleGroup('guides');
            }}
            class={[groupRow, 'flex-1']}
          >
            <ChevronRight
              size={13}
              class="shrink-0 transition-transform motion-reduce:transition-none {groups.guides
                ? 'rotate-90'
                : ''}"
              aria-hidden="true"
            />
            <span class="flex-1">Guides</span>
            {#if guidesLoading}
              <span class="sr-only">Loading guides</span>
              <Loader2
                size={12}
                class="animate-spin motion-reduce:animate-none text-gray-600 dark:text-dark-text-secondary"
                aria-hidden="true"
              />
            {:else if guidesError}
              <AlertTriangle size={12} class="text-amber-700 dark:text-amber-300" aria-hidden="true" />
            {:else}
              <span class="text-[11px] font-normal tabular-nums text-gray-600 dark:text-dark-text-secondary">
                {searching ? `${guideEntries.length}/${guideTotal}` : guideTotal}
              </span>
            {/if}
          </button>
          <button
            type="button"
            onclick={onnewguide}
            aria-label="New guide"
            title="New guide"
            class="mr-2 shrink-0 border border-gray-300 p-1 text-gray-700 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:hover:text-dark-text dark:focus-visible:outline-accent"
          >
            <Plus size={13} aria-hidden="true" />
          </button>
        </div>

        {#if groups.guides}
          <ul role="group" id="docs-group-guides">
            {#if guidesError}
              <li role="none" class="px-4 py-2">
                <p class="text-[11px] leading-relaxed text-amber-800 dark:text-amber-300">
                  {guidesError}
                </p>
                <button
                  type="button"
                  onclick={onretryguides}
                  class="mt-1.5 border border-gray-300 px-2 py-1 text-[11px] font-medium text-gray-700 hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent"
                >
                  Retry
                </button>
              </li>
            {/if}

            {#each guideEntries as guide, i (guide.id)}
              {#if i === 0 && guide.builtin}
                <li role="none" class={subHeading}>
                  <Lock size={10} aria-hidden="true" />
                  Built-in
                </li>
              {/if}
              {#if i === firstUserIndex}
                <li role="none" class={subHeading}>
                  <Pencil size={10} aria-hidden="true" />
                  My guides
                </li>
              {/if}
              {@render entryRow(
                'guides',
                guide.id,
                guide.title,
                guide.description,
                iconFor(guide.iconName),
              )}
            {/each}

            {#if !guidesLoading && !guidesError && firstUserIndex < 0 && !searching}
              <li role="none" class="px-4 pb-2 pt-1">
                <p class="text-[11px] leading-relaxed text-gray-600 dark:text-dark-text-secondary">
                  No guides of your own yet. Use
                  <span class="font-medium text-gray-900 dark:text-dark-text">+</span> above to write one.
                </p>
              </li>
            {/if}

            {#if searching && guideEntries.length === 0 && !guidesError}
              <li role="none" class="px-4 py-2 text-[11px] text-gray-600 dark:text-dark-text-secondary">
                No guide matches.
              </li>
            {/if}
          </ul>
        {/if}
      </li>
    </ul>

    {#if noResults}
      <div class="border-t border-gray-200 px-4 py-6 text-center dark:border-dark-border">
        <Search size={20} class="mx-auto text-gray-600 dark:text-dark-text-secondary" aria-hidden="true" />
        <p class="mt-2 text-xs leading-relaxed text-gray-700 dark:text-dark-text-secondary">
          Nothing matches <span class="font-medium text-gray-900 dark:text-dark-text">“{query}”</span>
          in the API docs or guides.
        </p>
        <button
          type="button"
          onclick={() => (query = '')}
          class="mt-3 border border-gray-300 px-2.5 py-1 text-[11px] font-medium text-gray-700 hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent"
        >
          Clear search
        </button>
      </div>
    {/if}
  </nav>
</div>
