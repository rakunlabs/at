<script lang="ts">
  import { storeNavbar, storeTheme, storeInfo } from '@/lib/store/store.svelte';
  import { ChevronLeft, Menu, Moon, Sun } from 'lucide-svelte';
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
    {#if storeInfo.name}
      <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted">{storeInfo.name}</span>
    {/if}
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