<script lang="ts">
  import { configurationLinks, routeAllowed } from '../lib/helper/navigation';
  import { storeNavbar } from '../lib/store/store.svelte';
  import { ChevronRight } from 'lucide-svelte';
  storeNavbar.title = 'Settings';
</script>
<div class="settings-page"><header><h1 class="text-2xl font-semibold">Settings</h1><p class="settings-note mt-2">Your account, workspace and installation configuration in one place.</p></header>
<nav aria-label="Configuration sections" class="divide-y divide-gray-200 dark:divide-dark-border">{#each configurationLinks.filter(item => routeAllowed(item.path)) as item}<a href={`#${item.path}`} class="flex items-center justify-between gap-4 py-4 rounded-md hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"><div><h2 class="text-sm font-semibold">{item.label}</h2><p class="settings-note mt-1">{item.description}</p></div><ChevronRight size={16} class="shrink-0" /></a>{/each}</nav>
</div>
