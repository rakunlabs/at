<script lang="ts">
  import { storeNavbar, storeTheme, storeInfo } from '@/lib/store/store.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { ChevronLeft, Menu, Moon, Sun } from 'lucide-svelte';

  $effect(() => {
    getInfo().then((info) => {
      storeInfo.name = info.name || 'AT';
      storeInfo.version = info.version || '';
      storeInfo.commit = info.commit || '';
      storeInfo.build_date = info.build_date || '';
      storeInfo.user = info.user || '';
      storeInfo.store_type = info.store_type || '';
      storeInfo.workspace_root = info.workspace_root || '';
      storeInfo.assets_root = info.assets_root || '';
    }).catch(() => {});
  });
</script>

<div class="bg-white dark:bg-dark-surface border-b border-gray-200 dark:border-dark-border flex items-center px-2 h-full transition-colors">
  <button
    class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:text-gray-900 dark:hover:text-dark-text transition-colors"
    onclick={() => (storeNavbar.sideBarOpen = !storeNavbar.sideBarOpen)}
  >
    {#if storeNavbar.sideBarOpen}
      <ChevronLeft size={16} />
    {:else}
      <Menu size={16} />
    {/if}
  </button>
  <span class="ml-2 text-sm font-medium text-gray-800 dark:text-dark-text">
    {storeNavbar.title}
  </span>

  <div class="ml-auto flex items-center gap-4">
    <div class="flex items-center gap-2 text-xs">
      {#if storeInfo.name}
        <span class="font-semibold text-gray-700 dark:text-dark-text-secondary">{storeInfo.name}</span>
      {/if}
      {#if storeInfo.user}
        <span class="h-3 w-px bg-gray-200 dark:bg-dark-border"></span>
        <span class="max-w-56 truncate text-gray-500 dark:text-dark-text-muted" title={storeInfo.user}>{storeInfo.user}</span>
      {/if}
    </div>
    <button
      onclick={() => (storeTheme.mode = storeTheme.mode === 'light' ? 'dark' : 'light')}
      class="p-1.5 text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text transition-colors"
      title="Toggle theme"
    >
      {#if storeTheme.mode === 'dark'}
        <Sun size={16} />
      {:else}
        <Moon size={16} />
      {/if}
    </button>
  </div>
</div>
