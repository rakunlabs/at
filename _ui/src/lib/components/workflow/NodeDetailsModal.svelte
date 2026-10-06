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
    running: 'border-blue-800 bg-blue-950 text-blue-300',
    completed: 'border-green-800 bg-green-950 text-green-300',
    error: 'border-red-900 bg-red-950 text-red-300',
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
    class="flex min-h-0 w-full flex-col border outline-none border-dark-border bg-dark-surface"
  >
    <header class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b px-4 py-2 border-dark-border bg-dark-base">
      <div class="flex min-w-0 items-center gap-2">
        <h2 class="truncate text-sm font-medium text-dark-text">{title}</h2>
        <span class="truncate font-mono text-[10px] text-dark-text-faint">{nodeId}</span>
        {#if status}
          <span class="border px-1.5 py-0.5 text-[10px] font-medium leading-none {statusClass[status] ?? 'border-dark-border bg-dark-elevated text-dark-text-secondary'}">{status}</span>
        {/if}
        {#if dirty}
          <span class="border px-1.5 py-0.5 text-[10px] font-medium leading-none border-amber-800 bg-amber-900/20 text-amber-400">Unapplied changes</span>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        {#if executing}
          <button onclick={onstop} class="inline-flex h-8 items-center gap-1.5 border px-2.5 text-xs border-red-900 text-red-400 hover:bg-red-950"><Square size={12} /> Stop</button>
        {:else}
          <button onclick={onexecute} disabled={running} title="Run this step and the upstream steps it needs" class="inline-flex h-8 items-center gap-1.5 border border-green-600 bg-green-600 px-2.5 text-xs text-white hover:bg-green-700 disabled:opacity-50"><Play size={14} /> Execute step</button>
        {/if}
        <button onclick={onclose} aria-label="Close step details" title={readonly ? 'Close (Esc)' : 'Apply and close (Esc)'} class="inline-flex h-8 w-8 items-center justify-center text-dark-text-secondary hover:bg-dark-elevated hover:text-dark-text"><X size={16} /></button>
      </div>
    </header>

    <nav aria-label="Step details sections" class="flex shrink-0 border-b lg:hidden border-dark-border">
      {#each panes as p (p.id)}
        <button onclick={() => pane = p.id} aria-pressed={pane === p.id} class="flex-1 border-b-2 px-2 py-2 text-xs font-medium {pane === p.id ? 'border-blue-400 text-blue-400' : 'border-transparent text-dark-text-secondary hover:bg-dark-elevated'}">{p.label}</button>
      {/each}
    </nav>

    <div class="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.25fr)_minmax(0,1fr)]">
      {#each panes as p (p.id)}
        <section
          aria-label={p.label}
          class="min-h-0 flex-col {pane === p.id ? 'flex' : 'hidden'} lg:flex {p.id === 'parameters' ? 'lg:border-x lg:border-dark-border' : 'bg-dark-base/40'}"
        >
          <div class="hidden shrink-0 border-b px-4 py-2 text-[10px] font-medium uppercase tracking-wider lg:block border-dark-border text-dark-text-muted">{p.label}</div>
          <div class="min-h-0 flex-1 overflow-y-auto p-4">
            {#if p.id === 'input'}{@render input()}{:else if p.id === 'parameters'}{@render parameters()}{:else}{@render output()}{/if}
          </div>
        </section>
      {/each}
    </div>

    <footer class="flex shrink-0 items-center justify-between gap-2 border-t px-4 py-2 border-dark-border">
      <p class="hidden text-[11px] sm:block text-dark-text-muted">
        {readonly ? 'Viewing a historical version — read-only.' : 'Changes are applied when you close this dialog. Save stores the workflow.'}
      </p>
      <div class="ml-auto flex items-center gap-2">
        {#if !readonly}
          <button onclick={ondiscard} disabled={!dirty} class="border px-3 py-1.5 text-xs disabled:opacity-50 border-dark-border-subtle text-dark-text hover:bg-dark-elevated">Discard changes</button>
          <button onclick={onapply} disabled={!dirty} class="px-3 py-1.5 text-xs disabled:opacity-50 bg-accent text-gray-950 hover:bg-accent-hover">Apply</button>
        {/if}
      </div>
    </footer>
  </div>
</div>
