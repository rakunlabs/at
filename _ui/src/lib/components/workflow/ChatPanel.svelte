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
  import { listVariables } from '@/lib/api/secrets';
  import { listNodeConfigs } from '@/lib/api/node-configs';
  import { getNodeTypes, type NodeTypeMeta, type PortMeta, type FieldMeta } from '@/lib/api/workflows';
  import { Send, Square, X, ChevronDown, Bot } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';

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

  // ─── Constants ───
  const MAX_TOOL_ITERATIONS = 20;

  // ─── Load models ───
  async function loadModels() {
    loadingModels = true;
    try {
      const info = await getInfo();
      const allModels: string[] = [];
      for (const p of info.providers) {
        if (p.models && p.models.length > 0) {
          for (const m of p.models) {
            allModels.push(`${p.key}/${m}`);
          }
        } else if (p.default_model) {
          allModels.push(`${p.key}/${p.default_model}`);
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

  // Frontend-only node types that don't have backend Meta() implementations.
  const FRONTEND_ONLY_TYPES = ['http_trigger', 'cron_trigger', 'group', 'sticky_note'];

  const flowTools: ToolDefinition[] = $derived.by(() => {
    // Build the dynamic enum of valid node types.
    const typeNames = nodeTypeMetas.map(m => m.type);
    for (const ft of FRONTEND_ONLY_TYPES) {
      if (!typeNames.includes(ft)) typeNames.push(ft);
    }

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
                enum: typeNames.length > 0 ? typeNames : undefined,
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
        key: p.key,
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

  loadSkills();
  loadVariables();
  loadNodeConfigs();

  // Get a live snapshot of the current workflow for the system prompt
  function getCurrentFlowSummary(): string {
    try {
      const json = flow.toJSON();
      const nodes = json.nodes || [];
      const edges = json.edges || [];
      if (nodes.length === 0) return 'The workflow is currently empty.';
      const nodeList = nodes.map((n: any) => `- ${n.id} (${n.type}): "${n.data?.label || n.data?.text || ''}"`)
      return `Current workflow has ${nodes.length} nodes and ${edges.length} edges:\n${nodeList.join('\n')}`;
    } catch {
      return 'Unable to read current workflow state.';
    }
  }

  // ─── Dynamic System Prompt ───

  /** Generate the "Available Node Types" section from backend metadata. */
  function buildNodeTypesDoc(): string {
    if (nodeTypeMetas.length === 0) return 'Loading node types...\n';

    let doc = '';
    for (const meta of nodeTypeMetas) {
      doc += `### ${meta.type}\n`;
      doc += `${meta.description}\n`;

      if (meta.inputs && meta.inputs.length > 0) {
        const handles = meta.inputs.map((p: PortMeta) => {
          let s = `id="${p.name}" (port: ${p.type}`;
          if (p.accept?.length) s += `, accepts: ${p.accept.join(', ')}`;
          if (p.position && p.position !== 'left') s += `, position: ${p.position}`;
          s += ')';
          return s;
        });
        doc += `- Input handles: ${handles.join(', ')}\n`;
      }

      if (meta.outputs && meta.outputs.length > 0) {
        const handles = meta.outputs.map((p: PortMeta) => {
          let s = `id="${p.name}" (port: ${p.type}`;
          if (p.position && p.position !== 'right') s += `, position: ${p.position}`;
          s += ')';
          return s;
        });
        doc += `- Output handles: ${handles.join(', ')}\n`;
      }

      if (meta.fields && meta.fields.length > 0) {
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

      doc += '\n';
    }

    // Frontend-only node types (no backend Noder).
    doc += `### http_trigger
Webhook trigger endpoint
- Output handles: id="output" (port: data)
- Data fields: label [required], trigger_id (auto-assigned on save), alias (optional URL path), public (boolean, skip auth)

### cron_trigger
Cron schedule trigger
- Output handles: id="output" (port: data)
- Data fields: label [required], schedule (cron expression e.g. "*/5 * * * *"), timezone (IANA e.g. "America/New_York"), payload (object)

### group
Visual grouping container (no handles)
- Data fields: label [required], color (CSS hex, default "#22c55e")
- When adding, also set style: { width: 400, height: 300 }

### sticky_note
Markdown annotation (no handles)
- Data fields: text (markdown content), color (CSS hex, default "#fef08a")
- NOTE: uses "text" instead of "label". Do NOT include a "label" field.
- When adding, also set style: { width: 200, height: 150 }
`;

    return doc;
  }

  /** Build default node data from metadata Fields, falling back to zero values. */
  function defaultNodeData(type: string): Record<string, any> {
    // Frontend-only types with static defaults.
    switch (type) {
      case 'http_trigger': return { label: 'HTTP Trigger', trigger_id: '', alias: '', public: false };
      case 'cron_trigger': return { label: 'Cron Trigger', schedule: '', timezone: '', payload: {} };
      case 'group': return { label: 'Group', color: '#22c55e' };
      case 'sticky_note': return { text: 'Double-click to edit...', color: '#fef08a' };
    }

    // Look up metadata and build defaults from Fields.
    const meta = nodeTypeMetas.find(m => m.type === type);
    if (!meta || !meta.fields?.length) return { label: type };

    const data: Record<string, any> = {};
    for (const field of meta.fields) {
      if (field.default !== undefined && field.default !== null) {
        data[field.name] = field.default;
      } else {
        // Zero-value by type.
        switch (field.type) {
          case 'string':  data[field.name] = ''; break;
          case 'number':  data[field.name] = 0; break;
          case 'boolean': data[field.name] = false; break;
          case 'array':   data[field.name] = []; break;
          case 'object':  data[field.name] = {}; break;
          default:        data[field.name] = ''; break;
        }
      }
    }

    // If no label was set from fields, use the meta label.
    if (!data.label && type !== 'sticky_note') {
      data.label = meta.label;
    }

    return data;
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

When creating llm_call, agent_call, or media nodes, use the provider key for the "provider" field and the model name for the "model" field from the list above.

## Available Skills
${skillsInfo.length > 0 ? skillsInfo.map(s => `- "${s.name}": ${s.description}`).join('\n') : '- No skills configured yet'}

When creating skill_config nodes, use skill names from this list in the "skills" array.

## Available Variables
${variablesInfo.length > 0 ? variablesInfo.map(v => `- "${v.key}"${v.description ? ': ' + v.description : ''}`).join('\n') : '- No variables configured yet'}

## Available Node Configs
${nodeConfigsInfo.length > 0 ? nodeConfigsInfo.map(c => `- id="${c.id}" name="${c.name}" type="${c.type}"`).join('\n') : '- No node configs configured yet'}

## Edge Connection Rules
- Edges connect a source output handle to a target input handle
- The source_handle and target_handle values must be the handle "id" (not the port or label)
- Edge IDs should be formatted as "source_id-source_handle-target_id-target_handle"

## Positioning Guidelines
- Place nodes with ~200px horizontal spacing and ~150px vertical spacing
- Keep related nodes close together
- Flow generally goes left-to-right or top-to-bottom
- Resource config nodes (skill_config, mcp_config, memory_config) should be placed BELOW the agent_call node they connect to

## Important
- Always use get_flow first to understand the current state before making changes
- Use meaningful node IDs that reflect the node's purpose
- Always include a "label" field in node data (except sticky_note which uses "text")
- group and sticky_note nodes are visual-only; they have no handles and cannot be connected with edges`);

  // ─── Tool Execution ───

  let nodeIdCounter = 0;

  function executeToolCall(name: string, args: Record<string, any>): string {
    try {
      switch (name) {
        case 'get_flow': {
          const json = flow.toJSON();
          return JSON.stringify(json, null, 2);
        }

        case 'add_node': {
          const { type, position, data, id } = args;
          nodeIdCounter++;
          const nodeId = id || `${type}_ai_${nodeIdCounter}`;
          const defaults = defaultNodeData(type);
          const nodeData = { ...defaults, ...(data || {}) };
          const nodeOpts: any = {
            id: nodeId,
            type,
            position: { x: position.x, y: position.y },
            data: nodeData,
          };
          // Visual-only nodes need explicit dimensions
          if (type === 'group') {
            nodeOpts.style = { width: 400, height: 300 };
          } else if (type === 'sticky_note') {
            nodeOpts.style = { width: 200, height: 150 };
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
            target_handle,
          });
          if (!added) {
            return JSON.stringify({ error: `Failed to add edge. Verify that source node "${source}" has output handle "${source_handle}" and target node "${target}" has input handle "${target_handle}". Check handle IDs match exactly.` });
          }
          return JSON.stringify({ success: true, id: edgeId });
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
          let args: Record<string, any> = {};
          try {
            args = JSON.parse(tc.function.arguments);
          } catch {
            // If args don't parse, pass empty
          }
          const result = executeToolCall(tc.function.name, args);

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
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="w-80 h-full bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col"
  onmousedown={(e) => e.stopPropagation()}
  onwheel={(e) => e.stopPropagation()}
  onkeydown={(e) => e.stopPropagation()}
>
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
    <div class="flex items-center gap-1.5">
      <Bot size={14} class="text-gray-500 dark:text-dark-text-muted" />
      <span class="text-xs font-medium text-gray-700 dark:text-dark-text">AI Assistant</span>
    </div>
    <button onclick={onclose} class="text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text">
      <X size={14} />
    </button>
  </div>

  <!-- Model selector -->
  <div class="px-3 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
    <div class="relative">
      <select
        bind:value={selectedModel}
        disabled={loadingModels || models.length === 0}
        class="w-full appearance-none px-2 py-1 pr-6 text-[11px] border border-gray-300 dark:border-dark-border-subtle rounded bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/50 disabled:opacity-50 disabled:bg-gray-50 dark:disabled:bg-dark-base"
      >
        {#if loadingModels}
          <option value="">Loading...</option>
        {:else if models.length === 0}
          <option value="">No models available</option>
        {:else}
          {#each models as model}
            <option value={model}>{model}</option>
          {/each}
        {/if}
      </select>
      <ChevronDown size={12} class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted pointer-events-none" />
    </div>
  </div>

  <!-- Messages -->
  <div bind:this={chatContainer} class="flex-1 overflow-y-auto min-h-0 p-3 space-y-3">
    {#if messages.length === 0}
      <div class="text-center text-[11px] text-gray-400 dark:text-dark-text-muted mt-8">
        <Bot size={24} class="mx-auto mb-2 text-gray-300 dark:text-dark-text-muted" />
        <p>Describe what you want to build or change in the workflow.</p>
        <p class="mt-1">The AI can add, remove, update, and connect nodes.</p>
      </div>
    {/if}

    {#each messages as msg, i}
      {#if msg.role === 'user'}
        <div class="flex justify-end">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-900 dark:bg-accent/80 text-white text-[11px] whitespace-pre-wrap">
            {getTextContent(msg.content)}
          </div>
        </div>
      {:else if msg.role === 'assistant'}
        <div class="flex justify-start">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border-subtle text-[11px]">
            {#if getTextContent(msg.content)}
              <Markdown source={getTextContent(msg.content)} class="text-gray-700 dark:text-dark-text" />
            {:else if streaming && i === messages.length - 1}
              <span class="text-gray-400 dark:text-dark-text-muted italic">Thinking...</span>
            {/if}
            {#if msg.tool_calls && msg.tool_calls.length > 0}
              <div class="mt-1.5 pt-1.5 border-t border-gray-200 dark:border-dark-border-subtle">
                {#each msg.tool_calls as tc}
                  <div class="flex items-center gap-1 text-[10px] text-gray-500 dark:text-dark-text-muted">
                    <span class="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 dark:bg-accent"></span>
                    <span class="font-mono">{tc.function.name}</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      {/if}
      <!-- tool messages are hidden (internal) -->
    {/each}
  </div>

  <!-- Input area -->
  <div class="px-3 py-2 border-t border-gray-200 dark:border-dark-border shrink-0">
    <div class="flex items-end gap-1.5">
      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        rows={1}
        class="flex-1 px-2 py-1.5 text-[11px] border border-gray-300 dark:border-dark-border-subtle rounded resize-none bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/50 placeholder:text-gray-400 dark:placeholder:text-dark-text-muted"
        placeholder="Describe changes..."
        disabled={!selectedModel || streaming}
      ></textarea>
      {#if streaming}
        <button
          onclick={stopStreaming}
          class="p-1.5 rounded bg-red-500 text-white hover:bg-red-600 transition-colors shrink-0"
        >
          <Square size={12} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={!userInput.trim() || !selectedModel}
          class="p-1.5 rounded bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent/80 disabled:opacity-30 disabled:cursor-not-allowed transition-colors shrink-0"
        >
          <Send size={12} />
        </button>
      {/if}
    </div>
    {#if messages.length > 0 && !streaming}
      <button
        onclick={clearChat}
        class="mt-1.5 w-full text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text transition-colors"
      >
        Clear conversation
      </button>
    {/if}
  </div>
</div>

<!-- Markdown typography is provided globally via `.markdown-body` rules in
     src/style/global.css. No component-local overrides needed. -->
