<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import {
    type ChatMessage,
    type ToolCall,
    type ToolDefinition,
    getTextContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { type FlowState, type FlowNode, type FlowEdge } from 'kaykay';
  import { listSkills } from '@/lib/api/skills';
  import { listAgents } from '@/lib/api/agents';
  import { listVariables } from '@/lib/api/secrets';
  import { listMCPSets } from '@/lib/api/mcp-sets';
  import { listNodeConfigs } from '@/lib/api/node-configs';
  import { getNodeTypes, type NodeTypeMeta, type PortMeta, type FieldMeta } from '@/lib/api/workflows';
  import { createDefaultWorkflowNodeData, getWorkflowNodeDimensions, isWorkflowNodeType, workflowNodeDefinitions, workflowNodeTypes } from '@/lib/workflow/node-definitions';
  import { canvasInputHandle } from '@/lib/workflow/ports';
  import { summarizeWorkflowToolCall } from '@/lib/workflow/chat-tool-summary';
  import { toolResultsByMessage } from '@/lib/helper/tool-activity';
  import { workspaceTransport } from '@/lib/api/transport';
  import { Send, Square, X, ChevronDown, Bot, Trash2 } from 'lucide-svelte';
  import ToolActivity from '@/lib/components/ToolActivity.svelte';
  import MessageContent from '@/lib/components/playground/MessageContent.svelte';

  // ─── Props ───
  let { onclose, flow }: { onclose: () => void; flow: FlowState } = $props();

  // ─── State ───
  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let messages = $state<ChatMessage[]>([]);
  let userInput = $state('');
  let streaming = $state(false);
  let abortController: AbortController | null = null;
  let chatContainer: HTMLDivElement | undefined = $state();
  let loadingModels = $state(true);
  let toolResults = $derived(toolResultsByMessage(messages));

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function growComposer(node: HTMLTextAreaElement, _value: string) {
    const resize = () => {
      node.style.height = 'auto';
      node.style.height = `${node.scrollHeight + node.offsetHeight - node.clientHeight}px`;
    };
    queueMicrotask(resize);
    return { update(_value: string) { queueMicrotask(resize); } };
  }

  // ─── Constants ───
  const MAX_TOOL_ITERATIONS = 20;

  // ─── Load models ───
  async function loadModels() {
    loadingModels = true;
    try {
      const info = await getInfo();
      const allModels: string[] = [];
      for (const p of info.providers) {
        const reference = p.reference || p.key;
        if (p.models && p.models.length > 0) {
          for (const m of p.models) {
            allModels.push(`${reference}/${m}`);
          }
        } else if (p.default_model) {
          allModels.push(`${reference}/${p.default_model}`);
        }
      }
      models = allModels;
      if (allModels.length > 0 && !selectedModel) {
        selectedModel = allModels[0];
      }
    } catch (e: any) {
      addToast('Failed to load models', 'alert');
    } finally {
      loadingModels = false;
    }
  }

  loadModels();

  // ─── Scroll ───
  function scrollToBottom() {
    if (chatContainer) {
      requestAnimationFrame(() => {
        chatContainer!.scrollTop = chatContainer!.scrollHeight;
      });
    }
  }

  // ─── Tool Definitions (derived from node type metadata) ───

  const flowTools: ToolDefinition[] = $derived.by(() => {
    return [
      {
        type: 'function',
        function: {
          name: 'get_flow',
          description: 'Get the current workflow flow state (all nodes and edges). ALWAYS call this first before making changes.',
          parameters: { type: 'object', properties: {}, required: [] },
        },
      },
      {
        type: 'function',
        function: {
          name: 'add_node',
          description: 'Add a new node to the workflow canvas',
          parameters: {
            type: 'object',
            properties: {
              type: {
                type: 'string',
                enum: workflowNodeTypes,
                description: 'The node type',
              },
              id: { type: 'string', description: 'Optional custom ID. Auto-generated if omitted.' },
              position: {
                type: 'object',
                properties: { x: { type: 'number' }, y: { type: 'number' } },
                required: ['x', 'y'],
                description: 'Canvas position {x, y}',
              },
              data: {
                type: 'object',
                description: 'Node-specific configuration. Must include "label" field (except sticky_note which uses "text" instead).',
              },
            },
            required: ['type', 'position', 'data'],
          },
        },
      },
      {
        type: 'function',
        function: {
          name: 'remove_node',
          description: 'Remove a node from the workflow (also removes connected edges)',
          parameters: {
            type: 'object',
            properties: {
              id: { type: 'string', description: 'The node ID to remove' },
            },
            required: ['id'],
          },
        },
      },
      {
        type: 'function',
        function: {
          name: 'update_node_data',
          description: 'Update a node\'s configuration data (partial merge)',
          parameters: {
            type: 'object',
            properties: {
              id: { type: 'string', description: 'The node ID to update' },
              data: { type: 'object', description: 'Partial data to merge into the node' },
            },
            required: ['id', 'data'],
          },
        },
      },
      {
        type: 'function',
        function: {
          name: 'update_node_position',
          description: 'Move a node to a new position',
          parameters: {
            type: 'object',
            properties: {
              id: { type: 'string', description: 'The node ID to move' },
              position: {
                type: 'object',
                properties: { x: { type: 'number' }, y: { type: 'number' } },
                required: ['x', 'y'],
              },
            },
            required: ['id', 'position'],
          },
        },
      },
      {
        type: 'function',
        function: {
          name: 'add_edge',
          description: 'Connect two nodes by adding an edge between their handles',
          parameters: {
            type: 'object',
            properties: {
              source: { type: 'string', description: 'Source node ID' },
              source_handle: { type: 'string', description: 'Source output handle ID' },
              target: { type: 'string', description: 'Target node ID' },
              target_handle: { type: 'string', description: 'Target input handle ID' },
            },
            required: ['source', 'source_handle', 'target', 'target_handle'],
          },
        },
      },
      {
        type: 'function',
        function: {
          name: 'fit_view',
          description: 'Zoom and pan the canvas so the whole workflow is visible. Call once after adding or moving several nodes.',
          parameters: { type: 'object', properties: {}, required: [] },
        },
      },
      {
        type: 'function',
        function: {
          name: 'remove_edge',
          description: 'Remove an edge by its ID',
          parameters: {
            type: 'object',
            properties: {
              id: { type: 'string', description: 'The edge ID to remove' },
            },
            required: ['id'],
          },
        },
      },
    ];
  });

  // ─── System Prompt ───

  let providersInfo = $state<{ key: string; models: string[] }[]>([]);

  async function loadProviders() {
    try {
      const info = await getInfo();
      providersInfo = info.providers.map((p: InfoProvider) => ({
        key: p.reference || p.key,
        models: p.models?.length ? p.models : p.default_model ? [p.default_model] : [],
      }));
    } catch {}
  }

  loadProviders();

  let nodeTypeMetas = $state<NodeTypeMeta[]>([]);

  async function loadNodeTypes() {
    try {
      nodeTypeMetas = await getNodeTypes();
    } catch {}
  }

  loadNodeTypes();

  let agentsInfo = $state<{ id: string; name: string; description: string }[]>([]);

  async function loadAgents() {
    try {
      const res = await listAgents({ _limit: 500 });
      agentsInfo = (res.data ?? []).map(a => ({ id: a.id, name: a.name, description: a.config?.description || '' }));
    } catch {}
  }

  loadAgents();

  let skillsInfo = $state<{ name: string; description: string }[]>([]);
  let variablesInfo = $state<{ key: string; description: string }[]>([]);
  let nodeConfigsInfo = $state<{ id: string; name: string; type: string }[]>([]);

  async function loadSkills() {
    try {
      const res = await listSkills();
      skillsInfo = res.data.map(s => ({ name: s.name, description: s.description }));
    } catch {}
  }

  async function loadVariables() {
    try {
      const res = await listVariables();
      variablesInfo = res.data.map(v => ({ key: v.key, description: v.description }));
    } catch {}
  }

  async function loadNodeConfigs() {
    try {
      const res = await listNodeConfigs();
      nodeConfigsInfo = res.data.map(c => ({ id: c.id, name: c.name, type: c.type }));
    } catch {}
  }

  let mcpSetsInfo = $state<{ name: string; description: string }[]>([]);

  async function loadMCPSets() {
    try {
      const res = await listMCPSets({ _limit: 500 });
      mcpSetsInfo = (res.data ?? []).map(s => ({ name: s.name, description: s.description }));
    } catch {}
  }

  loadSkills();
  loadMCPSets();
  loadVariables();
  loadNodeConfigs();

  // Get a live snapshot of the current workflow for the system prompt
  function getCurrentFlowSummary(): string {
    try {
      const json = flow.toJSON();
      const nodes = json.nodes || [];
      const edges = json.edges || [];
      if (nodes.length === 0) return 'The workflow is currently empty.';
      const nodeList = nodes.map((n: any) => {
        const x = Math.round(n.position?.x ?? 0);
        const y = Math.round(n.position?.y ?? 0);
        return `- ${n.id} (${n.type}) at (${x}, ${y}): "${n.data?.label || n.data?.text || ''}"`;
      });
      return `Current workflow has ${nodes.length} nodes and ${edges.length} edges:\n${nodeList.join('\n')}`;
    } catch {
      return 'Unable to read current workflow state.';
    }
  }

  // ─── Dynamic System Prompt ───

  /** Generate docs only for node types the frontend can render. */
  function buildNodeTypesDoc(): string {
    let doc = '';
    for (const definition of workflowNodeDefinitions) {
      const meta = nodeTypeMetas.find(candidate => candidate.type === definition.type);
      doc += `### ${definition.type}\n`;
      doc += `${meta?.description || definition.description}\n`;

      if (meta?.inputs && meta.inputs.length > 0) {
        const handles = meta.inputs.map((p: PortMeta) => {
          let s = `id="${p.name}" (port: ${p.type}`;
          if (p.accept?.length) s += `, accepts: ${p.accept.join(', ')}`;
          if (p.position && p.position !== 'left') s += `, position: ${p.position}`;
          s += ')';
          return s;
        });
        doc += `- Input handles: ${handles.join(', ')}\n`;
      }

      if (meta?.outputs && meta.outputs.length > 0) {
        const handles = meta.outputs.map((p: PortMeta) => {
          let s = `id="${p.name}" (port: ${p.type}`;
          if (p.position && p.position !== 'right') s += `, position: ${p.position}`;
          s += ')';
          return s;
        });
        doc += `- Output handles: ${handles.join(', ')}\n`;
      }

      if (meta?.fields && meta.fields.length > 0) {
        const fields = meta.fields.map((f: FieldMeta) => {
          let s = f.name;
          if (f.type !== 'string') s += ` (${f.type})`;
          if (f.required) s += ' [required]';
          if (f.default !== undefined && f.default !== null && f.default !== '') s += ` (default: ${JSON.stringify(f.default)})`;
          if (f.enum?.length) s += ` (values: ${f.enum.join(', ')})`;
          if (f.description && f.name !== 'label') s += ` — ${f.description}`;
          return s;
        });
        doc += `- Data fields: ${fields.join(', ')}\n`;
      }

      if (!meta) {
        if (definition.type === 'http_trigger') {
          doc += '- Output handles: id="output" (port: data)\n';
          doc += '- Data fields: label [required], trigger_id (auto-assigned on save), alias (optional URL path), public (boolean, skip auth)\n';
        } else if (definition.type === 'cron_trigger') {
          doc += '- Output handles: id="output" (port: data)\n';
          doc += '- Data fields: label [required], schedule (cron expression e.g. "*/5 * * * *"), timezone (IANA e.g. "America/New_York"), payload (object)\n';
        } else if (definition.type === 'group') {
          doc += '- No handles; this node cannot be connected with edges\n';
          doc += '- Data fields: label [required], color (CSS hex, default "#22c55e")\n';
        } else if (definition.type === 'sticky_note') {
          doc += '- No handles; this node cannot be connected with edges\n';
          doc += '- Data fields: text (markdown content), color (CSS hex, default "#fef08a")\n';
          doc += '- NOTE: uses "text" instead of "label". Do NOT include a "label" field.\n';
        }
      }

      const dimensions = getWorkflowNodeDimensions(definition.type);
      if (dimensions) {
        doc += `- Canvas dimensions: ${dimensions.width}x${dimensions.height}, applied automatically by add_node\n`;
      }

      doc += '\n';
    }

    return doc;
  }

  const systemPrompt = $derived(`You are a workflow editor AI assistant. You help users build and modify visual node-based workflows.

IMPORTANT: Always call get_flow FIRST before making any changes, to see the current state of the workflow.

## Current Workflow Summary
${getCurrentFlowSummary()}

## Available Node Types

Each node has specific input/output handles (ports) for connecting edges. The handle "id" is what you must use as source_handle or target_handle when adding edges.

${buildNodeTypesDoc()}
## Available Providers
${providersInfo.length > 0 ? providersInfo.map(p => `- "${p.key}": models [${p.models.map(m => `"${m}"`).join(', ')}]`).join('\n') : '- No providers configured yet'}

When creating media nodes, or an agent_call node without an agent, use the provider key for the "provider" field and the model name for the "model" field from the list above.

## Available Agents
${agentsInfo.length > 0 ? agentsInfo.map(a => `- id="${a.id}" name="${a.name}"${a.description ? ': ' + a.description : ''}`).join('\n') : '- No agents configured yet'}

For LLM steps use an agent_call node (llm_call is legacy; do not create new ones). Prefer setting "agent_id" to one of the agents above: the agent's system prompt, model, skills, MCP sets, built-in tools and workflows are used, so leave "provider"/"model" empty unless you need to override them.

## Available Skills
${skillsInfo.length > 0 ? skillsInfo.map(s => `- "${s.name}": ${s.description}`).join('\n') : '- No skills configured yet'}

When creating skill_config nodes, use skill names from this list in the "skills" array.

## Available MCP Sets
${mcpSetsInfo.length > 0 ? mcpSetsInfo.map(s => `- "${s.name}"${s.description ? ': ' + s.description : ''}`).join('\n') : '- No MCP sets registered yet'}

When creating mcp_config nodes, put MCP set names from this list in the "mcp_sets" array and connect its "mcp_urls" output to the agent_call "mcp" input. Never put raw MCP server URLs in a workflow; if the needed MCP is not listed, tell the user to register it on the MCP Sets page first.

## Available Variables
${variablesInfo.length > 0 ? variablesInfo.map(v => `- "${v.key}"${v.description ? ': ' + v.description : ''}`).join('\n') : '- No variables configured yet'}

## Available Node Configs
${nodeConfigsInfo.length > 0 ? nodeConfigsInfo.map(c => `- id="${c.id}" name="${c.name}" type="${c.type}"`).join('\n') : '- No node configs configured yet'}

## Edge Connection Rules
- Edges connect a source output handle to a target input handle
- The source_handle and target_handle values must be the handle "id" (not the port or label)
- Edge IDs should be formatted as "source_id-source_handle-target_id-target_handle"

## Canvas and Layout
The canvas is an infinite, pannable and zoomable surface — it is NOT limited to the visible screen. The user zooms out to see large workflows, so never cram nodes together to make them fit a small area. Prefer a wide, tidy, readable layout over a compact one.

- Step cards are about 288px wide and 120–220px tall (agent_call and nodes with many fields are taller). Treat each card as roughly 300x220 when planning.
- Main flow goes left-to-right: put each successive step in its own column, ~400px apart horizontally (x = 0, 400, 800, 1200, …). Long flows may extend thousands of pixels to the right — that is fine.
- Parallel branches (conditional true/false, fan-out) go in separate rows ~300px apart vertically, aligned in the same columns as their siblings.
- Resource config nodes (skill_config, mcp_config, agent_config) go directly BELOW the agent_call node they connect to, ~280px lower, spreading sideways if there are several.
- Align nodes on a grid: share x within a column and y within a row. Never overlap cards; check existing positions from get_flow before placing new ones, and place new nodes in free space (usually right of or below the existing content).
- When the user asks to tidy or reorganize, move existing nodes with update_node_position to this grid instead of recreating them.
- Coordinates may be negative or large; there is no edge of the canvas.
- After adding or moving several nodes, call fit_view once so the user sees the whole result.

## Important
- Always use get_flow first to understand the current state before making changes
- Use only the node types listed above; add_node rejects other backend node types
- Use meaningful node IDs that reflect the node's purpose
- Always include a "label" field in node data (except sticky_note which uses "text")
- group and sticky_note nodes are visual-only; they have no handles and cannot be connected with edges`);

  // ─── Tool Execution ───

  let nodeIdCounter = 0;
  const MCP_URL_REFUSAL = 'mcp_config does not accept raw MCP URLs. Use "mcp_sets" with names of registered MCP sets; if none fits, ask the user to register the MCP on the MCP Sets page.';

  function executeToolCall(name: string, args: Record<string, any>): string {
    try {
      switch (name) {
        case 'get_flow': {
          const json = flow.toJSON();
          return JSON.stringify(json, null, 2);
        }

        case 'add_node': {
          const { type, position, data, id } = args;
          if (!isWorkflowNodeType(type)) {
            return JSON.stringify({ error: `Unsupported node type "${String(type)}"` });
          }
          if (type === 'mcp_config' && data?.mcp_urls?.length) {
            return JSON.stringify({ error: MCP_URL_REFUSAL });
          }
          nodeIdCounter++;
          const nodeId = id || `${type}_ai_${nodeIdCounter}`;
          const defaults = createDefaultWorkflowNodeData(type);
          const nodeData = { ...defaults, ...(data || {}) };
          const nodeOpts: any = {
            id: nodeId,
            type,
            position: { x: position.x, y: position.y },
            data: nodeData,
          };
          const dimensions = getWorkflowNodeDimensions(type);
          if (dimensions) {
            nodeOpts.width = dimensions.width;
            nodeOpts.height = dimensions.height;
          }
          flow.addNode(nodeOpts);
          return JSON.stringify({ success: true, id: nodeId });
        }

        case 'remove_node': {
          const { id } = args;
          const node = flow.getNode(id);
          if (!node) return JSON.stringify({ error: `Node "${id}" not found` });
          flow.removeNode(id);
          return JSON.stringify({ success: true });
        }

        case 'update_node_data': {
          const { id, data } = args;
          const node = flow.getNode(id);
          if (!node) return JSON.stringify({ error: `Node "${id}" not found` });
          if (node.type === 'mcp_config' && data?.mcp_urls?.length) {
            return JSON.stringify({ error: MCP_URL_REFUSAL });
          }
          flow.updateNodeData(id, data);
          return JSON.stringify({ success: true });
        }

        case 'update_node_position': {
          const { id, position } = args;
          const node = flow.getNode(id);
          if (!node) return JSON.stringify({ error: `Node "${id}" not found` });
          flow.updateNodePosition(id, { x: position.x, y: position.y });
          return JSON.stringify({ success: true });
        }

        case 'add_edge': {
          const { source, source_handle, target, target_handle } = args;
          const edgeId = `${source}-${source_handle}-${target}-${target_handle}`;
          const added = flow.addEdge({
            id: edgeId,
            source,
            source_handle,
            target,
            target_handle: canvasInputHandle(flow.getNode(target)?.type ?? '', target_handle),
          });
          if (!added) {
            return JSON.stringify({ error: `Failed to add edge. Verify that source node "${source}" has output handle "${source_handle}" and target node "${target}" has input handle "${target_handle}". Check handle IDs match exactly.` });
          }
          return JSON.stringify({ success: true, id: edgeId });
        }

        case 'fit_view': {
          flow.fitView(80);
          return JSON.stringify({ success: true, zoom: Number(flow.viewport.zoom.toFixed(2)) });
        }

        case 'remove_edge': {
          const { id } = args;
          const edge = flow.getEdge(id);
          if (!edge) return JSON.stringify({ error: `Edge "${id}" not found` });
          flow.removeEdge(id);
          return JSON.stringify({ success: true });
        }

        default:
          return JSON.stringify({ error: `Unknown tool: ${name}` });
      }
    } catch (e: any) {
      return JSON.stringify({ error: e.message || 'Tool execution failed' });
    }
  }

  // ─── Send Message (with tool call loop) ───

  async function sendMessage() {
    const text = userInput.trim();
    if (!text || !selectedModel || streaming) return;

    // Add user message
    messages = [...messages, { role: 'user', content: text }];
    userInput = '';
    scrollToBottom();

    await runCompletion();
  }

  async function runCompletion(depth: number = 0) {
    // Guard against infinite tool-call loops
    if (depth >= MAX_TOOL_ITERATIONS) {
      messages = [...messages, {
        role: 'assistant',
        content: `Stopped after ${MAX_TOOL_ITERATIONS} tool call iterations to prevent infinite loops.`,
      }];
      return;
    }

    // Build request messages
    const reqMessages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string }> = [];
    reqMessages.push({ role: 'system', content: systemPrompt });

    for (const m of messages) {
      const msg: any = { role: m.role, content: m.content };
      if (m.tool_calls) msg.tool_calls = m.tool_calls;
      if (m.tool_call_id) msg.tool_call_id = m.tool_call_id;
      reqMessages.push(msg);
    }

    // Add assistant placeholder
    messages = [...messages, { role: 'assistant', content: '' }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    // Accumulate tool calls from the stream
    let pendingToolCalls: ToolCall[] = [];

    try {
      await streamChatCompletion(
        'api/v1/chat/completions',
        {
          model: selectedModel,
          messages: reqMessages,
          tools: flowTools,
          stream: true,
        },
        {
          onDelta: (deltaContent) => {
            const lastIdx = messages.length - 1;
            const prev = messages[lastIdx];
            messages[lastIdx] = {
              ...prev,
              content: mergeDeltaContent(prev.content, deltaContent),
            };
            scrollToBottom();
          },
          onToolCalls: (toolCalls) => {
            pendingToolCalls = toolCalls;
          },
          onError: (error) => {
            addToast(error, 'alert');
          },
        },
        controller.signal,
      );

      // After streaming completes, check if there are tool calls to execute
      if (pendingToolCalls.length > 0) {
        // Attach tool calls to the assistant message
        const lastIdx = messages.length - 1;
        messages[lastIdx] = { ...messages[lastIdx], tool_calls: pendingToolCalls };

        // Execute each tool call and add tool result messages
        for (const tc of pendingToolCalls) {
          let args: Record<string, any> | null = null;
          try {
            const parsed = JSON.parse(tc.function.arguments || '{}');
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) args = parsed;
          } catch {}
          const result = args
            ? executeToolCall(tc.function.name, args)
            : JSON.stringify({ error: `Arguments for ${tc.function.name} are not a single JSON object. Call the tool again with one valid JSON object per call.` });

          messages = [
            ...messages,
            {
              role: 'tool',
              content: result,
              tool_call_id: tc.id,
            },
          ];
        }
        scrollToBottom();

        // Reset streaming state before recursive call
        streaming = false;
        abortController = null;

        // Continue the conversation so the LLM can see tool results
        await runCompletion(depth + 1);
        return;
      }
    } catch (e: any) {
      if (e.name !== 'AbortError') {
        addToast(e.message || 'Chat request failed', 'alert');
        // Remove empty assistant message on error
        const lastIdx = messages.length - 1;
        if (messages[lastIdx]?.role === 'assistant' && !getTextContent(messages[lastIdx].content)) {
          messages = messages.slice(0, -1);
        }
      }
    } finally {
      streaming = false;
      abortController = null;
    }
  }

  function stopStreaming() {
    if (abortController) {
      abortController.abort();
    }
  }

  function clearChat() {
    messages = [];
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      sendMessage();
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="w-[26rem] max-w-full h-full bg-dark-surface border-l border-dark-border shrink-0 min-h-0 flex flex-col"
  onmousedown={(e) => e.stopPropagation()}
  onwheel={(e) => e.stopPropagation()}
  onkeydown={(e) => e.stopPropagation()}
>
  <!-- Toolbar: same controls and sizing as Chats -->
  <div class="border-b border-dark-border bg-dark-surface px-3 py-1 flex items-center gap-1.5 shrink-0">
    <Bot size={14} class="shrink-0 text-dark-text-muted" />
    <div class="relative min-w-0 flex-1">
      <select
        bind:value={selectedModel}
        aria-label="Model"
        disabled={loadingModels || models.length === 0 || streaming}
        class="h-9 w-full truncate border border-dark-border-subtle pl-2.5 pr-8 text-xs appearance-none bg-dark-surface text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent disabled:bg-dark-base disabled:text-dark-text-muted"
      >
        {#if loadingModels}
          <option value="">Loading…</option>
        {:else if models.length === 0}
          <option value="">No models available</option>
        {:else}
          {#each models as model}
            <option value={model}>{model}</option>
          {/each}
        {/if}
      </select>
      <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-dark-text-muted" />
    </div>
    <button
      onclick={clearChat}
      disabled={messages.length === 0 || streaming}
      aria-label="Clear conversation"
      title="Clear conversation"
      class="h-9 w-9 shrink-0 inline-flex items-center justify-center border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary hover:text-dark-text disabled:opacity-30 focus-visible:outline-2 focus-visible:outline-accent"
    >
      <Trash2 size={14} />
    </button>
    <button
      onclick={onclose}
      aria-label="Close AI assistant"
      title="Close"
      class="h-9 w-9 shrink-0 inline-flex items-center justify-center border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
    >
      <X size={14} />
    </button>
  </div>

  <!-- Messages -->
  <div bind:this={chatContainer} class="flex-1 overflow-y-auto min-h-0 px-3 py-4 space-y-4">
    {#if messages.length === 0}
      <div class="text-center py-12">
        <div class="text-sm text-dark-text-muted mb-1.5">Describe what to build or change</div>
        <div class="text-xs text-dark-text-muted">
          The assistant can add, update, move and connect steps.
          {#if selectedModel}
            <br />Using <code class="font-mono bg-dark-elevated px-1.5 py-0.5 text-dark-text-secondary">{selectedModel}</code>
          {/if}
        </div>
      </div>
    {/if}

    {#each messages as msg, i}
      {#if msg.role === 'user'}
        <div class="flex justify-end">
          <div class="max-w-[85%] px-3 py-2 text-sm leading-relaxed bg-[#2B2D42] text-white">
            <MessageContent message={msg} workspace={workspaceTransport.selected} {formatSize} />
          </div>
        </div>
      {:else if msg.role === 'assistant'}
        {@const hasText = !!getTextContent(msg.content).trim()}
        {@const thinking = streaming && i === messages.length - 1}
        {#if hasText || thinking || msg.tool_calls?.length}
          <div class="flex justify-start">
            <div class="min-w-0 w-full px-3 py-2 text-sm leading-relaxed bg-dark-elevated border border-dark-border-subtle shadow-sm text-dark-text">
              {#if hasText || (thinking && !msg.tool_calls?.length)}
                <MessageContent message={msg} workspace={workspaceTransport.selected} {thinking} {formatSize} />
              {/if}
              {#if msg.tool_calls && msg.tool_calls.length > 0}
                <div class={['space-y-1', hasText ? 'mt-2 pt-2 border-t border-dark-border' : '']}>
                  {#each msg.tool_calls as tc (tc.id)}
                    <ToolActivity
                      call={tc}
                      result={toolResults.get(i)?.get(tc.id)}
                      source="Canvas"
                      summary={summarizeWorkflowToolCall(tc.function.name, tc.function.arguments)}
                    />
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}
      {/if}
      <!-- Tool results are shown inside the originating call's card. -->
    {/each}
  </div>

  <!-- Composer -->
  <div class="border-t border-dark-border bg-dark-elevated px-3 py-3 shrink-0">
    <div class="flex items-end gap-2">
      <textarea
        bind:value={userInput}
        use:growComposer={userInput}
        onkeydown={handleKeydown}
        rows={1}
        aria-label="Message"
        placeholder={models.length === 0 ? 'No models available' : 'Describe changes…'}
        disabled={!selectedModel || streaming}
        class="min-w-0 min-h-10 max-h-[min(16rem,35dvh)] overflow-y-auto flex-1 border border-dark-border bg-dark-surface text-dark-text placeholder:text-dark-text-muted px-3 py-2 text-sm leading-[22px] resize-none focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle disabled:bg-dark-base disabled:text-dark-text-muted"
      ></textarea>
      {#if streaming}
        <button
          onclick={stopStreaming}
          title="Stop"
          aria-label="Stop response"
          class="inline-flex size-10 shrink-0 items-center justify-center bg-red-600 text-white hover:bg-red-700 focus-visible:outline-2 focus-visible:outline-accent"
        >
          <Square size={18} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={!userInput.trim() || !selectedModel}
          title="Send (Enter) — Shift+Enter for a new line"
          aria-label="Send message"
          class="inline-flex size-10 shrink-0 items-center justify-center bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-30 disabled:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent"
        >
          <Send size={18} />
        </button>
      {/if}
    </div>
  </div>
</div>
