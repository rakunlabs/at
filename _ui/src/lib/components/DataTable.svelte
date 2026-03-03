<script lang="ts" generics="T">
  import { type Snippet, type Component } from 'svelte';
  import { Search, X } from 'lucide-svelte';
  import Pagination from './Pagination.svelte';

  interface Props {
    items: T[];
    loading?: boolean;
    
    // Pagination props
    total?: number;
    limit?: number;
    offset?: number;
    onchange?: (newOffset: number) => void;

    // Search props
    searchValue?: string;
    searchPlaceholder?: string;
    onsearch?: (value: string) => void;

    // Snippets
    header: Snippet;
    row: Snippet<[T]>;
    empty?: Snippet;
    
    // Empty state configuration (used if empty snippet is not provided)
    emptyTitle?: string;
    emptyDescription?: string;
    emptyAction?: Snippet;
    emptyIcon?: any;
  }

  let { 
    items = [], 
    loading = false,
    total = 0,
    limit = 10,
    offset = $bindable(0),
    onchange,
    searchValue = $bindable(''),
    searchPlaceholder = 'Search by name...',
    onsearch,
    header,
    row,
    empty,
    emptyTitle = 'No items found',
    emptyDescription = '',
    emptyAction,
    emptyIcon: Icon
  }: Props = $props();

  let searchInput = $state(searchValue);

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      commitSearch();
    }
    if (e.key === 'Escape') {
      searchInput = '';
      commitSearch();
    }
  }

  function commitSearch() {
    searchValue = searchInput;
    onsearch?.(searchInput);
  }

  function clearSearch() {
    searchInput = '';
    searchValue = '';
    onsearch?.('');
  }
</script>

<div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
  {#if onsearch}
    <div class="px-4 py-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
      <div class="relative w-64">
        <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" size={13} />
        <input
          type="text"
          bind:value={searchInput}
          onkeydown={handleSearchKeydown}
          onblur={commitSearch}
          placeholder={searchPlaceholder}
          class="w-full pl-8 pr-7 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:border-gray-500 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
        />
        {#if searchInput}
          <button
            onclick={clearSearch}
            class="absolute right-1.5 top-1/2 -translate-y-1/2 p-0.5 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
          >
            <X size={12} />
          </button>
        {/if}
      </div>
    </div>
  {/if}

  {#if loading}
    <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">Loading...</div>
  {:else if items.length === 0}
    {#if empty}
      {@render empty()}
    {:else}
      <div class="px-4 py-10 text-center">
        {#if Icon}
          <Icon size={24} class="mx-auto text-gray-300 dark:text-dark-text-faint mb-2" />
        {/if}
        <div class="text-gray-400 dark:text-dark-text-muted mb-1">{emptyTitle}</div>
        {#if emptyDescription}
          <div class="text-xs text-gray-400 dark:text-dark-text-muted mb-3">{emptyDescription}</div>
        {/if}
        {#if emptyAction}
          {@render emptyAction()}
        {/if}
      </div>
    {/if}
  {:else}
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
          {@render header()}
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
        {#each items as item}
          {@render row(item)}
        {/each}
      </tbody>
    </table>
    
    {#if total > 0}
      <Pagination {total} {limit} bind:offset {onchange} />
    {/if}
  {/if}
</div>
