<script lang="ts">
  import { Crown, Plus, Minus, Scan } from 'lucide-svelte';
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
  const NODE_W = 224;
  const NODE_H = 100;
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

  $effect(() => {
    if (!containerEl) return;
    // Panels and viewport changes resize the canvas without changing agents.
    const observer = new ResizeObserver(() => fitView());
    observer.observe(containerEl);
    return () => observer.disconnect();
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
      case 'active': return 'var(--color-oc-green)';
      case 'busy': return 'var(--color-oc-peach)';
      case 'offline': return 'var(--color-oc-red)';
      default: return 'var(--color-dark-text-muted)';
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
  class="w-full h-full overflow-hidden relative select-none bg-dark-base grain-background"
  style="cursor: {isPanning ? 'grabbing' : 'grab'}"
  role="application"
  aria-label="Organization agent hierarchy"
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
        {@const parts = conn.id.split('-')}
        {@const parentId = parts[0]}
        {@const childId = parts[1]}
        {@const isHighlighted = selectedAgentId === parentId || selectedAgentId === childId}
        <!--
          A delegation chain in flight always shows up as: parent agent
          has an active delegation AND child agent has an active
          delegation (the parent's goroutine is blocked in wg.Wait
          while the child runs). Highlight that edge in green so the
          user can trace the live path through the org chart.
        -->
        {@const isLive =
          (agents.find((a) => a.agent_id === parentId)?.active_count ?? 0) > 0 &&
          (agents.find((a) => a.agent_id === childId)?.active_count ?? 0) > 0}
        <path
          d={stepPath(conn)}
          fill="none"
          stroke={isLive
            ? 'var(--color-oc-green)'
            : isHighlighted
              ? 'var(--color-oc-peach)'
              : 'var(--color-dark-border)'}
          stroke-width={isLive ? 2.5 : isHighlighted ? 2 : 1}
        />
      {/each}
    </svg>

    <!-- Nodes -->
    {#each layout.nodes as node (node.agent.agent_id)}
      {@const isSelected = selectedAgentId === node.agent.agent_id}
      {@const isHead = node.agent.is_head}
      <div
        class="org-node absolute cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        role="button"
        tabindex="0"
        aria-label={`${node.agent.name}${isHead ? ', head agent' : ''}, ${node.agent.active_count ? 'working' : statusLabel(node.agent.status)}`}
        aria-pressed={isSelected}
        style="left: {node.x}px; top: {node.y}px; width: {NODE_W}px;"
        onclick={(e) => { e.stopPropagation(); handleNodeClick(node.agent.agent_id); }}
        onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); handleNodeClick(node.agent.agent_id); } }}
      >
        <div
          style="height: {NODE_H}px;"
          class={[
            'border overflow-hidden ',
            isSelected
              ? 'border-oc-peach bg-dark-elevated'
              : isHead
                ? 'border-dark-text-muted bg-dark-base'
                : 'border-dark-border bg-dark-base hover:border-dark-text-muted',
          ]}
        >
          <!-- Content -->
          <div class="px-3 py-3">
            <!-- Name row -->
            <div class="flex items-center gap-2 mb-1">
              <div class="relative shrink-0">
                <img src={agentAvatar(node.agent.avatar_seed, node.agent.name, 22)} alt="" class="w-[22px] h-[22px] bg-dark-elevated" />
                {#if isHead}
                  <Crown size={10} class="absolute -top-1 -right-1 text-oc-peach" />
                {/if}
              </div>
              <span class={['text-xs font-medium truncate', isSelected ? 'text-oc-peach' : 'text-dark-text']}>
                {node.agent.name}
              </span>
              {#if node.agent.active_count && node.agent.active_count > 0}
                <span
                  class="shrink-0 ml-auto flex items-center gap-1"
                  title="{node.agent.active_count} active delegation{node.agent.active_count === 1 ? '' : 's'}"
                >
                  <span class="w-1.5 h-1.5 bg-oc-green"></span>
                  {#if node.agent.active_count > 1}
                    <span class="text-xs font-medium text-oc-green">{node.agent.active_count}</span>
                  {/if}
                </span>
              {:else}
                <span
                  class="shrink-0 ml-auto w-1.5 h-1.5"
                  style="background-color: {statusColor(node.agent.status)}"
                  title={statusLabel(node.agent.status)}
                ></span>
              {/if}
            </div>

            <!-- Details -->
            <div class="space-y-0.5 ml-[30px]">
              {#if node.agent.title}
                <div class="text-xs text-dark-text-secondary truncate" title={node.agent.title}>
                  {node.agent.title}
                </div>
              {/if}
              {#if node.agent.role}
                <div class="text-xs text-dark-text-secondary truncate" title={node.agent.role}>{node.agent.role}</div>
              {/if}
              {#if node.agent.description && !node.agent.title && !node.agent.role}
                <div class="text-xs text-dark-text-secondary truncate" title={node.agent.description}>{node.agent.description}</div>
              {/if}
              {#if node.agent.model}
                <div class="text-[11px] text-dark-text-muted truncate" title={node.agent.model}>
                  {node.agent.model}
                </div>
              {/if}
              {#if !node.agent.title && !node.agent.role && !node.agent.description && !node.agent.model}
                <div class="text-[10px] text-dark-text-faint">--</div>
              {/if}
            </div>
          </div>
        </div>
      </div>
    {/each}
  </div>

  <!-- Controls -->
  <div class="org-controls absolute bottom-3 left-3 flex items-center gap-px border border-dark-border bg-dark-surface">
    <button
      onclick={(e) => { e.stopPropagation(); zoomTo(scale * 1.25); }}
      class="w-11 h-11 sm:w-9 sm:h-9 flex items-center justify-center text-dark-text-secondary hover:bg-dark-elevated border-r border-dark-border focus-visible:outline-2 focus-visible:outline-accent"
      title="Zoom in"
      aria-label="Zoom in"
    ><Plus size={14} /></button>
    <button
      onclick={(e) => { e.stopPropagation(); zoomTo(scale * 0.8); }}
      class="w-11 h-11 sm:w-9 sm:h-9 flex items-center justify-center text-dark-text-secondary hover:bg-dark-elevated border-r border-dark-border focus-visible:outline-2 focus-visible:outline-accent"
      title="Zoom out"
      aria-label="Zoom out"
    ><Minus size={14} /></button>
    <button
      onclick={(e) => { e.stopPropagation(); fitView(); }}
      class="h-11 sm:h-9 px-3 gap-1.5 flex items-center justify-center text-xs text-dark-text-secondary hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"
      title="Fit view"
    ><Scan size={14} /> Fit</button>
  </div>

  <div class="absolute bottom-3 right-3 px-2 py-1 bg-dark-base text-xs text-dark-text-secondary tabular-nums">
    {Math.round(scale * 100)}%
  </div>
</div>
