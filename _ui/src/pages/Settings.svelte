<script lang="ts">
  import { configurationLinks, settingsLinkVisible } from '../lib/helper/navigation';
  import { storeInfo, storeNavbar } from '../lib/store/store.svelte';
  import { loadFeatures } from '../lib/store/features.svelte';
  import { ChevronRight } from 'lucide-svelte';
  import InstallApp from '../lib/components/InstallApp.svelte';
  import { formatLocalDateTime, formatUTCDateTime } from '../lib/helper/format';
  storeNavbar.title = 'Settings';
  $effect(() => { void loadFeatures().catch(() => {}); });
</script>
<svelte:head><title>AT | Settings</title></svelte:head>
<div class="settings-page"><header><h1 class="settings-title">Settings</h1><p class="settings-subtitle">Your account, workspace and installation configuration in one place.</p></header>
<nav aria-label="Configuration sections" class="divide-y divide-dark-border">{#each configurationLinks.filter(item => settingsLinkVisible(item.path)) as item}<a href={`#${item.path}`} class="flex items-center justify-between gap-4 py-4 hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"><div><h2 class="text-sm font-semibold">{item.label}</h2><p class="settings-note mt-1">{item.description}</p></div><ChevronRight size={16} class="shrink-0" /></a>{/each}</nav>
<InstallApp />
<section class="settings-section" aria-labelledby="application-info-title">
  <div>
    <h2 id="application-info-title" class="settings-section-title">{storeInfo.name || 'AT'}</h2>
    <p class="settings-note mt-1">Application build information</p>
  </div>
  <dl class="grid gap-4 sm:grid-cols-3">
    {#each [['Version', storeInfo.version], ['Commit', storeInfo.commit], ['Build date', formatLocalDateTime(storeInfo.build_date)]] as [label, value]}
      <div class="min-w-0">
        <dt class="settings-note">{label}</dt>
        <dd class="mt-1 break-all font-mono text-xs tabular-nums text-dark-text" title={label === 'Build date' ? formatUTCDateTime(storeInfo.build_date) : undefined}>{value || 'Unavailable'}</dd>
      </div>
    {/each}
  </dl>
</section>
</div>
