<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { listVariables } from '@/lib/api/secrets';
  import {
    type ChatMessage,
    type ToolCall,
    type ToolDefinition,
    getTextContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { Send, Square, X, ChevronDown, Bot } from 'lucide-svelte';
  import type { MCPHTTPTool } from '@/lib/api/mcp-servers';

  // ─── Props ───
  let {
    onclose,
    formHTTPTools = $bindable(),
  }: {
    onclose: () => void;
    formHTTPTools: MCPHTTPTool[];
  } = $props();

  // ─── State ───
  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let messages = $state<ChatMessage[]>([]);
  let userInput = $state('');
  let streaming = $state(false);
  let abortController: AbortController | null = null;
  let chatContainer: HTMLDivElement | undefined = $state();
  let loadingModels = $state(true);

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
    } catch {
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

  const builderTools: ToolDefinition[] = [
    {
      type: 'function',
      function: {
        name: 'get_current_tools',
        description: 'Get the current list of HTTP tools configured in the form',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
    {
      type: 'function',
      function: {
        name: 'add_http_tool',
        description: 'Add a new HTTP tool definition',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Tool name (snake_case recommended)' },
            description: { type: 'string', description: 'What the tool does' },
            method: { type: 'string', enum: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD'], description: 'HTTP method' },
            url: { type: 'string', description: 'URL template (supports Go template syntax e.g. {{.id}} and {{var:key}} for variables)' },
            headers: { type: 'object', description: 'Request headers as key-value pairs. Use {{var:key}} to reference stored variables.' },
            body_template: { type: 'string', description: 'Body template for POST/PUT/PATCH (Go template syntax)' },
            input_schema: {
              type: 'object',
              description: 'JSON Schema for tool input parameters (type, properties, required)',
            },
          },
          required: ['name', 'description', 'method', 'url'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'update_http_tool',
        description: 'Update an existing HTTP tool by name (partial update — only provided fields are changed)',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Name of the tool to update' },
            new_name: { type: 'string', description: 'New tool name (if renaming)' },
            description: { type: 'string', description: 'New description' },
            method: { type: 'string', enum: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD'] },
            url: { type: 'string', description: 'New URL template' },
            headers: { type: 'object', description: 'New headers (replaces all headers)' },
            body_template: { type: 'string', description: 'New body template' },
            input_schema: { type: 'object', description: 'New input schema' },
          },
          required: ['name'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'remove_http_tool',
        description: 'Remove an HTTP tool by name',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Name of the tool to remove' },
          },
          required: ['name'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'list_variables',
        description: 'List available variables (keys only, secret values redacted). Use to check if needed variables exist for {{var:key}} references.',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
  ];

  // ─── System Prompt ───

  const systemPrompt = `You are an HTTP Tool Builder AI assistant. You help users create and edit HTTP tools for MCP server endpoints.

## What is an HTTP Tool?
An HTTP tool defines an API call that can be invoked as an MCP tool. Each tool has:
- **name**: A unique identifier (snake_case recommended, e.g., get_user, create_issue)
- **description**: What the tool does (shown to the LLM calling the tool)
- **method**: HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD)
- **url**: URL template — supports Go template syntax with tool arguments (e.g., \`https://api.example.com/users/{{.user_id}}\`)
- **headers**: Request headers as key-value pairs
- **body_template**: Body template for POST/PUT/PATCH requests (Go template syntax)
- **input_schema**: JSON Schema defining what arguments the tool accepts

## Template Syntax
- \`{{.arg_name}}\` — references a tool argument from input_schema
- \`{{var:key}}\` — references a stored variable (for secrets like API keys)

## Examples

### GET with path parameter
- URL: \`https://api.github.com/repos/{{.owner}}/{{.repo}}\`
- Headers: \`{"Authorization": "Bearer {{var:github_token}}"}\`
- Input Schema: \`{"type": "object", "properties": {"owner": {"type": "string"}, "repo": {"type": "string"}}, "required": ["owner", "repo"]}\`

### POST with body
- URL: \`https://api.example.com/items\`
- Body: \`{"name": "{{.name}}", "description": "{{.description}}"}\`
- Input Schema: \`{"type": "object", "properties": {"name": {"type": "string"}, "description": {"type": "string"}}, "required": ["name"]}\`

## Workflow
1. Call \`get_current_tools\` to see existing tools
2. Use \`add_http_tool\` to create new tools
3. Use \`update_http_tool\` to modify existing tools
4. Use \`list_variables\` to check available variables for authentication headers
5. Always include a proper input_schema so the LLM knows what arguments to provide

## Important
- Always check current tools first with get_current_tools
- Use {{var:key}} for secrets/tokens in headers — never hardcode secrets
- Check available variables with list_variables before referencing them
- input_schema should follow JSON Schema format with type, properties, and required fields
- Description should clearly explain what the tool does and what it returns`;

  // ─── Tool Execution ───

  async function executeToolCall(name: string, args: Record<string, any>): Promise<string> {
    try {
      switch (name) {
        case 'get_current_tools': {
          return JSON.stringify({
            tools: formHTTPTools.map(t => ({
              name: t.name,
              description: t.description,
              method: t.method,
              url: t.url,
              headers: t.headers,
              body_template: t.body_template,
              input_schema: t.input_schema,
            })),
            count: formHTTPTools.length,
          }, null, 2);
        }

        case 'add_http_tool': {
          const existing = formHTTPTools.find(t => t.name === args.name);
          if (existing) {
            return JSON.stringify({ error: `Tool "${args.name}" already exists. Use update_http_tool to modify it.` });
          }
          formHTTPTools = [...formHTTPTools, {
            name: args.name,
            description: args.description || '',
            method: args.method || 'GET',
            url: args.url || '',
            headers: args.headers || {},
            body_template: args.body_template || '',
            input_schema: args.input_schema || { type: 'object', properties: {} },
          }];
          return JSON.stringify({ success: true, tool_count: formHTTPTools.length });
        }

        case 'update_http_tool': {
          const idx = formHTTPTools.findIndex(t => t.name === args.name);
          if (idx === -1) {
            return JSON.stringify({ error: `Tool "${args.name}" not found` });
          }
          const updated = { ...formHTTPTools[idx] };
          if (args.new_name !== undefined) updated.name = args.new_name;
          if (args.description !== undefined) updated.description = args.description;
          if (args.method !== undefined) updated.method = args.method;
          if (args.url !== undefined) updated.url = args.url;
          if (args.headers !== undefined) updated.headers = args.headers;
          if (args.body_template !== undefined) updated.body_template = args.body_template;
          if (args.input_schema !== undefined) updated.input_schema = args.input_schema;
          formHTTPTools[idx] = updated;
          formHTTPTools = [...formHTTPTools];
          return JSON.stringify({ success: true });
        }

        case 'remove_http_tool': {
          const idx = formHTTPTools.findIndex(t => t.name === args.name);
          if (idx === -1) {
            return JSON.stringify({ error: `Tool "${args.name}" not found` });
          }
          formHTTPTools = formHTTPTools.filter((_, i) => i !== idx);
          return JSON.stringify({ success: true, tool_count: formHTTPTools.length });
        }

        case 'list_variables': {
          try {
            const res = await listVariables();
            const variables = res.data;
            return JSON.stringify({
              variables: variables.map(v => ({ key: v.key, description: v.description, secret: v.secret })),
              count: variables.length,
            });
          } catch (e: any) {
            return JSON.stringify({ error: e.message || 'Failed to list variables' });
          }
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

    messages = [...messages, { role: 'user', content: text }];
    userInput = '';
    scrollToBottom();

    await runCompletion();
  }

  async function runCompletion() {
    const reqMessages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string }> = [];
    reqMessages.push({ role: 'system', content: systemPrompt });

    for (const m of messages) {
      const msg: any = { role: m.role, content: m.content };
      if (m.tool_calls) msg.tool_calls = m.tool_calls;
      if (m.tool_call_id) msg.tool_call_id = m.tool_call_id;
      reqMessages.push(msg);
    }

    messages = [...messages, { role: 'assistant', content: '' }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    let pendingToolCalls: ToolCall[] = [];

    try {
      await streamChatCompletion(
        'api/v1/chat/completions',
        {
          model: selectedModel,
          messages: reqMessages,
          tools: builderTools,
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

      if (pendingToolCalls.length > 0) {
        const lastIdx = messages.length - 1;
        messages[lastIdx] = { ...messages[lastIdx], tool_calls: pendingToolCalls };

        for (const tc of pendingToolCalls) {
          let tcArgs: Record<string, any> = {};
          try {
            tcArgs = JSON.parse(tc.function.arguments);
          } catch {
            // pass empty on parse failure
          }
          const result = await executeToolCall(tc.function.name, tcArgs);

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

        streaming = false;
        abortController = null;

        await runCompletion();
        return;
      }
    } catch (e: any) {
      if (e.name !== 'AbortError') {
        addToast(e.message || 'Chat request failed', 'alert');
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

<div class="w-80 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
    <div class="flex items-center gap-1.5">
      <Bot size={14} class="text-gray-500 dark:text-dark-text-muted" />
      <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">HTTP Tool Builder</span>
    </div>
    <button onclick={onclose} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
      <X size={14} />
    </button>
  </div>

  <!-- Model selector -->
  <div class="px-3 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
    <div class="relative">
      <select
        bind:value={selectedModel}
        disabled={loadingModels || models.length === 0}
        class="w-full appearance-none px-2 py-1 pr-6 text-[11px] border border-gray-300 dark:border-dark-border-subtle rounded bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 disabled:opacity-50 disabled:bg-gray-50 dark:disabled:bg-dark-surface"
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
      <ChevronDown size={12} class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-faint pointer-events-none" />
    </div>
  </div>

  <!-- Messages -->
  <div bind:this={chatContainer} class="flex-1 overflow-y-auto min-h-0 p-3 space-y-3">
    {#if messages.length === 0}
      <div class="text-center text-[11px] text-gray-400 dark:text-dark-text-faint mt-8">
        <Bot size={24} class="mx-auto mb-2 text-gray-300 dark:text-dark-text-faint" />
        <p>Describe the HTTP tools you want to create.</p>
        <p class="mt-1">I can generate API tool definitions with URLs, headers, and schemas.</p>
      </div>
    {/if}

    {#each messages as msg, i}
      {#if msg.role === 'user'}
        <div class="flex justify-end">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-900 dark:bg-accent text-white text-[11px] whitespace-pre-wrap">
            {getTextContent(msg.content)}
          </div>
        </div>
      {:else if msg.role === 'assistant'}
        <div class="flex justify-start">
          <div class="max-w-[85%] px-2.5 py-1.5 rounded-lg bg-gray-50 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-[11px]">
            {#if getTextContent(msg.content)}
              <span class="whitespace-pre-wrap text-gray-700 dark:text-dark-text-secondary">{getTextContent(msg.content)}</span>
            {:else if streaming && i === messages.length - 1}
              <span class="text-gray-400 dark:text-dark-text-faint italic">Thinking...</span>
            {/if}
            {#if msg.tool_calls && msg.tool_calls.length > 0}
              <div class="mt-1.5 pt-1.5 border-t border-gray-200 dark:border-dark-border">
                {#each msg.tool_calls as tc}
                  <div class="flex items-center gap-1 text-[10px] text-gray-500 dark:text-dark-text-muted">
                    <span class="inline-block w-1.5 h-1.5 rounded-full bg-green-400"></span>
                    <span class="font-mono">{tc.function.name}</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </div>
      {/if}
    {/each}
  </div>

  <!-- Input area -->
  <div class="px-3 py-2 border-t border-gray-200 dark:border-dark-border shrink-0">
    <div class="flex items-end gap-1.5">
      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        rows={1}
        class="flex-1 px-2 py-1.5 text-[11px] border border-gray-300 dark:border-dark-border-subtle rounded resize-none focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text"
        placeholder="Describe HTTP tools to create..."
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
          class="p-1.5 rounded bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 disabled:cursor-not-allowed transition-colors shrink-0"
        >
          <Send size={12} />
        </button>
      {/if}
    </div>
    {#if messages.length > 0 && !streaming}
      <button
        onclick={clearChat}
        class="mt-1.5 w-full text-[10px] text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
      >
        Clear conversation
      </button>
    {/if}
  </div>
</div>
