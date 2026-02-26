<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import { testHandler } from '@/lib/api/skills';
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
  import type { SkillTool, Skill } from '@/lib/api/skills';
  import { listSkills, getSkill, createSkill, updateSkill } from '@/lib/api/skills';

  // ─── Props ───
  // The panel reads/writes the parent's form state via these reactive bindings.
  let {
    onclose,
    formName = $bindable(),
    formDescription = $bindable(),
    formSystemPrompt = $bindable(),
    formTools = $bindable(),
    editingId = $bindable(),
    showForm = $bindable(),
    onSaved,
  }: {
    onclose: () => void;
    formName: string;
    formDescription: string;
    formSystemPrompt: string;
    formTools: SkillTool[];
    editingId: string | null;
    showForm: boolean;
    onSaved: () => void;
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

  const skillTools: ToolDefinition[] = [
    {
      type: 'function',
      function: {
        name: 'get_current_skill',
        description: 'Get the current skill form state (name, description, system_prompt, tools)',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
    {
      type: 'function',
      function: {
        name: 'set_skill_metadata',
        description: 'Set the skill name, description, and/or system_prompt',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Skill name (snake_case recommended)' },
            description: { type: 'string', description: 'What the skill does' },
            system_prompt: { type: 'string', description: 'Instructions for the agent when using this skill' },
          },
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'add_tool',
        description: 'Add a new tool definition to the skill',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Tool name (snake_case)' },
            description: { type: 'string', description: 'What the tool does' },
            inputSchema: {
              type: 'object',
              description: 'JSON Schema for tool input parameters (type, properties, required)',
            },
            handler: { type: 'string', description: 'Handler code (JS function body or bash script)' },
            handler_type: {
              type: 'string',
              enum: ['js', 'bash'],
              description: 'Handler type: "js" (Goja VM with args object, httpGet/httpPost/getVar helpers) or "bash" (ARG_* and VAR_* env vars, use curl/jq)',
            },
          },
          required: ['name', 'description', 'handler', 'handler_type'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'update_tool',
        description: 'Update an existing tool by name (partial update — only provided fields are changed)',
        parameters: {
          type: 'object',
          properties: {
            name: { type: 'string', description: 'Name of the tool to update' },
            new_name: { type: 'string', description: 'New tool name (if renaming)' },
            description: { type: 'string', description: 'New description' },
            inputSchema: { type: 'object', description: 'New input schema' },
            handler: { type: 'string', description: 'New handler code' },
            handler_type: { type: 'string', enum: ['js', 'bash'], description: 'New handler type' },
          },
          required: ['name'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'remove_tool',
        description: 'Remove a tool by name',
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
        name: 'list_tools',
        description: 'List the names of all currently defined tools',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
    {
      type: 'function',
      function: {
        name: 'list_variables',
        description: 'List available variables (keys only, secret values redacted). Use to check if needed variables exist.',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
    {
      type: 'function',
      function: {
        name: 'test_tool_handler',
        description: 'Test a tool handler by running it server-side with sample arguments',
        parameters: {
          type: 'object',
          properties: {
            tool_name: { type: 'string', description: 'Name of the tool whose handler to test (must exist in current tools)' },
            arguments: { type: 'object', description: 'Sample arguments to pass to the handler' },
          },
          required: ['tool_name', 'arguments'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'save_skill',
        description: 'Save/create the skill via API. Uses the current form state.',
        parameters: { type: 'object', properties: {}, required: [] },
      },
    },
    {
      type: 'function',
      function: {
        name: 'load_skill',
        description: 'Load an existing skill into the form by name or ID',
        parameters: {
          type: 'object',
          properties: {
            name_or_id: { type: 'string', description: 'Skill name or ID to load' },
          },
          required: ['name_or_id'],
        },
      },
    },
  ];

  // ─── System Prompt ───

  const systemPrompt = `You are a Skill Builder AI assistant. You help users create and edit skills for the AT workflow automation platform.

## What is a Skill?
A skill is a reusable set of tool definitions that can be attached to agent_call workflow nodes. Each skill has:
- **name**: A unique identifier (snake_case recommended)
- **description**: What the skill does
- **system_prompt**: Instructions appended to the agent's system prompt when using this skill
- **tools**: An array of tool definitions, each with a name, description, inputSchema (JSON Schema), handler code, and handler_type

## Handler Types

### JS Handler (handler_type: "js")
- Runs in a Goja (Go) JavaScript VM — NOT Node.js
- Access tool arguments via the \`args\` object (e.g., \`args.query\`, \`args.url\`)
- Available helpers: \`httpGet(url)\`, \`httpPost(url, body)\`, \`httpPut(url, body)\`, \`httpDelete(url)\`, \`getVar(key)\`, \`btoa(str)\`, \`atob(str)\`, \`JSON_stringify(obj)\`, \`jsonParse(str)\`
- HTTP functions return \`{status, body, headers}\` — body is a string
- Use \`return\` to return the result (string or object, objects are auto-serialized to JSON)
- Example:
\`\`\`
var token = getVar("github_token");
var resp = httpGet("https://api.github.com/user");
var data = jsonParse(resp.body);
return data.login;
\`\`\`

### Bash Handler (handler_type: "bash")
- Runs as a bash script with a 60-second timeout
- Tool arguments are set as \`ARG_<NAME>\` environment variables (uppercased, dots/hyphens → underscores)
- All variables are set as \`VAR_<KEY>\` environment variables
- stdout is captured as the tool result
- Example:
\`\`\`
curl -s -H "Authorization: Bearer $VAR_GITHUB_TOKEN" \\
  "https://api.github.com/repos/$ARG_OWNER/$ARG_REPO/issues" | jq '.[0].title'
\`\`\`

## Input Schema
Tool input schemas use JSON Schema format:
\`\`\`json
{
  "type": "object",
  "properties": {
    "query": { "type": "string", "description": "Search query" },
    "limit": { "type": "number", "description": "Max results" }
  },
  "required": ["query"]
}
\`\`\`

## Workflow
1. Always start by calling \`get_current_skill\` to see the current state
2. Use \`set_skill_metadata\` to set the skill name, description, and system prompt
3. Use \`add_tool\` to add tools, \`update_tool\` to modify, \`remove_tool\` to delete
4. Use \`list_variables\` to check if required variables exist before writing handlers that need them
5. Use \`test_tool_handler\` to test handlers with sample data
6. Use \`save_skill\` when the user is satisfied with the result

## Important
- Always use get_current_skill first to understand the current form state
- When creating handlers that need variables, check available variables with list_variables
- Test handlers before saving to catch errors early
- Prefer JS handlers for API calls (simpler, no shell escaping). Use bash when the user needs shell tools like curl piping, jq, grep, etc.
- Keep system_prompt concise — it's appended to the agent's system prompt`;

  // ─── Tool Execution ───

  async function executeToolCall(name: string, args: Record<string, any>): Promise<string> {
    try {
      switch (name) {
        case 'get_current_skill': {
          return JSON.stringify({
            name: formName,
            description: formDescription,
            system_prompt: formSystemPrompt,
            editing_id: editingId,
            tools: formTools.map(t => ({
              name: t.name,
              description: t.description,
              inputSchema: t.inputSchema,
              handler: t.handler,
              handler_type: t.handler_type || 'js',
            })),
          }, null, 2);
        }

        case 'set_skill_metadata': {
          if (args.name !== undefined) formName = args.name;
          if (args.description !== undefined) formDescription = args.description;
          if (args.system_prompt !== undefined) formSystemPrompt = args.system_prompt;
          showForm = true;
          return JSON.stringify({ success: true, name: formName, description: formDescription });
        }

        case 'add_tool': {
          const existing = formTools.find(t => t.name === args.name);
          if (existing) {
            return JSON.stringify({ error: `Tool "${args.name}" already exists. Use update_tool to modify it.` });
          }
          formTools = [...formTools, {
            name: args.name,
            description: args.description || '',
            inputSchema: args.inputSchema || {},
            handler: args.handler || '',
            handler_type: args.handler_type || 'js',
          }];
          showForm = true;
          return JSON.stringify({ success: true, tool_count: formTools.length });
        }

        case 'update_tool': {
          const idx = formTools.findIndex(t => t.name === args.name);
          if (idx === -1) {
            return JSON.stringify({ error: `Tool "${args.name}" not found` });
          }
          const updated = { ...formTools[idx] };
          if (args.new_name !== undefined) updated.name = args.new_name;
          if (args.description !== undefined) updated.description = args.description;
          if (args.inputSchema !== undefined) updated.inputSchema = args.inputSchema;
          if (args.handler !== undefined) updated.handler = args.handler;
          if (args.handler_type !== undefined) updated.handler_type = args.handler_type;
          formTools[idx] = updated;
          formTools = [...formTools]; // trigger reactivity
          return JSON.stringify({ success: true });
        }

        case 'remove_tool': {
          const idx = formTools.findIndex(t => t.name === args.name);
          if (idx === -1) {
            return JSON.stringify({ error: `Tool "${args.name}" not found` });
          }
          formTools = formTools.filter((_, i) => i !== idx);
          return JSON.stringify({ success: true, tool_count: formTools.length });
        }

        case 'list_tools': {
          return JSON.stringify({
            tools: formTools.map(t => ({
              name: t.name,
              handler_type: t.handler_type || 'js',
            })),
            count: formTools.length,
          });
        }

        case 'list_variables': {
          try {
            const variables = await listVariables();
            return JSON.stringify({
              variables: variables.map(v => ({ key: v.key, description: v.description, secret: v.secret })),
              count: variables.length,
            });
          } catch (e: any) {
            return JSON.stringify({ error: e.message || 'Failed to list variables' });
          }
        }

        case 'test_tool_handler': {
          const tool = formTools.find(t => t.name === args.tool_name);
          if (!tool) {
            return JSON.stringify({ error: `Tool "${args.tool_name}" not found in current tools` });
          }
          if (!tool.handler) {
            return JSON.stringify({ error: `Tool "${args.tool_name}" has no handler code` });
          }
          try {
            const resp = await testHandler({
              handler: tool.handler,
              handler_type: tool.handler_type || 'js',
              arguments: args.arguments || {},
            });
            return JSON.stringify(resp);
          } catch (e: any) {
            return JSON.stringify({ error: e?.response?.data?.message || e.message || 'Test failed' });
          }
        }

        case 'save_skill': {
          if (!formName.trim()) {
            return JSON.stringify({ error: 'Skill name is required' });
          }
          try {
            const payload: Partial<Skill> = {
              name: formName.trim(),
              description: formDescription.trim(),
              system_prompt: formSystemPrompt,
              tools: formTools.filter(t => t.name.trim()),
            };
            let saved: Skill;
            if (editingId) {
              saved = await updateSkill(editingId, payload);
            } else {
              saved = await createSkill(payload);
              editingId = saved.id;
            }
            onSaved();
            return JSON.stringify({ success: true, id: saved.id, name: saved.name });
          } catch (e: any) {
            return JSON.stringify({ error: e?.response?.data?.message || e.message || 'Save failed' });
          }
        }

        case 'load_skill': {
          try {
            const allSkills = await listSkills();
            const found = allSkills.find(
              s => s.id === args.name_or_id || s.name === args.name_or_id
            );
            if (!found) {
              return JSON.stringify({ error: `Skill "${args.name_or_id}" not found` });
            }
            const full = await getSkill(found.id);
            formName = full.name;
            formDescription = full.description;
            formSystemPrompt = full.system_prompt;
            formTools = (full.tools || []).map(t => ({ ...t }));
            editingId = full.id;
            showForm = true;
            return JSON.stringify({ success: true, id: full.id, name: full.name, tool_count: formTools.length });
          } catch (e: any) {
            return JSON.stringify({ error: e?.response?.data?.message || e.message || 'Load failed' });
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
          tools: skillTools,
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

<div class="w-80 bg-white border-l border-gray-200 shrink-0 min-h-0 flex flex-col">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 border-b border-gray-200 shrink-0">
    <div class="flex items-center gap-1.5">
      <Bot size={14} class="text-gray-500" />
      <span class="text-xs font-medium text-gray-700">Skill Builder AI</span>
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
        <p>Describe the skill you want to create or modify.</p>
        <p class="mt-1">I can generate tools, write handlers, test them, and save the skill.</p>
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
        placeholder="Describe a skill..."
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
