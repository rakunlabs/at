<script lang="ts">
  import BrandLogo from './BrandLogo.svelte';
  import { storeInfo } from '../store/store.svelte';
  import { router } from 'svelte-spa-router';
  import { routeAllowed, inSettingsArea } from '../helper/navigation';
  import { onMount } from 'svelte';
  import { loadFeatures } from '../store/features.svelte';
  import { routeFeatureEnabled } from '../helper/feature-routes';
  import { House, MessageSquare, Bot, Workflow, ClipboardList, FolderOpen, Settings, Activity, BookOpen, Building2, Clapperboard, WandSparkles, Radio, Package, Tally5, TerminalSquare, X, Cpu, Layers, Container } from 'lucide-svelte';
  interface Props { onclose?: () => void }
  let { onclose }: Props = $props();
  let scroller = $state<HTMLElement | null>(null);
  let scrollContent = $state<HTMLElement | null>(null);
  let scrollThumbVisible = $state(false);
  let scrollThumbHeight = $state(0);
  let scrollThumbTop = $state(0);
  let scrollHideTimer: ReturnType<typeof setTimeout> | null = null;
  const items = [
    {path:'/',label:'Home',icon:House},
    {path:'/chats',label:'Chats',icon:MessageSquare}, {path:'/sessions',label:'Sessions',icon:MessageSquare},
    {path:'/providers',label:'Model Providers',icon:Cpu},
    {path:'/agents',label:'Agents',icon:Bot}, {path:'/tasks',label:'Tasks',icon:ClipboardList},
    {path:'/organizations',label:'Organizations',icon:Building2}, {path:'/workflows',label:'Workflows',icon:Workflow},
    {path:'/runs',label:'Runs',icon:Activity}, {path:'/skills',label:'Skills',icon:WandSparkles},
    {path:'/mcps',label:'MCP Sets',icon:Layers},
    {path:'/bots',label:'Bots',icon:Radio}, {path:'/studio',label:'Studio',icon:Clapperboard},
    {path:'/files',label:'Files',icon:FolderOpen}, {path:'/developer-spaces',label:'Developer Spaces',icon:Container}, {path:'/integrations',label:'Integrations',icon:Package},
    {path:'/usage',label:'Usage',icon:Tally5}, {path:'/traces',label:'Traces',icon:Activity},
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
    if (path === '/settings') return inSettingsArea(router.location);
    return router.location === path || (path !== '/' && router.location.startsWith(path + '/'));
  }
  const navClass = (active: boolean) => [
    'flex min-w-0 items-center gap-2 px-2 py-2 text-xs focus-visible:outline-2 focus-visible:outline-accent',
    active
      ? 'bg-dark-elevated font-semibold'
      : 'text-dark-text-secondary hover:bg-dark-elevated',
  ];

  function updateScrollThumb() {
    if (!scroller) return;
    const { clientHeight, scrollHeight, scrollTop } = scroller;
    if (scrollHeight <= clientHeight + 1) {
      scrollThumbHeight = 0;
      scrollThumbTop = 0;
      scrollThumbVisible = false;
      return;
    }

    scrollThumbHeight = Math.max(24, (clientHeight / scrollHeight) * clientHeight);
    const maxThumbTop = clientHeight - scrollThumbHeight;
    scrollThumbTop = (scrollTop / (scrollHeight - clientHeight)) * maxThumbTop;
  }

  function handleScroll() {
    updateScrollThumb();
    if (scrollThumbHeight === 0) return;
    scrollThumbVisible = true;
    if (scrollHideTimer) clearTimeout(scrollHideTimer);
    scrollHideTimer = setTimeout(() => {
      scrollThumbVisible = false;
      scrollHideTimer = null;
    }, 700);
  }

  onMount(() => {
    let frame = requestAnimationFrame(updateScrollThumb);
    const refresh = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(updateScrollThumb);
    };
    void loadFeatures().catch(() => {}).finally(refresh);

    const observer = new ResizeObserver(refresh);
    if (scroller) observer.observe(scroller);
    if (scrollContent) observer.observe(scrollContent);

    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      if (scrollHideTimer) clearTimeout(scrollHideTimer);
    };
  });
</script>
<aside class="app-sidebar relative h-full overflow-hidden border-r border-dark-border bg-dark-surface">
  <div bind:this={scroller} onscroll={handleScroll} class="sidebar-scroller h-full overflow-y-auto">
    <div bind:this={scrollContent} class="flex min-h-full flex-col">
      <div class="flex items-center justify-between gap-2 pr-2">
        <a href="#/" class="flex min-w-0 items-center gap-2 px-3 py-2 text-base font-semibold focus-visible:outline-2 focus-visible:outline-accent"><BrandLogo decorative /><span class="truncate" title={storeInfo.name || 'AT'}>{storeInfo.name || 'AT'}</span></a>
        {#if onclose}
          <button type="button" aria-label="Close navigation" onclick={onclose} class="flex size-11 shrink-0 items-center justify-center text-dark-text-muted hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"><X size={20} /></button>
        {/if}
      </div>
      <nav aria-label="Main navigation" class="flex-1 px-2 space-y-1 pb-4">
        <!-- Keyed by path: the list shrinks once the feature catalog loads (everything
             reads enabled until then), and an unkeyed each reuses each index's DOM —
             which updated the label but left the previous entry's icon behind. -->
        {#each items.filter(item => routeAllowed(item.path) && routeFeatureEnabled(item.path)) as item (item.path)}<a href={`#${item.path}`} aria-current={navActive(item.path) ? 'page' : undefined} class={navClass(navActive(item.path))}><item.icon size={15} class="shrink-0" />{@render navLabel(item.label)}</a>{/each}
      </nav>
      <!-- Settings is never gated (it is the way back to the Features page, and the
           only surface an account with no workspace can use), but Documentation is:
           its guides API answers 404 once the feature is off and 403 for an account
           with no workspace, so the link is filtered on both. -->
      <nav aria-label="Application" class="px-2 py-3 border-t border-dark-border space-y-1">{#if routeAllowed('/docs') && routeFeatureEnabled('/docs')}<a href="#/docs" aria-current={navActive('/docs') ? 'page' : undefined} class={navClass(navActive('/docs'))}><BookOpen size={15} class="shrink-0" />{@render navLabel('Documentation')}</a>{/if}<a href="#/settings" aria-current={navActive('/settings') ? 'page' : undefined} class={navClass(navActive('/settings'))}><Settings size={15} class="shrink-0" />{@render navLabel('Settings')}</a></nav>
    </div>
  </div>
  {#if scrollThumbHeight > 0}
    <div
      aria-hidden="true"
      class={['pointer-events-none absolute right-0.5 z-10 w-1 rounded-full bg-dark-text-muted/70', scrollThumbVisible ? '' : 'hidden']}
      style={`height: ${scrollThumbHeight}px; transform: translateY(${scrollThumbTop}px);`}
    ></div>
  {/if}
</aside>

<!-- The selected link is semibold. A hidden semibold copy shares the grid cell
     so the label always reserves its bold width: selecting a link no longer
     widens it, and a long label cannot wrap onto a second line. -->
{#snippet navLabel(label: string)}
  <span class="grid min-w-0">
    <span class="col-start-1 row-start-1 truncate whitespace-nowrap">{label}</span>
    <span aria-hidden="true" class="invisible col-start-1 row-start-1 h-0 overflow-hidden whitespace-nowrap font-semibold">{label}</span>
  </span>
{/snippet}
