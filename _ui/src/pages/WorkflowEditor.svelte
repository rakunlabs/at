<script lang="ts">
  import { tick, untrack, onDestroy } from 'svelte';
  import { push } from 'svelte-spa-router';
  import { storeNavbar, storeTheme } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listWorkflows, getWorkflow, updateWorkflow, runWorkflowStream, listWorkflowVersions, getWorkflowVersion, setActiveVersion, type Workflow, type WorkflowVersion, type WorkflowNode, type WorkflowEdge } from '@/lib/api/workflows';
  import { workflowRun, clearRunState, handleStreamEvent, getNodeStatuses } from '@/lib/store/workflow-run.svelte';
  import { cloneWorkflowNodeData, createDefaultWorkflowNodeData, getWorkflowNodeDimensions, getWorkflowNodeDefinition, isWorkflowNodeType, validateWorkflowNodeDefinitions, type WorkflowNodeType } from '@/lib/workflow/node-definitions';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listNodeConfigs, type NodeConfig } from '@/lib/api/node-configs';
  import { Canvas, Controls, Minimap, GroupNode, type FlowNode, type FlowEdge, type FlowState, type NodeTypes } from 'kaykay';
  import { ArrowLeft, Save, Play, Plus, X, Bot, History, Check, Clock, Undo2, Redo2 } from 'lucide-svelte';
  import ChatPanel from '@/lib/components/workflow/ChatPanel.svelte';
  import NodePalette from '@/lib/components/workflow/NodePalette.svelte';
  import NodeDataView from '@/lib/components/workflow/NodeDataView.svelte';
  import InputMapper from '@/lib/components/workflow/InputMapper.svelte';
  import NodeExecutionSettings from '@/lib/components/workflow/NodeExecutionSettings.svelte';
  import DataOperationNode from '@/lib/components/workflow/DataOperationNode.svelte';
  import DataOperationProps from '@/lib/components/workflow/DataOperationProps.svelte';
  import WaitNode from '@/lib/components/workflow/WaitNode.svelte';
  import WaitProps from '@/lib/components/workflow/WaitProps.svelte';
  import SavedWorkflowRuns from '@/lib/components/workflow/SavedWorkflowRuns.svelte';
  import { switchOutputPorts } from '@/lib/workflow/data-operations';
  import { canvasInputHandle, storedInputHandle } from '@/lib/workflow/ports';
  import { findNodePlacement } from '@/lib/workflow/node-placement';
  import { buildTestRunOptions, pinNodeOutput, pinUnavailableReason, type PinnedNode } from '@/lib/workflow/test-runs';
  import '@/style/workflow.css';

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
  import AgentConfigNode from '@/lib/components/workflow/AgentConfigNode.svelte';
  import MCPConfigNode from '@/lib/components/workflow/MCPConfigNode.svelte';
  import EmailNode from '@/lib/components/workflow/EmailNode.svelte';
  import LogNode from '@/lib/components/workflow/LogNode.svelte';
  import WorkflowCallNode from '@/lib/components/workflow/WorkflowCallNode.svelte';
  import MarkdownStickyNote from '@/lib/components/workflow/MarkdownStickyNote.svelte';
  import ImageGenerateNode from '@/lib/components/workflow/ImageGenerateNode.svelte';
  import VisionAnalyzeNode from '@/lib/components/workflow/VisionAnalyzeNode.svelte';
  import AudioGenerateNode from '@/lib/components/workflow/AudioGenerateNode.svelte';
  import AudioTranscribeNode from '@/lib/components/workflow/AudioTranscribeNode.svelte';
  import EmbeddingNode from '@/lib/components/workflow/EmbeddingNode.svelte';

  // ─── Property Panel Components ───
  import InputProps from '@/lib/components/workflow/InputProps.svelte';
  import OutputProps from '@/lib/components/workflow/OutputProps.svelte';
  import LLMCallProps from '@/lib/components/workflow/LLMCallProps.svelte';
  import AgentCallProps from '@/lib/components/workflow/AgentCallProps.svelte';
  import TemplateProps from '@/lib/components/workflow/TemplateProps.svelte';
  import HttpTriggerProps from '@/lib/components/workflow/HttpTriggerProps.svelte';
  import CronTriggerProps from '@/lib/components/workflow/CronTriggerProps.svelte';
  import HttpRequestProps from '@/lib/components/workflow/HttpRequestProps.svelte';
  import ConditionalProps from '@/lib/components/workflow/ConditionalProps.svelte';
  import LoopProps from '@/lib/components/workflow/LoopProps.svelte';
  import ScriptProps from '@/lib/components/workflow/ScriptProps.svelte';
  import ExecProps from '@/lib/components/workflow/ExecProps.svelte';
  import SkillConfigProps from '@/lib/components/workflow/SkillConfigProps.svelte';
  import AgentConfigProps from '@/lib/components/workflow/AgentConfigProps.svelte';
  import MCPConfigProps from '@/lib/components/workflow/MCPConfigProps.svelte';
  import EmailProps from '@/lib/components/workflow/EmailProps.svelte';
  import LogProps from '@/lib/components/workflow/LogProps.svelte';
  import WorkflowCallProps from '@/lib/components/workflow/WorkflowCallProps.svelte';
  import GroupProps from '@/lib/components/workflow/GroupProps.svelte';
  import StickyNoteProps from '@/lib/components/workflow/StickyNoteProps.svelte';
  import ImageGenerateProps from '@/lib/components/workflow/ImageGenerateProps.svelte';
  import VisionAnalyzeProps from '@/lib/components/workflow/VisionAnalyzeProps.svelte';
  import AudioGenerateProps from '@/lib/components/workflow/AudioGenerateProps.svelte';
  import AudioTranscribeProps from '@/lib/components/workflow/AudioTranscribeProps.svelte';
  import EmbeddingProps from '@/lib/components/workflow/EmbeddingProps.svelte';

  // ─── Props Component Map ───
  const propsComponents: Record<string, any> = {
    wait: WaitProps,
    edit_fields: DataOperationProps,
    filter: DataOperationProps,
    switch: DataOperationProps,
    merge: DataOperationProps,
    aggregate: DataOperationProps,
    input: InputProps,
    output: OutputProps,
    llm_call: LLMCallProps,
    agent_call: AgentCallProps,
    template: TemplateProps,
    http_trigger: HttpTriggerProps,
    cron_trigger: CronTriggerProps,
    http_request: HttpRequestProps,
    conditional: ConditionalProps,
    loop: LoopProps,
    script: ScriptProps,
    exec: ExecProps,
    skill_config: SkillConfigProps,
    agent_config: AgentConfigProps,
    mcp_config: MCPConfigProps,
    email: EmailProps,
    log: LogProps,
    workflow_call: WorkflowCallProps,
    group: GroupProps,
    sticky_note: StickyNoteProps,
    image_generate: ImageGenerateProps,
    vision_analyze: VisionAnalyzeProps,
    audio_generate: AudioGenerateProps,
    audio_transcribe: AudioTranscribeProps,
    embedding: EmbeddingProps,
  } satisfies Record<WorkflowNodeType, any>;

  // ─── Props ───
  let { params = { id: '' } }: { params?: { id: string } } = $props();

  storeNavbar.title = 'Workflow Editor';

  // ─── Node Types ───
  const nodeTypes: NodeTypes = {
    wait: WaitNode,
    edit_fields: DataOperationNode,
    filter: DataOperationNode,
    switch: DataOperationNode,
    merge: DataOperationNode,
    aggregate: DataOperationNode,
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
    agent_config: AgentConfigNode,
    mcp_config: MCPConfigNode,
    email: EmailNode,
    log: LogNode,
    workflow_call: WorkflowCallNode,
    group: GroupNode,
    sticky_note: MarkdownStickyNote,
    image_generate: ImageGenerateNode,
    vision_analyze: VisionAnalyzeNode,
    audio_generate: AudioGenerateNode,
    audio_transcribe: AudioTranscribeNode,
    embedding: EmbeddingNode,
  } satisfies Record<WorkflowNodeType, NodeTypes[string]>;

  const definitionErrors = validateWorkflowNodeDefinitions(Object.keys(nodeTypes), Object.keys(propsComponents));
  if (definitionErrors.length > 0) throw new Error(definitionErrors.join('\n'));

  // ─── State ───
  let workflow = $state<Workflow | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let running = $state(false);
  let providers = $state<ProviderRecord[]>([]);
  let allWorkflows = $state<Workflow[]>([]);
  let skills = $state<Skill[]>([]);
  let nodeConfigs = $state<NodeConfig[]>([]);
  let runResult = $state<any>(null);
  let runError = $state<string | null>(null);

  // Property editor
  let selectedNodeId = $state<string | null>(null);
  let selectedNodeData = $state<Record<string, any>>({});
  let selectedNodeType = $state<string>('');
  let selectedNodeOriginalData = $state<Record<string, any>>({});
  let inspectorTab = $state<'parameters' | 'input' | 'output' | 'settings'>('parameters');

  // Run inputs
  let showRunPanel = $state(false);
  let showSavedRuns = $state(false);
  let savedRunsRefresh = $state(0);
  let showChatPanel = $state(false);
  let runInputsJson = $state('');
  let runInputMode = $state<'text' | 'json'>('text');
  let runTargetNodeId = $state<string | null>(null);
  let pinnedNodes = $state<Record<string, PinnedNode>>({});
  let usePinnedData = $state(true);
  let runEntryNodeId = $state<string>(''); // '' means all input nodes
  let runFormValues = $state<Record<string, any>>({});
  let runUseForm = $state(false);

  interface InputFieldDef {
    name: string;
    type: 'string' | 'number' | 'boolean' | 'select' | 'textarea';
    description?: string;
    default?: any;
    options?: string[];
  }

  // Collect input nodes from the canvas for the entry-node selector.
  function getInputNodes(): { id: string; label: string; fields?: InputFieldDef[] }[] {
    if (!canvasRef) return [];
    const flow = canvasRef.getFlow();
    return flow.nodes
      .filter((n: any) => n.type === 'input')
      .map((n: any) => ({
        id: n.id,
        label: n.data?.label || 'Input',
        fields: Array.isArray(n.data?.fields) ? n.data.fields : undefined,
      }));
  }

  function syncEditorFormFromEntry() {
    const nodes = getInputNodes();
    const node = nodes.find(n => n.id === runEntryNodeId);
    if (node?.fields && node.fields.length > 0) {
      runUseForm = true;
      const values: Record<string, any> = {};
      for (const f of node.fields) {
        values[f.name] = f.default ?? (f.type === 'number' ? 0 : f.type === 'boolean' ? false : '');
      }
      runFormValues = values;
    } else {
      runUseForm = false;
    }
  }

  // Versioning
  let versions = $state<WorkflowVersion[]>([]);
  let showVersionPanel = $state(false);
  let viewingVersion = $state<number | null>(null); // non-null when viewing a historical version
  let runVersion = $state<number | undefined>(undefined); // version override for run panel
  let loadingVersions = $state(false);
  let settingActive = $state(false);

  // Canvas ref
  let canvasRef: { getFlow: () => FlowState; clientToCanvas: (x: number, y: number) => { x: number; y: number } | null; getContainer: () => HTMLDivElement | null } | undefined = $state();
  let showPalette = $state(false);
  let pendingConnection = $state<{ nodeId: string; handleId: string } | null>(null);
  let flow = $derived(canvasRef?.getFlow());
  let outputHandles = $derived(selectedNodeId && flow ? Object.entries(flow.handle_registry)
    .filter(([key, handle]) => key === `${selectedNodeId}:${handle.id}` && handle.type === 'output')
    .map(([, handle]) => handle) : []);
  let inputHandles = $derived(selectedNodeId && flow ? Object.entries(flow.handle_registry)
    .filter(([key, handle]) => key === `${selectedNodeId}:${handle.id}` && handle.type === 'input')
    .map(([, handle]) => ({ ...handle, id: storedInputHandle(selectedNodeType, handle.id) })) : []);

  $effect(() => {
    const currentFlow = flow;
    const locked = viewingVersion != null;
    if (currentFlow) untrack(() => currentFlow.setLocked(locked));
  });

  // Stream run state
  let streamAbort: AbortController | null = $state(null);
  let runGeneration = 0;
  let nodeStatuses = $derived(getNodeStatuses());
  clearRunState();
  onDestroy(() => { runGeneration++; streamAbort?.abort(); });

  // ─── Helpers ───

  function toFlowNodes(nodes: WorkflowNode[]): FlowNode[] {
    return nodes.map((n) => ({
      id: n.id,
      type: n.type,
      position: { x: n.position.x, y: n.position.y },
      data: { ...(n.data || {}), ...(n.node_number != null && { node_number: n.node_number }) },
      ...(n.width != null && { width: n.width }),
      ...(n.height != null && { height: n.height }),
      ...(n.parent_id && { parent_id: n.parent_id }),
      ...(n.z_index != null && { z_index: n.z_index }),
    }));
  }

  function toFlowEdges(edges: WorkflowEdge[], nodes = workflow?.graph.nodes ?? []): FlowEdge[] {
    return edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      source_handle: e.source_handle,
      target_handle: canvasInputHandle(nodes.find(node => node.id === e.target)?.type ?? '', e.target_handle),
    }));
  }

  function flowToGraph(flow: FlowState): { nodes: WorkflowNode[]; edges: WorkflowEdge[] } {
    const json = flow.toJSON();
    const nodes: WorkflowNode[] = json.nodes.map((n: FlowNode) => {
      const { node_number, ...data } = (n.data || {}) as Record<string, any>;
      return {
        id: n.id,
        type: n.type,
        position: { x: n.position.x, y: n.position.y },
        data,
        ...(n.width != null && { width: n.width }),
        ...(n.height != null && { height: n.height }),
        ...(n.parent_id && { parent_id: n.parent_id }),
        ...(n.z_index != null && { z_index: n.z_index }),
        ...(node_number != null && { node_number }),
      };
    });
    const edges: WorkflowEdge[] = json.edges.map((e: FlowEdge) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      source_handle: e.source_handle,
      target_handle: storedInputHandle(flow.getNode(e.target)?.type ?? '', e.target_handle),
    }));
    return { nodes, edges };
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
      // Initialize nodeCounter from the max existing node_number so new nodes
      // get the next sequential number without collisions.
      const maxNum = workflow.graph.nodes.reduce(
        (max, n) => Math.max(max, n.node_number ?? 0),
        0,
      );
      nodeCounter = maxNum;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load workflow', 'alert');
      push('/workflows');
    } finally {
      loading = false;
    }
  }

  async function loadProviders() {
    try {
      const res = await listProviders();
      providers = res.data || [];
    } catch {
      // Non-critical
    }
  }

  async function loadAllWorkflows() {
    try {
      const res = await listWorkflows();
      allWorkflows = res.data || [];
    } catch {
      // Non-critical
    }
  }

  async function loadSkills() {
    try {
      const res = await listSkills();
      skills = res.data || [];
    } catch {
      // Non-critical
    }
  }

  async function loadNodeConfigs() {
    try {
      const res = await listNodeConfigs({ type: 'email' });
      nodeConfigs = res.data || [];
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
      // Load atomically: addEdge requires mounted handles and can otherwise
      // silently drop edges while switching graphs.
      flow.fromJSON({ nodes: toFlowNodes(v.graph.nodes), edges: toFlowEdges(v.graph.edges, v.graph.nodes) });
      closePropertyEditor();
      showPalette = false;
      showChatPanel = false;
      pendingConnection = null;
      clearRunState();
      pinnedNodes = {};
      runTargetNodeId = null;
      viewingVersion = version;
      addToast(`Loaded version ${version}`, 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load version', 'alert');
    }
  }

  function loadCurrentToCanvas() {
    if (!workflow || !canvasRef) return;
    const flow = canvasRef.getFlow();
    flow.fromJSON({ nodes: toFlowNodes(workflow.graph.nodes), edges: toFlowEdges(workflow.graph.edges) });
    closePropertyEditor();
    viewingVersion = null;
    clearRunState();
    pinnedNodes = {};
    runTargetNodeId = null;
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
    if (!workflow || !canvasRef || viewingVersion != null || saving) return;
    saving = true;
    try {
      if (hasNodeEdits() && !applyNodeData()) return;
      const flow = canvasRef.getFlow();
      const graph = flowToGraph(flow);
      workflow = await updateWorkflow(workflow.id, {
        name: workflow.name,
        description: workflow.description,
        graph,
      });
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
    if (!workflow || running) return;
    const generation = ++runGeneration;
    running = true;
    runResult = null;
    runError = null;
    clearRunState();

    try {
      const target = runTargetNodeId;
      const test = buildTestRunOptions(target, pinnedNodes, usePinnedData);
      // Save first
      if (canvasRef && viewingVersion == null) {
        if (hasNodeEdits() && !applyNodeData()) throw new Error('Node changes could not be applied. Check the failure output connections.');
        const flow = canvasRef.getFlow();
        const graph = flowToGraph(flow);
        workflow = await updateWorkflow(workflow.id, {
          name: workflow.name,
          description: workflow.description,
          graph,
        });
      }
      if (generation !== runGeneration) return;
      const inputs = runUseForm
        ? { ...runFormValues }
        : runInputMode === 'json'
          ? JSON.parse(runInputsJson || '{}')
          : { text: runInputsJson };
      const entryNodeIds = runEntryNodeId ? [runEntryNodeId] : undefined;

      // Use streaming run for real-time per-node updates.
      if (streamAbort) {
        streamAbort.abort();
      }
      streamAbort = runWorkflowStream(
        workflow.id,
        inputs,
        (event) => {
          if (generation !== runGeneration) return;
          if (event.event_type === 'durable_started') {
            showSavedRuns = true;
            showRunPanel = false;
            savedRunsRefresh++;
            addToast('Workflow queued. Follow its progress in Saved runs.', 'info');
            return;
          }
          handleStreamEvent(event);
          // Also update the local runResult/runError for the panel display.
          if (event.event_type === 'done') {
            runResult = event.outputs ?? event.data ?? null;
          } else if (event.event_type === 'error' && !event.node_id) {
            runError = event.error || 'Execution failed';
          }
        },
        () => {
          if (generation !== runGeneration) return;
          running = false;
          streamAbort = null;
        },
        runVersion ?? viewingVersion ?? undefined,
        entryNodeIds,
        test,
      );
    } catch (e: any) {
      if (generation !== runGeneration) return;
      if (e instanceof SyntaxError) {
        runError = 'Invalid JSON in inputs';
      } else {
        runError = e?.response?.data?.message || e?.message || 'Execution failed';
      }
      running = false;
    }
  }

  function stopRun() {
    runGeneration++;
    streamAbort?.abort();
    streamAbort = null;
    running = false;
    runError = 'Run stopped';
    workflowRun.status = 'error';
    workflowRun.error = runError;
    for (const state of Object.values(workflowRun.nodeRunStates)) {
      if (state.status === 'running') {
        state.status = 'error';
        state.error = 'Run stopped';
        state.retry_delay_ms = undefined;
      }
    }
  }

  function prepareStepRun() {
    if (!selectedNodeId || running) return;
    runTargetNodeId = selectedNodeId;
    runVersion = viewingVersion ?? undefined;
    showRunPanel = true;
  }

  function pinSelectedOutput() {
    if (!selectedNodeId || running) return;
    try {
      if (Object.keys(pinnedNodes).length >= 32 && !pinnedNodes[selectedNodeId]) throw new Error('A test run supports up to 32 pins. Unpin another step first.');
      pinnedNodes = { ...pinnedNodes, [selectedNodeId]: pinNodeOutput(workflowRun.nodeRunStates[selectedNodeId]) };
      addToast('Output pinned for test runs in this editor session', 'info');
    } catch (error: any) { addToast(error.message || 'Cannot pin this output', 'alert'); }
  }

  function unpinNode(id: string) {
    const next = { ...pinnedNodes };
    delete next[id];
    pinnedNodes = next;
  }

  // ─── Add Node ───

  let nodeCounter = $state(0);

  async function addNode(type: string, position?: { x: number; y: number }) {
    if (!canvasRef || viewingVersion != null || !isWorkflowNodeType(type)) return;
    const flow = canvasRef.getFlow();
    do { nodeCounter++; } while (flow.getNode(`${type}_${nodeCounter}`));
    const defaultData: Record<string, any> = {
      ...createDefaultWorkflowNodeData(type),
      node_number: nodeCounter,
    };
    const source = pendingConnection ? flow.getNode(pendingConnection.nodeId) : undefined;
    const sourcePosition = source ? flow.getAbsolutePosition(source.id) : undefined;
    const center = flow.screenToCanvas({ x: flow.canvas_width / 2, y: flow.canvas_height / 2 });
    const preferred = source && sourcePosition
      ? { x: sourcePosition.x + source.computed_width + 100, y: sourcePosition.y }
      : { x: center.x - 130, y: center.y - 60 };
    const pos = position ?? findNodePlacement(preferred, getWorkflowNodeDimensions(type) ?? { width: 256, height: 140 },
      flow.nodes.filter(node => node.type !== 'group').map(node => ({
        ...flow.getAbsolutePosition(node.id), width: node.computed_width || 256, height: node.computed_height || 140,
      })));
    const nodeOpts: Record<string, any> = {
      id: `${type}_${nodeCounter}`,
      type,
      position: pos,
      data: defaultData,
    };
    const dimensions = getWorkflowNodeDimensions(type);
    if (dimensions) {
      nodeOpts.width = dimensions.width;
      nodeOpts.height = dimensions.height;
    }
    flow.addNode(nodeOpts as FlowNode);
    const connection = pendingConnection;
    pendingConnection = null;
    showPalette = false;
    await tick();
    // Kaykay registers handles in its first layout frame, after Svelte mounts.
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
    if (flow.locked || !flow.getNode(nodeOpts.id)) return;
    if (connection && source) {
      const targets = [...(flow.getNode(nodeOpts.id)?.handles.values() ?? [])].filter(handle =>
        handle.type === 'input' && flow.canConnect(source.id, connection.handleId, nodeOpts.id, handle.id));
      if (targets.length === 1) {
        flow.addEdge({ id: `edge_${nodeOpts.id}_${Date.now()}`, source: source.id, source_handle: connection.handleId, target: nodeOpts.id, target_handle: targets[0].id });
      } else {
        addToast(targets.length ? 'Step added. Connect the input you want to use.' : 'Step added. No compatible input for this output.', 'info');
      }
    }
    flow.selectNode(nodeOpts.id);
    selectNodeForEditor(nodeOpts.id);
    if (!position) {
      await tick();
      const rect = canvasRef?.getContainer()?.getBoundingClientRect();
      if (rect) flow.setViewport({
        x: rect.width / 2 - (pos.x + 128) * flow.viewport.zoom,
        y: rect.height / 2 - (pos.y + 60) * flow.viewport.zoom,
        zoom: flow.viewport.zoom,
      });
    }
  }

  // ─── Drag & Drop ───

  let draggingOver = $state(false);

  function handleDragStart(e: DragEvent, type: string) {
    if (!e.dataTransfer || viewingVersion != null) return;
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

    const canvasPos = canvasRef.clientToCanvas(e.clientX, e.clientY);
    if (canvasPos) addNode(type, canvasPos);
  }

  // ─── Palette Collapse ───

  function openPalette() {
    pendingConnection = null;
    showPalette = true;
  }

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
      inspectorTab = 'parameters';
      selectedNodeType = node.type;
      const defaults = createDefaultWorkflowNodeData(node.type);
      const hydratedData = { ...defaults, ...node.data };
      selectedNodeData = cloneWorkflowNodeData(hydratedData);
      selectedNodeOriginalData = cloneWorkflowNodeData(hydratedData);
    }
  }

  function onNodeClick(nodeId: string) {
    showSavedRuns = false;
    if (nodeId !== selectedNodeId) selectNodeForEditor(nodeId);
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

  function applyNodeData(): boolean {
    if (!canvasRef || !selectedNodeId || viewingVersion != null) return false;
    const flow = canvasRef.getFlow();
    if (selectedNodeData.execution?.on_error !== 'error_output' && flow.edges.some(edge => edge.source === selectedNodeId && edge.source_handle === '__error')) {
      addToast('Remove failure-output connections before changing the error policy.', 'alert');
      return false;
    }
    if (selectedNodeType === 'switch') {
      const ports = new Set([...switchOutputPorts(selectedNodeData).map(port => port.id), '__error']);
      if (flow.edges.some(edge => edge.source === selectedNodeId && !ports.has(edge.source_handle))) {
        addToast('Remove connections to deleted Switch cases before applying. Renaming or reordering cases keeps their connections.', 'alert');
        return false;
      }
    }

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

    flow.updateNodeData(selectedNodeId, cloneWorkflowNodeData(selectedNodeData));
    selectedNodeOriginalData = cloneWorkflowNodeData(selectedNodeData);
    addToast('Node updated', 'info');
    return true;
  }

  // ─── Toolbar button styles (single variant set, uniform geometry) ───

  const toolbarBtn = 'inline-flex h-8 shrink-0 items-center gap-1.5 border px-2.5 text-xs leading-none disabled:opacity-50';
  const toolbarBtnDefault = `${toolbarBtn} border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary dark:hover:bg-dark-elevated`;
  const toolbarBtnActive = `${toolbarBtn} border-gray-900 bg-gray-900 text-white hover:bg-gray-800 dark:border-accent dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover`;
  const toolbarBtnPrimary = `${toolbarBtn} border-green-600 bg-green-600 text-white hover:bg-green-700`;

  // ─── Init ───

  loadWorkflow().then(() => loadVersions());
  loadProviders();
  loadAllWorkflows();
  loadSkills();
  loadNodeConfigs();

</script>

<svelte:window onkeydown={event => {
  if (event.key === 'Escape' && showPalette) { showPalette = false; pendingConnection = null; }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') { event.preventDefault(); handleSave(); }
}} />

<svelte:head>
  <title>AT | {workflow?.name || 'Workflow'}</title>
</svelte:head>

{#if loading}
  <div class="p-8 text-center text-sm text-gray-500 dark:text-dark-text-muted">Loading workflow...</div>
{:else if workflow}
  <div class="workflow-workbench flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center justify-between gap-2 px-3 py-2 bg-white dark:bg-dark-surface border-b border-gray-200 dark:border-dark-border shrink-0">
      <div class="flex items-center gap-3">
        <button
          onclick={() => push('/workflows')}
          class="flex items-center gap-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text "
        >
          <ArrowLeft size={14} />
          Back
        </button>
        <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
        <div class="flex flex-col">
          <div class="flex items-center gap-2">
            <input
              type="text"
              bind:value={workflow.name}
              class="text-sm font-medium text-gray-900 dark:text-dark-text bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
              placeholder="Workflow name"
            />
            <div class="flex items-center gap-1 group relative">
              <button type="button" class="text-[10px] font-mono text-gray-400 dark:text-dark-text-faint cursor-pointer hover:text-gray-600 dark:hover:text-dark-text-secondary" title="Click to copy ID" onclick={() => { navigator.clipboard.writeText(workflow?.id || ''); addToast('ID copied', 'info'); }}>
                 {workflow.id}
              </button>
            </div>
          </div>
            <input
            type="text"
            bind:value={workflow.description}
            class="text-[10px] text-gray-400 dark:text-dark-text-faint bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
            placeholder="Add description..."
          />
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if workflow.active_version != null}
          <span class="inline-flex h-8 shrink-0 items-center gap-1 border px-2 text-[11px] font-medium leading-none {viewingVersion != null ? 'text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 border-amber-200 dark:border-amber-800' : 'text-gray-500 dark:text-dark-text-muted bg-gray-100 dark:bg-dark-elevated border-gray-200 dark:border-dark-border'}">
            {#if viewingVersion != null}
              v{viewingVersion}
              {#if viewingVersion === workflow.active_version}
                <Check size={12} class="text-green-600" />
              {/if}
            {:else}
              v{workflow.active_version}
              <Check size={12} class="text-green-600" />
            {/if}
          </span>
        {/if}
        <button onclick={openPalette} disabled={viewingVersion != null} class={toolbarBtnDefault}>
          <Plus size={14} />
          Add step
        </button>
        <button
          onclick={() => { showSavedRuns = !showSavedRuns; if (showSavedRuns) { showRunPanel = false; showVersionPanel = false; showChatPanel = false; } }}
          class={showSavedRuns ? toolbarBtnActive : toolbarBtnDefault}
        >
          <Clock size={14} />
          Saved runs
        </button>
        <button
          onclick={() => { showSavedRuns = false; showVersionPanel = !showVersionPanel; if (showVersionPanel) loadVersions(); }}
          class={showVersionPanel ? toolbarBtnActive : toolbarBtnDefault}
        >
          <History size={14} />
          Versions
        </button>
        <button
          onclick={() => { showSavedRuns = false; showChatPanel = !showChatPanel; }}
          disabled={viewingVersion != null}
          class={showChatPanel ? toolbarBtnActive : toolbarBtnDefault}
        >
          <Bot size={14} />
          AI
        </button>
        <button onclick={handleSave} disabled={saving || viewingVersion != null} class={toolbarBtnDefault}>
          <Save size={14} />
          {saving ? 'Saving...' : 'Save'}
        </button>
        <button
          onclick={() => { showSavedRuns = false; runTargetNodeId = null; showRunPanel = !showRunPanel; }}
          class={toolbarBtnPrimary}
        >
          <Play size={14} />
          Run
        </button>
      </div>
    </div>

    <!-- Version viewing banner -->
    {#if viewingVersion != null}
      <div class="flex items-center justify-between px-3 py-1 bg-amber-50 dark:bg-amber-900/20 border-b border-amber-200 dark:border-amber-800 shrink-0">
        <span class="text-xs text-amber-700 dark:text-amber-400">
          Viewing version {viewingVersion}{viewingVersion === workflow.active_version ? ' (active)' : ''} — canvas is read-only until you return to latest
        </span>
        <button
          onclick={loadCurrentToCanvas}
          class="px-2 py-0.5 text-xs text-amber-700 dark:text-amber-400 bg-white dark:bg-dark-surface border border-amber-300 dark:border-amber-800 rounded hover:bg-amber-100 dark:hover:bg-amber-900/30 "
        >
          Back to latest
        </button>
      </div>
    {/if}

    <!-- Main area -->
    <div class="relative flex flex-1 min-h-0 overflow-hidden">
      <!-- Node Palette -->
      {#if showPalette}
        <div class="absolute inset-y-0 left-0 z-30 max-w-full lg:static">
          <NodePalette onadd={type => addNode(type)} ondragstart={handleDragStart} onclose={() => { showPalette = false; pendingConnection = null; }} disabled={viewingVersion != null} />
        </div>
      {/if}

      <!-- Canvas -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div
        class="isolate flex-1 min-w-0 relative bg-gray-50 dark:bg-dark-base {storeTheme.mode === 'dark' ? 'kaykay-dark' : ''} {draggingOver ? 'ring-2 ring-inset ring-blue-400 dark:ring-accent' : ''}"
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
          node_statuses={nodeStatuses}
          config={{ snap_to_grid: true, grid_size: 20, default_edge_type: 'bezier', prevent_cycles: true }}
          callbacks={{ on_node_click: onNodeClick, on_selection_change: onSelectionChange }}
        >
          {#snippet controls()}
            <Controls position="bottom-left" />
            <Minimap width={160} height={100} />
          {/snippet}

        </Canvas>
        {#if flow}
          <div class="canvas-actions absolute top-3 left-3 z-[1000] flex items-center gap-[2px] bg-white p-1 dark:bg-dark-surface" aria-label="Canvas actions">
            <button onclick={() => flow?.undo()} disabled={!flow.canUndo || viewingVersion != null} aria-label="Undo" title="Undo (Ctrl/Cmd+Z)" class="flex h-8 w-8 items-center justify-center text-gray-700 hover:bg-gray-100 disabled:opacity-40 dark:text-dark-text dark:hover:bg-dark-elevated"><Undo2 size={16} /></button>
            <button onclick={() => flow?.redo()} disabled={!flow.canRedo || viewingVersion != null} aria-label="Redo" title="Redo" class="flex h-8 w-8 items-center justify-center text-gray-700 hover:bg-gray-100 disabled:opacity-40 dark:text-dark-text dark:hover:bg-dark-elevated"><Redo2 size={16} /></button>
          </div>
          {#if flow.nodes.length === 0 && !showPalette}
            <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div class="pointer-events-auto max-w-xs border border-gray-200 bg-white p-5 dark:border-dark-border dark:bg-dark-surface">
                <h2 class="text-base font-semibold text-gray-900 dark:text-dark-text">Build your first step</h2>
                <p class="mt-2 text-sm text-gray-600 dark:text-dark-text-secondary">Start with Input, add an action, then connect an Output to return the result.</p>
                <button onclick={() => addNode('input')} disabled={viewingVersion != null} class="mt-4 flex items-center gap-2 bg-gray-900 px-3 py-2 text-sm text-white disabled:opacity-50 dark:bg-accent dark:text-gray-950"><Plus size={16} /> Add Input</button>
              </div>
            </div>
          {/if}
        {/if}
      </div>

      <!-- AI Chat Panel -->
      {#if showChatPanel && canvasRef}
        <ChatPanel onclose={() => { showChatPanel = false; }} flow={canvasRef.getFlow()} />
      {/if}

      <!-- Version History Panel -->
      {#if showVersionPanel}
        <div class="w-64 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Version History</span>
            <button onclick={() => { showVersionPanel = false; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="overflow-y-auto min-h-0 flex-1">
            {#if loadingVersions}
              <div class="p-3 text-xs text-gray-500 dark:text-dark-text-muted text-center">Loading...</div>
            {:else if versions.length === 0}
              <div class="p-3 text-xs text-gray-400 dark:text-dark-text-faint text-center">No versions yet. Save to create the first version.</div>
            {:else}
              <!-- Return to latest button when viewing old version -->
              {#if viewingVersion != null}
                <button
                  onclick={loadCurrentToCanvas}
                  class="w-full px-3 py-2 text-xs text-blue-600 dark:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-900/20 border-b border-gray-100 dark:border-dark-border text-left "
                >
                  Back to latest
                </button>
              {/if}
              {#each versions as v (v.id)}
                {@const isActive = workflow.active_version === v.version}
                {@const isViewing = viewingVersion === v.version}
                <div
                  class="px-3 py-2 border-b border-gray-100 dark:border-dark-border {isViewing ? 'bg-amber-50 dark:bg-amber-900/20' : 'hover:bg-gray-50 dark:hover:bg-dark-elevated'} "
                >
                  <div class="flex items-center justify-between mb-0.5">
                    <div class="flex items-center gap-1.5">
                      <span class="text-xs font-medium text-gray-800 dark:text-dark-text">v{v.version}</span>
                      {#if isActive}
                        <span class="flex items-center gap-0.5 px-1 py-0 text-[9px] font-medium text-green-700 dark:text-green-400 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 rounded">
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
                          class="px-1.5 py-0.5 text-[10px] text-gray-500 dark:text-dark-text-muted hover:text-green-700 dark:hover:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/30 rounded disabled:opacity-50"
                          title="Set as active version"
                        >
                          Set active
                        </button>
                      {/if}
                      {#if !isViewing}
                        <button
                          onclick={() => loadVersionToCanvas(v.version)}
                          class="px-1.5 py-0.5 text-[10px] text-gray-500 dark:text-dark-text-muted hover:text-blue-700 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-900/20 rounded "
                          title="Load this version into canvas"
                        >
                          Load
                        </button>
                      {/if}
                    </div>
                  </div>
                  <div class="flex items-center gap-1 text-[10px] text-gray-400 dark:text-dark-text-faint">
                    <Clock size={9} />
                    {new Date(v.created_at).toLocaleDateString()} {new Date(v.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    {#if v.created_by}
                      <span class="ml-1 text-gray-300 dark:text-dark-text-faint">|</span> <span class="ml-1">by {v.created_by}</span>
                    {/if}
                  </div>
                  {#if v.name && v.name !== workflow.name}
                    <div class="text-[10px] text-gray-500 dark:text-dark-text-muted mt-0.5 truncate" title={v.name}>{v.name}</div>
                  {/if}
                </div>
              {/each}
            {/if}
          </div>
        </div>
      {/if}

      <!-- Property Editor Panel -->
      {#if selectedNodeId && !noPropertyPanelTypes.has(selectedNodeType) && !showSavedRuns}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="absolute inset-y-0 right-0 z-20 w-80 max-w-full bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col outline-none xl:static xl:w-96"
          tabindex="-1"
          onmousedown={(e) => { e.stopPropagation(); e.currentTarget.focus(); }}
        >
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">{getWorkflowNodeDefinition(selectedNodeType)?.label ?? 'Properties'}</span>
              {#if hasNodeEdits()}
                <span class="text-[10px] font-medium leading-none text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded px-1.5 py-0.5">Unsaved</span>
              {/if}
            </div>
            <button onclick={closePropertyEditor} aria-label="Close node properties" class="p-2 text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text">
              <X size={14} />
            </button>
          </div>
          <nav aria-label="Node inspector sections" class="flex border-b border-gray-200 dark:border-dark-border">
            {#each ['parameters', 'input', 'output', 'settings'] as tab}
              <button onclick={() => inspectorTab = tab as typeof inspectorTab} aria-pressed={inspectorTab === tab} class="flex-1 border-b-2 px-2 py-2 text-xs font-medium {inspectorTab === tab ? 'border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-400' : 'border-transparent text-gray-600 hover:bg-gray-50 dark:text-dark-text-secondary dark:hover:bg-dark-elevated'}">{tab === 'parameters' ? 'Parameters' : tab === 'input' ? 'Input' : tab === 'output' ? 'Output' : 'Settings'}</button>
            {/each}
          </nav>
          <div class="border-b border-gray-200 px-3 py-2 dark:border-dark-border">
            <button onclick={prepareStepRun} disabled={running} class="flex items-center gap-2 border border-gray-300 px-3 py-2 text-xs text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text dark:hover:bg-dark-elevated"><Play size={14} /> Execute step</button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto min-h-0 flex-1">
            {#if inspectorTab === 'parameters'}
            {#if outputHandles.length && viewingVersion == null}
              <div class="border-b border-gray-200 pb-3 dark:border-dark-border">
                <p class="mb-2 text-xs text-gray-600 dark:text-dark-text-secondary">Add the next step from an output:</p>
                <div class="flex flex-wrap gap-2">
                  {#each outputHandles as handle (handle.id)}
                    <button onclick={() => { if (hasNodeEdits() && !applyNodeData()) return; pendingConnection = { nodeId: selectedNodeId!, handleId: handle.id }; showPalette = true; closePropertyEditor(); }} class="flex items-center gap-1 border border-gray-300 px-2 py-2 text-xs text-gray-700 hover:bg-gray-50 dark:border-dark-border-subtle dark:text-dark-text dark:hover:bg-dark-elevated"><Plus size={14} />{handle.label || handle.id}</button>
                  {/each}
                </div>
              </div>
            {/if}
            <!-- Common: Label (not shown for sticky notes which use 'text' instead) -->
            {#if selectedNodeType !== 'sticky_note'}
              <div>
                <label class="block">
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Label</span>
                <input
                  type="text"
                  bind:value={selectedNodeData.label}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text"
                /></label>
              </div>
            {/if}

            <!-- Type-specific fields -->
            {#if propsComponents[selectedNodeType]}
              {@const PropsComponent = propsComponents[selectedNodeType]}
              <PropsComponent
                data={selectedNodeData}
                nodeType={selectedNodeType}
                {providers}
                {skills}
                {nodeConfigs}
                {allWorkflows}
                {workflow}
              />
            {/if}

            {:else if inspectorTab === 'settings'}
              <NodeExecutionSettings data={selectedNodeData} nodeType={selectedNodeType} disabled={viewingVersion != null || running} />
            {:else}
              {@const nodeState = workflowRun.nodeRunStates[selectedNodeId]}
              <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Last run snapshot. Editing the workflow does not update this data.</p>
              {#if (nodeState?.invocations ?? 0) > 1}
                <p class="text-xs text-gray-600 dark:text-dark-text-secondary">{nodeState?.invocations} invocations — showing the latest-started invocation only.</p>
              {/if}
              {#if nodeState?.error}<p role="alert" class="break-words border border-red-300 bg-red-50 p-3 text-xs text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300">{nodeState.error}</p>{/if}
              {#if nodeState?.error_policy}<p class="text-xs text-amber-800 dark:text-amber-300">{nodeState.error_policy === 'error_output' ? 'Failure handled: sent to the failure output.' : 'Failure handled: skipped this branch; independent branches continue.'}</p>{/if}
              {#if nodeState?.attempt_history?.length}
                <details class="border border-gray-200 p-2 text-xs dark:border-dark-border" open={(nodeState.max_attempts ?? 1) > 1}>
                  <summary class="cursor-pointer text-gray-700 dark:text-dark-text-secondary">Attempts ({nodeState.attempt_history.length}/{nodeState.max_attempts ?? 1})</summary>
                  <ol class="mt-2 space-y-2">
                    {#each nodeState.attempt_history as attempt (attempt.attempt)}
                      <li class="break-words text-gray-700 dark:text-dark-text-secondary">Attempt {attempt.attempt}: {attempt.status}{attempt.duration_ms != null ? ` · ${attempt.duration_ms} ms` : ''}{#if attempt.error}<p class="mt-1 text-red-700 dark:text-red-400">{attempt.error}</p>{/if}</li>
                    {/each}
                  </ol>
                  {#if nodeState.retry_delay_ms != null}<p class="mt-2 text-gray-600 dark:text-dark-text-secondary">Waiting {nodeState.retry_delay_ms} ms before retry.</p>{/if}
                </details>
              {/if}
              {#if inspectorTab === 'input'}
                <InputMapper data={selectedNodeData} ports={inputHandles} state={nodeState} disabled={viewingVersion != null || running} />
              {:else}
                {#if nodeState}<p class="text-xs text-gray-600 dark:text-dark-text-secondary">Status: {nodeState.pinned ? 'Pinned output used (node not executed)' : nodeState.skipped ? 'Skipped — inactive branch' : nodeState.status}{nodeState.duration_ms != null ? ` · ${nodeState.duration_ms} ms` : ''}</p>{/if}
                {#if pinnedNodes[selectedNodeId]}
                  <div class="space-y-2 border border-blue-200 bg-blue-50 p-3 text-xs text-blue-900 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-200">
                    <p>Output pinned for this editor session. Production runs ignore pins.</p>
                    <details><summary class="cursor-pointer py-1">View pinned data</summary><pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(pinnedNodes[selectedNodeId].data, null, 2)}</pre></details>
                    <button onclick={() => unpinNode(selectedNodeId!)} disabled={running} class="border border-blue-300 px-3 py-1.5 disabled:opacity-50 dark:border-blue-700">Unpin output</button>
                  </div>
                {:else}
                  <button onclick={pinSelectedOutput} disabled={running || !!pinUnavailableReason(nodeState)} class="border border-gray-300 px-3 py-2 text-xs text-gray-700 disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text">Pin output for tests</button>
                  {#if pinUnavailableReason(nodeState)}<p class="text-xs text-gray-600 dark:text-dark-text-secondary">{pinUnavailableReason(nodeState)}</p>{/if}
                {/if}
                <NodeDataView value={nodeState?.data} omitted={nodeState?.data_omitted} />
              {/if}
            {/if}

          </div>
          <div class="px-3 py-2 border-t border-gray-200 dark:border-dark-border shrink-0">
            <button
              onclick={applyNodeData}
              disabled={viewingVersion != null || !hasNodeEdits()}
              class="w-full px-2 py-1 text-xs text-white bg-gray-900 dark:bg-accent rounded hover:bg-gray-800 dark:hover:bg-accent-hover "
            >
              Apply
            </button>
          </div>
        </div>
      {/if}

      <!-- Run Panel -->
      {#if showSavedRuns}<SavedWorkflowRuns workflowId={workflow.id} refreshKey={savedRunsRefresh} onclose={() => showSavedRuns = false} />{/if}
      {#if showRunPanel}
        <div class="absolute inset-y-0 right-0 z-30 w-80 max-w-full bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 overflow-y-auto xl:static">
          <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 dark:border-dark-border">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">{runTargetNodeId ? 'Test step' : 'Run Workflow'}</span>
            <button onclick={() => { showRunPanel = false; }} aria-label="Close run panel" class="p-2 text-gray-600 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3">
            {#if runTargetNodeId}
              <div class="space-y-2 border border-gray-200 p-3 text-xs text-gray-700 dark:border-dark-border dark:text-dark-text-secondary">
                <p class="font-semibold text-gray-900 dark:text-dark-text">{String(flow?.getNode(runTargetNodeId)?.data.label || runTargetNodeId)}</p>
                <p>Runs this step and its required upstream nodes. Later steps and unrelated branches will not run. The selected step executes even if its output is pinned.</p>
                <button onclick={() => runTargetNodeId = null} disabled={running} class="underline">Switch to full workflow</button>
              </div>
            {/if}
            {#if Object.keys(pinnedNodes).length}
              <div class="space-y-2 border border-blue-200 p-3 text-xs text-gray-700 dark:border-blue-900 dark:text-dark-text-secondary">
                <label class="flex items-start gap-2"><input type="checkbox" bind:checked={usePinnedData} disabled={running} /> Use pinned outputs (test mode)</label>
                <p>{Object.keys(pinnedNodes).length} pinned step(s). Upstream steps still run unless pinned too. Pins are checked against node configuration, upstream wiring and run inputs.</p>
                {#each Object.keys(pinnedNodes) as id (id)}
                  <div class="flex items-start justify-between gap-2"><span class="min-w-0 break-words">{String(flow?.getNode(id)?.data.label || id)}</span><button onclick={() => unpinNode(id)} disabled={running} class="shrink-0 text-blue-700 dark:text-blue-400">Unpin</button></div>
                {/each}
                <button onclick={() => pinnedNodes = {}} disabled={running} class="underline">Clear all pins</button>
              </div>
            {/if}
            <!-- Entry Point selector (always show if multiple) -->
            {#if getInputNodes().length > 1}
              <div>
                <label for="run-entry-select" class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Entry Point</label>
                <select
                  id="run-entry-select"
                  bind:value={runEntryNodeId}
                  onchange={() => syncEditorFormFromEntry()}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                >
                  <option value="">All input nodes</option>
                  {#each getInputNodes() as node}
                    <option value={node.id}>{node.label} ({node.id.slice(0, 6)})</option>
                  {/each}
                </select>
                <div class="mt-0.5 text-[10px] text-gray-400 dark:text-dark-text-faint">
                  {runEntryNodeId ? 'Only the selected input node will run' : 'All input nodes will be triggered'}
                </div>
              </div>
            {/if}

            <!-- Inputs: Form or Text/JSON -->
            {#if runUseForm}
              {@const selectedNode = getInputNodes().find(n => n.id === runEntryNodeId)}
              {@const fields = selectedNode?.fields || []}
              <div class="space-y-2">
                <div class="flex items-center justify-between">
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Inputs</span>
                  <button
                    onclick={() => { runUseForm = false; runInputMode = 'json'; runInputsJson = JSON.stringify(runFormValues, null, 2); }}
                    class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary"
                  >JSON</button>
                </div>
                {#each fields as field}
                  <label class="block">
                    <span class="text-[10px] font-medium text-gray-600 dark:text-dark-text-secondary block mb-0.5">
                      {field.name}
                      {#if field.description}
                        <span class="font-normal text-gray-400 dark:text-dark-text-muted ml-1">— {field.description}</span>
                      {/if}
                    </span>
                    {#if field.type === 'select' && field.options}
                      <select
                        value={runFormValues[field.name] ?? field.default ?? ''}
                        onchange={(e) => { runFormValues[field.name] = (e.target as HTMLSelectElement).value; }}
                        class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                      >
                        {#each field.options as opt}
                          <option value={opt}>{opt}</option>
                        {/each}
                      </select>
                    {:else if field.type === 'number'}
                      <input
                        type="number"
                        value={runFormValues[field.name] ?? field.default ?? 0}
                        oninput={(e) => { runFormValues[field.name] = Number((e.target as HTMLInputElement).value); }}
                        class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                      />
                    {:else if field.type === 'boolean'}
                      <label class="flex items-center gap-2 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={runFormValues[field.name] ?? field.default ?? false}
                          onchange={(e) => { runFormValues[field.name] = (e.target as HTMLInputElement).checked; }}
                          class="w-3.5 h-3.5 dark:bg-dark-elevated dark:border-dark-border-subtle dark:accent-accent"
                        />
                        <span class="text-xs text-gray-600 dark:text-dark-text-secondary">{runFormValues[field.name] ? 'Yes' : 'No'}</span>
                      </label>
                    {:else if field.type === 'textarea'}
                      <textarea
                        value={runFormValues[field.name] ?? field.default ?? ''}
                        oninput={(e) => { runFormValues[field.name] = (e.target as HTMLTextAreaElement).value; }}
                        rows={3}
                        class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text resize-y"
                      ></textarea>
                    {:else}
                      <input
                        type="text"
                        value={runFormValues[field.name] ?? field.default ?? ''}
                        oninput={(e) => { runFormValues[field.name] = (e.target as HTMLInputElement).value; }}
                        class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                      />
                    {/if}
                  </label>
                {/each}
              </div>
            {:else}
              <div>
                <div class="flex items-center justify-between mb-0.5">
                  <label for="run-inputs" class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Inputs</label>
                  <div class="flex items-center gap-1">
                    {#if getInputNodes().find(n => n.id === runEntryNodeId)?.fields}
                      <button
                        onclick={() => syncEditorFormFromEntry()}
                        class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary mr-1"
                      >Form</button>
                    {/if}
                    <div class="flex rounded overflow-hidden border border-gray-300 dark:border-dark-border-subtle">
                      <button
                        onclick={() => { runInputMode = 'text'; }}
                        class="px-1.5 py-0.5 text-[10px] font-medium {runInputMode === 'text' ? 'bg-gray-700 dark:bg-accent text-white' : 'bg-white dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-highest'}"
                      >Text</button>
                      <button
                        onclick={() => { runInputMode = 'json'; }}
                        class="px-1.5 py-0.5 text-[10px] font-medium border-l border-gray-300 dark:border-dark-border-subtle {runInputMode === 'json' ? 'bg-gray-700 dark:bg-accent text-white' : 'bg-white dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-highest'}"
                      >JSON</button>
                    </div>
                  </div>
                </div>
                <textarea
                  id="run-inputs"
                  bind:value={runInputsJson}
                  rows={5}
                  class="w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text resize-y {runInputMode === 'json' ? 'font-mono' : ''}"
                  placeholder={runInputMode === 'text' ? 'Type your input text...' : '{"key": "value"}'}
                ></textarea>
              </div>
            {/if}

            {#if versions.length > 0}
              <div>
                <label for="run-version-select" class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Run Version</label>
                <select
                  id="run-version-select"
                  bind:value={runVersion}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                >
                  <option value={undefined}>Latest (save first)</option>
                  {#each versions as v}
                    <option value={v.version}>v{v.version}{workflow.active_version === v.version ? ' (active)' : ''}</option>
                  {/each}
                </select>
                <div class="mt-0.5 text-[10px] text-gray-400 dark:text-dark-text-faint">
                  {runVersion !== undefined ? `Run version ${runVersion}` : 'Saves then runs latest graph'}
                </div>
              </div>
            {/if}
            <button
              onclick={handleRun}
              disabled={running}
              class="w-full flex items-center justify-center gap-1 px-2 py-1.5 text-xs text-white bg-green-600 rounded hover:bg-green-700 disabled:opacity-50 "
            >
              <Play size={12} />
              {running ? 'Running...' : runTargetNodeId ? 'Run to this step' : usePinnedData && Object.keys(pinnedNodes).length ? 'Run test with pins' : 'Execute'}
            </button>
            {#if running}
              <button onclick={stopRun} class="w-full border border-red-300 px-3 py-2 text-sm text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950">Stop run</button>
            {/if}

            {#if runError}
              <div class="p-2 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded text-xs text-red-700 dark:text-red-400">
                {runError}
              </div>
            {/if}

            {#if runResult}
              <div>
                <div class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider mb-1">Result</div>
                <pre class="p-2 bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border rounded text-[11px] font-mono text-gray-700 dark:text-dark-text-secondary overflow-x-auto whitespace-pre-wrap max-h-60 overflow-y-auto">{JSON.stringify(runResult, null, 2)}</pre>
              </div>
            {/if}

            {#if runResult || runError || Object.keys(workflowRun.nodeRunStates).length > 0}
              <button
                onclick={() => { clearRunState(); runResult = null; runError = null; }}
                class="w-full px-2 py-1 text-[10px] text-gray-500 dark:text-dark-text-muted border border-gray-300 dark:border-dark-border-subtle rounded hover:bg-gray-100 dark:hover:bg-dark-elevated "
              >
                Clear Results
              </button>
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

  :global(.kaykay-controls-btn[title="Lock"]) {
    display: none !important;
  }

  /* Matches kaykay's Controls chrome (shadow, z-index, faded until hovered) so
     the undo/redo group reads as the same class of canvas overlay. No opacity
     transition: this UI does not animate state changes. */
  .canvas-actions {
    box-shadow: 0 2px 8px #00000026;
    opacity: 0.4;
  }

  .canvas-actions:hover,
  .canvas-actions:focus-within {
    opacity: 1;
  }

  :global(.kaykay-dark) .canvas-actions {
    box-shadow: 0 2px 8px rgb(0 0 0 / 40%);
  }
</style>
