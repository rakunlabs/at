<script lang="ts">
  import { ChevronDown, ChevronUp, Plus, RotateCw, TerminalSquare, X } from 'lucide-svelte';
  import HostTerminal from '@/lib/components/HostTerminal.svelte';
  import { developerTerminalURL } from '@/lib/api/developer-spaces';
  import type { DeveloperTerminalTab } from '@/lib/helper/developer-space';

  interface Props {
    tabs: DeveloperTerminalTab[];
    active: string;
    onselect: (id: string) => void;
    onclose: (id: string) => void;
    onnew: () => void;
    onreconnect: (id: string) => void;
    minimized?: boolean;
    ontoggleminimize?: () => void;
  }
  let { tabs, active, onselect, onclose, onnew, onreconnect, minimized = false, ontoggleminimize }: Props = $props();
  let statuses = $state<Record<string, { status: string; message: string }>>({});
</script>

<div class="flex h-full min-h-0 flex-col bg-[#1e1e1e]">
  <div class="flex h-8 shrink-0 items-center gap-0.5 overflow-x-auto border-b border-gray-200 dark:border-dark-border bg-gray-100 dark:bg-dark-surface px-1 text-xs">
    {#each tabs as tab, index (tab.id)}
      {@const state = statuses[tab.id]?.status}
      <div class={['group flex h-7 shrink-0 items-center gap-1 pl-2 pr-1', tab.id === active ? 'bg-white text-gray-900 dark:bg-dark-base dark:text-dark-text' : 'text-gray-600 hover:bg-gray-200 dark:text-dark-text-muted dark:hover:bg-dark-elevated']}>
        <button type="button" class="inline-flex items-center gap-1.5" onclick={() => { onselect(tab.id); if (minimized) ontoggleminimize?.(); }} title={`/workspace${tab.cwd ? `/${tab.cwd}` : ''}`}>
          <TerminalSquare size={12} class={state === 'connected' ? 'text-green-600' : state === 'error' || state === 'disconnected' ? 'text-red-500' : ''} />
          <span class="max-w-40 truncate">{tab.cwd || 'workspace'} {tabs.length > 1 ? index + 1 : ''}</span>
        </button>
        <button type="button" class="p-0.5 opacity-60 hover:opacity-100" onclick={() => onclose(tab.id)} aria-label="Close terminal"><X size={12} /></button>
      </div>
    {/each}
    <button type="button" onclick={onnew} class="ml-1 p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated dark:hover:text-dark-text" title="New terminal in the current project" aria-label="New terminal"><Plus size={13} /></button>
    <span class="flex-1"></span>
    {#if statuses[active] && statuses[active].status !== 'connected' && statuses[active].status !== 'connecting'}
      <span class="truncate px-2 text-gray-500 dark:text-dark-text-muted">{statuses[active].message}</span>
      <button type="button" onclick={() => onreconnect(active)} class="inline-flex shrink-0 items-center gap-1 px-2 py-0.5 text-gray-700 hover:bg-gray-200 dark:text-dark-text-secondary dark:hover:bg-dark-elevated"><RotateCw size={12} /> Reconnect</button>
    {/if}
    {#if ontoggleminimize}
      <button type="button" onclick={ontoggleminimize} class="shrink-0 p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-elevated dark:hover:text-dark-text" title={minimized ? 'Restore terminal' : 'Minimize terminal'} aria-label={minimized ? 'Restore terminal' : 'Minimize terminal'} aria-expanded={!minimized}>
        {#if minimized}<ChevronUp size={14} />{:else}<ChevronDown size={14} />{/if}
      </button>
    {/if}
  </div>
  <div class="relative min-h-0 flex-1">
    {#each tabs as tab (tab.id)}
      <div class={['absolute inset-0', tab.id === active ? '' : 'invisible']}>
        {#key tab.generation}
          <HostTerminal id={tab.id} socketUrl={developerTerminalURL(tab.cwd)} persistentShell={false} onstatus={(status, message) => { statuses = { ...statuses, [tab.id]: { status, message } }; }} />
        {/key}
      </div>
    {:else}
      <div class="flex h-full items-center justify-center">
        <button type="button" onclick={onnew} class="inline-flex items-center gap-2 border border-gray-600 px-3 py-1.5 text-xs text-gray-300 hover:bg-white/5"><Plus size={13} /> Open a terminal</button>
      </div>
    {/each}
  </div>
</div>
