<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import {
    type ContentPart,
    type ChatMessage,
    type ToolCall,
    type ToolDefinition,
    type ChatUsage,
    getTextContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { listMCPTools, callMCPTool, callSkillTool, listBuiltinTools, callBuiltinTool, listRAGTools, callRAGTool, type MCPToolInfo, type BuiltinToolDef, type RAGToolDef } from '@/lib/api/mcp';
  import { listCollections, type RAGCollection } from '@/lib/api/rag';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { Send, Trash2, ChevronDown, Square, Settings, ImagePlus, X, RotateCcw, Wrench, Plus, Loader2, ListChecks, MessageCircleQuestion } from 'lucide-svelte';
  import { md, renderMarkdown } from '@/lib/helper/markdown';

  storeNavbar.title = 'Chat';

  // ─── Types ───

  interface PendingImage {
    name: string;
    dataUrl: string;
  }

  /** Maps a tool name to its source for dispatch. */
  interface ToolSource {
    type: 'mcp' | 'skill' | 'builtin' | 'frontend' | 'rag';
    /** MCP server URL (when type === 'mcp') */
    serverUrl?: string;
    /** Skill name (when type === 'skill') */
    skillName?: string;
  }

  interface TodoItem {
    content: string;
    status: 'pending' | 'in_progress' | 'completed' | 'cancelled';
    priority: 'high' | 'medium' | 'low';
  }

  interface PendingQuestion {
    question: string;
    header?: string;
    options: Array<{ label: string; description?: string }>;
    multiple?: boolean;
    custom?: boolean;
    resolve: (answer: string) => void;
  }

  // ─── Constants ───

  const MAX_TOOL_ITERATIONS = 20;

  /** Frontend-only tool definitions (run entirely in the browser). */
  const FRONTEND_TOOLS: ToolDefinition[] = [
    {
      type: 'function',
      function: {
        name: 'todo_write',
        description: 'Create or update a structured todo list. Replaces the entire list with the provided items. Each item has content (description), status (pending/in_progress/completed/cancelled), and priority (high/medium/low).',
        parameters: {
          type: 'object',
          properties: {
            todos: {
              type: 'array',
              description: 'The updated todo list',
              items: {
                type: 'object',
                properties: {
                  content: { type: 'string', description: 'Brief description of the task' },
                  status: { type: 'string', enum: ['pending', 'in_progress', 'completed', 'cancelled'], description: 'Current status' },
                  priority: { type: 'string', enum: ['high', 'medium', 'low'], description: 'Priority level' },
                },
                required: ['content', 'status', 'priority'],
              },
            },
          },
          required: ['todos'],
        },
      },
    },
    {
      type: 'function',
      function: {
        name: 'todo_read',
        description: 'Read the current todo list. Returns all items with their content, status, and priority.',
        parameters: { type: 'object', properties: {} },
      },
    },
    {
      type: 'function',
      function: {
        name: 'question',
        description: 'Ask the user a question with predefined options. Pauses execution until the user responds. Use for gathering preferences, clarifying requirements, or getting decisions.',
        parameters: {
          type: 'object',
          properties: {
            question: { type: 'string', description: 'The question to ask the user' },
            header: { type: 'string', description: 'Short label for the question (max 30 chars)' },
            options: {
              type: 'array',
              description: 'Available choices',
              items: {
                type: 'object',
                properties: {
                  label: { type: 'string', description: 'Display text (1-5 words)' },
                  description: { type: 'string', description: 'Explanation of choice' },
                },
                required: ['label'],
              },
            },
            multiple: { type: 'boolean', description: 'Allow selecting multiple choices' },
            custom: { type: 'boolean', description: 'Allow typing a custom answer (default true)' },
          },
          required: ['question', 'options'],
        },
      },
    },
  ];

  /** Names of frontend tools for quick lookup. */
  const FRONTEND_TOOL_NAMES = FRONTEND_TOOLS.map(t => t.function.name);

  // ─── State ───

  let models = $state<string[]>([]);
  let selectedModel = $state('');
  let systemPrompt = $state('');
  let userInput = $state('');
  let messages = $state<ChatMessage[]>([]);
  let loading = $state(true);
  let streaming = $state(false);
  let abortController = $state<AbortController | null>(null);
  let chatContainer: HTMLDivElement | undefined = $state();
  let showSystemPrompt = $state(false);
  let pendingImages = $state<PendingImage[]>([]);
  let fileInput: HTMLInputElement | undefined = $state();
  let dragging = $state(false);

  // ─── Token Usage State ───

  /** Cumulative token usage across all completion calls in the conversation. */
  let contextTokens = $state(0);
  let completionTokens = $state(0);
  let totalTokens = $state(0);

  // ─── Tools State ───

  let showToolsConfig = $state(false);
  let mcpUrls = $state<string[]>([]);
  let mcpNewUrl = $state('');
  let agents = $state<Agent[]>([]);
  let selectedAgentId = $state('');
  let skills = $state<Skill[]>([]);
  let selectedSkillNames = $state<string[]>([]);

  // Built-in server tools
  let builtinTools = $state<BuiltinToolDef[]>([]);
  let enabledBuiltinTools = $state<string[]>([]);

  // RAG tools
  let ragTools = $state<RAGToolDef[]>([]);
  let ragAvailable = $state(false);
  let enabledRagTools = $state<string[]>([]);
  let ragCollections = $state<RAGCollection[]>([]);
  let selectedRagCollectionIds = $state<string[]>([]);

  // Frontend-only tools
  let enabledFrontendTools = $state<string[]>([]);

  // Todo panel
  let todos = $state<TodoItem[]>([]);
  let showTodoPanel = $state(false);

  // Question modal
  let pendingQuestion = $state<PendingQuestion | null>(null);

  // Discovered tools and dispatch map
  let discoveredTools = $state<ToolDefinition[]>([]);
  let toolSourceMap = $state<Record<string, ToolSource>>({});
  let skillSystemPrompts = $state<string[]>([]);
  let loadingTools = $state(false);
  let toolCount = $derived(discoveredTools.length);
  let todoActiveCount = $derived(todos.filter(t => t.status === 'pending' || t.status === 'in_progress').length);

  // ─── Load providers/models ───

  async function loadInfo() {
    loading = true;
    try {
      const info = await getInfo();

      // Build full model list: provider_key/model
      const allModels: string[] = [];
      for (const p of info.providers ?? []) {
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
      addToast(e?.response?.data?.message || 'Failed to load provider info', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadAgents() {
    try {
      const res = await listAgents();
      agents = res.data ?? [];
    } catch {
      // Agents may not be available
    }
  }

  async function loadSkills() {
    try {
      const res = await listSkills();
      skills = (res.data ?? []).map(s => ({ ...s, tools: s.tools ?? [] }));
    } catch {
      // Skills may not be available
    }
  }

  async function loadBuiltinTools() {
    try {
      const res = await listBuiltinTools();
      builtinTools = res.tools ?? [];
    } catch {
      // Built-in tools endpoint may not be available
    }
  }

  async function loadRAGTools() {
    try {
      const res = await listRAGTools();
      ragTools = res.tools ?? [];
      ragAvailable = res.available ?? false;
    } catch {
      // RAG tools endpoint may not be available
    }
  }

  async function loadRAGCollections() {
    try {
      const res = await listCollections();
      ragCollections = res.data ?? [];
    } catch {
      // RAG collections endpoint may not be available
    }
  }

  loadInfo();
  loadAgents();
  loadSkills();
  loadBuiltinTools();
  loadRAGTools();
  loadRAGCollections();

  // ─── Scroll ───

  function scrollToBottom() {
    if (chatContainer) {
      requestAnimationFrame(() => {
        chatContainer!.scrollTop = chatContainer!.scrollHeight;
      });
    }
  }

  // ─── Image handling ───

  function readFileAsDataURL(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });
  }

  async function addImageFiles(files: FileList | File[]) {
    for (const file of files) {
      if (!file.type.startsWith('image/')) continue;
      if (file.size > 20 * 1024 * 1024) {
        addToast(`Image "${file.name}" is too large (max 20MB)`, 'alert');
        continue;
      }
      try {
        const dataUrl = await readFileAsDataURL(file);
        pendingImages = [...pendingImages, { name: file.name, dataUrl }];
      } catch {
        addToast(`Failed to read "${file.name}"`, 'alert');
      }
    }
  }

  function removeImage(index: number) {
    pendingImages = pendingImages.filter((_, i) => i !== index);
  }

  function handlePaste(e: ClipboardEvent) {
    const items = e.clipboardData?.items;
    if (!items) return;

    const imageFiles: File[] = [];
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile();
        if (file) imageFiles.push(file);
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      addImageFiles(imageFiles);
    }
  }

  function handleFilePick(e: Event) {
    const input = e.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      addImageFiles(input.files);
      input.value = '';
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault();
    dragging = true;
  }

  function handleDragLeave(e: DragEvent) {
    e.preventDefault();
    dragging = false;
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    if (e.dataTransfer?.files) {
      addImageFiles(e.dataTransfer.files);
    }
  }

  // ─── Tools Management ───

  function addMcpUrl() {
    const url = mcpNewUrl.trim();
    if (!url) return;
    if (mcpUrls.includes(url)) {
      addToast('URL already added', 'alert');
      return;
    }
    mcpUrls = [...mcpUrls, url];
    mcpNewUrl = '';
    refreshTools();
  }

  function removeMcpUrl(index: number) {
    mcpUrls = mcpUrls.filter((_, i) => i !== index);
    refreshTools();
  }

  function onAgentSelected() {
    if (!selectedAgentId) return;
    const agent = agents.find(a => a.id === selectedAgentId);
    if (!agent) return;

    // Merge agent's MCP URLs (avoid duplicates)
    const newUrls = agent.config.mcp_urls?.filter(u => !mcpUrls.includes(u)) || [];
    if (newUrls.length > 0) {
      mcpUrls = [...mcpUrls, ...newUrls];
    }

    // Merge agent's skills (avoid duplicates)
    const newSkills = agent.config.skills?.filter(s => !selectedSkillNames.includes(s)) || [];
    if (newSkills.length > 0) {
      selectedSkillNames = [...selectedSkillNames, ...newSkills];
    }

    // Apply agent's system prompt if we don't have one
    if (agent.config.system_prompt && !systemPrompt.trim()) {
      systemPrompt = agent.config.system_prompt;
    }

    selectedAgentId = '';
    refreshTools();
  }

  function toggleSkill(skillName: string) {
    if (selectedSkillNames.includes(skillName)) {
      selectedSkillNames = selectedSkillNames.filter(s => s !== skillName);
    } else {
      selectedSkillNames = [...selectedSkillNames, skillName];
    }
    refreshTools();
  }

  function toggleBuiltinTool(toolName: string) {
    if (enabledBuiltinTools.includes(toolName)) {
      enabledBuiltinTools = enabledBuiltinTools.filter(t => t !== toolName);
    } else {
      enabledBuiltinTools = [...enabledBuiltinTools, toolName];
    }
    refreshTools();
  }

  function toggleFrontendTool(toolName: string) {
    if (enabledFrontendTools.includes(toolName)) {
      enabledFrontendTools = enabledFrontendTools.filter(t => t !== toolName);
    } else {
      enabledFrontendTools = [...enabledFrontendTools, toolName];
    }
    // Show todo panel automatically when todo tools are enabled
    if (toolName === 'todo_write' || toolName === 'todo_read') {
      showTodoPanel = enabledFrontendTools.includes('todo_write') || enabledFrontendTools.includes('todo_read');
    }
    refreshTools();
  }

  function toggleRagTool(toolName: string) {
    if (enabledRagTools.includes(toolName)) {
      enabledRagTools = enabledRagTools.filter(t => t !== toolName);
    } else {
      enabledRagTools = [...enabledRagTools, toolName];
    }
    refreshTools();
  }

  function toggleRagCollection(collectionId: string) {
    if (selectedRagCollectionIds.includes(collectionId)) {
      selectedRagCollectionIds = selectedRagCollectionIds.filter(id => id !== collectionId);
    } else {
      selectedRagCollectionIds = [...selectedRagCollectionIds, collectionId];
    }
    // Auto-enable rag_search when a collection is selected
    if (selectedRagCollectionIds.length > 0 && enabledRagTools.length === 0) {
      enabledRagTools = ['rag_search'];
      refreshTools();
    }
  }

  /** Discover tools from MCP servers, selected skills, enabled builtins, frontend tools, and RAG. Build the dispatch map. */
  async function refreshTools() {
    loadingTools = true;
    const newTools: ToolDefinition[] = [];
    const newSourceMap: Record<string, ToolSource> = {};
    const newSkillPrompts: string[] = [];

    try {
      // 1. Discover MCP tools
      if (mcpUrls.length > 0) {
        const res = await listMCPTools(mcpUrls);
        if (res.errors && res.errors.length > 0) {
          for (const err of res.errors) {
            addToast(`MCP: ${err}`, 'alert');
          }
        }
        for (const t of res.tools ?? []) {
          if (newSourceMap[t.name]) continue;
          newTools.push({
            type: 'function',
            function: {
              name: t.name,
              description: t.description,
              parameters: t.input_schema || { type: 'object', properties: {} },
            },
          });
          newSourceMap[t.name] = { type: 'mcp', serverUrl: t.server_url };
        }
      }

      // 2. Discover skill tools
      for (const skillName of selectedSkillNames) {
        const skill = skills.find(s => s.name === skillName);
        if (!skill) continue;

        if (skill.system_prompt) {
          newSkillPrompts.push(skill.system_prompt);
        }

        for (const tool of skill.tools) {
          if (newSourceMap[tool.name]) continue;
          newTools.push({
            type: 'function',
            function: {
              name: tool.name,
              description: tool.description,
              parameters: tool.inputSchema || { type: 'object', properties: {} },
            },
          });
          newSourceMap[tool.name] = { type: 'skill', skillName: skill.name };
        }
      }

      // 3. Add enabled built-in server tools
      for (const toolName of enabledBuiltinTools) {
        const def = builtinTools.find(t => t.name === toolName);
        if (!def || newSourceMap[def.name]) continue;
        newTools.push({
          type: 'function',
          function: {
            name: def.name,
            description: def.description,
            parameters: def.input_schema || { type: 'object', properties: {} },
          },
        });
        newSourceMap[def.name] = { type: 'builtin' };
      }

      // 4. Add enabled frontend tools
      for (const toolName of enabledFrontendTools) {
        const def = FRONTEND_TOOLS.find(t => t.function.name === toolName);
        if (!def || newSourceMap[def.function.name]) continue;
        newTools.push(def);
        newSourceMap[def.function.name] = { type: 'frontend' };
      }

      // 5. Add selected RAG tools
      if (enabledRagTools.length > 0 && ragAvailable) {
        for (const tool of ragTools) {
          if (!enabledRagTools.includes(tool.name)) continue;
          if (newSourceMap[tool.name]) continue;
          newTools.push({
            type: 'function',
            function: {
              name: tool.name,
              description: tool.description,
              parameters: tool.input_schema || { type: 'object', properties: {} },
            },
          });
          newSourceMap[tool.name] = { type: 'rag' };
        }
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to discover tools', 'alert');
    } finally {
      discoveredTools = newTools;
      toolSourceMap = newSourceMap;
      skillSystemPrompts = newSkillPrompts;
      loadingTools = false;
    }
  }

  /** Execute a tool call by dispatching to the correct backend or frontend handler. */
  async function executeToolCall(tc: ToolCall): Promise<string> {
    let args: Record<string, any> = {};
    try {
      args = JSON.parse(tc.function.arguments);
    } catch {
      // If args don't parse, pass empty
    }

    const source = toolSourceMap[tc.function.name];
    if (!source) {
      return `Error: no handler found for tool "${tc.function.name}"`;
    }

    try {
      if (source.type === 'mcp' && source.serverUrl) {
        const res = await callMCPTool(source.serverUrl, tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'skill' && source.skillName) {
        const res = await callSkillTool(source.skillName, tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'builtin') {
        const res = await callBuiltinTool(tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'rag') {
        // If user selected specific collections, inject them into rag_search
        if ((tc.function.name === 'rag_search' || tc.function.name === 'rag_search_and_fetch') && selectedRagCollectionIds.length > 0) {
          args.collection_ids = selectedRagCollectionIds;
        }
        const res = await callRAGTool(tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'frontend') {
        return await executeFrontendTool(tc.function.name, args);
      }
      return `Error: unknown tool source type`;
    } catch (e: any) {
      return `Error: ${e.message || 'tool execution failed'}`;
    }
  }

  /** Execute a frontend-only tool (runs entirely in the browser). */
  async function executeFrontendTool(name: string, args: Record<string, any>): Promise<string> {
    switch (name) {
      case 'todo_write': {
        const items = args.todos;
        if (!Array.isArray(items)) return 'Error: todos must be an array';
        todos = items.map((t: any) => ({
          content: String(t.content || ''),
          status: t.status || 'pending',
          priority: t.priority || 'medium',
        }));
        showTodoPanel = true;
        return JSON.stringify({ success: true, count: todos.length });
      }
      case 'todo_read': {
        return JSON.stringify({ todos });
      }
      case 'question': {
        const question = args.question || 'Please answer:';
        const options = Array.isArray(args.options) ? args.options : [];
        const header = args.header;
        const multiple = args.multiple ?? false;
        const custom = args.custom ?? true;

        // Create a promise that resolves when the user answers
        const answer = await new Promise<string>((resolve) => {
          pendingQuestion = {
            question,
            header,
            options,
            multiple,
            custom,
            resolve,
          };
          scrollToBottom();
        });

        return JSON.stringify({ answer });
      }
      default:
        return `Error: unknown frontend tool "${name}"`;
    }
  }

  // ─── Send message ───

  async function sendMessage() {
    const text = userInput.trim();
    if ((!text && pendingImages.length === 0) || !selectedModel) return;
    if (streaming) return;

    // Build user message content
    let userContent: string | ContentPart[];
    if (pendingImages.length > 0) {
      const parts: ContentPart[] = [];
      for (const img of pendingImages) {
        parts.push({ type: 'image_url', image_url: { url: img.dataUrl } });
      }
      if (text) {
        parts.push({ type: 'text', text });
      }
      userContent = parts;
    } else {
      userContent = text;
    }

    // Add user message to chat
    messages = [...messages, { role: 'user', content: userContent }];
    userInput = '';
    pendingImages = [];
    scrollToBottom();

    await runCompletion();
  }

  /** Recursive completion loop that handles tool calls. */
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

    // System prompt: combine user system prompt + skill system prompts
    const fullSystemPrompt = [systemPrompt.trim(), ...skillSystemPrompts].filter(Boolean).join('\n\n');
    if (fullSystemPrompt) {
      reqMessages.push({ role: 'system', content: fullSystemPrompt });
    }

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
          tools: discoveredTools.length > 0 ? discoveredTools : undefined,
          stream: true,
          stream_options: { include_usage: true },
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
          onUsage: (usage) => {
            contextTokens = usage.prompt_tokens;
            completionTokens += usage.completion_tokens;
            totalTokens = contextTokens + completionTokens;
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
          const result = await executeToolCall(tc);
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
      if (e.name === 'AbortError') {
        // User cancelled — don't show error
      } else {
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
    systemPrompt = '';
    pendingImages = [];
    todos = [];
    pendingQuestion = null;
    contextTokens = 0;
    completionTokens = 0;
    totalTokens = 0;
  }

  /** Retry from a specific user message index. */
  async function retryFromIndex(index: number) {
    if (streaming) return;
    messages = messages.slice(0, index + 1);
    scrollToBottom();
    await runCompletion();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }

  function handleMcpUrlKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      addMcpUrl();
    }
  }
</script>

<svelte:head>
  <title>AT | Chat</title>
</svelte:head>

<div
  class="flex flex-col h-full"
  ondragover={handleDragOver}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
  role="application"
>
  <!-- Drag overlay -->
  {#if dragging}
    <div class="absolute inset-0 z-50 bg-gray-900/10 dark:bg-dark-base/30 border-2 border-dashed border-gray-400 dark:border-dark-border-subtle flex items-center justify-center pointer-events-none">
      <div class="bg-white dark:bg-dark-surface px-4 py-2 text-sm text-gray-600 dark:text-dark-text-secondary shadow-sm">Drop images here</div>
    </div>
  {/if}

  <!-- Toolbar -->
  <div class="border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-2 flex items-center gap-2 shrink-0">
    <!-- Model selector -->
    <div class="relative flex-1 max-w-xs">
      <select
        bind:value={selectedModel}
        disabled={loading || models.length === 0}
        class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted transition-colors"
      >
        {#if models.length === 0}
          <option value="">No models available</option>
        {/if}
        {#each models as model}
          <option value={model}>{model}</option>
        {/each}
      </select>
      <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-muted" />
    </div>

    <!-- System prompt toggle -->
    <button
      onclick={() => (showSystemPrompt = !showSystemPrompt)}
      class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 dark:border-dark-border-subtle dark:hover:bg-dark-elevated dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
      class:bg-gray-900={showSystemPrompt}
      class:text-white={showSystemPrompt}
      class:border-gray-900={showSystemPrompt}
      class:hover:bg-gray-800={showSystemPrompt}
      class:hover:text-white={showSystemPrompt}
      title="System prompt"
    >
      <Settings size={14} />
    </button>

    <!-- Tools toggle -->
    <button
      onclick={() => (showToolsConfig = !showToolsConfig)}
      class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 dark:border-dark-border-subtle dark:hover:bg-dark-elevated dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors relative"
      class:bg-gray-900={showToolsConfig}
      class:text-white={showToolsConfig}
      class:border-gray-900={showToolsConfig}
      class:hover:bg-gray-800={showToolsConfig}
      class:hover:text-white={showToolsConfig}
      title="Tools (MCP, Skills, Built-in, Chat)"
    >
      <Wrench size={14} />
      {#if toolCount > 0}
        <span class="absolute -top-1.5 -right-1.5 min-w-[16px] h-4 bg-blue-600 text-white text-[9px] font-medium flex items-center justify-center px-1">{toolCount}</span>
      {/if}
    </button>

    <!-- Todo panel toggle -->
    {#if todos.length > 0}
      <button
        onclick={() => (showTodoPanel = !showTodoPanel)}
        class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 dark:border-dark-border-subtle dark:hover:bg-dark-elevated dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors relative"
        class:bg-gray-900={showTodoPanel}
        class:text-white={showTodoPanel}
        class:border-gray-900={showTodoPanel}
        class:hover:bg-gray-800={showTodoPanel}
        class:hover:text-white={showTodoPanel}
        title="Todo list"
      >
        <ListChecks size={14} />
        {#if todoActiveCount > 0}
          <span class="absolute -top-1.5 -right-1.5 min-w-[16px] h-4 bg-amber-500 text-white text-[9px] font-medium flex items-center justify-center px-1">{todoActiveCount}</span>
        {/if}
      </button>
    {/if}

    <!-- Token usage (right-aligned) -->
    {#if totalTokens > 0}
      <div class="ml-auto text-[11px] text-gray-400 dark:text-dark-text-muted font-mono tabular-nums" title="Context: {contextTokens.toLocaleString()} prompt + {completionTokens.toLocaleString()} completion = {totalTokens.toLocaleString()} total tokens">
        {totalTokens.toLocaleString()} tok
      </div>
    {/if}

    <!-- Clear -->
    <button
      onclick={clearChat}
      disabled={messages.length === 0 && !systemPrompt && pendingImages.length === 0}
      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-400 transition-colors {totalTokens === 0 ? 'ml-auto' : ''}"
      title="Clear chat"
    >
      <Trash2 size={14} />
    </button>
  </div>

  <!-- System prompt -->
  {#if showSystemPrompt}
    <div class="border-b border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/50 px-4 py-2.5 shrink-0">
      <textarea
        bind:value={systemPrompt}
        placeholder="System prompt (optional)"
        rows={2}
        class="w-full border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm resize-y focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
      ></textarea>
    </div>
  {/if}

  <!-- Tools configuration panel -->
  {#if showToolsConfig}
    <div class="border-b border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/50 px-4 py-3 shrink-0 space-y-3 max-h-80 overflow-y-auto">
      <!-- Agent selector (quick-fill MCP URLs + skills from agent config) -->
      {#if agents.length > 0}
        <div>
          <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Import from Agent</label>
          <div class="flex gap-2">
            <div class="relative flex-1">
              <select
                bind:value={selectedAgentId}
                class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
              >
                <option value="">Select an agent...</option>
                {#each agents as agent}
                  <option value={agent.id}>{agent.name}{agent.config.description ? ` — ${agent.config.description}` : ''}</option>
                {/each}
              </select>
              <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-muted" />
            </div>
            <button
              onclick={onAgentSelected}
              disabled={!selectedAgentId}
              class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 transition-colors"
            >
              Import
            </button>
          </div>
        </div>
      {/if}

      <!-- MCP Server URLs -->
      <div>
        <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">MCP Servers</label>
        <div class="space-y-1.5">
          {#each mcpUrls as url, i}
            <div class="flex gap-2 items-center">
              <code class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1 text-xs font-mono text-gray-700 dark:text-dark-text truncate">{url}</code>
              <button
                onclick={() => removeMcpUrl(i)}
                class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                title="Remove"
              >
                <X size={12} />
              </button>
            </div>
          {/each}
          <div class="flex gap-2">
            <input
              type="text"
              bind:value={mcpNewUrl}
              onkeydown={handleMcpUrlKeydown}
              placeholder="http://localhost:8000/mcp"
              class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
            <button
              onclick={addMcpUrl}
              disabled={!mcpNewUrl.trim()}
              class="px-2.5 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary disabled:opacity-30 transition-colors flex items-center gap-1"
            >
              <Plus size={12} />
              Add
            </button>
          </div>
        </div>
      </div>

      <!-- Skills -->
      {#if skills.length > 0}
        <div>
          <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Skills</label>
          <div class="flex flex-wrap gap-1.5">
            {#each skills as skill}
              <button
                onclick={() => toggleSkill(skill.name)}
                class="px-2.5 py-1 text-xs border transition-colors {selectedSkillNames.includes(skill.name)
                  ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                  : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                title={skill.description || skill.name}
              >
                {skill.name}
                {#if skill.tools.length > 0}
                  <span class="ml-1 opacity-60">({skill.tools.length})</span>
                {/if}
              </button>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Server Tools (built-in) -->
      {#if builtinTools.length > 0}
        <div>
          <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Server Tools</label>
          <div class="flex flex-wrap gap-1.5">
            {#each builtinTools as tool}
              <button
                onclick={() => toggleBuiltinTool(tool.name)}
                class="px-2.5 py-1 text-xs border transition-colors {enabledBuiltinTools.includes(tool.name)
                  ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                  : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                title={tool.description}
              >
                {tool.name}
              </button>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Chat Tools (frontend-only) -->
      <div>
        <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Chat Tools</label>
        <div class="flex flex-wrap gap-1.5">
          {#each FRONTEND_TOOLS as tool}
            <button
              onclick={() => toggleFrontendTool(tool.function.name)}
              class="px-2.5 py-1 text-xs border transition-colors {enabledFrontendTools.includes(tool.function.name)
                ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
              title={tool.function.description}
            >
              {tool.function.name}
            </button>
          {/each}
        </div>
      </div>

      <!-- RAG Knowledge Base -->
      {#if ragAvailable && ragTools.length > 0}
        <div>
          <label class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">RAG Knowledge Base</label>
          <div class="space-y-1.5">
            <div class="flex flex-wrap gap-1.5">
              {#each ragTools as tool}
                <button
                  onclick={() => toggleRagTool(tool.name)}
                  class="px-2.5 py-1 text-xs border transition-colors {enabledRagTools.includes(tool.name)
                    ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                    : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                  title={tool.description}
                >
                  {tool.name}
                </button>
              {/each}
            </div>
            {#if enabledRagTools.length > 0 && ragCollections.length > 0}
              <div>
                <span class="text-[10px] text-gray-400 dark:text-dark-text-muted mb-1 block">Collections {selectedRagCollectionIds.length > 0 ? `(${selectedRagCollectionIds.length} selected)` : '(all)'}</span>
                <div class="flex flex-wrap gap-1.5">
                  {#each ragCollections as col}
                    <button
                      onclick={() => toggleRagCollection(col.id)}
                      class="px-2.5 py-1 text-xs border transition-colors {selectedRagCollectionIds.includes(col.id)
                        ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                        : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                      title={col.config.description || col.name}
                    >
                      {col.name}
                    </button>
                  {/each}
                </div>
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Discovered tools summary -->
      <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted pt-1 border-t border-gray-200 dark:border-dark-border">
        {#if loadingTools}
          <Loader2 size={12} class="animate-spin" />
          <span>Discovering tools...</span>
        {:else if toolCount > 0}
          <Wrench size={12} />
          <span>{toolCount} tool{toolCount !== 1 ? 's' : ''} available</span>
          <span class="text-gray-300 dark:text-dark-border">|</span>
          <span class="truncate">{discoveredTools.map(t => t.function.name).join(', ')}</span>
        {:else if mcpUrls.length > 0 || selectedSkillNames.length > 0 || enabledBuiltinTools.length > 0 || enabledFrontendTools.length > 0 || enabledRagTools.length > 0}
          <span>No tools discovered</span>
        {:else}
          <span>Add MCP servers, enable skills, or toggle tools above</span>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Todo panel -->
  {#if showTodoPanel && todos.length > 0}
    <div class="border-b border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/50 px-4 py-2.5 shrink-0 max-h-48 overflow-y-auto">
      <div class="flex items-center justify-between mb-1.5">
        <div class="flex items-center gap-1.5 text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide">
          <ListChecks size={12} />
          Todos
          <span class="normal-case tracking-normal font-normal">({todos.filter(t => t.status === 'completed').length}/{todos.length})</span>
        </div>
        <button
          onclick={() => (showTodoPanel = false)}
          class="p-0.5 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        >
          <X size={12} />
        </button>
      </div>
      <div class="space-y-0.5">
        {#each todos as todo}
          <div class="flex items-start gap-2 py-0.5 text-xs {todo.status === 'completed' ? 'opacity-50' : ''} {todo.status === 'cancelled' ? 'opacity-30 line-through' : ''}">
            <span class="shrink-0 mt-0.5">
              {#if todo.status === 'completed'}
                <span class="inline-block w-3.5 h-3.5 rounded-full bg-green-500 text-white text-[8px] flex items-center justify-center">&#10003;</span>
              {:else if todo.status === 'in_progress'}
                <Loader2 size={14} class="animate-spin text-blue-500" />
              {:else if todo.status === 'cancelled'}
                <span class="inline-block w-3.5 h-3.5 rounded-full bg-gray-400 text-white text-[8px] flex items-center justify-center">&times;</span>
              {:else}
                <span class="inline-block w-3.5 h-3.5 rounded-full border-2 {todo.priority === 'high' ? 'border-red-400' : todo.priority === 'medium' ? 'border-amber-400' : 'border-gray-300 dark:border-dark-border-subtle'}"></span>
              {/if}
            </span>
            <span class="text-gray-700 dark:text-dark-text-secondary leading-tight">{todo.content}</span>
            {#if todo.priority === 'high' && todo.status !== 'completed' && todo.status !== 'cancelled'}
              <span class="shrink-0 text-[9px] text-red-500 font-medium uppercase">high</span>
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Chat messages -->
  <div
    bind:this={chatContainer}
    class="flex-1 overflow-y-auto px-4 py-4 space-y-4"
  >
    {#if loading}
      <div class="text-center py-12 text-gray-400 dark:text-dark-text-muted text-sm">Loading providers...</div>
    {:else if models.length === 0}
      <div class="text-center py-12">
        <div class="text-gray-400 dark:text-dark-text-muted mb-2">No providers configured</div>
        <div class="text-xs text-gray-400 dark:text-dark-text-muted">
          Add providers on the <a href="#/providers" class="underline underline-offset-2 hover:text-gray-700 dark:hover:text-dark-text transition-colors">Providers</a> page first.
        </div>
      </div>
    {:else if messages.length === 0}
      <div class="text-center py-12">
        <div class="text-gray-400 dark:text-dark-text-muted mb-1.5">Send a message to start chatting</div>
        <div class="text-xs text-gray-400 dark:text-dark-text-muted">
          Using <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-gray-600 dark:text-dark-text-secondary">{selectedModel}</code>
          {#if toolCount > 0}
            <span class="ml-1">with {toolCount} tool{toolCount !== 1 ? 's' : ''}</span>
          {/if}
        </div>
      </div>
    {:else}
      {#each messages as msg, i}
        {#if msg.role === 'user'}
          <div class="flex justify-end">
            <div class="max-w-[75%]">
              <div class="px-4 py-2.5 text-sm leading-relaxed bg-gray-900 dark:bg-accent text-white">
                {#if typeof msg.content === 'string'}
                  <span class="whitespace-pre-wrap">{msg.content}</span>
                {:else}
                  {#each msg.content as part}
                    {#if part.type === 'image_url' && part.image_url?.url}
                      <img src={part.image_url.url} alt="" class="max-w-full max-h-64 mb-2 border border-gray-600 dark:border-accent/50" />
                    {:else if part.type === 'text' && part.text}
                      <span class="whitespace-pre-wrap">{part.text}</span>
                    {/if}
                  {/each}
                {/if}
              </div>
              {#if !streaming}
                <div class="mt-1 flex justify-end">
                  <button
                    onclick={() => retryFromIndex(i)}
                    class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text flex items-center gap-1 transition-colors"
                    title="Retry from this message"
                  >
                    <RotateCcw size={11} />
                    Retry
                  </button>
                </div>
              {/if}
            </div>
          </div>
        {:else if msg.role === 'assistant'}
          <div class="flex justify-start">
            <div class="max-w-[75%]">
              <div class="px-4 py-2.5 text-sm leading-relaxed bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shadow-sm text-gray-800 dark:text-dark-text">
                {#if typeof msg.content === 'string'}
                  {#if !msg.content && streaming && i === messages.length - 1}
                    <span class="text-gray-400 dark:text-dark-text-muted italic">Thinking...</span>
                  {:else}
                    <div class="markdown-body" use:renderMarkdown>{@html md(msg.content)}</div>
                  {/if}
                {:else}
                  {#each msg.content as part}
                    {#if part.type === 'image_url' && part.image_url?.url}
                      <img src={part.image_url.url} alt="" class="max-w-full max-h-64 mb-2 border border-gray-200 dark:border-dark-border" />
                    {:else if part.type === 'text' && part.text}
                      <div class="markdown-body" use:renderMarkdown>{@html md(part.text)}</div>
                    {/if}
                  {/each}
                {/if}
                <!-- Tool call indicators -->
                {#if msg.tool_calls && msg.tool_calls.length > 0}
                  <div class="mt-2 pt-2 border-t border-gray-200 dark:border-dark-border space-y-1">
                    {#each msg.tool_calls as tc}
                      <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted">
                        <span class="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 shrink-0"></span>
                        <span class="font-mono">{tc.function.name}</span>
                        <span class="text-gray-300 dark:text-dark-border">
                          {#if toolSourceMap[tc.function.name]?.type === 'mcp'}
                            (MCP)
                          {:else if toolSourceMap[tc.function.name]?.type === 'skill'}
                            (Skill: {toolSourceMap[tc.function.name]?.skillName})
                          {:else if toolSourceMap[tc.function.name]?.type === 'builtin'}
                            (Built-in)
                          {:else if toolSourceMap[tc.function.name]?.type === 'frontend'}
                            (Chat)
                          {:else if toolSourceMap[tc.function.name]?.type === 'rag'}
                            (RAG)
                          {/if}
                        </span>
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
            </div>
          </div>
        {/if}
        <!-- tool messages are hidden (internal) -->
      {/each}
    {/if}
  </div>

  <!-- Input area -->
  <div class="border-t border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-3 shrink-0">
    <!-- Pending image previews -->
    {#if pendingImages.length > 0}
      <div class="flex gap-2 mb-2 flex-wrap">
        {#each pendingImages as img, i}
          <div class="relative group">
            <img
              src={img.dataUrl}
              alt={img.name}
              class="w-16 h-16 object-cover border border-gray-300 dark:border-dark-border-subtle"
            />
            <button
              onclick={() => removeImage(i)}
              class="absolute -top-1.5 -right-1.5 w-5 h-5 bg-gray-900 dark:bg-accent text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
              title="Remove"
            >
              <X size={12} />
            </button>
            <div class="absolute bottom-0 left-0 right-0 bg-black/50 text-white text-[9px] px-1 truncate">
              {img.name}
            </div>
          </div>
        {/each}
      </div>
    {/if}

    <div class="flex gap-2">
      <!-- Hidden file input -->
      <input
        bind:this={fileInput}
        type="file"
        accept="image/*"
        multiple
        class="hidden"
        onchange={handleFilePick}
      />

      <!-- Image attach button -->
      <button
        onclick={() => fileInput?.click()}
        disabled={models.length === 0}
        class="px-2.5 py-2 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-500 transition-colors"
        title="Attach image"
      >
        <ImagePlus size={14} />
      </button>

      <textarea
        bind:value={userInput}
        onkeydown={handleKeydown}
        onpaste={handlePaste}
        placeholder={models.length === 0 ? 'No models available' : 'Type a message... (Enter to send, Shift+Enter for new line)'}
        disabled={models.length === 0}
        rows={1}
        class="flex-1 border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-4 py-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted transition-colors"
      ></textarea>
      {#if streaming}
        <button
          onclick={stopStreaming}
          class="px-3 py-2 bg-red-600 text-white hover:bg-red-700 flex items-center gap-1.5 transition-colors"
          title="Stop"
        >
          <Square size={14} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={(!userInput.trim() && pendingImages.length === 0) || !selectedModel || models.length === 0}
          class="px-3 py-2 bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 disabled:hover:bg-gray-900 flex items-center gap-1.5 transition-colors"
          title="Send"
        >
          <Send size={14} />
        </button>
      {/if}
    </div>
  </div>

  <!-- Question modal overlay -->
  {#if pendingQuestion}
    <div class="absolute inset-0 z-40 bg-gray-900/30 dark:bg-black/50 flex items-center justify-center p-4">
      <div class="bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shadow-lg max-w-md w-full">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
          <div class="flex items-center gap-2">
            <MessageCircleQuestion size={16} class="text-blue-500 shrink-0" />
            {#if pendingQuestion.header}
              <span class="text-sm font-medium text-gray-800 dark:text-dark-text">{pendingQuestion.header}</span>
            {:else}
              <span class="text-sm font-medium text-gray-800 dark:text-dark-text">Question</span>
            {/if}
          </div>
        </div>
        <div class="px-4 py-3">
          <p class="text-sm text-gray-700 dark:text-dark-text-secondary mb-3 whitespace-pre-wrap">{pendingQuestion.question}</p>
          <div class="space-y-1.5">
            {#each pendingQuestion.options as opt}
              <button
                onclick={() => { const q = pendingQuestion; if (q) { pendingQuestion = null; q.resolve(opt.label); } }}
                class="w-full text-left px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors"
              >
                <div class="font-medium">{opt.label}</div>
                {#if opt.description}
                  <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{opt.description}</div>
                {/if}
              </button>
            {/each}
            {#if pendingQuestion.custom !== false}
              <div class="pt-1.5">
                <form
                  onsubmit={(e) => { e.preventDefault(); const input = (e.target as HTMLFormElement).elements.namedItem('custom_answer') as HTMLInputElement; const val = input?.value?.trim(); if (val && pendingQuestion) { const q = pendingQuestion; pendingQuestion = null; q.resolve(val); } }}
                  class="flex gap-2"
                >
                  <input
                    name="custom_answer"
                    type="text"
                    placeholder="Type your own answer..."
                    class="flex-1 border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
                  />
                  <button
                    type="submit"
                    class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
                  >
                    Submit
                  </button>
                </form>
              </div>
            {/if}
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  @reference "../style/global.css";

  .markdown-body :global(p) {
    @apply mb-2 last:mb-0;
  }
  .markdown-body :global(a) {
    @apply underline underline-offset-2 hover:opacity-80;
  }
  .markdown-body :global(strong) {
    @apply font-semibold;
  }
  .markdown-body :global(code) {
    @apply font-mono text-[0.85em] bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 rounded;
  }
  .markdown-body :global(pre) {
    @apply bg-gray-100 dark:bg-dark-elevated px-3 py-2 my-2 overflow-x-auto text-[0.85em] rounded;
  }
  .markdown-body :global(pre code) {
    @apply bg-transparent px-0 py-0;
  }
  .markdown-body :global(ul) {
    @apply list-disc pl-5 mb-2;
  }
  .markdown-body :global(ol) {
    @apply list-decimal pl-5 mb-2;
  }
  .markdown-body :global(li) {
    @apply mb-0.5;
  }
  .markdown-body :global(blockquote) {
    @apply border-l-2 border-gray-300 dark:border-dark-border pl-3 my-2 text-gray-600 dark:text-dark-text-secondary;
  }
  .markdown-body :global(h1) {
    @apply text-lg font-semibold mb-2;
  }
  .markdown-body :global(h2) {
    @apply text-base font-semibold mb-1.5;
  }
  .markdown-body :global(h3) {
    @apply text-sm font-semibold mb-1;
  }
  .markdown-body :global(h4),
  .markdown-body :global(h5),
  .markdown-body :global(h6) {
    @apply text-sm font-medium mb-1;
  }
  .markdown-body :global(hr) {
    @apply border-t border-gray-200 dark:border-dark-border my-3;
  }
  .markdown-body :global(img) {
    @apply max-w-full my-2;
  }
  .markdown-body :global(table) {
    @apply w-full border-collapse my-2 text-sm;
  }
  .markdown-body :global(th),
  .markdown-body :global(td) {
    @apply border border-gray-200 dark:border-dark-border px-2 py-1 text-left;
  }
  .markdown-body :global(th) {
    @apply bg-gray-50 dark:bg-dark-elevated font-medium;
  }
</style>
