<script lang="ts">
  import { location } from 'svelte-spa-router';
  import { routeAllowed, configurationLinks } from '../helper/navigation';
</script>
<aside class="border-b sm:border-b-0 sm:border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3">
  <a href="#/settings" class="block px-2 py-2 font-semibold text-sm">Settings</a>
  <nav aria-label="Settings" class="flex sm:flex-col overflow-x-auto gap-1">
    {#each configurationLinks.filter(item => routeAllowed(item.path)) as item}<a href={`#${item.path}`} aria-current={$location === item.path ? 'page' : undefined} class={['shrink-0 rounded-md px-2 py-2 text-xs focus-visible:outline-2 focus-visible:outline-accent', $location === item.path ? 'bg-gray-100 dark:bg-dark-elevated font-semibold' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}>{item.label}</a>{/each}
  </nav>
</aside>
