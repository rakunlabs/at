<script lang="ts">
  import { storeNavbar, storeTheme, storeInfo } from '@/lib/store/store.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { ChevronLeft, Menu, Moon, Sun } from 'lucide-svelte';
  import AccountMenu from './AccountMenu.svelte';
  import { workspaceState } from '../store/workspace.svelte';
  import { workspaceTransport, switchWorkspace } from '../api/transport';
  import { isFeatureEnabled } from '../store/features.svelte';
  import { FEATURE_WORKSPACE_MANAGEMENT } from '../api/features';
  let workspaceError = $state('');
  // Single-workspace mode pins every request to Default, so a switcher would
  // only offer the workspace already in use.
  let maySwitch = $derived(isFeatureEnabled(FEATURE_WORKSPACE_MANAGEMENT));

  interface Props { onlogout?: () => Promise<void>; loggingOut?: boolean }
  let { onlogout, loggingOut = false }: Props = $props();

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

<div class="app-navbar min-h-9 min-w-0 max-w-full bg-white dark:bg-dark-surface border-b border-gray-200 dark:border-dark-border flex items-stretch px-2">
  <button
    aria-label="Toggle navigation"
    aria-expanded={storeNavbar.sideBarOpen}
    class="inline-flex shrink-0 items-center justify-center py-2 px-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:text-gray-900 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
    onclick={() => (storeNavbar.sideBarOpen = !storeNavbar.sideBarOpen)}
  >
    {#if storeNavbar.sideBarOpen}
      <ChevronLeft size={16} />
    {:else}
      <Menu size={16} />
    {/if}
  </button>
  <span class="ml-2 min-w-0 flex-1 self-center truncate text-sm font-medium text-gray-800 dark:text-dark-text">
    {storeNavbar.title}
  </span>

  <div class="ml-auto flex shrink-0 items-stretch gap-1 sm:gap-4">
    {#if maySwitch && workspaceState.items.length}<label class="flex items-stretch text-xs"><span class="sr-only">Workspace</span><select aria-label="Workspace" value={workspaceTransport.selected} class="max-w-32 sm:max-w-56 border-l border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-2 py-1" onchange={async e => { try { await switchWorkspace(e.currentTarget.value); } catch { workspaceError = 'Could not switch workspaces. Check your access and browser storage.'; } }}>
      {#each workspaceState.items.filter(w => !w.archived) as workspace}<option value={workspace.id}>{workspace.name}</option>{/each}
    </select></label>{/if}
    {#if workspaceError}<span role="alert" class="settings-error self-center">{workspaceError}</span>{/if}
    <button
      onclick={() => (storeTheme.mode = storeTheme.mode === 'light' ? 'dark' : 'light')}
      class="inline-flex shrink-0 items-center justify-center px-1.5 text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
      aria-label={storeTheme.mode === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
      title="Toggle theme"
    >
      {#if storeTheme.mode === 'dark'}
        <Sun size={16} />
      {:else}
        <Moon size={16} />
      {/if}
    </button>
    <AccountMenu {onlogout} {loggingOut} />
  </div>
</div>
