<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Play, Square, X } from 'lucide-svelte';

  // n8n-style node details view: input on the left, configuration in the
  // middle, output on the right, so a step can be configured and tested
  // against real data without leaving the dialog. Below `lg` the three
  // columns collapse into tabs.
  let {
    title, nodeId, status = '', dirty = false, readonly = false, running = false, executing = false,
    onexecute, onstop, onclose, ondiscard, onapply,
    input, parameters, output,
  }: {
    title: string;
    nodeId: string;
    status?: string;
    dirty?: boolean;
    readonly?: boolean;
    /** Any workflow run is in progress. */
    running?: boolean;
    /** The in-progress run was started from this dialog for this step. */
    executing?: boolean;
    onexecute: () => void;
    onstop: () => void;
    onclose: () => void;
    ondiscard: () => void;
    onapply: () => void;
    input: Snippet;
    parameters: Snippet;
    output: Snippet;
  } = $props();

  let pane = $state<'input' | 'parameters' | 'output'>('parameters');
  let panel: HTMLDivElement | undefined = $state();
  let backdropPress = false;

  $effect(() => { panel?.focus(); });

  const statusClass: Record<string, string> = {
    running: 'border-blue-300 bg-blue-50 text-blue-800 dark:border-blue-800 dark:bg-blue-950 dark:text-blue-300',
    completed: 'border-green-300 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-300',
    error: 'border-red-300 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300',
  };

  const panes = [
    { id: 'input', label: 'Input' },
    { id: 'parameters', label: 'Parameters' },
    { id: 'output', label: 'Output' },
  ] as const;
</script>

<!-- Escape is read on window: focus falls back to <body> whenever the focused
     control disappears (Execute step turns into Stop while the step runs). -->
<svelte:window onkeydown={event => { if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); onclose(); } }} />

<!-- svelte-ignore a11y_no_static_element_interactions, a11y_click_events_have_key_events -->
<div
  class="fixed inset-0 z-[100] flex bg-black/50 p-0 sm:p-4 lg:p-8"
  onmousedown={event => { backdropPress = event.target === event.currentTarget; }}
  onclick={event => { if (backdropPress && event.target === event.currentTarget) onclose(); backdropPress = false; }}
>
  <div
    bind:this={panel}
    role="dialog"
    aria-modal="true"
    aria-label={`${title} step details`}
    tabindex="-1"
    class="flex min-h-0 w-full flex-col border border-gray-200 bg-white outline-none dark:border-dark-border dark:bg-dark-surface"
  >
    <header class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-gray-200 bg-gray-50 px-4 py-2 dark:border-dark-border dark:bg-dark-base">
      <div class="flex min-w-0 items-center gap-2">
        <h2 class="truncate text-sm font-medium text-gray-900 dark:text-dark-text">{title}</h2>
        <span class="truncate font-mono text-[10px] text-gray-500 dark:text-dark-text-faint">{nodeId}</span>
        {#if status}
          <span class="border px-1.5 py-0.5 text-[10px] font-medium leading-none {statusClass[status] ?? 'border-gray-200 bg-gray-100 text-gray-600 dark:border-dark-border dark:bg-dark-elevated dark:text-dark-text-secondary'}">{status}</span>
        {/if}
        {#if dirty}
          <span class="border border-amber-200 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium leading-none text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400">Unapplied changes</span>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        {#if executing}
          <button onclick={onstop} class="inline-flex h-8 items-center gap-1.5 border border-red-300 px-2.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950"><Square size={12} /> Stop</button>
        {:else}
          <button onclick={onexecute} disabled={running} title="Run this step and the upstream steps it needs" class="inline-flex h-8 items-center gap-1.5 border border-green-600 bg-green-600 px-2.5 text-xs text-white hover:bg-green-700 disabled:opacity-50"><Play size={14} /> Execute step</button>
        {/if}
        <button onclick={onclose} aria-label="Close step details" title={readonly ? 'Close (Esc)' : 'Apply and close (Esc)'} class="inline-flex h-8 w-8 items-center justify-center text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:hover:text-dark-text"><X size={16} /></button>
      </div>
    </header>

    <nav aria-label="Step details sections" class="flex shrink-0 border-b border-gray-200 lg:hidden dark:border-dark-border">
      {#each panes as p (p.id)}
        <button onclick={() => pane = p.id} aria-pressed={pane === p.id} class="flex-1 border-b-2 px-2 py-2 text-xs font-medium {pane === p.id ? 'border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-400' : 'border-transparent text-gray-600 hover:bg-gray-50 dark:text-dark-text-secondary dark:hover:bg-dark-elevated'}">{p.label}</button>
      {/each}
    </nav>

    <div class="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.25fr)_minmax(0,1fr)]">
      {#each panes as p (p.id)}
        <section
          aria-label={p.label}
          class="min-h-0 flex-col {pane === p.id ? 'flex' : 'hidden'} lg:flex {p.id === 'parameters' ? 'lg:border-x lg:border-gray-200 lg:dark:border-dark-border' : 'bg-gray-50/60 dark:bg-dark-base/40'}"
        >
          <div class="hidden shrink-0 border-b border-gray-200 px-4 py-2 text-[10px] font-medium uppercase tracking-wider text-gray-500 lg:block dark:border-dark-border dark:text-dark-text-muted">{p.label}</div>
          <div class="min-h-0 flex-1 overflow-y-auto p-4">
            {#if p.id === 'input'}{@render input()}{:else if p.id === 'parameters'}{@render parameters()}{:else}{@render output()}{/if}
          </div>
        </section>
      {/each}
    </div>

    <footer class="flex shrink-0 items-center justify-between gap-2 border-t border-gray-200 px-4 py-2 dark:border-dark-border">
      <p class="hidden text-[11px] text-gray-500 sm:block dark:text-dark-text-muted">
        {readonly ? 'Viewing a historical version — read-only.' : 'Changes are applied when you close this dialog. Save stores the workflow.'}
      </p>
      <div class="ml-auto flex items-center gap-2">
        {#if !readonly}
          <button onclick={ondiscard} disabled={!dirty} class="border border-gray-300 px-3 py-1.5 text-xs text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text dark:hover:bg-dark-elevated">Discard changes</button>
          <button onclick={onapply} disabled={!dirty} class="bg-gray-900 px-3 py-1.5 text-xs text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover">Apply</button>
        {/if}
      </div>
    </footer>
  </div>
</div>
