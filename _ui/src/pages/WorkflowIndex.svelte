<script lang="ts">
  import { router } from 'svelte-spa-router';
  import { Activity, Workflow } from 'lucide-svelte';
  import { routeFeatureEnabled } from '@/lib/helper/feature-routes';
  import { createRouteLoader } from '@/lib/helper/route-loader';
  import RouteLoading from '@/lib/components/RouteLoading.svelte';
  import RouteLoadError from '@/lib/components/RouteLoadError.svelte';

  interface PageModule {
    default: typeof RouteLoadError | typeof import('./Workflows.svelte').default | typeof import('./Runs.svelte').default;
  }
  const loadWorkflows = createRouteLoader<PageModule>(() => import('./Workflows.svelte'), () => ({ default: RouteLoadError }));
  const loadRuns = createRouteLoader<PageModule>(() => import('./Runs.svelte'), () => ({ default: RouteLoadError }));
  let runsSelected = $derived(router.location === '/workflows/runs');
  let page = $derived(runsSelected ? loadRuns() : loadWorkflows());
  const tabClass = (active: boolean) => [
    'inline-flex min-h-11 items-center gap-2 border-b-2 px-4 text-xs focus-visible:outline-2 focus-visible:outline-accent',
    active ? 'border-oc-peach text-dark-text' : 'border-transparent text-dark-text-secondary hover:bg-dark-elevated hover:text-dark-text',
  ];
</script>

<div class="min-h-full bg-dark-base">
  <nav aria-label="Workflow views" class="flex border-b border-dark-border px-4 sm:px-6">
    {#if routeFeatureEnabled('/workflows')}
      <a href="#/workflows" aria-current={!runsSelected ? 'page' : undefined} class={tabClass(!runsSelected)}>
        <Workflow size={14} class={!runsSelected ? 'text-oc-peach' : ''} /> Workflows
      </a>
    {/if}
    {#if routeFeatureEnabled('/workflows/runs')}
      <a href="#/workflows/runs" aria-current={runsSelected ? 'page' : undefined} class={tabClass(runsSelected)}>
        <Activity size={14} class={runsSelected ? 'text-oc-peach' : ''} /> Runs
      </a>
    {/if}
  </nav>
  {#await page}
    <RouteLoading />
  {:then module}
    {@const Page = module.default}
    <Page />
  {/await}
</div>
