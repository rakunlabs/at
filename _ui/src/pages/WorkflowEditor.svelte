<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getWorkflow, updateWorkflow, runWorkflow, type Workflow, type WorkflowNode, type WorkflowEdge } from '@/lib/api/workflows';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { Canvas, Controls, Minimap, getFlow, type FlowNode, type FlowEdge, type FlowState, type NodeTypes } from 'kaykay';
  import { ArrowLeft, Save, Play, Plus, X, Bot } from 'lucide-svelte';
  import ChatPanel from '@/lib/components/workflow/ChatPanel.svelte';

  import InputNode from '@/lib/components/workflow/InputNode.svelte';
  import OutputNode from '@/lib/components/workflow/OutputNode.svelte';
  import LLMCallNode from '@/lib/components/workflow/LLMCallNode.svelte';
  import PromptTemplateNode from '@/lib/components/workflow/PromptTemplateNode.svelte';
  import HttpTriggerNode from '@/lib/components/workflow/HttpTriggerNode.svelte';
  import CronTriggerNode from '@/lib/components/workflow/CronTriggerNode.svelte';
  import HttpRequestNode from '@/lib/components/workflow/HttpRequestNode.svelte';
  import ConditionalNode from '@/lib/components/workflow/ConditionalNode.svelte';
  import LoopNode from '@/lib/components/workflow/LoopNode.svelte';
  import ScriptNode from '@/lib/components/workflow/ScriptNode.svelte';

  // ─── Props ───
  let { params = { id: '' } }: { params?: { id: string } } = $props();

  storeNavbar.title = 'Workflow Editor';

  // ─── Node Types ───
  const nodeTypes: NodeTypes = {
    input: InputNode,
    output: OutputNode,
    llm_call: LLMCallNode,
    prompt_template: PromptTemplateNode,
    http_trigger: HttpTriggerNode,
    cron_trigger: CronTriggerNode,
    http_request: HttpRequestNode,
    conditional: ConditionalNode,
    loop: LoopNode,
    script: ScriptNode,
  };

  const paletteGroups = [
    {
      label: 'Triggers',
      nodes: [
        { type: 'http_trigger', label: 'HTTP Trigger', description: 'Webhook-triggered entry' },
        { type: 'cron_trigger', label: 'Cron Trigger', description: 'Schedule-triggered entry' },
      ],
    },
    {
      label: 'Processing',
      nodes: [
        { type: 'input', label: 'Input', description: 'Manual input data' },
        { type: 'llm_call', label: 'LLM Call', description: 'Call an LLM provider' },
        { type: 'prompt_template', label: 'Prompt Template', description: 'Template with variables' },
        { type: 'http_request', label: 'HTTP Request', description: 'Make an HTTP request' },
        { type: 'script', label: 'Script', description: 'Run JavaScript code' },
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
      label: 'Output',
      nodes: [
        { type: 'output', label: 'Output', description: 'Workflow output data' },
      ],
    },
  ];

  // ─── State ───
  let workflow = $state<Workflow | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let running = $state(false);
  let providers = $state<ProviderRecord[]>([]);
  let runResult = $state<any>(null);
  let runError = $state<string | null>(null);

  // Property editor
  let selectedNodeId = $state<string | null>(null);
  let selectedNodeData = $state<Record<string, any>>({});
  let selectedNodeType = $state<string>('');

  // Run inputs
  let showRunPanel = $state(false);
  let showChatPanel = $state(false);
  let runInputsJson = $state('{}');

  // Canvas ref
  let canvasRef: { getFlow: () => FlowState } | undefined = $state();

  // ─── Helpers ───

  function toFlowNodes(nodes: WorkflowNode[]): FlowNode[] {
    return nodes.map((n) => ({
      id: n.id,
      type: n.type,
      position: { x: n.position.x, y: n.position.y },
      data: n.data || {},
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
      addToast('Workflow saved', 'info');
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
      const inputs = JSON.parse(runInputsJson);
      runResult = await runWorkflow(workflow.id, inputs);
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

  function addNode(type: string) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    nodeCounter++;
    const defaultData: Record<string, any> = {};
    if (type === 'input') {
      defaultData.label = 'Input';
      defaultData.fields = [];
    } else if (type === 'output') {
      defaultData.label = 'Output';
      defaultData.fields = [];
    } else if (type === 'llm_call') {
      defaultData.label = 'LLM Call';
      defaultData.provider = '';
      defaultData.model = '';
      defaultData.system_prompt = '';
    } else if (type === 'prompt_template') {
      defaultData.label = 'Prompt Template';
      defaultData.template = '';
      defaultData.variables = [];
    } else if (type === 'http_trigger') {
      defaultData.label = 'HTTP Trigger';
      defaultData.trigger_id = '';
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
    }
    flow.addNode({
      id: `${type}_${nodeCounter}`,
      type,
      position: { x: 200 + nodeCounter * 30, y: 150 + nodeCounter * 30 },
      data: defaultData,
    });
  }

  // ─── Property Editor ───

  function onNodeClick(nodeId: string) {
    if (!canvasRef) return;
    const flow = canvasRef.getFlow();
    const node = flow.getNode(nodeId);
    if (node) {
      selectedNodeId = nodeId;
      selectedNodeType = node.type;
      selectedNodeData = { ...node.data };
    }
  }

  function applyNodeData() {
    if (!canvasRef || !selectedNodeId) return;
    const flow = canvasRef.getFlow();

    // Handle script node input_count changes — remap edges to new handle IDs.
    if (selectedNodeType === 'script') {
      const currentNode = flow.getNode(selectedNodeId);
      const oldCount = currentNode?.data?.input_count || 1;
      const newCount = selectedNodeData.input_count || 1;

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
    addToast('Node updated', 'info');
  }

  function closePropertyEditor() {
    selectedNodeId = null;
    selectedNodeData = {};
    selectedNodeType = '';
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
        selectedNodeData = { ...selectedNode.data };
      }
    }
  }

  // ─── Init ───

  loadWorkflow();
  loadProviders();
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
          <input
            type="text"
            bind:value={workflow.name}
            class="text-sm font-medium text-gray-900 bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
            placeholder="Workflow name"
          />
          <input
            type="text"
            bind:value={workflow.description}
            class="text-[10px] text-gray-400 bg-transparent border-none outline-none focus:ring-0 w-48 p-0"
            placeholder="Add description..."
          />
        </div>
      </div>
      <div class="flex items-center gap-2">
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

    <!-- Main area -->
    <div class="flex flex-1 overflow-hidden">
      <!-- Node Palette -->
      <div class="w-44 bg-white border-r border-gray-200 shrink-0 overflow-y-auto">
        <div class="p-2">
          {#each paletteGroups as group}
            <div class="text-[10px] font-medium text-gray-400 uppercase tracking-wider mb-1 mt-2 first:mt-0">{group.label}</div>
            {#each group.nodes as opt}
              <button
                onclick={() => addNode(opt.type)}
                class="w-full flex items-center gap-2 px-2 py-1.5 text-xs text-left text-gray-700 rounded hover:bg-gray-100 transition-colors mb-0.5"
              >
                <Plus size={11} class="text-gray-400 shrink-0" />
                <div>
                  <div class="font-medium">{opt.label}</div>
                  <div class="text-[10px] text-gray-400">{opt.description}</div>
                </div>
              </button>
            {/each}
          {/each}
        </div>
      </div>

      <!-- Canvas -->
      <div class="flex-1 relative bg-gray-50">
        <Canvas
          bind:this={canvasRef}
          nodes={toFlowNodes(workflow.graph.nodes)}
          edges={toFlowEdges(workflow.graph.edges)}
          {nodeTypes}
          config={{ snap_to_grid: true, grid_size: 20, default_edge_type: 'bezier' }}
          callbacks={{ on_node_click: onNodeClick }}
        >
          {#snippet controls()}
            <Controls position="bottom-left" />
            <Minimap width={160} height={100} />
          {/snippet}

          {#if showChatPanel}
            <div class="absolute top-0 right-0 h-full z-50">
              <ChatPanel onclose={() => { showChatPanel = false; }} />
            </div>
          {/if}
        </Canvas>
      </div>

      <!-- Property Editor Panel -->
      {#if selectedNodeId}
        <div class="w-60 bg-white border-l border-gray-200 shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 shrink-0">
            <span class="text-xs font-medium text-gray-700">Properties</span>
            <button onclick={closePropertyEditor} class="text-gray-400 hover:text-gray-600">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto min-h-0 flex-1">
            <!-- Common: Label -->
            <div>
              <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Label</label>
              <input
                type="text"
                bind:value={selectedNodeData.label}
                class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
              />
            </div>

            <!-- Type-specific fields -->
            {#if selectedNodeType === 'input' || selectedNodeType === 'output'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Fields (comma separated)</label>
                <input
                  type="text"
                  value={selectedNodeData.fields?.join(', ') || ''}
                  oninput={(e) => { selectedNodeData.fields = (e.target as HTMLInputElement).value.split(',').map((s: string) => s.trim()).filter(Boolean); }}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="field1, field2"
                />
              </div>
            {/if}

            {#if selectedNodeType === 'llm_call'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Provider</label>
                <select
                  bind:value={selectedNodeData.provider}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                >
                  <option value="">Select provider</option>
                  {#each providers as p}
                    <option value={p.key}>{p.key}</option>
                  {/each}
                </select>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Model</label>
                <input
                  type="text"
                  bind:value={selectedNodeData.model}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="Model name (optional)"
                />
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">System Prompt</label>
                <textarea
                  bind:value={selectedNodeData.system_prompt}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="System prompt (optional)"
                ></textarea>
              </div>
            {/if}

            {#if selectedNodeType === 'prompt_template'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Template</label>
                <textarea
                  bind:value={selectedNodeData.template}
                  rows={4}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="Hello {{name}}, ..."
                ></textarea>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Variables (comma separated)</label>
                <input
                  type="text"
                  value={selectedNodeData.variables?.join(', ') || ''}
                  oninput={(e) => { selectedNodeData.variables = (e.target as HTMLInputElement).value.split(',').map((s: string) => s.trim()).filter(Boolean); }}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="name, topic"
                />
              </div>
            {/if}

            {#if selectedNodeType === 'http_trigger'}
              <div>
                {#if selectedNodeData.trigger_id}
                  <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Webhook URL</label>
                  <div class="mt-0.5 px-2 py-1 text-[10px] font-mono text-gray-600 bg-gray-50 border border-gray-200 rounded break-all">
                    /api/v1/webhooks/{selectedNodeData.trigger_id}
                  </div>
                  <div class="mt-1 text-[10px] text-gray-400">
                    ID: <span class="font-mono">{selectedNodeData.trigger_id}</span>
                  </div>
                {:else}
                  <div class="text-[10px] text-gray-400 italic">Save the workflow to generate a webhook URL</div>
                {/if}
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Fields</label>
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
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Schedule (cron)</label>
                <input
                  type="text"
                  bind:value={selectedNodeData.schedule}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="*/5 * * * *"
                />
                <div class="mt-0.5 text-[10px] text-gray-400">Standard 5-field cron expression</div>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Static Payload (JSON)</label>
                <textarea
                  value={JSON.stringify(selectedNodeData.payload || {}, null, 2)}
                  oninput={(e) => { try { selectedNodeData.payload = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"key": "value"}'}
                ></textarea>
              </div>
            {/if}

            {#if selectedNodeType === 'http_request'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">URL (Go template)</label>
                <input
                  type="text"
                  bind:value={selectedNodeData.url}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder={'https://api.example.com/\x7B\x7B.path\x7D\x7D'}
                />
                <div class="mt-0.5 text-[10px] text-gray-400">Supports Go templates with data from "values" input</div>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Method (Go template)</label>
                <input
                  type="text"
                  bind:value={selectedNodeData.method}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="GET"
                />
                <div class="mt-0.5 text-[10px] text-gray-400">GET, POST, PUT, PATCH, DELETE or a Go template</div>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Headers (JSON, values support templates)</label>
                <textarea
                  value={JSON.stringify(selectedNodeData.headers || {}, null, 2)}
                  oninput={(e) => { try { selectedNodeData.headers = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
                  rows={2}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"Authorization": "Bearer \x7B\x7B.token\x7D\x7D"}'}
                ></textarea>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Body (Go template)</label>
                <textarea
                  bind:value={selectedNodeData.body}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={'{"name": "\x7B\x7B.name\x7D\x7D", "count": \x7B\x7B.count\x7D\x7D}'}
                ></textarea>
                <div class="mt-0.5 text-[10px] text-gray-400">Leave empty to auto-send input data as JSON for POST/PUT/PATCH</div>
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timeout (seconds)</label>
                <input
                  type="number"
                  bind:value={selectedNodeData.timeout}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="30"
                  min="1"
                  max="300"
                />
              </div>
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Proxy URL</label>
                <input
                  type="text"
                  bind:value={selectedNodeData.proxy}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
                  placeholder="http://proxy.example.com:8080"
                />
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
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Expression (JS)</label>
                <textarea
                  bind:value={selectedNodeData.expression}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="data.score > 0.8"
                ></textarea>
                <div class="mt-0.5 text-[10px] text-gray-400">JS expression that evaluates to true/false</div>
              </div>
            {/if}

            {#if selectedNodeType === 'loop'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Expression (JS)</label>
                <textarea
                  bind:value={selectedNodeData.expression}
                  rows={3}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder="data.items"
                ></textarea>
                <div class="mt-0.5 text-[10px] text-gray-400">JS expression returning an array to iterate</div>
              </div>
            {/if}

            {#if selectedNodeType === 'script'}
              <div>
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</label>
                <input
                  type="number"
                  bind:value={selectedNodeData.input_count}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
                  min="1"
                  max="10"
                  placeholder="1"
                />
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
                <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Code (JS)</label>
                <textarea
                  bind:value={selectedNodeData.code}
                  rows={6}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                  placeholder={
                    (selectedNodeData.input_count || 1) <= 1
                      ? '// Access inputs via data\nconst value = data.value * 2;\nreturn { doubled: value };'
                      : '// Access inputs via data1, data2, ...\nconst sum = data1.value + data2.value;\nreturn { sum: sum };'
                  }
                ></textarea>
                <div class="mt-0.5 text-[10px] text-gray-400">Use <code class="font-mono bg-gray-100 px-0.5 rounded">return</code> to set the result. Truthy → "true" port, falsy → "false" port, "always" always fires.</div>
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
              <label class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs (JSON)</label>
              <textarea
                bind:value={runInputsJson}
                rows={5}
                class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
                placeholder={'{"key": "value"}'}
              ></textarea>
            </div>
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
