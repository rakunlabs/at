<script lang="ts">
  import { location } from 'svelte-spa-router';
  import { settingsLinkVisible, configurationLinks } from '../helper/navigation';
</script>
<aside class="flex flex-col min-h-0 min-w-0 overflow-hidden border-b sm:border-b-0 sm:border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3">
  <!-- No "Settings" heading: the navbar already titles the page and the shell's
       sidebar now marks Settings as the current section, which is also the way
       back to the settings index. -->
  <nav aria-label="Settings" class="flex sm:flex-col min-h-0 min-w-0 sm:flex-1 overflow-x-auto sm:overflow-x-hidden sm:overflow-y-auto overscroll-contain gap-1">
    {#each configurationLinks.filter(item => settingsLinkVisible(item.path)) as item}<a href={`#${item.path}`} aria-current={$location === item.path ? 'page' : undefined} class={['shrink-0 rounded-md px-2 py-2 text-xs focus-visible:outline-2 focus-visible:outline-accent', $location === item.path ? 'bg-gray-100 dark:bg-dark-elevated font-semibold' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}>{item.label}</a>{/each}
  </nav>
</aside>
