<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getWorkflow, updateWorkflow, runWorkflow, listWorkflowVersions, getWorkflowVersion, setActiveVersion, type Workflow, type WorkflowVersion, type WorkflowNode, type WorkflowEdge } from '@/lib/api/workflows';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listNodeConfigs, type NodeConfig } from '@/lib/api/node-configs';
  import { Canvas, Controls, Minimap, GroupNode, getFlow, type FlowNode, type FlowEdge, type FlowState, type NodeTypes } from 'kaykay';
  import { ArrowLeft, Save, Play, Plus, X, Bot, ChevronRight, History, Check, Clock } from 'lucide-svelte';
  import ChatPanel from '@/lib/components/workflow/ChatPanel.svelte';

  import InputNode from '@/lib/components/workflow/InputNode.svelte';
  import OutputNode from '@/lib/components/workflow/OutputNode.svelte';
  import LLMCallNode from '@/lib/components/workflow/LLMCallNode.svelte';
  import AgentCallNode from '@/lib/components/workflow/AgentCallNode.svelte';
  import TemplateNode from '@/lib/components/workflow/TemplateNode.svelte';
  import HttpTriggerNode from '@/lib/components/workflow/HttpTriggerNode.svelte';
  import CronTriggerNode from '@/lib/components/workflow/CronTriggerNode.svelte';
  import HttpRequestNode from '@/lib/components/workflow/HttpRequestNode.svelte';
  import ConditionalNode from '@/lib/components/workflow/ConditionalNode.svelte';
  import LoopNode from '@/lib/components/workflow/LoopNode.svelte';
  import ScriptNode from '@/lib/components/workflow/ScriptNode.svelte';
  import ExecNode from '@/lib/components/workflow/ExecNode.svelte';
  import SkillConfigNode from '@/lib/components/workflow/SkillConfigNode.svelte';
  import MCPConfigNode from '@/lib/components/workflow/MCPConfigNode.svelte';
  import MemoryConfigNode from '@/lib/components/workflow/MemoryConfigNode.svelte';
  import EmailNode from '@/lib/components/workflow/EmailNode.svelte';
  import LogNode from '@/lib/components/workflow/LogNode.svelte';
  import MarkdownStickyNote from '@/lib/components/workflow/MarkdownStickyNote.svelte';

  // ─── Props ───
  let { params = { id: '' } }: { params?: { id: string } } = $props();

  storeNavbar.title = 'Workflow Editor';

  // ─── Node Types ───
  const nodeTypes: NodeTypes = {
    input: InputNode,
    output: OutputNode,
    llm_call: LLMCallNode,
    agent_call: AgentCallNode,
    template: TemplateNode,
    http_trigger: HttpTriggerNode,
    cron_trigger: CronTriggerNode,
    http_request: HttpRequestNode,
    conditional: ConditionalNode,
    loop: LoopNode,
    script: ScriptNode,
    exec: ExecNode,
    skill_config: SkillConfigNode,
    mcp_config: MCPConfigNode,
    memory_config: MemoryConfigNode,
    email: EmailNode,
    log: LogNode,
    group: GroupNode,
    sticky_note: MarkdownStickyNote,
  };

  const paletteGroups = [
    {
      label: 'Triggers',
      nodes: [
        { type: 'input', label: 'Input', description: 'Manual input data' },
        { type: 'http_trigger', label: 'HTTP Trigger', description: 'Webhook-triggered entry' },
        { type: 'cron_trigger', label: 'Cron Trigger', description: 'Schedule-triggered entry' },
      ],
    },
    {
      label: 'Processing',
      nodes: [
        { type: 'llm_call', label: 'LLM Call', description: 'Call an LLM provider' },
        { type: 'agent_call', label: 'Agent Call', description: 'Agentic loop with tools' },
        { type: 'template', label: 'Template', description: 'Template with variables' },
        { type: 'http_request', label: 'HTTP Request', description: 'Make an HTTP request' },
        { type: 'email', label: 'Email', description: 'Send email via SMTP' },
        { type: 'script', label: 'Script', description: 'Run JavaScript code' },
        { type: 'exec', label: 'Exec', description: 'Run a shell command' },
        { type: 'log', label: 'Log', description: 'Log data and pass through' },
      ],
    },
    {
      label: 'Flow Control',
      nodes: [
        { type: 'conditional', label: 'Conditional', description: 'If/else branching' },
        { type: 'loop', label: 'Loop', description: 'For-each fan-out' },
      ],
    },
    {
      label: 'Resources',
      nodes: [
        { type: 'skill_config', label: 'Skill Config', description: 'Skills for agent nodes' },
        { type: 'mcp_config', label: 'MCP Config', description: 'MCP servers for agents' },
        { type: 'memory_config', label: 'Memory', description: 'Memory/context for agents' },
      ],
    },
    {
      label: 'Output',
      nodes: [
        { type: 'output', label: 'Output', description: 'Workflow output data' },
      ],
    },
    {
      label: 'Annotation',
      nodes: [
        { type: 'group', label: 'Group', description: 'Visual grouping of nodes' },
        { type: 'sticky_note', label: 'Sticky Note', description: 'Markdown note on canvas' },
      ],
    },
  ];

  // ─── State ───
  let workflow = $state<Workflow | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let running = $state(false);
  let providers = $state<ProviderRecord[]>([]);
  let skills = $state<Skill[]>([]);
  let nodeConfigs = $state<NodeConfig[]>([]);
  let runResult = $state<any>(null);
  let runError = $state<string | null>(null);

  // Property editor
  let selectedNodeId = $state<string | null>(null);
  let selectedNodeData = $state<Record<string, any>>({});
  let selectedNodeType = $state<string>('');
  let selectedNodeOriginalData = $state<Record<string, any>>({});

  // Run inputs
  let showRunPanel = $state(false);
  let showChatPanel = $state(false);
  let runInputsJson = $state('');
  let runInputMode = $state<'text' | 'json'>('text');
  let runSync = $state(true);

  // Versioning
  let versions = $state<WorkflowVersion[]>([]);
  let showVersionPanel = $state(false);
  let viewingVersion = $state<number | null>(null); // non-null when viewing a historical version
  let runVersion = $state<number | undefined>(undefined); // version override for run panel
  let loadingVersions = $state(false);
  let settingActive = $state(false);

  // Canvas ref
  let canvasRef: { getFlow: () => FlowState } | undefined = $state();


  // ─── Helpers ───

  function toFlowNodes(nodes: WorkflowNode[]): FlowNode[] {
    return nodes.map((n) => ({
      id: n.id,
      type: n.type,
      position: { x: n.position.x, y: n.position.y },
      data: n.data || {},
      ...(n.width != null && { width: n.width }),
      ...(n.height != null && { height: n.height }),
      ...(n.parent_id && { parent_id: n.parent_id }),
      ...(n.z_index != null && { z_index: n.z_index }),
    }));
  }

  function toFlowEdges(edges: WorkflowEdge[]): FlowEdge[] {
    return edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      source_handle: e.source_handle,
      target_handle: e.target_handle,
    }));
  }

  function flowToGraph(flow: FlowState): { nodes: WorkflowNode[]; edges: WorkflowEdge[] } {
    const json = flow.toJSON();
    const nodes: WorkflowNode[] = json.nodes.map((n: FlowNode) => ({
      id: n.id,
      type: n.type,
      position: { x: n.position.x, y: n.position.y },
      data: n.data || {},
      ...(n.width != null && { width: n.width }),
      ...(n.height != null && { height: n.height }),
      ...(n.parent_id && { parent_id: n.parent_id }),
      ...(n.z_index != null && { z_index: n.z_index }),
    }));
    const edges: WorkflowEdge[] = json.edges.map((e: FlowEdge) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      source_handle: e.source_handle,
      target_handle: e.target_handle,
    }));
    return { nodes, edges };
  }

  function defaultNodeData(type: string): Record<string, any> {
    if (type === 'input') {
      return { label: 'Input' };
    }
    if (type === 'output') {
      return { label: 'Output' };
    }
    if (type === 'llm_call') {
      return { label: 'LLM Call', provider: '', model: '', system_prompt: '' };
    }
    if (type === 'agent_call') {
      return { label: 'Agent Call', provider: '', model: '', system_prompt: '', max_iterations: 10 };
    }
    if (type === 'skill_config') {
      return { label: 'Skill Config', skills: [] };
    }
    if (type === 'mcp_config') {
      return { label: 'MCP Config', mcp_urls: [] };
    }
    if (type === 'memory_config') {
      return { label: 'Memory' };
    }
    if (type === 'template') {
      return { label: 'Template', template: '', variables: [] };
    }
    if (type === 'http_trigger') {
      return { label: 'HTTP Trigger', trigger_id: '', alias: '', public: false };
    }
    if (type === 'cron_trigger') {
      return { label: 'Cron Trigger', schedule: '', payload: {} };
    }
    if (type === 'http_request') {
      return {
        label: 'HTTP Request',
        url: '',
        method: 'GET',
        headers: {},
        body: '',
        timeout: 30,
        proxy: '',
        insecure_skip_verify: false,
        retry: false,
      };
    }
    if (type === 'conditional') {
      return { label: 'Conditional', expression: '' };
    }
    if (type === 'loop') {
      return { label: 'Loop', expression: '' };
    }
    if (type === 'script') {
      return { label: 'Script', code: '', input_count: 1 };
    }
    if (type === 'exec') {
      return { label: 'Exec', command: '', working_dir: '', timeout: 60, sandbox_root: '/tmp/at-sandbox', input_count: 1 };
    }
    if (type === 'email') {
      return {
        label: 'Email',
        config_id: '',
        to: '',
        cc: '',
        bcc: '',
        subject: '',
        body: '',
        content_type: 'text/plain',
        from: '',
        reply_to: '',
      };
    }
    if (type === 'group') {
      return { label: 'Group', color: '#22c55e' };
    }
    if (type === 'sticky_note') {
      return { text: 'Double-click to edit...', color: '#fef08a' };
    }
    if (type === 'log') {
      return { label: 'Log', level: 'info', message: '' };
    }
    return {};
  }

  function stableStringify(value: any): string {
    if (value === null || typeof value !== 'object') return JSON.stringify(value);
    if (Array.isArray(value)) return `[${value.map((item) => stableStringify(item)).join(',')}]`;
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${stableStringify(value[key])}`).join(',')}}`;
  }

  function hasNodeEdits(): boolean {
    if (!selectedNodeId) return false;
    return stableStringify(selectedNodeData) !== stableStringify(selectedNodeOriginalData);
  }

  // ─── Load ───

  async function loadWorkflow() {
    loading = true;
    try {
      workflow = await getWorkflow(params.id);
      storeNavbar.title = `Workflow: ${workflow.name}`;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load workflow', 'alert');
      push('/workflows');
    } finally {
      loading = false;
    }
  }

  async function loadProviders() {
    try {
      providers = await listProviders();
    } catch {
      // Non-critical
    }
  }

  async function loadSkills() {
    try {
      skills = await listSkills();
    } catch {
      // Non-critical
    }
  }

  async function loadNodeConfigs() {
    try {
      nodeConfigs = await listNodeConfigs('email');
    } catch {
      // Non-critical
    }
  }

  // ─── Versions ───

  async function loadVersions() {
    if (!workflow) return;
    loadingVersions = true;
    try {
      versions = await listWorkflowVersions(workflow.id);
    } catch {
      versions = [];
    } finally {
      loadingVersions = false;
    }
  }

  async function loadVersionToCanvas(version: number) {
    if (!workflow || !canvasRef) return;
    try {
      const v = await getWorkflowVersion(workflow.id, version);
      const flow = canvasRef.getFlow();
      // Clear existing nodes and edges, then load the version's graph
      for (const edge of flow.edges) flow.removeEdge(edge.id);
      for (const node of flow.nodes) flow.removeNode(node.id);
      for (const node of toFlowNodes(v.graph.nodes)) flow.addNode(node);
      for (const edge of toFlowEdges(v.graph.edges)) flow.addEdge(edge);
      viewingVersion = version;
      addToast(`Loaded version ${version}`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load version', 'alert');
    }
  }

  function loadCurrentToCanvas() {
    if (!workflow || !canvasRef) return;
    const flow = canvasRef.getFlow();
    for (const edge of flow.edges) flow.removeEdge(edge.id);
    for (const node of flow.nodes) flow.removeNode(node.id);
    for (const node of toFlowNodes(workflow.graph.nodes)) flow.addNode(node);
    for (const edge of toFlowEdges(workflow.graph.edges)) flow.addEdge(edge);
    viewingVersion = null;
    addToast('Loaded latest version', 'info');
  }

  async function handleSetActiveVersion(version: number) {
    if (!workflow) return;
    settingActive = true;
    try {
      await setActiveVersion(workflow.id, version);
      workflow.active_version = version;
      await loadVersions();
      addToast(`Version ${version} set as active`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to set active version', 'alert');
    } finally {
      settingActive = false;
    }
  }

  // ─── Save ───

  async function handleSave() {
    if (!workflow || !canvasRef) return;
    saving = true;
    try {
      const flow = canvasRef.getFlow();
      const graph = flowToGraph(flow);
      workflow = await updateWorkflow(workflow.id, {
        name: workflow.name,
        description: workflow.description,
        graph,
      });
      // Push trigger IDs assigned by the backend back into canvas node data,
      // so trigger nodes immediately show their webhook URLs / status.
      pushTriggerIdsToCanvas(workflow);
      viewingVersion = null;
      addToast('Workflow saved', 'info');
      // Reload version list in background
      loadVersions();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save workflow', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Run ───

  async function handleRun() {
    if (!workflow) return;
    running = true;
    runResult = null;
    runError = null;
    try {
      // Save first
      if (canvasRef) {
        const flow = canvasRef.getFlow();
        const graph = flowToGraph(flow);
        workflow = await updateWorkflow(workflow.id, {
          name: workflow.name,
          description: workflow.description,
          graph,
        });
        pushTriggerIdsToCanvas(workflow);
      }
      const inputs = runInputMode === 'json'
        ? JSON.parse(runInputsJson || '{}')
        : { text: runInputsJson };
      runResult = await runWorkflow(workflow.id, inputs, runSync, runVersion);
    } catch (e: any) {
      if (e instanceof SyntaxError) {
        runError = 'Invalid JSON in inputs';
      } else {
        runError = e?.response?.data?.message || e?.message || 'Execution failed';
      }
    } finally {
      running = false;
    }
  }

  // ─── Add Node ───

  let nodeCounter = $state(0);

  function addNode(type: string, position?: { x: number; y: number }) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    nodeCounter++;
    const defaultData: Record<string, any> = {};
    if (type === 'input') {
      defaultData.label = 'Input';
    } else if (type === 'output') {
      defaultData.label = 'Output';
    } else if (type === 'llm_call') {
      defaultData.label = 'LLM Call';
      defaultData.provider = '';
      defaultData.model = '';
      defaultData.system_prompt = '';
    } else if (type === 'agent_call') {
      defaultData.label = 'Agent Call';
      defaultData.provider = '';
      defaultData.model = '';
      defaultData.system_prompt = '';
      defaultData.max_iterations = 10;
    } else if (type === 'skill_config') {
      defaultData.label = 'Skill Config';
      defaultData.skills = [];
    } else if (type === 'mcp_config') {
      defaultData.label = 'MCP Config';
      defaultData.mcp_urls = [];
    } else if (type === 'memory_config') {
      defaultData.label = 'Memory';
    } else if (type === 'template') {
      defaultData.label = 'Template';
      defaultData.template = '';
      defaultData.variables = [];
    } else if (type === 'http_trigger') {
      defaultData.label = 'HTTP Trigger';
      defaultData.trigger_id = '';
      defaultData.alias = '';
      defaultData.public = false;
    } else if (type === 'cron_trigger') {
      defaultData.label = 'Cron Trigger';
      defaultData.schedule = '';
      defaultData.payload = {};
    } else if (type === 'http_request') {
      defaultData.label = 'HTTP Request';
      defaultData.url = '';
      defaultData.method = 'GET';
      defaultData.headers = {};
      defaultData.body = '';
      defaultData.timeout = 30;
      defaultData.proxy = '';
      defaultData.insecure_skip_verify = false;
      defaultData.retry = false;
    } else if (type === 'conditional') {
      defaultData.label = 'Conditional';
      defaultData.expression = '';
    } else if (type === 'loop') {
      defaultData.label = 'Loop';
      defaultData.expression = '';
    } else if (type === 'script') {
      defaultData.label = 'Script';
      defaultData.code = '';
      defaultData.input_count = 1;
    } else if (type === 'exec') {
      defaultData.label = 'Exec';
      defaultData.command = '';
      defaultData.working_dir = '';
      defaultData.timeout = 60;
      defaultData.sandbox_root = '/tmp/at-sandbox';
      defaultData.input_count = 1;
    } else if (type === 'email') {
      defaultData.label = 'Email';
      defaultData.config_id = '';
      defaultData.to = '';
      defaultData.cc = '';
      defaultData.bcc = '';
      defaultData.subject = '';
      defaultData.body = '';
      defaultData.content_type = 'text/plain';
      defaultData.from = '';
      defaultData.reply_to = '';
    } else if (type === 'group') {
      defaultData.label = 'Group';
      defaultData.color = '#22c55e';
    } else if (type === 'sticky_note') {
      defaultData.text = 'Double-click to edit...';
      defaultData.color = '#fef08a';
    }
    const pos = position ?? { x: 200 + nodeCounter * 30, y: 150 + nodeCounter * 30 };
    const nodeOpts: Record<string, any> = {
      id: `${type}_${nodeCounter}`,
      type,
      position: pos,
      data: defaultData,
    };
    if (type === 'group') {
      nodeOpts.width = 250;
      nodeOpts.height = 200;
    } else if (type === 'sticky_note') {
      nodeOpts.width = 200;
      nodeOpts.height = 140;
    }
    flow.addNode(nodeOpts as FlowNode);
  }

  // ─── Drag & Drop ───

  let draggingOver = $state(false);

  function handleDragStart(e: DragEvent, type: string) {
    if (!e.dataTransfer) return;
    e.dataTransfer.setData('application/at-node-type', type);
    e.dataTransfer.effectAllowed = 'copy';
  }

  function handleDragOver(e: DragEvent) {
    if (!e.dataTransfer?.types.includes('application/at-node-type')) return;
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
    const type = e.dataTransfer.getData('application/at-node-type');
    if (!type) return;
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

    addNode(type, canvasPos);
  }

  // ─── Palette Collapse ───

  let collapsedGroups = $state<Record<string, boolean>>({});

  // ─── Property Editor ───

  const noPropertyPanelTypes = new Set(['group', 'sticky_note']);

  function selectNodeForEditor(nodeId: string) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    const node = flow.getNode(nodeId);
    if (node) {
      if (noPropertyPanelTypes.has(node.type)) {
        selectedNodeId = null;
        selectedNodeData = {};
        selectedNodeType = '';
        selectedNodeOriginalData = {};
        return;
      }
      selectedNodeId = nodeId;
      selectedNodeType = node.type;
      const defaults = defaultNodeData(node.type);
      selectedNodeData = { ...defaults, ...node.data };
      selectedNodeOriginalData = { ...defaults, ...node.data };
    }
  }

  function onNodeClick(nodeId: string) {
    selectNodeForEditor(nodeId);
  }

  function onSelectionChange(nodeIds: string[], _edgeIds: string[]) {
    if (nodeIds.length === 1 && nodeIds[0] !== selectedNodeId && canvasRef) {
      selectNodeForEditor(nodeIds[0]);
    }
    // If the currently selected node is no longer in the canvas selection, close the property editor
    if (selectedNodeId && !nodeIds.includes(selectedNodeId)) {
      selectedNodeId = null;
      selectedNodeData = {};
      selectedNodeType = '';
      selectedNodeOriginalData = {};
    }
  }

  function closePropertyEditor() {
    selectedNodeId = null;
    selectedNodeData = {};
    selectedNodeType = '';
    selectedNodeOriginalData = {};
    // Also clear canvas selection so the node is visually deselected
    if (canvasRef) {
      canvasRef.getFlow().clearSelection();
    }
  }

  function applyNodeData() {
    if (!canvasRef || !selectedNodeId) return;
    const flow = canvasRef.getFlow();

    // Handle script node input_count changes — remap edges to new handle IDs.
    if (selectedNodeType === 'script') {
      const currentNode = flow.getNode(selectedNodeId);
      const oldCount = Number(currentNode?.data?.input_count ?? 1);
      const newCount = Number(selectedNodeData.input_count ?? 1);

      if (oldCount !== newCount) {
        // Find all edges targeting this node's input handles.
        const incomingEdges = flow.edges.filter((e: any) => e.target === selectedNodeId);

        for (const edge of incomingEdges) {
          const handle = edge.target_handle;

          if (oldCount === 1 && newCount > 1) {
            // "data" → "data1"
            if (handle === 'data') {
              flow.updateEdge(edge.id, { target_handle: 'data1' });
            }
          } else if (oldCount > 1 && newCount === 1) {
            // "data1" → "data", remove data2+
            if (handle === 'data1') {
              flow.updateEdge(edge.id, { target_handle: 'data' });
            } else {
              flow.removeEdge(edge.id);
            }
          } else {
            // Both > 1: keep handles within new range, remove excess.
            const match = handle.match(/^data(\d+)$/);
            if (match) {
              const idx = parseInt(match[1], 10);
              if (idx > newCount) {
                flow.removeEdge(edge.id);
              }
            }
          }
        }
      }
    }

    flow.updateNodeData(selectedNodeId, selectedNodeData);
    selectedNodeOriginalData = { ...selectedNodeData };
    addToast('Node updated', 'info');
  }

  // ─── Trigger Sync ───

  // After saving, the backend populates trigger_id in trigger node data.
  // Push those IDs back into the canvas so nodes re-render with webhook URLs.
  function pushTriggerIdsToCanvas(wf: Workflow) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    for (const node of wf.graph.nodes) {
      if ((node.type === 'http_trigger' || node.type === 'cron_trigger') && node.data?.trigger_id) {
        flow.updateNodeData(node.id, { ...node.data });
      }
    }
    // Also refresh the property editor if a trigger node is currently selected.
    if (selectedNodeId) {
      const selectedNode = wf.graph.nodes.find((n) => n.id === selectedNodeId);
      if (selectedNode && (selectedNode.type === 'http_trigger' || selectedNode.type === 'cron_trigger')) {
        const defaults = defaultNodeData(selectedNode.type);
        selectedNodeData = { ...defaults, ...selectedNode.data };
        selectedNodeOriginalData = { ...defaults, ...selectedNode.data };
      }
    }
  }

  // ─── Init ───

  loadWorkflow().then(() => loadVersions());
  loadProviders();
  loadSkills();
  loadNodeConfigs();

</script>

<svelte:head>
  <title>AT | {workflow?.name || 'Workflow'}</title>
</svelte:head>

{#if loading}
  <div class="p-8 text-center text-sm text-gray-500">Loading workflow...</div>
{:else if workflow}
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex items-center justify-between px-3 py-1.5 bg-white border-b border-gray-200 shrink-0">
      <div class="flex items-center gap-3">
        <button
          onclick={() => push('/workflows')}
          class="flex items-center gap-1 text-xs text-gray-500 hover:text-gray-700 transition-colors"
        >
          <ArrowLeft size={14} />
          Back
        </button>
        <div class="h-4 border-l border-gray-200"></div>
        <div class="flex flex-col">
          <div class="flex items-center gap-2">
            <input
              type="text"
              bind:value={workflow.name}
              class="text-sm font-medium text-gray-900 bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
              placeholder="Workflow name"
            />
            <div class="flex items-center gap-1 group relative">
              <span class="text-[10px] font-mono text-gray-400 cursor-pointer hover:text-gray-600" title="Click to copy ID" onclick={() => { navigator.clipboard.writeText(workflow?.id || ''); addToast('ID copied', 'info'); }}>
                {workflow.id}
              </span>
            </div>
          </div>
          <input
            type="text"
            bind:value={workflow.description}
            class="text-[10px] text-gray-400 bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
            placeholder="Add description..."
          />
        </div>
      </div>
      <div class="flex items-center gap-2">
        {#if workflow.active_version != null}
          <span class="flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-medium rounded {viewingVersion != null ? 'text-amber-700 bg-amber-50 border border-amber-200' : 'text-gray-500 bg-gray-100 border border-gray-200'}">
            {#if viewingVersion != null}
              v{viewingVersion}
              {#if viewingVersion === workflow.active_version}
                <Check size={10} class="text-green-600" />
              {/if}
            {:else}
              v{workflow.active_version}
              <Check size={10} class="text-green-600" />
            {/if}
          </span>
        {/if}
        <button
          onclick={() => { showVersionPanel = !showVersionPanel; if (showVersionPanel) loadVersions(); }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showVersionPanel ? 'text-white bg-gray-900' : 'text-gray-700 bg-white border border-gray-300'} rounded hover:bg-gray-800 hover:text-white transition-colors"
        >
          <History size={12} />
          Versions
        </button>
        <button
          onclick={handleSave}
          disabled={saving}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-700 bg-white border border-gray-300 rounded hover:bg-gray-50 disabled:opacity-50 transition-colors"
        >
          <Save size={12} />
          {saving ? 'Saving...' : 'Save'}
        </button>
        <button
          onclick={() => { showChatPanel = !showChatPanel; }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showChatPanel ? 'text-white bg-gray-900' : 'text-gray-700 bg-white border border-gray-300'} rounded hover:bg-gray-800 hover:text-white transition-colors"
        >
          <Bot size={12} />
          AI
        </button>
        <button
          onclick={() => (showRunPanel = !showRunPanel)}
          class="flex items-center gap-1 px-2 py-1 text-xs text-white bg-green-600 rounded hover:bg-green-700 transition-colors"
        >
          <Play size={12} />
          Run
        </button>
      </div>
    </div>

    <!-- Version viewing banner -->
    {#if viewingVersion != null}
      <div class="flex items-center justify-between px-3 py-1 bg-amber-50 border-b border-amber-200 shrink-0">
        <span class="text-xs text-amber-700">
          Viewing version {viewingVersion}{viewingVersion === workflow.active_version ? ' (active)' : ''} — canvas is read-only until you return to latest
        </span>
        <button
          onclick={loadCurrentToCanvas}
          class="px-2 py-0.5 text-xs text-amber-700 bg-white border border-amber-300 rounded hover:bg-amber-100 transition-colors"
        >
          Back to latest
        </button>
      </div>
    {/if}

    <!-- Main area -->
    <div class="flex flex-1 overflow-hidden">
      <!-- Node Palette -->
      <div class="w-44 bg-white border-r border-gray-200 shrink-0 overflow-y-auto">
        <div class="p-2">
          {#each paletteGroups as group}
            <button
              onclick={() => { collapsedGroups[group.label] = !collapsedGroups[group.label]; }}
              class="w-full flex items-center gap-1 mt-2 first:mt-0 mb-1 text-left group"
            >
              <ChevronRight
                size={10}
                class="text-gray-400 transition-transform {collapsedGroups[group.label] ? '' : 'rotate-90'}"
              />
              <span class="text-[10px] font-medium text-gray-400 uppercase tracking-wider">{group.label}</span>
            </button>
            {#if !collapsedGroups[group.label]}
              {#each group.nodes as opt}
                <button
                  draggable="true"
                  ondragstart={(e) => handleDragStart(e, opt.type)}
                  onclick={() => addNode(opt.type)}
                  class="w-full flex items-center gap-2 px-2 py-1.5 text-xs text-left text-gray-700 rounded hover:bg-gray-100 transition-colors mb-0.5 cursor-grab active:cursor-grabbing"
                >
                  <Plus size={11} class="text-gray-400 shrink-0" />
                  <div>
                    <div class="font-medium">{opt.label}</div>
                    <div class="text-[10px] text-gray-400">{opt.description}</div>
                  </div>
                </button>
              {/each}
            {/if}
          {/each}
        </div>
      </div>

      <!-- Canvas -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div
        class="flex-1 relative bg-gray-50 {draggingOver ? 'ring-2 ring-inset ring-blue-400' : ''}"
        role="application"
        ondragover={handleDragOver}
        ondragleave={handleDragLeave}
        ondrop={handleDrop}
      >
        <Canvas
          bind:this={canvasRef}
          nodes={toFlowNodes(workflow.graph.nodes)}
          edges={toFlowEdges(workflow.graph.edges)}
          {nodeTypes}
          config={{ snap_to_grid: true, grid_size: 20, default_edge_type: 'bezier' }}
          callbacks={{ on_node_click: onNodeClick, on_selection_change: onSelectionChange }}
        >
          {#snippet controls()}
            <Controls position="bottom-left" />
            <Minimap width={160} height={100} />
          {/snippet}

        </Canvas>
      </div>

      <!-- AI Chat Panel -->
      {#if showChatPanel && canvasRef}
        <ChatPanel onclose={() => { showChatPanel = false; }} flow={canvasRef.getFlow()} />
      {/if}

      <!-- Version History Panel -->
      {#if showVersionPanel}
        <div class="w-64 bg-white border-l border-gray-200 shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 shrink-0">
            <span class="text-xs font-medium text-gray-700">Version History</span>
            <button onclick={() => { showVersionPanel = false; }} class="text-gray-400 hover:text-gray-600">
              <X size={14} />
            </button>
          </div>
          <div class="overflow-y-auto min-h-0 flex-1">
            {#if loadingVersions}
              <div class="p-3 text-xs text-gray-500 text-center">Loading...</div>
            {:else if versions.length === 0}
              <div class="p-3 text-xs text-gray-400 text-center">No versions yet. Save to create the first version.</div>
            {:else}
              <!-- Return to latest button when viewing old version -->
              {#if viewingVersion != null}
                <button
                  onclick={loadCurrentToCanvas}
                  class="w-full px-3 py-2 text-xs text-blue-600 hover:bg-blue-50 border-b border-gray-100 text-left transition-colors"
                >
                  Back to latest
                </button>
              {/if}
              {#each versions as v (v.id)}
                {@const isActive = workflow.active_version === v.version}
                {@const isViewing = viewingVersion === v.version}
                <div
                  class="px-3 py-2 border-b border-gray-100 {isViewing ? 'bg-amber-50' : 'hover:bg-gray-50'} transition-colors"
                >
                  <div class="flex items-center justify-between mb-0.5">
                    <div class="flex items-center gap-1.5">
                      <span class="text-xs font-medium text-gray-800">v{v.version}</span>
                      {#if isActive}
                        <span class="flex items-center gap-0.5 px-1 py-0 text-[9px] font-medium text-green-700 bg-green-50 border border-green-200 rounded">
                          <Check size={8} />
                          active
                        </span>
                      {/if}
                    </div>
                    <div class="flex items-center gap-1">
                      {#if !isActive}
                        <button
                          onclick={() => handleSetActiveVersion(v.version)}
                          disabled={settingActive}
                          class="px-1.5 py-0.5 text-[10px] text-gray-500 hover:text-green-700 hover:bg-green-50 rounded transition-colors disabled:opacity-50"
                          title="Set as active version"
                        >
                          Set active
                        </button>
                      {/if}
                      {#if !isViewing}
                        <button
                          onclick={() => loadVersionToCanvas(v.version)}
                          class="px-1.5 py-0.5 text-[10px] text-gray-500 hover:text-blue-700 hover:bg-blue-50 rounded transition-colors"
                          title="Load this version into canvas"
                        >
                          Load
                        </button>
                      {/if}
                    </div>
                  </div>
                  <div class="flex items-center gap-1 text-[10px] text-gray-400">
                    <Clock size={9} />
                    {new Date(v.created_at).toLocaleDateString()} {new Date(v.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    {#if v.created_by}
                      <span class="ml-1 text-gray-300">|</span> <span class="ml-1">by {v.created_by}</span>
                    {/if}
                  </div>
                  {#if v.name && v.name !== workflow.name}
                    <div class="text-[10px] text-gray-500 mt-0.5 truncate" title={v.name}>{v.name}</div>
                  {/if}
                </div>
              {/each}
            {/if}
          </div>
        </div>
      {/if}

      <!-- Property Editor Panel -->
      {#if selectedNodeId && !noPropertyPanelTypes.has(selectedNodeType)}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="w-60 bg-white border-l border-gray-200 shrink-0 min-h-0 flex flex-col outline-none"
          tabindex="-1"
          onmousedown={(e) => { e.stopPropagation(); e.currentTarget.focus(); }}
        >
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 shrink-0">
            <div class="flex items-center gap-2">
              <span class="text-xs font-medium text-gray-700">Properties</span>
              {#if hasNodeEdits()}
                <span class="text-[10px] font-medium leading-none text-amber-700 bg-amber-50 border border-amber-200 rounded px-1.5 py-0.5">Unsaved</span>
              {/if}
            </div>
            <button onclick={closePropertyEditor} class="text-gray-400 hover:text-gray-600">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto min-h-0 flex-1">
            <!-- Common: Label (not shown for sticky notes which use 'text' instead) -->
            {#if selectedNodeType !== 'sticky_note'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Label</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.label}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                /></label>
              </div>
            {/if}

            <!-- Type-specific fields -->
            {#if selectedNodeType === 'llm_call'}
              {@const selectedProvider = providers.find(p => p.key === selectedNodeData.provider)}
              {@const availableModels = selectedProvider?.config?.models?.length ? selectedProvider.config.models : selectedProvider?.config?.model ? [selectedProvider.config.model] : []}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Provider</span>
                <select
                  bind:value={selectedNodeData.provider}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select provider</option>
                  {#each providers as p}
                    <option value={p.key}>{p.key}</option>
                  {/each}
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Model</span>
                <select
                  bind:value={selectedNodeData.model}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select model</option>
                  {#each availableModels as m}
                    <option value={m}>{m}</option>
                  {/each}
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">System Prompt</span>
                <textarea
                  bind:value={selectedNodeData.system_prompt}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="System prompt (optional)"
                ></textarea></label>
              </div>
              <!-- Port descriptions -->
              <div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
                <div>
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
                  <div class="mt-1 space-y-1">
                    <div title="The main user message or instruction sent to the LLM. This is required. Falls back to 'text' or 'data' inputs if the prompt port is not connected.">
                      <span class="text-[11px] font-mono font-medium text-gray-700">prompt</span>
                      <span class="text-[10px] text-gray-400 ml-1">— Main instruction sent to the LLM (required)</span>
                    </div>
                    <div title="Optional supplementary data appended to the prompt under a 'Context:' header. Use this for reference documents, previous node outputs, or fetched content.">
                      <span class="text-[11px] font-mono font-medium text-gray-700">context</span>
                      <span class="text-[10px] text-gray-400 ml-1">— Extra reference data appended to prompt (optional)</span>
                    </div>
                  </div>
                </div>
                <div>
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
                  <div class="mt-1 space-y-1">
                    <div title="Returns a map with the LLM response text. Uses the 'data' port type so it can connect to any downstream node.">
                      <span class="text-[11px] font-mono font-medium text-gray-700">response</span>
                      <span class="text-[10px] text-gray-400 ml-1">— Map with LLM response, connectable to any node</span>
                    </div>
                  </div>
                </div>
              </div>
            {/if}

            {#if selectedNodeType === 'agent_call'}
              {@const selectedAgentProvider = providers.find(p => p.key === selectedNodeData.provider)}
              {@const availableAgentModels = selectedAgentProvider?.config?.models?.length ? selectedAgentProvider.config.models : selectedAgentProvider?.config?.model ? [selectedAgentProvider.config.model] : []}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Provider</span>
                <select
                  bind:value={selectedNodeData.provider}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select provider</option>
                  {#each providers as p}
                    <option value={p.key}>{p.key}</option>
                  {/each}
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Model</span>
                <select
                  bind:value={selectedNodeData.model}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select model</option>
                  {#each availableAgentModels as m}
                    <option value={m}>{m}</option>
                  {/each}
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">System Prompt</span>
                <textarea
                  bind:value={selectedNodeData.system_prompt}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="System prompt (optional)"
                ></textarea></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Max Iterations</span>
                <input
                  type="number"
                  bind:value={selectedNodeData.max_iterations}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="10"
                  min="0"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">0 = unlimited</div>
              </div>
            {/if}

            {#if selectedNodeType === 'skill_config'}
              <div>
                <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Skills</span>
                {#if skills.length > 0}
                  <div class="mt-0.5 space-y-0.5">
                    {#each skills as skill}
                      <label class="flex items-center gap-1.5 text-[11px] text-gray-700 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={selectedNodeData.skills?.includes(skill.name) || false}
                          onchange={(e) => {
                            const current = selectedNodeData.skills || [];
                            if ((e.target as HTMLInputElement).checked) {
                              selectedNodeData.skills = [...current, skill.name];
                            } else {
                              selectedNodeData.skills = current.filter((s: string) => s !== skill.name);
                            }
                          }}
                          class="rounded border-gray-300"
                        />
                        <span class="font-mono">{skill.name}</span>
                      </label>
                    {/each}
                  </div>
                {:else}
                  <div class="mt-0.5 text-[10px] text-gray-400 italic">No skills available</div>
                {/if}
              </div>
              <div class="mt-1 px-2 py-1.5 bg-green-50 border border-green-200 rounded text-[10px] text-green-700">
                Connect this node's <span class="font-mono font-medium">skills</span> output to an Agent Call's <span class="font-mono font-medium">skills</span> input.
              </div>
            {/if}

            {#if selectedNodeType === 'mcp_config'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">MCP Server URLs</span>
                <textarea
                  value={selectedNodeData.mcp_urls?.join('\n') || ''}
                  oninput={(e) => { selectedNodeData.mcp_urls = (e.target as HTMLTextAreaElement).value.split('\n').map((s: string) => s.trim()).filter(Boolean); }}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="https://mcp-server.example.com/sse"
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">One URL per line</div>
              </div>
              <div class="mt-1 px-2 py-1.5 bg-orange-50 border border-orange-200 rounded text-[10px] text-orange-700">
                Connect this node's <span class="font-mono font-medium">mcp_urls</span> output to an Agent Call's <span class="font-mono font-medium">mcp</span> input.
              </div>
            {/if}

            {#if selectedNodeType === 'memory_config'}
              <div class="mt-1 px-2 py-1.5 bg-teal-50 border border-teal-200 rounded text-[10px] text-teal-700">
                Connect upstream data to this node's <span class="font-mono font-medium">data</span> input, then connect the <span class="font-mono font-medium">memory</span> output to an Agent Call's <span class="font-mono font-medium">memory</span> input.
              </div>
              <div class="text-[10px] text-gray-400 mt-1">Passes upstream data as additional context to the agent.</div>
            {/if}

            {#if selectedNodeType === 'template'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Template</span>
                <textarea
                  bind:value={selectedNodeData.template}
                  rows={4}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                   placeholder={'Hello \x7B\x7B.name\x7D\x7D, ...'}
                ></textarea></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Variables (comma separated)</span>
                <input
                  type="text"
                  value={selectedNodeData.variables?.join(', ') || ''}
                  oninput={(e) => { selectedNodeData.variables = (e.target as HTMLInputElement).value.split(',').map((s: string) => s.trim()).filter(Boolean); }}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="name, topic"
                /></label>
              </div>
            {/if}

            {#if selectedNodeType === 'http_trigger'}
              <div>
                <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Public</span>
                <label class="mt-0.5 flex items-center gap-1.5 cursor-pointer">
                  <input
                    type="checkbox"
                    bind:checked={selectedNodeData.public}
                    class="rounded border-gray-300 text-gray-900 focus:ring-gray-400"
                  />
                  <span class="text-[10px] text-gray-600">
                    {selectedNodeData.public ? 'No authentication required' : 'Requires Bearer token'}
                  </span>
                </label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Alias</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.alias}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="e.g. order-created"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Optional human-friendly URL slug</div>
              </div>
              <div>
                {#if selectedNodeData.trigger_id}
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Webhook URL</span>
                  <div class="mt-0.5 px-2 py-1 text-[10px] font-mono text-gray-600 bg-gray-50 border border-gray-200 rounded break-all">
                    /webhooks/{selectedNodeData.alias || selectedNodeData.trigger_id}
                  </div>
                  <div class="mt-1 text-[10px] text-gray-400">
                    ID: <span class="font-mono">{selectedNodeData.trigger_id}</span>
                  </div>
                  {#if !selectedNodeData.public}
                    <div class="mt-1 px-2 py-1 bg-yellow-50 border border-yellow-200 rounded text-[10px] text-yellow-700">
                      Requires <span class="font-mono">Authorization: Bearer &lt;token&gt;</span> header
                    </div>
                  {/if}
                {:else}
                  <div class="text-[10px] text-gray-400 italic">Save the workflow to generate a webhook URL</div>
                {/if}
              </div>
              <div>
                <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Fields</span>
                <div class="mt-0.5 px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-[10px] font-mono text-gray-600 space-y-0.5">
                  <div><span class="text-gray-400">data.</span>method <span class="text-gray-400 font-sans">— HTTP method</span></div>
                  <div><span class="text-gray-400">data.</span>path <span class="text-gray-400 font-sans">— request path</span></div>
                  <div><span class="text-gray-400">data.</span>query <span class="text-gray-400 font-sans">— query params (map)</span></div>
                  <div><span class="text-gray-400">data.</span>headers <span class="text-gray-400 font-sans">— request headers (map)</span></div>
                  <div><span class="text-gray-400">data.</span>body <span class="text-gray-400 font-sans">— raw body (reader)</span></div>
                </div>
                <div class="mt-1 px-2 py-1 bg-gray-50 border border-gray-200 rounded text-[10px] text-gray-500">
                  <div class="font-medium text-gray-600 mb-0.5">Body methods:</div>
                  <div class="font-mono space-y-0.5">
                    <div>data.body.toString()</div>
                    <div>data.body.jsonParse()</div>
                    <div>data.body.toBase64()</div>
                    <div>data.body.bytes()</div>
                  </div>
                </div>
              </div>
            {/if}

            {#if selectedNodeType === 'cron_trigger'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Schedule (cron)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.schedule}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="*/5 * * * *"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Standard 5-field cron expression</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Static Payload (JSON)</span>
                <textarea
                  value={JSON.stringify(selectedNodeData.payload || {}, null, 2)}
                  oninput={(e) => { try { selectedNodeData.payload = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"key": "value"}'}
                ></textarea></label>
              </div>
            {/if}

            {#if selectedNodeType === 'http_request'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">URL (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.url}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder={'https://api.example.com/\x7B\x7B.path\x7D\x7D'}
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Supports Go templates with data from "values" input</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Method (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.method}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="GET"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">GET, POST, PUT, PATCH, DELETE or a Go template</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Headers (JSON, values support templates)</span>
                <textarea
                  value={JSON.stringify(selectedNodeData.headers || {}, null, 2)}
                  oninput={(e) => { try { selectedNodeData.headers = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
                  rows={2}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"Authorization": "Bearer \x7B\x7B.token\x7D\x7D"}'}
                ></textarea></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Body (Go template)</span>
                <textarea
                  bind:value={selectedNodeData.body}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"name": "\x7B\x7B.name\x7D\x7D", "count": \x7B\x7B.count\x7D\x7D}'}
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Leave empty to auto-send input data as JSON for POST/PUT/PATCH</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timeout (seconds)</span>
                <input
                  type="number"
                  bind:value={selectedNodeData.timeout}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="30"
                  min="1"
                  max="300"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Proxy URL</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.proxy}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="http://proxy.example.com:8080"
                /></label>
              </div>
              <div class="flex items-center gap-4">
                <label class="flex items-center gap-1.5 text-[10px] font-medium text-gray-500 uppercase tracking-wider cursor-pointer">
                  <input type="checkbox" bind:checked={selectedNodeData.insecure_skip_verify} class="rounded border-gray-300" />
                  Insecure TLS
                </label>
                <label class="flex items-center gap-1.5 text-[10px] font-medium text-gray-500 uppercase tracking-wider cursor-pointer">
                  <input type="checkbox" bind:checked={selectedNodeData.retry} class="rounded border-gray-300" />
                  Retry
                </label>
              </div>
            {/if}

            {#if selectedNodeType === 'conditional'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Expression (JS)</span>
                <textarea
                  bind:value={selectedNodeData.expression}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="data.score > 0.8"
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">JS expression that evaluates to true/false</div>
              </div>
            {/if}

            {#if selectedNodeType === 'loop'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Expression (JS)</span>
                <textarea
                  bind:value={selectedNodeData.expression}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="data.items"
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">JS expression returning an array to iterate</div>
              </div>
            {/if}

            {#if selectedNodeType === 'script'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</span>
                <input
                  type="number"
                  bind:value={selectedNodeData.input_count}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  min="1"
                  max="10"
                  placeholder="1"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">
                  {#if (selectedNodeData.input_count || 1) <= 1}
                    Available as <code class="font-mono bg-gray-100 px-0.5 rounded">data</code> in JS
                  {:else}
                    Available as
                    {#each Array(Math.min(selectedNodeData.input_count || 1, 10)) as _, i}
                      <code class="font-mono bg-gray-100 px-0.5 rounded">data{i + 1}</code>{i < (selectedNodeData.input_count || 1) - 1 ? ', ' : ''}
                    {/each}
                    in JS
                  {/if}
                </div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Code (JS)</span>
                <textarea
                  bind:value={selectedNodeData.code}
                  rows={6}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={
                    (selectedNodeData.input_count || 1) <= 1
                      ? '// Access inputs via data\nconst value = data.value * 2;\nreturn { doubled: value };'
                      : '// Access inputs via data1, data2, ...\nconst sum = data1.value + data2.value;\nreturn { sum: sum };'
                  }
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Use <code class="font-mono bg-gray-100 px-0.5 rounded">return</code> to set the result → "true" port. <code class="font-mono bg-gray-100 px-0.5 rounded">throw</code> → "false" port (with <code class="font-mono bg-gray-100 px-0.5 rounded">error</code> in output). "always" always fires.</div>
              </div>
              <div>
                <div class="text-[10px] font-medium text-gray-500 uppercase tracking-wider mb-1">Built-in Functions</div>
                <div class="px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-[10px] font-mono text-gray-600 space-y-1">
                  <div><span class="text-gray-800">log.info</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— info log</span></div>
                  <div><span class="text-gray-800">log.warn</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— warning log</span></div>
                  <div><span class="text-gray-800">log.error</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— error log</span></div>
                  <div><span class="text-gray-800">log.debug</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— debug log</span></div>
                  <div><span class="text-gray-800">toString</span>(v) <span class="font-sans text-gray-400">— bytes/value to string</span></div>
                  <div><span class="text-gray-800">jsonParse</span>(v) <span class="font-sans text-gray-400">— parse string/bytes as JSON</span></div>
                  <div><span class="text-gray-800">JSON_stringify</span>(v) <span class="font-sans text-gray-400">— marshal value to JSON string</span></div>
                  <div><span class="text-gray-800">btoa</span>(v) <span class="font-sans text-gray-400">— base64 encode</span></div>
                  <div><span class="text-gray-800">atob</span>(s) <span class="font-sans text-gray-400">— base64 decode</span></div>
                  <div><span class="text-gray-800">getVar</span>(key) <span class="font-sans text-gray-400">— read workflow variable</span></div>
                  <div><span class="text-gray-800">httpGet</span>(url, headers?) <span class="font-sans text-gray-400">— HTTP GET</span></div>
                  <div><span class="text-gray-800">httpPost</span>(url, body?, headers?) <span class="font-sans text-gray-400">— HTTP POST</span></div>
                  <div><span class="text-gray-800">httpPut</span>(url, body?, headers?) <span class="font-sans text-gray-400">— HTTP PUT</span></div>
                  <div><span class="text-gray-800">httpDelete</span>(url, headers?) <span class="font-sans text-gray-400">— HTTP DELETE</span></div>
                </div>
                <div class="mt-1 text-[10px] text-gray-400">HTTP functions return <code class="font-mono bg-gray-100 px-0.5 rounded">{"{ status, headers, body }"}</code>. Body has <code class="font-mono bg-gray-100 px-0.5 rounded">.toString()</code>, <code class="font-mono bg-gray-100 px-0.5 rounded">.jsonParse()</code> methods.</div>
              </div>
            {/if}

            {#if selectedNodeType === 'exec'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</span>
                <input
                  type="number"
                  bind:value={selectedNodeData.input_count}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  min="1"
                  max="10"
                  placeholder="1"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Command</span>
                <textarea
                  bind:value={selectedNodeData.command}
                  rows={4}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="echo 'Hello World'"
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Shell command (supports <code class="font-mono bg-gray-100 px-0.5 rounded">{'{{.var}}'}</code> templates from inputs)</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Working Dir</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.working_dir}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="(sandbox root)"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Subdirectory within sandbox</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timeout (sec)</span>
                <input
                  type="number"
                  bind:value={selectedNodeData.timeout}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  min="1"
                  max="600"
                  placeholder="60"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Sandbox Root</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.sandbox_root}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="/tmp/at-sandbox"
                /></label>
                <div class="mt-0.5 text-[10px] text-gray-400">All commands run inside this directory</div>
              </div>
            {/if}

            {#if selectedNodeType === 'email'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">SMTP Config</span>
                <select
                  bind:value={selectedNodeData.config_id}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select config</option>
                  {#each nodeConfigs as nc}
                    <option value={nc.id}>{nc.name}</option>
                  {/each}
                </select></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Configure SMTP servers in Node Configs</div>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">To (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.to}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder={'user@example.com, \x7B\x7B.email\x7D\x7D'}
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">CC (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.cc}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="cc@example.com"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">BCC (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.bcc}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="bcc@example.com"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Subject (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.subject}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder={'Alert: \x7B\x7B.title\x7D\x7D'}
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Body (Go template)</span>
                <textarea
                  bind:value={selectedNodeData.body}
                  rows={4}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'Hello \x7B\x7B.name\x7D\x7D,\n\nYour report is ready.'}
                ></textarea></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Content Type</span>
                <select
                  bind:value={selectedNodeData.content_type}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="text/plain">text/plain</option>
                  <option value="text/html">text/html</option>
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">From Override (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.from}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="(uses config default)"
                /></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Reply-To (Go template)</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.reply_to}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="reply@example.com"
                /></label>
              </div>
            {/if}

            {#if selectedNodeType === 'log'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Level</span>
                <select
                  bind:value={selectedNodeData.level}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="info">info</option>
                  <option value="warn">warn</option>
                  <option value="error">error</option>
                  <option value="debug">debug</option>
                </select></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Message (Go template)</span>
                <textarea
                  bind:value={selectedNodeData.message}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'Processing \x7B\x7B.name\x7D\x7D'}
                ></textarea></label>
                <div class="mt-0.5 text-[10px] text-gray-400">Supports Go templates. Data passes through unchanged.</div>
              </div>
            {/if}

            {#if selectedNodeType === 'group'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Color</span>
                <input
                  type="color"
                  bind:value={selectedNodeData.color}
                  class="mt-0.5 w-full h-8 border border-gray-300 rounded cursor-pointer"
                /></label>
              </div>
            {/if}

            {#if selectedNodeType === 'sticky_note'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Text (Markdown)</span>
                <textarea
                  bind:value={selectedNodeData.text}
                  rows={5}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="**Bold**, _italic_, `code`, [link](url)"
                ></textarea></label>
              </div>
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Color</span>
                <input
                  type="color"
                  bind:value={selectedNodeData.color}
                  class="mt-0.5 w-full h-8 border border-gray-300 rounded cursor-pointer"
                /></label>
              </div>
            {/if}

          </div>
          <div class="px-3 py-2 border-t border-gray-200 shrink-0">
            <button
              onclick={applyNodeData}
              class="w-full px-2 py-1 text-xs text-white bg-gray-900 rounded hover:bg-gray-800 transition-colors"
            >
              Apply
            </button>
          </div>
        </div>
      {/if}

      <!-- Run Panel -->
      {#if showRunPanel}
        <div class="w-72 bg-white border-l border-gray-200 shrink-0 overflow-y-auto">
          <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200">
            <span class="text-xs font-medium text-gray-700">Run Workflow</span>
            <button onclick={() => { showRunPanel = false; }} class="text-gray-400 hover:text-gray-600">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3">
            <div>
              <div class="flex items-center justify-between mb-0.5">
                <label for="run-inputs" class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</label>
                <div class="flex rounded overflow-hidden border border-gray-300">
                  <button
                    onclick={() => { runInputMode = 'text'; }}
                    class="px-1.5 py-0.5 text-[10px] font-medium transition-colors {runInputMode === 'text' ? 'bg-gray-700 text-white' : 'bg-white text-gray-500 hover:bg-gray-100'}"
                  >Text</button>
                  <button
                    onclick={() => { runInputMode = 'json'; }}
                    class="px-1.5 py-0.5 text-[10px] font-medium transition-colors border-l border-gray-300 {runInputMode === 'json' ? 'bg-gray-700 text-white' : 'bg-white text-gray-500 hover:bg-gray-100'}"
                  >JSON</button>
                </div>
              </div>
              <textarea
                id="run-inputs"
                bind:value={runInputsJson}
                rows={5}
                class="w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y {runInputMode === 'json' ? 'font-mono' : ''}"
                placeholder={runInputMode === 'text' ? 'Type your input text...' : '{"key": "value"}'}
              ></textarea>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Mode</span>
              <div class="flex rounded overflow-hidden border border-gray-300">
                <button
                  onclick={() => { runSync = true; }}
                  class="px-1.5 py-0.5 text-[10px] font-medium transition-colors {runSync ? 'bg-gray-700 text-white' : 'bg-white text-gray-500 hover:bg-gray-100'}"
                >Sync</button>
                <button
                  onclick={() => { runSync = false; }}
                  class="px-1.5 py-0.5 text-[10px] font-medium transition-colors border-l border-gray-300 {!runSync ? 'bg-gray-700 text-white' : 'bg-white text-gray-500 hover:bg-gray-100'}"
                >Async</button>
              </div>
            </div>
            {#if versions.length > 0}
              <div>
                <label for="run-version-select" class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Run Version</label>
                <select
                  id="run-version-select"
                  bind:value={runVersion}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value={undefined}>Latest (save first)</option>
                  {#each versions as v}
                    <option value={v.version}>v{v.version}{workflow.active_version === v.version ? ' (active)' : ''}</option>
                  {/each}
                </select>
                <div class="mt-0.5 text-[10px] text-gray-400">
                  {runVersion !== undefined ? `Run version ${runVersion}` : 'Saves then runs latest graph'}
                </div>
              </div>
            {/if}
            <button
              onclick={handleRun}
              disabled={running}
              class="w-full flex items-center justify-center gap-1 px-2 py-1.5 text-xs text-white bg-green-600 rounded hover:bg-green-700 disabled:opacity-50 transition-colors"
            >
              <Play size={12} />
              {running ? 'Running...' : 'Execute'}
            </button>

            {#if runError}
              <div class="p-2 bg-red-50 border border-red-200 rounded text-xs text-red-700">
                {runError}
              </div>
            {/if}

            {#if runResult}
              <div>
                <div class="text-[10px] font-medium text-gray-500 uppercase tracking-wider mb-1">Result</div>
                <pre class="p-2 bg-gray-50 border border-gray-200 rounded text-[11px] font-mono text-gray-700 overflow-x-auto whitespace-pre-wrap max-h-60 overflow-y-auto">{JSON.stringify(runResult, null, 2)}</pre>
              </div>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  :global(.kaykay-canvas) {
    width: 100%;
    height: 100%;
  }
</style>
