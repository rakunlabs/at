<script lang="ts">
  import { ChevronDown, ChevronRight, ExternalLink } from 'lucide-svelte';
  import ObservationIcon from './ObservationIcon.svelte';
  import type { LLMCall } from '@/lib/api/llm-calls';
  import {
    type TreeNode, type Timeline,
    flattenTree, barGeometry, timelineTicks, observationType, observationLabel, isErrorObservation,
    formatDurationMs, formatTokens, formatCost,
  } from '@/lib/helper/trace-view';

  interface Props {
    roots: TreeNode<LLMCall>[];
    timeline: Timeline;
    selectedID: string;
    collapsed: Set<string>;
    /** Show the timing bars (waterfall) beside the tree. */
    waterfall?: boolean;
    onselect: (id: string) => void;
    ontoggle: (id: string) => void;
    onopentrace?: (traceID: string) => void;
  }
  let { roots, timeline, selectedID, collapsed, waterfall = true, onselect, ontoggle, onopentrace }: Props = $props();

  const rows = $derived(flattenTree(roots, collapsed));
  const ticks = $derived(timelineTicks(timeline.duration, 5));
  let container = $state<HTMLDivElement | null>(null);

  const barColor: Record<string, string> = {
    generation: 'bg-blue-400/70',
    tool: 'bg-purple-400/70',
    agent: 'bg-emerald-400/60',
    span: 'bg-slate-500/70',
    embedding: 'bg-cyan-400/70',
  };

  function childTraceID(o: LLMCall): string {
    const v = o.metadata?.['child_trace_id'];
    return typeof v === 'string' ? v : '';
  }

  // Arrow/j/k navigation over the visible rows; left/right collapse/expand.
  function onkeydown(e: KeyboardEvent) {
    if (!rows.length) return;
    const index = Math.max(rows.findIndex((r) => r.obs.id === selectedID), 0);
    const node = rows[index];
    let next = -1;
    switch (e.key) {
      case 'ArrowDown': case 'j': next = Math.min(index + 1, rows.length - 1); break;
      case 'ArrowUp': case 'k': next = Math.max(index - 1, 0); break;
      case 'Home': next = 0; break;
      case 'End': next = rows.length - 1; break;
      case 'ArrowRight': case 'l':
        if (node.children.length && collapsed.has(node.obs.id)) ontoggle(node.obs.id);
        else if (node.children.length) next = index + 1;
        break;
      case 'ArrowLeft': case 'h':
        if (node.children.length && !collapsed.has(node.obs.id)) ontoggle(node.obs.id);
        else {
          const parent = rows.findIndex((r) => r.obs.id === node.obs.parent_observation_id);
          if (parent >= 0) next = parent;
        }
        break;
      default: return;
    }
    e.preventDefault();
    if (next >= 0) {
      onselect(rows[next].obs.id);
      container?.querySelector<HTMLElement>(`[data-row="${rows[next].obs.id}"]`)?.scrollIntoView({ block: 'nearest' });
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div bind:this={container} class="min-w-0 outline-none focus-visible:ring-1 focus-visible:ring-accent" tabindex="0" role="tree" aria-label="Trace observations" {onkeydown}>
  {#if waterfall}
    <div class="sticky top-0 z-10 flex border-b text-[10px] border-dark-border bg-dark-base text-dark-text-muted">
      <div class="w-[46%] shrink-0 px-2 py-1 font-medium uppercase tracking-wider">Observation</div>
      <div class="relative flex-1 py-1">
        {#each ticks as tick}
          {@const left = timeline.duration ? (tick / timeline.duration) * 100 : 0}
          <span class="absolute -translate-x-1/2 font-mono" style:left={`${Math.min(left, 96)}%`}>{formatDurationMs(tick)}</span>
        {/each}
        &nbsp;
      </div>
    </div>
  {/if}
  {#each rows as node (node.obs.id)}
    {@const o = node.obs}
    {@const type = observationType(o)}
    {@const error = isErrorObservation(o)}
    {@const selected = o.id === selectedID}
    {@const bar = barGeometry(timeline, node.start, node.end)}
    {@const child = childTraceID(o)}
    <div
      data-row={o.id}
      role="treeitem"
      aria-selected={selected}
      aria-expanded={node.children.length ? !collapsed.has(o.id) : undefined}
      tabindex="-1"
      class={['flex cursor-pointer items-stretch border-b text-xs border-dark-border/60',
        selected ? 'shadow-[inset_2px_0_0_0] bg-dark-highest shadow-dark-text' : 'hover:bg-dark-elevated/60',
        error && !selected ? 'bg-red-950/20' : '']}
      onclick={() => onselect(o.id)}
      onkeydown={() => {}}
    >
      <div class={['flex min-w-0 items-center gap-1 py-1 pr-2', waterfall ? 'w-[46%] shrink-0' : 'flex-1']} style:padding-left={`${6 + node.depth * 14}px`}>
        {#if node.children.length}
          <button class="shrink-0 text-dark-text-muted hover:text-dark-text" onclick={(e) => { e.stopPropagation(); ontoggle(o.id); }} aria-label={collapsed.has(o.id) ? 'Expand' : 'Collapse'}>
            {#if collapsed.has(o.id)}<ChevronRight size={12} />{:else}<ChevronDown size={12} />{/if}
          </button>
        {:else}
          <span class="w-3 shrink-0"></span>
        {/if}
        <ObservationIcon {type} {error} />
        <span class={['truncate', selected ? 'font-medium text-dark-text' : 'text-dark-text-secondary']} title={observationLabel(o)}>{observationLabel(o)}</span>
        {#if child && onopentrace}
          <button class="shrink-0 hover:underline text-blue-400" title="Open child trace" onclick={(e) => { e.stopPropagation(); onopentrace(child); }}><ExternalLink size={11} /></button>
        {/if}
        <span class="ml-auto flex shrink-0 items-center gap-2 pl-2 font-mono text-[10px] text-dark-text-muted">
          {#if node.tokens}<span title="Tokens (subtree)">{formatTokens(node.tokens)}t</span>{/if}
          {#if node.costCents}<span title="Cost (subtree)">{formatCost(node.costCents)}</span>{/if}
          {#if type !== 'event'}<span class="w-12 text-right" title="Duration">{formatDurationMs(node.end - node.start)}</span>{/if}
        </span>
      </div>
      {#if waterfall}
        <div class="relative flex-1 border-l border-dark-border/60">
          {#if type === 'event'}
            <span class="absolute top-1/2 h-2 w-2 -translate-x-1/2 -translate-y-1/2 rotate-45 bg-dark-text-muted" style:left={`${bar.left}%`} title={o.name}></span>
          {:else}
            <span
              class={['absolute top-1/2 h-2.5 -translate-y-1/2', error ? 'bg-red-500/80' : barColor[type] || barColor.span, selected ? 'ring-1 ring-white' : '']}
              style:left={`${bar.left}%`}
              style:width={`${bar.width}%`}
              title={`${observationLabel(o)} · ${formatDurationMs(node.end - node.start)}`}
            ></span>
          {/if}
        </div>
      {/if}
    </div>
  {/each}
</div>
