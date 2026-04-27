<script lang="ts">
  import { Crown, Cpu, Briefcase } from 'lucide-svelte';
  import { agentAvatar } from '@/lib/helper/avatar';

  // ─── Types ───
  interface OrgAgent {
    agent_id: string;
    name: string;
    description?: string;
    title?: string;
    role?: string;
    model?: string;
    status?: string;
    parent_agent_id?: string;
    is_head?: boolean;
    avatar_seed?: string;
    /** Number of in-flight delegation goroutines for this agent. */
    active_count?: number;
  }

  interface LayoutNode {
    agent: OrgAgent;
    x: number;
    y: number;
    children: LayoutNode[];
  }

  interface Props {
    agents: OrgAgent[];
    selectedAgentId?: string | null;
    onselect?: (agentId: string) => void;
  }

  let { agents, selectedAgentId = null, onselect }: Props = $props();

  // ─── Layout Constants ───
  const NODE_W = 200;
  const NODE_H = 82;
  const H_GAP = 36;
  const V_GAP = 60;
  const PADDING = 60;

  // ─── Pan & Zoom State ───
  let containerEl: HTMLDivElement | undefined = $state();
  let viewX = $state(0);
  let viewY = $state(0);
  let scale = $state(1);
  let isPanning = $state(false);
  let panStartX = 0;
  let panStartY = 0;
  let panStartViewX = 0;
  let panStartViewY = 0;
  let prevAgentCount = -1;

  // ─── Build tree from flat list ───
  function buildTree(agents: OrgAgent[]): LayoutNode[] {
    const agentIds = new Set(agents.map(a => a.agent_id));
    const childrenMap = new Map<string, OrgAgent[]>();
    const roots: OrgAgent[] = [];

    for (const a of agents) {
      if (!a.parent_agent_id || !agentIds.has(a.parent_agent_id)) {
        roots.push(a);
      } else {
        const siblings = childrenMap.get(a.parent_agent_id) || [];
        siblings.push(a);
        childrenMap.set(a.parent_agent_id, siblings);
      }
    }

    function subtreeWidth(agentId: string): number {
      const children = childrenMap.get(agentId) || [];
      if (children.length === 0) return NODE_W;
      const childWidths = children.map(c => subtreeWidth(c.agent_id));
      return childWidths.reduce((s, w) => s + w, 0) + (children.length - 1) * H_GAP;
    }

    function layout(agent: OrgAgent, depth: number, xCenter: number): LayoutNode {
      const children = childrenMap.get(agent.agent_id) || [];
      const y = depth * (NODE_H + V_GAP);
      const x = xCenter - NODE_W / 2;

      const childNodes: LayoutNode[] = [];
      if (children.length > 0) {
        const totalW = subtreeWidth(agent.agent_id);
        let startX = xCenter - totalW / 2;
        for (const child of children) {
          const cw = subtreeWidth(child.agent_id);
          const cc = startX + cw / 2;
          childNodes.push(layout(child, depth + 1, cc));
          startX += cw + H_GAP;
        }
      }

      return { agent, x, y, children: childNodes };
    }

    const rootWidths = roots.map(r => subtreeWidth(r.agent_id));
    const totalWidth = rootWidths.reduce((s, w) => s + w, 0) + (roots.length - 1) * H_GAP;
    let startX = -totalWidth / 2;
    const trees: LayoutNode[] = [];

    for (let i = 0; i < roots.length; i++) {
      const center = startX + rootWidths[i] / 2;
      trees.push(layout(roots[i], 0, center));
      startX += rootWidths[i] + H_GAP;
    }

    return trees;
  }

  // ─── Flatten tree for rendering ───
  interface FlatNode {
    agent: OrgAgent;
    x: number;
    y: number;
  }

  interface Connection {
    id: string;
    x1: number;
    y1: number;
    x2: number;
    y2: number;
  }

  function flatten(trees: LayoutNode[]): { nodes: FlatNode[]; connections: Connection[] } {
    const nodes: FlatNode[] = [];
    const connections: Connection[] = [];

    function walk(node: LayoutNode) {
      nodes.push({ agent: node.agent, x: node.x, y: node.y });
      for (const child of node.children) {
        connections.push({
          id: `${node.agent.agent_id}-${child.agent.agent_id}`,
          x1: node.x + NODE_W / 2,
          y1: node.y + NODE_H,
          x2: child.x + NODE_W / 2,
          y2: child.y,
        });
        walk(child);
      }
    }

    for (const tree of trees) walk(tree);
    return { nodes, connections };
  }

  // ─── Derived layout ───
  let trees = $derived(buildTree(agents));
  let layout = $derived(flatten(trees));

  let bounds = $derived.by(() => {
    if (layout.nodes.length === 0) return { minX: 0, minY: 0, maxX: 0, maxY: 0, width: 0, height: 0 };
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of layout.nodes) {
      minX = Math.min(minX, n.x);
      minY = Math.min(minY, n.y);
      maxX = Math.max(maxX, n.x + NODE_W);
      maxY = Math.max(maxY, n.y + NODE_H);
    }
    return { minX, minY, maxX, maxY, width: maxX - minX, height: maxY - minY };
  });

  $effect(() => {
    const count = agents.length;
    if (count !== prevAgentCount) {
      prevAgentCount = count;
      fitView();
    }
  });

  function fitView() {
    if (!containerEl || layout.nodes.length === 0) return;
    const rect = containerEl.getBoundingClientRect();
    const contentW = bounds.width + PADDING * 2;
    const contentH = bounds.height + PADDING * 2;
    const scaleX = rect.width / contentW;
    const scaleY = rect.height / contentH;
    scale = Math.min(scaleX, scaleY, 1.2);
    viewX = rect.width / 2 - (bounds.minX + bounds.width / 2) * scale;
    viewY = rect.height / 2 - (bounds.minY + bounds.height / 2) * scale;
  }

  // ─── Step-type edge path (right-angle lines) ───
  function stepPath(conn: Connection): string {
    const midY = conn.y1 + (conn.y2 - conn.y1) / 2;
    return `M ${conn.x1} ${conn.y1} V ${midY} H ${conn.x2} V ${conn.y2}`;
  }

  // ─── Status ───
  function statusColor(status?: string): string {
    switch (status) {
      case 'active': return '#22c55e';
      case 'busy': return '#f59e0b';
      case 'offline': return '#ef4444';
      default: return '#6b7280';
    }
  }

  function statusLabel(status?: string): string {
    switch (status) {
      case 'active': return 'Active';
      case 'busy': return 'Busy';
      case 'offline': return 'Offline';
      default: return 'Idle';
    }
  }

  // ─── Pan & Zoom ───
  function handleWheel(e: WheelEvent) {
    e.preventDefault();
    if (!containerEl) return;
    const rect = containerEl.getBoundingClientRect();
    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;
    const oldScale = scale;
    const delta = e.deltaY > 0 ? 0.9 : 1.1;
    const newScale = Math.max(0.1, Math.min(3, oldScale * delta));
    viewX = mouseX - (mouseX - viewX) * (newScale / oldScale);
    viewY = mouseY - (mouseY - viewY) * (newScale / oldScale);
    scale = newScale;
  }

  function zoomTo(newScale: number) {
    if (!containerEl) return;
    const rect = containerEl.getBoundingClientRect();
    const cx = rect.width / 2;
    const cy = rect.height / 2;
    const oldScale = scale;
    const clamped = Math.max(0.1, Math.min(3, newScale));
    viewX = cx - (cx - viewX) * (clamped / oldScale);
    viewY = cy - (cy - viewY) * (clamped / oldScale);
    scale = clamped;
  }

  function handlePointerDown(e: PointerEvent) {
    const target = e.target as HTMLElement;
    if (target.closest('.org-node') || target.closest('.org-controls')) return;
    isPanning = true;
    panStartX = e.clientX;
    panStartY = e.clientY;
    panStartViewX = viewX;
    panStartViewY = viewY;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function handlePointerMove(e: PointerEvent) {
    if (!isPanning) return;
    viewX = panStartViewX + (e.clientX - panStartX);
    viewY = panStartViewY + (e.clientY - panStartY);
  }

  function handlePointerUp() {
    isPanning = false;
  }

  function handleNodeClick(agentId: string) {
    onselect?.(agentId);
  }

  function handleBackgroundClick(e: MouseEvent) {
    const target = e.target as HTMLElement;
    if (!target.closest('.org-node') && !target.closest('.org-controls')) {
      onselect?.('');
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  bind:this={containerEl}
  class="w-full h-full overflow-hidden relative select-none org-chart-container"
  style="cursor: {isPanning ? 'grabbing' : 'grab'}"
  role="application"
  onwheel={handleWheel}
  onpointerdown={handlePointerDown}
  onpointermove={handlePointerMove}
  onpointerup={handlePointerUp}
  onclick={handleBackgroundClick}
>
  <!-- Transformed layer -->
  <div
    class="absolute origin-top-left"
    style="transform: translate({viewX}px, {viewY}px) scale({scale})"
  >
    <!-- SVG step connections -->
    <svg
      class="absolute pointer-events-none overflow-visible"
      style="top: 0; left: 0; width: 1px; height: 1px;"
    >
      {#each layout.connections as conn (conn.id)}
        {@const isHighlighted =
          selectedAgentId === conn.id.split('-')[0] ||
          selectedAgentId === conn.id.split('-')[1]}
        <path
          d={stepPath(conn)}
          fill="none"
          stroke={isHighlighted ? 'var(--color-accent, #00d926)' : 'var(--conn-color)'}
          stroke-width={isHighlighted ? 2 : 1}
        />
      {/each}
    </svg>

    <!-- Nodes -->
    {#each layout.nodes as node (node.agent.agent_id)}
      {@const isSelected = selectedAgentId === node.agent.agent_id}
      {@const isHead = node.agent.is_head}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="org-node absolute"
        style="left: {node.x}px; top: {node.y}px; width: {NODE_W}px;"
        onclick={(e) => { e.stopPropagation(); handleNodeClick(node.agent.agent_id); }}
      >
        <div
          style="height: {NODE_H}px;"
          class={[
            'border overflow-hidden transition-colors duration-100',
            isSelected
              ? 'border-accent bg-white dark:bg-dark-surface'
              : isHead
                ? 'border-gray-400 dark:border-dark-text-muted bg-white dark:bg-dark-surface'
                : 'border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface hover:border-gray-400 dark:hover:border-dark-border',
          ]}
        >
          <!-- Top bar -->
          <div
            class={[
              'h-0.5',
              isHead
                ? 'bg-accent'
                : isSelected
                  ? 'bg-accent'
                  : 'bg-gray-300 dark:bg-dark-border',
            ]}
          ></div>

          <!-- Content -->
          <div class="px-3 py-2">
            <!-- Name row -->
            <div class="flex items-center gap-2 mb-1">
              <div class="relative shrink-0">
                <img src={agentAvatar(node.agent.avatar_seed, node.agent.name, 22)} alt="" class="w-[22px] h-[22px] rounded-full bg-gray-100 dark:bg-dark-elevated" />
                {#if isHead}
                  <Crown size={8} class="absolute -top-0.5 -right-0.5 text-amber-500 drop-shadow" />
                {/if}
              </div>
              <span class="text-xs font-medium text-gray-900 dark:text-dark-text truncate">
                {node.agent.name}
              </span>
              {#if node.agent.active_count && node.agent.active_count > 0}
                <span
                  class="shrink-0 ml-auto flex items-center gap-1"
                  title="{node.agent.active_count} active delegation{node.agent.active_count === 1 ? '' : 's'}"
                >
                  <span class="relative flex w-1.5 h-1.5">
                    <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                    <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
                  </span>
                  {#if node.agent.active_count > 1}
                    <span class="text-[9px] font-medium text-green-600 dark:text-green-400">{node.agent.active_count}</span>
                  {/if}
                </span>
              {:else}
                <span
                  class="shrink-0 ml-auto w-1.5 h-1.5 rounded-full"
                  style="background-color: {statusColor(node.agent.status)}"
                  title={statusLabel(node.agent.status)}
                ></span>
              {/if}
            </div>

            <!-- Details -->
            <div class="space-y-0.5 ml-[30px]">
              {#if node.agent.title}
                <div class="text-[10px] text-gray-600 dark:text-dark-text-secondary truncate">
                  {node.agent.title}
                </div>
              {/if}
              {#if node.agent.role}
                <div class="text-[10px] text-gray-500 dark:text-dark-text-muted truncate">{node.agent.role}</div>
              {/if}
              {#if node.agent.description && !node.agent.title && !node.agent.role}
                <div class="text-[10px] text-gray-500 dark:text-dark-text-muted truncate">{node.agent.description}</div>
              {/if}
              {#if node.agent.model}
                <div class="text-[10px] text-gray-400 dark:text-dark-text-faint font-mono truncate">
                  {node.agent.model}
                </div>
              {/if}
              {#if !node.agent.title && !node.agent.role && !node.agent.description && !node.agent.model}
                <div class="text-[10px] text-gray-400 dark:text-dark-text-faint">--</div>
              {/if}
            </div>
          </div>
        </div>
      </div>
    {/each}
  </div>

  <!-- Controls -->
  <div class="org-controls absolute bottom-3 left-3 flex items-center gap-px border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
    <button
      onclick={(e) => { e.stopPropagation(); zoomTo(scale * 1.25); }}
      class="w-7 h-7 flex items-center justify-center text-xs text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors border-r border-gray-200 dark:border-dark-border"
      title="Zoom in"
    >+</button>
    <button
      onclick={(e) => { e.stopPropagation(); zoomTo(scale * 0.8); }}
      class="w-7 h-7 flex items-center justify-center text-xs text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors border-r border-gray-200 dark:border-dark-border"
      title="Zoom out"
    >-</button>
    <button
      onclick={(e) => { e.stopPropagation(); fitView(); }}
      class="h-7 px-2 flex items-center justify-center text-[10px] text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
      title="Fit view"
    >fit</button>
  </div>

  <div class="absolute bottom-3 right-3 text-[10px] text-gray-400 dark:text-dark-text-faint font-mono">
    {Math.round(scale * 100)}%
  </div>
</div>

<style>
  @reference "tailwindcss";

  .org-chart-container {
    --conn-color: #d1d5db;
    background-color: #f9fafb;
  }

  :global(.dark) .org-chart-container {
    --conn-color: #3a3836;
    background-color: #161618;
  }
</style>
