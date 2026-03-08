<script lang="ts">
  import { tick } from 'svelte';
  import { push } from 'svelte-spa-router';
  import { storeNavbar, storeTheme } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getOrganization,
    updateOrganization,
    listOrgAgents,
    addAgentToOrg,
    removeAgentFromOrg,
    updateOrgAgent,
    submitOrgTask,
    type Organization,
    type OrganizationAgent,
    type CanvasLayout,
    type IntakeTaskResponse,
  } from '@/lib/api/organizations';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { listGoals, type Goal } from '@/lib/api/goals';
  import { TASK_PRIORITIES, TASK_PRIORITY_LABELS } from '@/lib/api/tasks';
  import { Canvas, Controls, Minimap, GroupNode, StickyNoteNode, type FlowNode, type FlowEdge, type FlowState, type NodeTypes } from 'kaykay';
  import { ArrowLeft, Save, Plus, X, RefreshCw, UserPlus, Trash2, ChevronRight, StickyNote, Group, Crown, Send } from 'lucide-svelte';
  import AgentNode from '@/lib/components/AgentNode.svelte';
  import MarkdownStickyNote from '@/lib/components/workflow/MarkdownStickyNote.svelte';

  // ─── Props ───
  let { params = { id: '' } }: { params?: { id: string } } = $props();

  storeNavbar.title = 'Organization';

  // ─── Node Types ───
  const nodeTypes: NodeTypes = {
    agent: AgentNode,
    group: GroupNode,
    sticky_note: MarkdownStickyNote,
  };

  // ─── State ───
  let organization = $state<Organization | null>(null);
  let memberships = $state<OrganizationAgent[]>([]);
  let allAgents = $state<Agent[]>([]);
  let loading = $state(true);
  let saving = $state(false);
  let dirty = $state(false);

  // Editing org info
  let editingOrg = $state(false);
  let editName = $state('');
  let editDescription = $state('');

  // Add-agent panel
  let showAddPanel = $state(false);

  // Submit-task panel
  let showTaskPanel = $state(false);
  let taskTitle = $state('');
  let taskDescription = $state('');
  let taskPriority = $state('');
  let taskGoalId = $state('');
  let submittingTask = $state(false);
  let lastTaskResult = $state<IntakeTaskResponse | null>(null);
  let orgGoals = $state<Goal[]>([]);

  // Selected node
  let selectedAgentId = $state<string | null>(null);

  // Canvas ref
  let canvasRef: { getFlow: () => FlowState } | undefined = $state();

  // Node counter for generating IDs
  let nodeCounter = 0;

  // Palette
  let showPalette = $state(false);

  // Pending parent updates (from edge connections) — saved in batch on Save
  let pendingParentUpdates = $state<Map<string, string | null>>(new Map());

  // ─── Layout Constants ───
  const NODE_WIDTH = 200;
  const NODE_HEIGHT = 100;
  const HORIZONTAL_GAP = 60;
  const VERTICAL_GAP = 120;

  // ─── Helpers ───

  /** Map agent ID → Agent for quick lookups */
  function agentMap(): Map<string, Agent> {
    return new Map(allAgents.map((a) => [a.id, a]));
  }

  /** Map agent ID → OrganizationAgent membership for this org */
  function membershipMap(): Map<string, OrganizationAgent> {
    return new Map(memberships.map((m) => [m.agent_id, m]));
  }

  /** Build tree-layout nodes and edges from memberships + agents, respecting saved positions */
  function buildFlowGraph(): { nodes: FlowNode[]; edges: FlowEdge[] } {
    const nodes: FlowNode[] = [];
    const edges: FlowEdge[] = [];
    const agents = agentMap();
    const mMap = membershipMap();
    const savedPositions = organization?.canvas_layout?.agent_positions || {};

    // Collect member agent IDs
    const memberAgentIds = new Set(memberships.map((m) => m.agent_id));

    // Build parent → children mapping from membership data
    const childrenMap = new Map<string, OrganizationAgent[]>();
    const roots: OrganizationAgent[] = [];

    for (const m of memberships) {
      if (!m.parent_agent_id || !memberAgentIds.has(m.parent_agent_id)) {
        roots.push(m);
      } else {
        const siblings = childrenMap.get(m.parent_agent_id) || [];
        siblings.push(m);
        childrenMap.set(m.parent_agent_id, siblings);
      }
    }

    // Compute subtree widths for centered layout
    function subtreeWidth(agentId: string): number {
      const children = childrenMap.get(agentId) || [];
      if (children.length === 0) return NODE_WIDTH;
      const childWidths = children.map((c) => subtreeWidth(c.agent_id));
      const totalGaps = (children.length - 1) * HORIZONTAL_GAP;
      return childWidths.reduce((sum, w) => sum + w, 0) + totalGaps;
    }

    function layoutTree(m: OrganizationAgent, depth: number, xCenter: number, parentAgentId: string | null) {
      const agent = agents.get(m.agent_id);
      const savedPos = savedPositions[m.agent_id];
      const y = savedPos ? savedPos.y : depth * (NODE_HEIGHT + VERTICAL_GAP) + 40;
      const x = savedPos ? savedPos.x : xCenter - NODE_WIDTH / 2;

      nodes.push({
        id: m.agent_id,
        type: 'agent',
        position: { x, y },
        data: {
          label: agent?.name || m.agent_id,
          agent_id: m.agent_id,
          name: agent?.name || m.agent_id,
          role: m.role || '',
          title: m.title || '',
          model: agent?.config.model || '',
          status: m.status || 'active',
          is_root: !parentAgentId,
        },
      });

      if (parentAgentId) {
        edges.push({
          id: `edge-${parentAgentId}-${m.agent_id}`,
          source: parentAgentId,
          target: m.agent_id,
          source_handle: 'children',
          target_handle: 'parent',
        });
      }

      const children = childrenMap.get(m.agent_id) || [];
      if (children.length > 0) {
        const totalWidth = subtreeWidth(m.agent_id);
        let startX = xCenter - totalWidth / 2;

        for (const child of children) {
          const childWidth = subtreeWidth(child.agent_id);
          const childCenter = startX + childWidth / 2;
          layoutTree(child, depth + 1, childCenter, m.agent_id);
          startX += childWidth + HORIZONTAL_GAP;
        }
      }
    }

    // Layout all root trees side by side
    if (roots.length > 0) {
      const rootWidths = roots.map((r) => subtreeWidth(r.agent_id));
      const totalWidth = rootWidths.reduce((sum, w) => sum + w, 0) + (roots.length - 1) * HORIZONTAL_GAP;
      let startX = -totalWidth / 2;

      for (let i = 0; i < roots.length; i++) {
        const rootCenter = startX + rootWidths[i] / 2;
        layoutTree(roots[i], 0, rootCenter, null);
        startX += rootWidths[i] + HORIZONTAL_GAP;
      }
    }

    // Add saved groups
    const savedGroups = organization?.canvas_layout?.groups || [];
    for (const g of savedGroups) {
      nodes.push({
        id: g.id,
        type: 'group',
        position: g.position,
        width: g.width,
        height: g.height,
        data: { label: g.label, color: g.color },
      });
      nodeCounter = Math.max(nodeCounter, parseInt(g.id.replace('group_', '')) + 1 || nodeCounter);
    }

    // Add saved sticky notes
    const savedNotes = organization?.canvas_layout?.sticky_notes || [];
    for (const n of savedNotes) {
      nodes.push({
        id: n.id,
        type: 'sticky_note',
        position: n.position,
        width: n.width,
        height: n.height,
        data: { text: n.text, color: n.color },
      });
      nodeCounter = Math.max(nodeCounter, parseInt(n.id.replace('sticky_note_', '')) + 1 || nodeCounter);
    }

    return { nodes, edges };
  }

  // ─── Load ───

  async function loadOrganization() {
    try {
      organization = await getOrganization(params.id);
      storeNavbar.title = `Org: ${organization.name}`;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load organization', 'alert');
      push('/organizations');
    }
  }

  async function loadMemberships() {
    try {
      memberships = await listOrgAgents(params.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load organization agents', 'alert');
    }
  }

  async function loadAllAgents() {
    try {
      const res = await listAgents({ _limit: 1000 });
      allAgents = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load agents', 'alert');
    }
  }

  async function loadOrgGoals() {
    try {
      const res = await listGoals({ organization_id: params.id, _limit: 200 });
      orgGoals = res.data || [];
    } catch {
      // Goals may not be configured; silently ignore
      orgGoals = [];
    }
  }

  async function load() {
    loading = true;
    await Promise.all([loadOrganization(), loadMemberships(), loadAllAgents(), loadOrgGoals()]);
    loading = false;
    dirty = false;
    pendingParentUpdates = new Map();
    // Wait for DOM to render the Canvas component before refreshing it.
    await tick();
    refreshCanvas();
  }

  function refreshCanvas() {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    // Clear existing
    for (const edge of flow.edges) flow.removeEdge(edge.id);
    for (const node of flow.nodes) flow.removeNode(node.id);
    // Build and add
    const graph = buildFlowGraph();
    for (const node of graph.nodes) flow.addNode(node);
    for (const edge of graph.edges) flow.addEdge(edge);
  }

  load();

  // ─── Org Edit ───

  function startEditOrg() {
    if (!organization) return;
    editName = organization.name;
    editDescription = organization.description;
    editingOrg = true;
  }

  async function saveOrg() {
    if (!organization) return;
    saving = true;
    try {
      organization = await updateOrganization(organization.id, {
        name: editName.trim(),
        description: editDescription.trim(),
      });
      storeNavbar.title = `Org: ${organization.name}`;
      editingOrg = false;
      addToast('Organization updated');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update organization', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Save Canvas (hierarchy + layout) ───

  async function handleHeadAgentChange(e: Event) {
    if (!organization) return;
    const value = (e.target as HTMLSelectElement).value;
    try {
      organization = await updateOrganization(organization.id, { head_agent_id: value });
      addToast(value ? 'Head agent updated' : 'Head agent cleared');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update head agent', 'alert');
    }
  }

  async function handleSubmitTask() {
    if (!organization || !taskTitle.trim()) return;
    submittingTask = true;
    try {
      const result = await submitOrgTask(organization.id, {
        title: taskTitle.trim(),
        description: taskDescription.trim() || undefined,
        priority_level: taskPriority || undefined,
        goal_id: taskGoalId || undefined,
      });
      lastTaskResult = result;
      taskTitle = '';
      taskDescription = '';
      taskPriority = '';
      taskGoalId = '';
      addToast(`Task ${result.identifier} submitted`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to submit task', 'alert');
    } finally {
      submittingTask = false;
    }
  }

  async function handleSave() {
    if (!organization || !canvasRef) return;
    saving = true;
    try {
      const flow = canvasRef.getFlow();

      // 1) Apply pending parent updates from edge connections
      for (const [agentId, parentId] of pendingParentUpdates) {
        await updateOrgAgent(params.id, agentId, { parent_agent_id: parentId || '' });
      }
      pendingParentUpdates = new Map();

      // 2) Build canvas layout from current flow state
      const canvasLayout: CanvasLayout = {
        groups: [],
        sticky_notes: [],
        agent_positions: {},
      };

      for (const node of flow.nodes) {
        if (node.type === 'group') {
          canvasLayout.groups!.push({
            id: node.id,
            position: node.position,
            width: node.width || 250,
            height: node.height || 200,
            label: node.data?.label || 'Group',
            color: node.data?.color || '#22c55e',
          });
        } else if (node.type === 'sticky_note') {
          canvasLayout.sticky_notes!.push({
            id: node.id,
            position: node.position,
            width: node.width || 200,
            height: node.height || 140,
            text: node.data?.text || '',
            color: node.data?.color || '#fef08a',
          });
        } else if (node.type === 'agent') {
          canvasLayout.agent_positions![node.id] = { x: node.position.x, y: node.position.y };
        }
      }

      // 3) Save layout to organization
      organization = await updateOrganization(organization.id, {
        canvas_layout: canvasLayout,
      });

      // 4) Reload memberships to reflect parent changes
      await loadMemberships();
      dirty = false;
      addToast('Organization canvas saved');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Agent Management (via membership API) ───

  /** Agents not yet in this organization */
  function availableAgents(): Agent[] {
    const memberIds = new Set(memberships.map((m) => m.agent_id));
    return allAgents.filter((a) => !memberIds.has(a.id));
  }

  async function handleAddAgent(agent: Agent) {
    try {
      await addAgentToOrg(params.id, { agent_id: agent.id });
      addToast(`Agent "${agent.name}" added to organization`);
      await loadMemberships();
      refreshCanvas();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add agent', 'alert');
    }
  }

  async function handleRemoveAgent(agentId: string) {
    const agent = allAgents.find((a) => a.id === agentId);
    try {
      await removeAgentFromOrg(params.id, agentId);
      addToast(`Agent "${agent?.name || agentId}" removed from organization`);
      selectedAgentId = null;
      await loadMemberships();
      refreshCanvas();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove agent', 'alert');
    }
  }

  async function setAgentParent(agentId: string, parentId: string | null) {
    try {
      await updateOrgAgent(params.id, agentId, { parent_agent_id: parentId || '' });
      addToast('Agent hierarchy updated');
      await loadMemberships();
      refreshCanvas();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update hierarchy', 'alert');
    }
  }

  async function handleUpdateHeartbeatSchedule(agentId: string, schedule: string) {
    try {
      await updateOrgAgent(params.id, agentId, { heartbeat_schedule: schedule.trim() });
      addToast('Heartbeat schedule updated');
      await loadMemberships();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update heartbeat schedule', 'alert');
    }
  }

  // ─── Canvas Callbacks ───

  function onNodeClick(nodeId: string) {
    // Only select agent nodes for the detail panel
    const memberIds = new Set(memberships.map((m) => m.agent_id));
    if (memberIds.has(nodeId)) {
      selectedAgentId = nodeId;
    } else {
      selectedAgentId = null;
    }
  }

  function onSelectionChange(nodeIds: string[], _edgeIds: string[]) {
    const memberIds = new Set(memberships.map((m) => m.agent_id));
    const agentNodes = nodeIds.filter((id) => memberIds.has(id));
    if (agentNodes.length === 1) {
      selectedAgentId = agentNodes[0];
    } else if (agentNodes.length === 0) {
      selectedAgentId = null;
    }
  }

  function onConnect(sourceId: string, targetId: string, sourceHandle?: string, targetHandle?: string) {
    // When an edge is drawn from agent A (children handle) to agent B (parent handle),
    // it means B reports to A.
    const memberIds = new Set(memberships.map((m) => m.agent_id));
    if (memberIds.has(sourceId) && memberIds.has(targetId)) {
      pendingParentUpdates.set(targetId, sourceId);
      dirty = true;
    }
  }

  function onEdgeDelete(edgeId: string) {
    // Parse deleted edge to clear parent relationship
    if (edgeId.startsWith('edge-')) {
      const parts = edgeId.replace('edge-', '').split('-');
      if (parts.length >= 2) {
        const targetId = parts.slice(1).join('-');
        pendingParentUpdates.set(targetId, null);
        dirty = true;
      }
    }
  }

  function onNodeMove() {
    dirty = true;
  }

  // ─── Add Group / Sticky Note ───

  function addCanvasNode(type: 'group' | 'sticky_note', position?: { x: number; y: number }) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    nodeCounter++;

    const data = type === 'group'
      ? { label: 'Group', color: '#22c55e' }
      : { text: 'Double-click to edit...', color: '#fef08a' };

    const pos = position ?? { x: 200 + nodeCounter * 30, y: 150 + nodeCounter * 30 };
    const nodeOpts: Record<string, any> = {
      id: `${type}_${nodeCounter}`,
      type,
      position: pos,
      data,
    };

    if (type === 'group') {
      nodeOpts.width = 250;
      nodeOpts.height = 200;
    } else {
      nodeOpts.width = 200;
      nodeOpts.height = 140;
    }

    flow.addNode(nodeOpts as FlowNode);
    dirty = true;
  }

  // ─── Drag & Drop ───

  let draggingOver = $state(false);

  function handleDragStart(e: DragEvent, type: string) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData('application/at-org-node-type', type);
    e.dataTransfer.effectAllowed = 'copy';
  }

  function handleDragOver(e: DragEvent) {
    if (!e.dataTransfer?.types.includes('application/at-org-node-type')) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
    draggingOver = true;
  }

  function handleDragLeave() {
    draggingOver = false;
  }

  function handleDrop(e: DragEvent) {
    draggingOver = false;
    if (!e.dataTransfer || !canvasRef) return;
    const type = e.dataTransfer.getData('application/at-org-node-type');
    if (!type || (type !== 'group' && type !== 'sticky_note')) return;
    e.preventDefault();

    // Convert drop coordinates to canvas coordinates.
    const canvasEl = (e.currentTarget as HTMLElement).querySelector('.kaykay-canvas');
    if (!canvasEl) return;
    const rect = canvasEl.getBoundingClientRect();
    const flow = canvasRef.getFlow();
    const canvasPos = flow.screenToCanvas({
      x: e.clientX - rect.left,
      y: e.clientY - rect.top,
    });

    addCanvasNode(type, canvasPos);
  }

  // ─── Computed ───

  function selectedMembership(): OrganizationAgent | null {
    if (!selectedAgentId) return null;
    return memberships.find((m) => m.agent_id === selectedAgentId) || null;
  }

  function selectedAgent(): Agent | null {
    if (!selectedAgentId) return null;
    return allAgents.find((a) => a.id === selectedAgentId) || null;
  }

  // Initial graph (will be empty since data hasn't loaded yet)
  const initialGraph = buildFlowGraph();

  // Palette items
  const paletteItems = [
    { type: 'group', label: 'Group', description: 'Visual grouping of agents', icon: Group },
    { type: 'sticky_note', label: 'Sticky Note', description: 'Markdown note on canvas', icon: StickyNote },
  ];
</script>

<svelte:head>
  <title>AT | {organization?.name || 'Organization'}</title>
</svelte:head>

{#if loading}
  <div class="p-8 text-center text-sm text-gray-500 dark:text-dark-text-muted">Loading organization...</div>
{:else if organization}
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex items-center justify-between px-3 py-1.5 bg-white dark:bg-dark-surface border-b border-gray-200 dark:border-dark-border shrink-0">
      <div class="flex items-center gap-3">
        <button
          onclick={() => push('/organizations')}
          class="flex items-center gap-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
        >
          <ArrowLeft size={14} />
          Back
        </button>
        <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
        {#if editingOrg}
          <div class="flex items-center gap-2">
            <input
              type="text"
              bind:value={editName}
              class="text-sm font-medium text-gray-900 dark:text-dark-text bg-transparent border border-gray-300 dark:border-dark-border-subtle rounded px-2 py-0.5 outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 w-48"
              placeholder="Organization name"
            />
            <input
              type="text"
              bind:value={editDescription}
              class="text-xs text-gray-500 dark:text-dark-text-muted bg-transparent border border-gray-300 dark:border-dark-border-subtle rounded px-2 py-0.5 outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 w-64"
              placeholder="Description..."
            />
            <button
              onclick={saveOrg}
              disabled={saving}
              class="flex items-center gap-1 px-2 py-1 text-xs text-white bg-gray-900 dark:bg-accent rounded hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              <Save size={12} />
              Save
            </button>
            <button
              onclick={() => { editingOrg = false; }}
              class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
            >
              Cancel
            </button>
          </div>
        {:else}
          <div class="flex flex-col">
            <button onclick={startEditOrg} class="text-left group">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-text group-hover:underline">{organization.name}</span>
            </button>
            {#if organization.description}
              <span class="text-[10px] text-gray-400 dark:text-dark-text-faint">{organization.description}</span>
            {/if}
          </div>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        <span class="text-[10px] text-gray-400 dark:text-dark-text-faint">{memberships.length} agent{memberships.length !== 1 ? 's' : ''}</span>
        {#if !editingOrg && memberships.length > 0}
          <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
          <div class="flex items-center gap-1.5">
            <Crown size={12} class="text-amber-500" />
            <span class="text-[10px] text-gray-500 dark:text-dark-text-muted">Head:</span>
            <select
              value={organization.head_agent_id || ''}
              onchange={handleHeadAgentChange}
              class="text-xs border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20"
            >
              <option value="">None</option>
              {#each memberships as m (m.agent_id)}
                {@const agent = allAgents.find(a => a.id === m.agent_id)}
                <option value={m.agent_id}>{agent?.name || m.agent_id}</option>
              {/each}
            </select>
          </div>
        {/if}
        <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
        <button
          onclick={() => { load(); }}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle rounded hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
        >
          <RefreshCw size={12} />
          Refresh
        </button>
        <button
          onclick={() => { showAddPanel = !showAddPanel; showTaskPanel = false; }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showAddPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors"
        >
          <UserPlus size={12} />
          Add Agent
        </button>
        <button
          onclick={() => { showTaskPanel = !showTaskPanel; showAddPanel = false; }}
          disabled={!organization.head_agent_id}
          title={organization.head_agent_id ? 'Submit a task to this organization' : 'Set a head agent first'}
          class="flex items-center gap-1 px-2 py-1 text-xs {showTaskPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Send size={12} />
          Submit Task
        </button>
        <button
          onclick={handleSave}
          disabled={saving}
          class="flex items-center gap-1 px-2 py-1 text-xs {dirty ? 'text-white bg-blue-600 hover:bg-blue-700' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated'} rounded disabled:opacity-50 transition-colors"
        >
          <Save size={12} />
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>

    <!-- Main area -->
    <div class="flex flex-1 overflow-hidden">
      <!-- Annotation Palette -->
      <div class="w-36 bg-white dark:bg-dark-surface border-r border-gray-200 dark:border-dark-border shrink-0 overflow-y-auto">
        <div class="p-2">
          <span class="text-[10px] font-medium text-gray-400 dark:text-dark-text-faint uppercase tracking-wider">Annotation</span>
          <div class="mt-1 space-y-1">
            {#each paletteItems as item}
              <button
                class="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left text-gray-700 dark:text-dark-text-secondary bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border cursor-grab hover:bg-gray-100 dark:hover:bg-dark-border active:cursor-grabbing transition-colors"
                draggable="true"
                ondragstart={(e) => handleDragStart(e, item.type)}
                onclick={() => addCanvasNode(item.type as 'group' | 'sticky_note')}
              >
                <item.icon size={14} class="text-gray-400 dark:text-dark-text-faint shrink-0" />
                <div class="min-w-0">
                  <div class="font-medium truncate">{item.label}</div>
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-faint truncate">{item.description}</div>
                </div>
              </button>
            {/each}
          </div>
        </div>
      </div>

      <!-- Canvas -->
      <div
        class="flex-1 relative bg-gray-50 dark:bg-dark-base {storeTheme.mode === 'dark' ? 'kaykay-dark' : ''} {draggingOver ? 'ring-2 ring-inset ring-blue-400/50' : ''}"
        role="application"
        ondragover={handleDragOver}
        ondragleave={handleDragLeave}
        ondrop={handleDrop}
      >
        <Canvas
          bind:this={canvasRef}
          nodes={initialGraph.nodes}
          edges={initialGraph.edges}
          {nodeTypes}
          config={{ snap_to_grid: true, grid_size: 20, default_edge_type: 'bezier' }}
          callbacks={{
            on_node_click: onNodeClick,
            on_selection_change: onSelectionChange,
            on_connect: onConnect,
            on_edge_delete: onEdgeDelete,
            on_node_move: onNodeMove,
          }}
        >
          {#snippet controls()}
            <Controls position="bottom-left" />
            <Minimap width={160} height={100} />
          {/snippet}
        </Canvas>

        {#if memberships.length === 0}
          <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
            <div class="text-center">
              <p class="text-sm text-gray-400 dark:text-dark-text-muted">No agents in this organization</p>
              <p class="text-xs text-gray-300 dark:text-dark-text-faint mt-1">Use "Add Agent" to assign agents</p>
            </div>
          </div>
        {/if}
      </div>

      <!-- Submit Task Panel -->
      {#if showTaskPanel}
        <div class="w-64 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Submit Task</span>
            <button onclick={() => { showTaskPanel = false; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
           <div class="p-3 space-y-3 overflow-y-auto flex-1">
            <div>
              <label class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Title *</label>
              <input type="text" bind:value={taskTitle} placeholder="What needs to be done?"
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
            </div>
            <div>
              <label class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Description</label>
              <textarea bind:value={taskDescription} rows="3" placeholder="Additional context..."
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text resize-y"></textarea>
            </div>
            <div>
              <label class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Priority</label>
              <select bind:value={taskPriority}
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text">
                <option value="">None</option>
                {#each TASK_PRIORITIES as prio}
                  <option value={prio}>{TASK_PRIORITY_LABELS[prio]}</option>
                {/each}
              </select>
            </div>
            {#if orgGoals.length > 0}
              <div>
                <label class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Goal</label>
                <select bind:value={taskGoalId}
                  class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text">
                  <option value="">None</option>
                  {#each orgGoals as goal}
                    <option value={goal.id}>{goal.name}</option>
                  {/each}
                </select>
              </div>
            {/if}
            <button
              onclick={handleSubmitTask}
              disabled={submittingTask || !taskTitle.trim()}
              class="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 text-xs text-white bg-gray-900 dark:bg-accent rounded hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              <Send size={12} />
              {submittingTask ? 'Submitting...' : 'Submit'}
            </button>
            {#if lastTaskResult}
              <div class="p-2 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded text-xs">
                <span class="font-medium text-green-700 dark:text-green-400">{lastTaskResult.identifier}</span>
                <span class="text-green-600 dark:text-green-500"> created — delegation in progress</span>
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Add Agent Panel -->
      {#if showAddPanel}
        <div class="w-64 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Add Agent</span>
            <button onclick={() => { showAddPanel = false; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="overflow-y-auto min-h-0 flex-1">
            {#if availableAgents().length === 0}
              <div class="p-3 text-xs text-gray-400 dark:text-dark-text-faint text-center">
                All agents are already in this organization
              </div>
            {:else}
              {#each availableAgents() as agent (agent.id)}
                <div class="flex items-center justify-between px-3 py-2 border-b border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">
                  <div class="min-w-0">
                    <div class="text-xs font-medium text-gray-800 dark:text-dark-text truncate">{agent.name}</div>
                    {#if agent.config.model}
                      <div class="text-[10px] text-gray-400 dark:text-dark-text-faint font-mono truncate">{agent.config.model}</div>
                    {/if}
                  </div>
                  <button
                    onclick={() => handleAddAgent(agent)}
                    class="shrink-0 ml-2 p-1 text-gray-400 dark:text-dark-text-muted hover:text-green-600 dark:hover:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 rounded transition-colors"
                    title="Add to organization"
                  >
                    <Plus size={14} />
                  </button>
                </div>
              {/each}
            {/if}
          </div>
        </div>
      {/if}

      <!-- Agent Detail Panel -->
      {#if selectedAgentId && selectedMembership()}
        {@const membership = selectedMembership()}
        {@const agent = selectedAgent()}
        <div class="w-60 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Agent Details</span>
            <button onclick={() => { selectedAgentId = null; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto min-h-0 flex-1">
            {#if membership && agent}
              <div>
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Name</span>
                <div class="text-xs font-medium text-gray-900 dark:text-dark-text mt-0.5">{agent.name}</div>
              </div>
              {#if membership.title}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Title</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary mt-0.5">{membership.title}</div>
                </div>
              {/if}
              {#if membership.role}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Role</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary mt-0.5">{membership.role}</div>
                </div>
              {/if}
              {#if agent.config.model}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Model</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary font-mono mt-0.5">{agent.config.model}</div>
                </div>
              {/if}
              {#if agent.config.description}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Description</span>
                  <div class="text-xs text-gray-600 dark:text-dark-text-muted mt-0.5">{agent.config.description}</div>
                </div>
              {/if}

              <!-- Parent selector -->
              <div>
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Reports To</span>
                <select
                  value={membership.parent_agent_id || ''}
                  onchange={(e) => setAgentParent(membership.agent_id, (e.target as HTMLSelectElement).value || null)}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text"
                >
                  <option value="">None (root)</option>
                  {#each memberships.filter((m) => m.agent_id !== membership.agent_id) as candidate (candidate.agent_id)}
                    {@const candidateAgent = allAgents.find((a) => a.id === candidate.agent_id)}
                    <option value={candidate.agent_id}>{candidateAgent?.name || candidate.agent_id}</option>
                  {/each}
                </select>
              </div>

              <!-- Heartbeat schedule -->
              <div>
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Heartbeat Schedule</span>
                <input
                  type="text"
                  value={membership.heartbeat_schedule || ''}
                  onchange={(e) => handleUpdateHeartbeatSchedule(membership.agent_id, (e.target as HTMLInputElement).value)}
                  placeholder="Cron (e.g., */5 * * * *)"
                  class="mt-0.5 w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
              </div>
            {:else if membership}
              <div class="text-xs text-gray-400 dark:text-dark-text-faint">
                Agent data unavailable (may have been deleted)
              </div>
            {/if}
          </div>
          <div class="px-3 py-2 border-t border-gray-200 dark:border-dark-border shrink-0">
            <button
              onclick={() => { if (selectedAgentId) handleRemoveAgent(selectedAgentId); }}
              class="w-full flex items-center justify-center gap-1 px-2 py-1 text-xs text-red-600 dark:text-red-400 border border-red-200 dark:border-red-800 rounded hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
            >
              <Trash2 size={12} />
              Remove from Org
            </button>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
