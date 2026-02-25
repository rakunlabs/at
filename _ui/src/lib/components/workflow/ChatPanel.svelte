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
  import { getFlow, type FlowState, type FlowNode, type FlowEdge } from 'kaykay';
  import { Send, Square, X, ChevronDown, Bot } from 'lucide-svelte';

  // ─── Props ───
  let { onclose }: { onclose: () => void } = $props();

  // ─── State ───
  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let messages = $state<ChatMessage[]>([]);
  let userInput = $state('');
  let streaming = $state(false);
  let abortController = $state<AbortController | null>(null);
  let chatContainer: HTMLDivElement | undefined = $state();
  let loadingModels = $state(true);

  // ─── Flow access ───
  const flow: FlowState = getFlow();

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

  // ─── Tool Definitions ───

  const flowTools: ToolDefinition[] = [
    {
      type: 'function',
      function: {
        name: 'get_flow',
        description: 'Get the current workflow flow state (all nodes and edges)',
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
              enum: ['input', 'output', 'llm_call', 'prompt_template', 'http_trigger', 'cron_trigger', 'http_request', 'conditional', 'loop', 'script'],
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
              description: 'Node-specific configuration. Must include "label" field.',
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

  // ─── System Prompt ───

  const systemPrompt = `You are a workflow editor AI assistant. You help users build and modify visual node-based workflows.

## Available Node Types

Each node has specific input/output handles (ports) for connecting edges.

### input
- Output handles: data (port: data)
- Data fields: label, fields (array of field names)

### output
- Input handles: data (port: data, accepts: data, text, llm_response)
- Data fields: label, fields (array of field names)

### http_trigger
- Output handles: data (port: data)
- Data fields: label, trigger_id (auto-assigned on save)
- Webhook receives: method, path, query, headers, body (as reader)

### cron_trigger
- Output handles: data (port: data)
- Data fields: label, schedule (cron expression, e.g. "*/5 * * * *"), payload (object)

### llm_call
- Input handles: prompt (port: text), context (port: data)
- Output handles: response (port: llm_response), text (port: text)
- Data fields: label, provider (provider key), model (model name), system_prompt

### prompt_template
- Input handles: data (port: data)
- Output handles: text (port: text)
- Data fields: label, template (Go template string with {{.var}}), variables (array of var names)

### http_request
- Input handles: values (port: data, index 0), data (port: data, index 1)
- Output handles: success (port: data, 2xx responses), error (port: data, >=400), always (port: data)
- Data fields: label, url, method, headers (object), body, timeout (seconds), proxy, insecure_skip_verify (bool), retry (bool)
- URL and headers support Go templates with data from "values" input

### conditional
- Input handles: data (port: data)
- Output handles: true (port: data), false (port: data)
- Data fields: label, expression (JavaScript expression evaluating to true/false, access input as "data")

### loop
- Input handles: data (port: data)
- Output handles: item (port: data)
- Data fields: label, expression (JavaScript expression returning an array, access input as "data")

### script
- Input handles: When input_count=1: data (port: data). When input_count>1: data1, data2, ... dataN (port: data)
- Output handles: true (port: data), false (port: data), always (port: data)
- Data fields: label, code (JavaScript code using return), input_count (1-10)
- Code is wrapped in IIFE: use "return { ... }" to set result. Truthy result -> true port, falsy -> false port.

## Edge Connection Rules
- Edges connect a source output handle to a target input handle
- Handle IDs follow snake_case naming
- source_handle must be an output handle of the source node
- target_handle must be an input handle of the target node
- Edge IDs should be formatted as "source_id-source_handle-target_id-target_handle"

## Positioning Guidelines
- Place nodes with ~200px horizontal spacing and ~150px vertical spacing
- Keep related nodes close together
- Flow generally goes left-to-right or top-to-bottom

## Important
- Always use get_flow first to understand the current state before making changes
- Use meaningful node IDs that reflect the node's purpose (e.g., "fetch_users", "check_status")
- Always include a "label" field in node data
- When connecting nodes, verify handle IDs match the node type's defined handles`;

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
          flow.addNode({
            id: nodeId,
            type,
            position: { x: position.x, y: position.y },
            data: data || {},
          });
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
          // Push directly to edges array to bypass handle validation
          // (handles may not be mounted yet if node was just added).
          flow.edges.push({
            id: edgeId,
            source,
            source_handle,
            target,
            target_handle,
          });
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

  async function runCompletion() {
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
        await runCompletion();
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

<div class="w-80 bg-white border-l border-gray-200 shrink-0 min-h-0 flex flex-col">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 shrink-0">
    <div class="flex items-center gap-1.5">
      <Bot size={14} class="text-gray-500" />
      <span class="text-xs font-medium text-gray-700">AI Assistant</span>
    </div>
    <button onclick={onclose} class="text-gray-400 hover:text-gray-600">
      <X size={14} />
    </button>
  </div>

  <!-- Model selector -->
  <div class="px-3 py-2 border-b border-gray-200 shrink-0">
    <div class="relative">
      <select
        bind:value={selectedModel}
        disabled={loadingModels || models.length === 0}
        class="w-full appearance-none px-2 py-1 pr-6 text-[11px] border border-gray-300 rounded bg-white focus:outline-none focus:ring-1 focus:ring-gray-400 disabled:opacity-50 disabled:bg-gray-50"
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
      <ChevronDown size={12} class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 pointer-events-none" />
    </div>
  </div>

  <!-- Messages -->
  <div bind:this={chatContainer} class="flex-1 overflow-y-auto min-h-0 p-3 space-y-3">
    {#if messages.length === 0}
      <div class="text-center text-[11px] text-gray-400 mt-8">
        <Bot size={24} class="mx-auto mb-2 text-gray-300" />
        <p>Describe what you want to build or change in the workflow.</p>
        <p class="mt-1">The AI can add, remove, update, and connect nodes.</p>
      </div>
    {/if}

    {#each messages as msg, i}
      {#if msg.role === 'user'}
        <div class="flex justify-end">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-900 text-white text-[11px] whitespace-pre-wrap">
            {getTextContent(msg.content)}
          </div>
        </div>
      {:else if msg.role === 'assistant'}
        <div class="flex justify-start">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-50 border border-gray-200 text-[11px]">
            {#if getTextContent(msg.content)}
              <span class="whitespace-pre-wrap text-gray-700">{getTextContent(msg.content)}</span>
            {:else if streaming && i === messages.length - 1}
              <span class="text-gray-400 italic">Thinking...</span>
            {/if}
            {#if msg.tool_calls && msg.tool_calls.length > 0}
              <div class="mt-1.5 pt-1.5 border-t border-gray-200">
                {#each msg.tool_calls as tc}
                  <div class="flex items-center gap-1 text-[10px] text-gray-500">
                    <span class="inline-block w-1.5 h-1.5 rounded-full bg-blue-400"></span>
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
  <div class="px-3 py-2 border-t border-gray-200 shrink-0">
    <div class="flex items-end gap-1.5">
      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        rows={1}
        class="flex-1 px-2 py-1.5 text-[11px] border border-gray-300 rounded resize-none focus:outline-none focus:ring-1 focus:ring-gray-400"
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
          class="p-1.5 rounded bg-gray-900 text-white hover:bg-gray-800 disabled:opacity-30 disabled:cursor-not-allowed transition-colors shrink-0"
        >
          <Send size={12} />
        </button>
      {/if}
    </div>
    {#if messages.length > 0 && !streaming}
      <button
        onclick={clearChat}
        class="mt-1.5 w-full text-[10px] text-gray-400 hover:text-gray-600 transition-colors"
      >
        Clear conversation
      </button>
    {/if}
  </div>
</div>
