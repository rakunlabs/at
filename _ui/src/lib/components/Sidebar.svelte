<script lang="ts">
  import BrandLogo from './BrandLogo.svelte';
  import { storeInfo } from '../store/store.svelte';
  import { location } from 'svelte-spa-router';
  import { routeAllowed, inSettingsArea } from '../helper/navigation';
  import { onMount } from 'svelte';
  import { loadFeatures } from '../store/features.svelte';
  import { routeFeatureEnabled } from '../helper/feature-routes';
  onMount(() => { void loadFeatures().catch(() => {}); });
  import { House, MessageSquare, Bot, Workflow, ClipboardList, FolderOpen, Settings, Activity, BookOpen, Building2, Clapperboard, WandSparkles, Radio, Package, Tally5, TerminalSquare, X } from 'lucide-svelte';
  interface Props { onclose?: () => void }
  let { onclose }: Props = $props();
  const items = [
    {path:'/',label:'Home',icon:House},
    {path:'/chats',label:'Chats',icon:MessageSquare}, {path:'/sessions',label:'Sessions',icon:MessageSquare},
    {path:'/agents',label:'Agents',icon:Bot}, {path:'/tasks',label:'Tasks',icon:ClipboardList},
    {path:'/organizations',label:'Organizations',icon:Building2}, {path:'/workflows',label:'Workflows',icon:Workflow},
    {path:'/runs',label:'Runs',icon:Activity}, {path:'/skills',label:'Skills',icon:WandSparkles},
    {path:'/bots',label:'Bots',icon:Radio}, {path:'/studio',label:'Studio',icon:Clapperboard},
    {path:'/files',label:'Files',icon:FolderOpen}, {path:'/integrations',label:'Integrations',icon:Package},
    {path:'/usage',label:'Usage',icon:Tally5}, {path:'/llm-calls',label:'Traces',icon:Activity},
    {path:'/terminal',label:'Terminal',icon:TerminalSquare},
  ];
  // One selection rule for every link, including the bottom nav: you are inside
  // a section whenever the route is the link or below it, so `/tasks/:id` keeps
  // Tasks marked and a configuration page keeps Settings marked — the settings
  // layout swaps the page area for its own sidebar, which otherwise left the
  // shell looking as though nothing was selected. `aria-current` follows the
  // same rule as the styling; it used to match only the exact path, so assistive
  // technology was told nothing was current on every detail route.
  function navActive(path: string) {
    if (path === '/settings') return inSettingsArea($location);
    return $location === path || (path !== '/' && $location.startsWith(path + '/'));
  }
  const navClass = (active: boolean) => [
    'flex items-center gap-2 rounded-md px-2 py-2 text-xs focus-visible:outline-2 focus-visible:outline-accent',
    active
      ? 'bg-gray-100 dark:bg-dark-elevated font-semibold'
      : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated',
  ];
</script>
<aside class="app-sidebar border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col h-full overflow-y-auto">
  <div class="flex items-center justify-between gap-2 pr-2">
    <a href="#/" class="flex min-w-0 items-center gap-2 px-3 py-2 text-base font-semibold focus-visible:outline-2 focus-visible:outline-accent"><BrandLogo decorative /><span class="truncate" title={storeInfo.name || 'AT'}>{storeInfo.name || 'AT'}</span></a>
    {#if onclose}
      <button type="button" aria-label="Close navigation" onclick={onclose} class="flex size-11 shrink-0 items-center justify-center text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"><X size={20} /></button>
    {/if}
  </div>
  <nav aria-label="Main navigation" class="flex-1 px-2 space-y-1 pb-4">
    <!-- Keyed by path: the list shrinks once the feature catalog loads (everything
         reads enabled until then), and an unkeyed each reuses each index's DOM —
         which updated the label but left the previous entry's icon behind. -->
    {#each items.filter(item => routeAllowed(item.path) && routeFeatureEnabled(item.path)) as item (item.path)}<a href={`#${item.path}`} aria-current={navActive(item.path) ? 'page' : undefined} class={navClass(navActive(item.path))}><item.icon size={15} /><span>{item.label}</span></a>{/each}
  </nav>
  <!-- Settings is never gated (it is the way back to the Features page, and the
       only surface an account with no workspace can use), but Documentation is:
       its guides API answers 404 once the feature is off and 403 for an account
       with no workspace, so the link is filtered on both. -->
  <nav aria-label="Application" class="px-2 py-3 border-t border-gray-200 dark:border-dark-border space-y-1">{#if routeAllowed('/docs') && routeFeatureEnabled('/docs')}<a href="#/docs" aria-current={navActive('/docs') ? 'page' : undefined} class={navClass(navActive('/docs'))}><BookOpen size={15} />Documentation</a>{/if}<a href="#/settings" aria-current={navActive('/settings') ? 'page' : undefined} class={navClass(navActive('/settings'))}><Settings size={15} />Settings</a></nav>
</aside>
