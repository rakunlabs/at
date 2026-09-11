<script lang="ts">
  import { location } from 'svelte-spa-router';
  import { routeAllowed, configurationLinks } from '../helper/navigation';
  import { onMount } from 'svelte';
  import { isFeatureEnabled, loadFeatures } from '../store/features.svelte';
  const features: Record<string, string> = { '/playground':'chat_workbench', '/sessions':'chat_workbench', '/agents':'agents', '/tasks':'organization_workflows', '/organizations':'organization_workflows', '/workflows':'automation', '/runs':'automation', '/bots':'chat_workbench', '/studio':'organization_workflows', '/files':'files', '/integrations':'connections_integrations', '/usage':'provider_setup' };
  onMount(() => { void loadFeatures().catch(() => {}); });
  import { House, MessageSquare, Bot, Workflow, ClipboardList, FolderOpen, Settings, Activity, BookOpen, Building2, Clapperboard, WandSparkles, Radio, Package, BarChart3 } from 'lucide-svelte';
  const items = [
    {path:'/',label:'Home',icon:House},
    {path:'/playground',label:'Playground',icon:MessageSquare}, {path:'/sessions',label:'Sessions',icon:MessageSquare},
    {path:'/agents',label:'Agents',icon:Bot}, {path:'/tasks',label:'Tasks',icon:ClipboardList},
    {path:'/organizations',label:'Organizations',icon:Building2}, {path:'/workflows',label:'Workflows',icon:Workflow},
    {path:'/runs',label:'Runs',icon:Activity}, {path:'/skills',label:'Skills',icon:WandSparkles},
    {path:'/bots',label:'Bots',icon:Radio}, {path:'/studio',label:'Studio',icon:Clapperboard},
    {path:'/files',label:'Files',icon:FolderOpen}, {path:'/integrations',label:'Integrations',icon:Package},
    {path:'/usage',label:'Usage',icon:BarChart3}, {path:'/llm-calls',label:'Traces',icon:Activity},
  ];
</script>
<aside class="border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col h-full overflow-y-auto">
  <a href="#/" class="px-3 py-4 text-base font-semibold">AT</a>
  <nav aria-label="Main navigation" class="flex-1 px-2 space-y-1 pb-4">
    {#each items.filter(item => routeAllowed(item.path) && (!features[item.path] || isFeatureEnabled(features[item.path]))) as item}<a href={`#${item.path}`} aria-current={$location === item.path ? 'page' : undefined} class={['flex items-center gap-2 rounded-md px-2 py-2 text-xs focus-visible:outline-2 focus-visible:outline-accent', $location === item.path || (item.path !== '/' && $location.startsWith(item.path + '/')) ? 'bg-gray-100 dark:bg-dark-elevated font-semibold' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated']}><item.icon size={15} /><span>{item.label}</span></a>{/each}
  </nav>
  <nav aria-label="Application" class="px-2 py-3 border-t border-gray-200 dark:border-dark-border space-y-1"><a href="#/docs" class="flex items-center gap-2 rounded-md px-2 py-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated"><BookOpen size={15} />Documentation</a><a href="#/settings" aria-current={$location.startsWith('/settings') || configurationLinks.some(l => l.path === $location) ? 'page' : undefined} class="flex items-center gap-2 rounded-md px-2 py-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated"><Settings size={15} />Settings</a></nav>
</aside>
