<script lang="ts">
  import { onMount } from 'svelte';
  import { Search, Plus, X, ChevronRight, Play, BrainCircuit, Image, GitBranch, Plug, ArrowRightFromLine, StickyNote, Database } from 'lucide-svelte';
  import { workflowPaletteGroups } from '@/lib/workflow/node-definitions';
  import { getWorkflowNodeAppearance, workflowKindTile } from '@/lib/workflow/node-appearance';

  let { onadd, ondragstart, onclose, disabled = false }: {
    onadd: (type: string) => void;
    ondragstart: (event: DragEvent, type: string) => void;
    onclose: () => void;
    disabled?: boolean;
  } = $props();
  let query = $state('');
  let searchInput: HTMLInputElement;
  onMount(() => searchInput?.focus());
  let collapsed = $state<Record<string, boolean>>({});
  const icons = { Entry: Play, Processing: BrainCircuit, Data: Database, Media: Image, 'Flow Control': GitBranch, Resources: Plug, Output: ArrowRightFromLine, Annotation: StickyNote };
  const groups = $derived(workflowPaletteGroups.map(group => ({
    ...group,
    nodes: group.nodes.filter(node => `${node.label} ${node.description} ${node.type} ${group.label}`.toLowerCase().includes(query.trim().toLowerCase())),
  })).filter(group => group.nodes.length));
</script>

<aside aria-label="Add workflow nodes" class="flex h-full w-72 max-w-full shrink-0 flex-col border-r border-dark-border bg-dark-surface">
  <div class="border-b p-3 border-dark-border">
    <div class="mb-2 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-dark-text">Add a step</h2>
      <button onclick={onclose} aria-label="Close node library" class="p-2 text-dark-text-secondary hover:bg-dark-elevated"><X size={16} /></button>
    </div>
    <label class="flex items-center gap-2 border px-2 border-dark-border-subtle focus-within:ring-2 focus-within:ring-blue-500">
      <Search size={16} class="shrink-0 text-dark-text-secondary" />
      <input bind:this={searchInput} type="search" bind:value={query} aria-label="Search nodes" placeholder="Search nodes…" class="min-w-0 w-full bg-transparent py-2 text-sm outline-none text-dark-text" />
    </label>
    <p class="mt-2 text-xs text-dark-text-secondary">Click to add, or drag onto the canvas.</p>
  </div>
  <div class="min-h-0 flex-1 overflow-y-auto p-2">
    {#each groups as group (group.label)}
      {@const Icon = icons[group.label]}
      <section class="mb-3">
        <button onclick={() => collapsed[group.label] = !collapsed[group.label]} aria-expanded={!!query.trim() || !collapsed[group.label]} class="flex w-full items-center gap-2 px-2 py-2 text-left text-xs font-semibold text-dark-text-secondary hover:bg-dark-elevated">
          <ChevronRight size={14} class={query.trim() || !collapsed[group.label] ? 'rotate-90' : ''} />
          <Icon size={14} />
          {group.label}<span class="ml-auto tabular-nums">{group.nodes.length}</span>
        </button>
        {#if query.trim() || !collapsed[group.label]}
          {#each group.nodes as node (node.type)}
            {@const appearance = getWorkflowNodeAppearance(node.type)}
            {@const NodeIcon = appearance.icon}
            <button {disabled} draggable={!disabled} ondragstart={event => ondragstart(event, node.type)} onclick={() => onadd(node.type)} class="group flex min-h-16 w-full items-center gap-3 border border-transparent px-2 py-3 text-left focus-visible:outline-2 focus-visible:outline-blue-500 disabled:opacity-50 hover:border-dark-border-subtle hover:bg-dark-elevated">
              <span class={['flex size-9 shrink-0 items-center justify-center', workflowKindTile[appearance.kind]]}><NodeIcon size={18} strokeWidth={2.25} /></span>
              <span class="min-w-0 flex-1"><span class="block text-sm font-medium text-dark-text">{node.label}</span><span class="mt-0.5 block text-xs leading-relaxed text-dark-text-secondary">{node.description}</span></span>
              <Plus size={16} class="shrink-0 text-dark-text-secondary" />
            </button>
          {/each}
        {/if}
      </section>
    {:else}
      <p class="p-3 text-sm text-dark-text-secondary">No nodes match “{query}”. Try “HTTP”, “agent” or “input”.</p>
      <button onclick={() => query = ''} class="px-3 py-2 text-sm text-blue-400">Clear search</button>
    {/each}
  </div>
</aside>
