<script lang="ts">
  import ToolActivity from '@/lib/components/ToolActivity.svelte';
  import { toolResultsByMessage } from '@/lib/helper/tool-activity';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { getInfo } from '@/lib/api/gateway';
  import {
    type ContentPart,
    type ChatMessage,
    type ToolCall,
    type ToolDefinition,
    type ChatUsage,
    type StreamCallbacks,
    getTextContent,
    assistantOutgoingContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { modelReasoningEfforts } from '@/lib/helper/reasoning';
  import { acceptAttribute, attachmentModality, attachmentPart, attachmentRefusal, contentModalities, type InputModality, type PendingAttachment } from '@/lib/helper/attachments';
  import { listBuiltinTools, callBuiltinTool, runSkill, waitSkillRun, type BuiltinToolDef, type SkillRunStatus, type SkillRunArtifact } from '@/lib/api/mcp';
  import BuiltinToolPicker from '@/lib/components/BuiltinToolPicker.svelte';
  import { builtinDisabledBy } from '@/lib/helper/builtin-tools';
  import { isChatTodoTool, initialWorkbenchSetup, newWorkbenchSetup, normalizeWorkbenchSetup, workbenchSetupsEqual, type WorkbenchSetup } from '@/lib/helper/chat-tool-selections';
  import { createDebouncedSave } from '@/lib/helper/debounced-save';
  import { createChatTurnLifecycle, runChatIterations, type ChatTurn } from '@/lib/helper/chat-turn';
  import { INTERRUPTED_TOOL_RESULT, isEmptyAssistant, queuedMessage, queuedPreview, takeQueued, unansweredToolCalls, userMessageContent, type QueuedChatMessage } from '@/lib/helper/chat-queue';
  import { dispatchChatTool } from '@/lib/helper/chat-tools';
  import { createTranscriptWriter } from '@/lib/helper/chat-persistence';
  import { isFeatureEnabled } from '@/lib/store/features.svelte';
  import { workspaceTransport } from '@/lib/api/transport';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listMCPSets, listMCPSetTools, callMCPSetTool, type MCPSet } from '@/lib/api/mcp-sets';
  import {
    listLocalMCPServers,
    saveLocalMCPServers,
    revealLocalMCPHeaders,
    reportLocalToolObservation,
    type LocalMCPServer,
  } from '@/lib/api/local-mcp';
  import {
    approvalFor,
    approveLocalMCP,
    clipLocalToolResult,
    localMCPToolName,
    revokeLocalMCP,
    toolsAddedSinceApproval,
  } from '@/lib/helper/local-mcp';
  import { LocalMCPClient, type LocalMCPTool } from '@/lib/helper/local-mcp-client';
  import LocalMCPServerEditor from '@/lib/components/playground/LocalMCPServerEditor.svelte';
  import LocalProviderEditor from '@/lib/components/playground/LocalProviderEditor.svelte';
  import {
    listLocalChatProviders,
    saveLocalChatProviders,
    revealLocalChatProvider,
    reportLocalGeneration,
    type LocalChatProvider,
    type LocalChatProviderSecrets,
  } from '@/lib/api/local-providers';
  import {
    describeLocalProviderError,
    disableLocalProvider,
    enableLocalProvider,
    isLocalModelRef,
    localModelRef,
    localProviderEnabled,
    localProviderEndpoint,
    localProviderHeaders,
    parseLocalModelRef,
    parseModelList,
    LOCAL_PROVIDER_CORS_HINT,
  } from '@/lib/helper/local-providers';
  import {
    CAPABILITY_TOOLS,
    ExtensionBridge,
    approveExtension,
    extensionApprovalFor,
    revokeExtension,
    type ExtensionDescriptor,
    type ExtensionTool,
  } from '@/lib/helper/extension-bridge';
  import { FEATURE_CHAT_EXTENSIONS, FEATURE_CHAT_LOCAL_MCP, FEATURE_CHAT_LOCAL_PROVIDERS, FEATURE_CHAT_SHARING } from '@/lib/api/features';
  import {
    type PlaygroundConversation,
    type PlaygroundConversationInput,
    type PlaygroundMessage,
    type PlaygroundMessageInput,
    type PlaygroundRole,
    type ChatPreset,
    PLAYGROUND_MESSAGE_BATCH_MAX,
    appendPlaygroundMessages,
    createPlaygroundConversation,
    deletePlaygroundConversation,
    forkPlaygroundConversation,
    getPlaygroundConversation,
    listPlaygroundConversations,
    listPlaygroundMessages,
    patchPlaygroundConversation,
    persistPlaygroundImages,
    playgroundErrorMessage,
    playgroundRoute,
    playgroundTitleFrom,
    truncatePlaygroundMessages,
    getPlaygroundDefaults,
    savePlaygroundDefaults,
    listChatPresets,
    saveChatPresets,
    listWorkspaceChatPresets,
    createWorkspaceChatPreset,
    updateWorkspaceChatPreset,
    deleteWorkspaceChatPreset,
    sortPlaygroundConversations,
  } from '@/lib/api/playground';
  import { formatMessageTime, formatLocalDateTime } from '@/lib/helper/format';
  import {
    dataUrlToBlob,
    getMediaDataURL,
    isMediaStorageDisabled,
    mediaUploadErrorMessage,
    uploadMedia,
  } from '@/lib/api/media';
  import ConversationList from '@/lib/components/playground/ConversationList.svelte';
  import ShareDialog from '@/lib/components/playground/ShareDialog.svelte';
  import { X, Wrench, Loader2, MessageCircleQuestion, PanelLeft, PanelRight, GitBranch, FileText, FileAudio, FileVideo } from 'lucide-svelte';
  import { onDestroy, untrack, tick } from 'svelte';
  import { push } from 'svelte-spa-router';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import MessageContent from '@/lib/components/playground/MessageContent.svelte';
  import { latestTodos, normalizeTodos, type TodoItem } from '@/lib/helper/chat-todos';
  import CommandPalette, { type PaletteGroup } from '@/lib/components/playground/CommandPalette.svelte';
  import ChatCommandsEditor from '@/lib/components/playground/ChatCommandsEditor.svelte';
  import { listChatCommands, listWorkspaceChatCommands, type ChatCommand } from '@/lib/api/chat-commands';
  import { COMPACTION_FLAG, compactionMessageText, compactionRequest, compactionStart, expandCommandTemplate, parseSlashInput, slashQuery } from '@/lib/helper/chat-commands';
  import { createChatMediaCache } from '@/lib/helper/chat-media-cache';

  storeNavbar.title = 'Chats';

  // svelte-spa-router yields `{id: null}` for the bare `/chats` route.
  let { params = {} }: { params?: { id?: string | null } } = $props();

  // ─── Types ───

  /** Maps a tool name to its source for dispatch. */
  interface ToolSource {
    type: 'mcp' | 'builtin' | 'frontend' | 'mcpset' | 'local' | 'extension' | 'skill';
    /** MCP server URL (when type === 'mcp') */
    serverUrl?: string;
    /** MCP Set name (when type === 'mcpset') */
    mcpSetName?: string;
    /**
     * Registry record id (when type === 'local'). `localToolName` is the name
     * the remote server knows, which differs from the exposed name when a
     * server-side tool already held it.
     */
    localServerId?: string;
    localToolName?: string;
    /**
     * Extension id (when type === 'extension'). `extensionToolName` is the name
     * the extension knows, which differs from the exposed one when another
     * source already held it.
     */
    extensionId?: string;
    extensionToolName?: string;
  }

  interface TurnContext extends ChatTurn {
    model: string;
    reasoning: string;
    systemPrompt: string;
    tools: ToolDefinition[];
    sources: Record<string, ToolSource>;
    sessionId: string;
    /** Set when the turn ended in an error; queued messages then wait for the user. */
    failed?: boolean;
    /** Stopped by "interrupt & send": the queue starts the next turn at once. */
    interrupted?: boolean;
  }

  const turnLifecycle = createChatTurnLifecycle();

  interface PendingQuestion {
    question: string;
    header?: string;
    options: Array<{ label: string; description?: string }>;
    multiple?: boolean;
    custom?: boolean;
    resolve: (answer: string) => void;
  }

  type WorkbenchTab = 'prompt' | 'skills' | 'tools' | 'chat' | 'providers' | 'commands';

  /**
   * Durable-history bookkeeping kept strictly parallel to `messages`: index `i`
   * of `meta` always describes `messages[i]`. `sequence` is the single source of
   * truth for "already persisted" — `null` means the message exists only in the
   * browser, a number is the gapless sequence the store assigned. Every append
   * only sends the `null` ones, so a retry after a failed save can never
   * duplicate a row.
   */
  interface MessageMeta {
    id?: string;
    client_id?: string;
    sequence: number | null;
    /** The provider/model pair that produced (or accompanied) this message. */
    provider_key: string;
    model: string;
    /**
     * When this entry came into being — a user message when it was sent, an
     * assistant message when its response finished. Set optimistically from
     * the browser clock and replaced by the stored `created_at` the moment the
     * append returns, so a reload shows the same stamp as the live transcript.
     * `''` while a response is still streaming: it has no completion time yet.
     */
    created_at: string;
    /** Original file names of attachments, consumed when persisting. */
    imageNames: string[];
    /** A /compact summary: the model's context starts here. */
    compaction?: boolean;
    /** Usage of the model call that produced this assistant entry. */
    usage?: CallUsage;
  }

  /** `cost_cents` is absent when the model has no installation price. */
  interface CallUsage { prompt: number; completion: number; cost_cents?: number }

  // ─── Constants ───

  const MAX_TOOL_ITERATIONS = 20;

  /** Frontend-only tool definitions (run entirely in the browser). */
  const FRONTEND_TOOLS: ToolDefinition[] = [
    {
      type: 'function',
      function: {
        name: 'todo_write',
        description: 'Create or update the visible todo list for this chat in the browser. Replaces the entire list with the provided items. Each item has content (description), status (pending/in_progress/completed/cancelled), and priority (high/medium/low).',
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
        description: 'Read the visible todo list for this chat in the browser. Returns all items with their content, status, and priority.',
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
  let modelGroups = $state<Array<{ label: string; models: string[] }>>([]);
  /** Server-side providers from /info; local providers are merged in by rebuildModelOptions. */
  let serverModels: string[] = [];
  let serverModelGroups: Array<{ label: string; models: string[] }> = [];
  let selectedModel = $state('');
  /** '' leaves the provider's default reasoning behaviour untouched. */
  let reasoningEffort = $state('');
  /** Adapter type and per-model efforts per provider reference. */
  let providerReasoning = $state<Record<string, { type: string; efforts?: Record<string, string[]>; capabilities?: Record<string, { input_modalities?: string[] }> }>>({});
  let systemPrompt = $state('');
  let userInput = $state('');
  let activeTool = $state<{ messageIndex: number; callID: string } | null>(null);
  /** Live status line per running agent-skill tool call, keyed by tool call ID. */
  let skillRunProgress = $state<Record<string, string>>({});
  /** Background skill runs started in the current turn. */
  let turnSkillRuns = $state<{ id: string; skill: string; reported: boolean }[]>([]);
  /** Files skill runs delivered during the current turn, attached to its final answer. */
  let turnArtifacts = $state<SkillRunArtifact[]>([]);
  /** Stored types rendered as <img>; every other delivered file is a card. */
  const INLINE_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
  const LOAD_SKILL_TOOL = 'load_skill';
  const RUN_STATUS_TOOL = 'agent_run_status';
  /** Assistant messages shown as raw text instead of rendered markdown, by index. */
  let rawMessages = $state<Record<number, boolean>>({});
  /** Index of the message whose text was just copied, for the check-mark feedback. */
  let copiedIndex = $state<number | null>(null);
  let copiedTimer: ReturnType<typeof setTimeout> | null = null;

  function messageText(content: ChatMessage['content']): string {
    return typeof content === 'string' ? content : getTextContent(content);
  }

  function formatFileSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  async function copyMessage(index: number) {
    const text = messageText(messages[index]?.content ?? '');
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        // Plain-HTTP LAN deployments have no Clipboard API.
        const area = document.createElement('textarea');
        area.value = text;
        area.style.position = 'fixed';
        area.style.opacity = '0';
        document.body.appendChild(area);
        area.select();
        const ok = document.execCommand('copy');
        area.remove();
        if (!ok) throw new Error('copy failed');
      }
      copiedIndex = index;
      if (copiedTimer) clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => { copiedIndex = null; }, 1500);
    } catch {
      addToast('Could not copy to the clipboard', 'alert');
    }
  }
  let messages = $state<ChatMessage[]>([]);
  let toolResults = $derived(toolResultsByMessage(messages));
  let loading = $state(true);
  let streaming = $state(false);

  // Voice input is shared with Sessions; a reset cancels pending microphone work.
  let chatRecording = $state(false);
  let chatTranscribing = $state(false);
  let voiceContext = $state(0);
  let abortController = $state<AbortController | null>(null);
  let chatContainer: HTMLDivElement | undefined = $state();
  let pendingImages = $state<PendingAttachment[]>([]);
  /** Messages written while a turn runs; delivered at its next step boundary. */
  let queuedMessages = $state<QueuedChatMessage<PendingAttachment>[]>([]);
  let activeTurn: TurnContext | null = null;
  let fileInput: HTMLInputElement | undefined = $state();
  let dragging = $state(false);

  // ─── Token Usage State ───

  /** Cumulative token usage across all completion calls in the conversation. */
  let contextTokens = $state(0);
  let completionTokens = $state(0);
  let totalTokens = $state(0);

  // ─── Tools State ───

  /**
   * The workbench — preset list, system prompt, skills and tool catalogues —
   * opens as a modal.
   *
   * It used to be two strips under the toolbar, one per concern, each capped
   * at a fixed height inside the chat column. Between them they took a third
   * of the page while still scrolling their own contents, so the setup was
   * cramped and the transcript was too. They are one dialog now because they
   * are one subject: what this conversation runs with.
   */
  let showWorkbench = $state(false);
  let workbenchPanel: HTMLDivElement | undefined = $state();
  let workbenchTab = $state<WorkbenchTab>('prompt');
  let workbenchBackdropPressStarted = false;
  const allWorkbenchTabs: Array<{ id: WorkbenchTab; label: string }> = [
    { id: 'prompt', label: 'System prompt' },
    { id: 'skills', label: 'Skills' },
    { id: 'tools', label: 'Server tools' },
    { id: 'chat', label: 'Chat tools' },
    { id: 'providers', label: 'Local providers' },
    { id: 'commands', label: 'Commands' },
  ];
  let workbenchTabs = $derived(allWorkbenchTabs.filter(tab => tab.id !== 'providers' || localProvidersAvailable));

  function handleWorkbenchBackdropPointerDown(e: PointerEvent) {
    workbenchBackdropPressStarted = e.target === e.currentTarget;
  }

  function handleWorkbenchBackdropClick(e: MouseEvent) {
    const directBackdropClick = workbenchBackdropPressStarted && e.target === e.currentTarget;
    workbenchBackdropPressStarted = false;
    if (directBackdropClick) showWorkbench = false;
  }
  /**
   * Direct MCP URLs are no longer configurable here: tools now come from MCP
   * sets registered in the installation, which carry credentials, stdio
   * processes and execution admission. Anything a saved conversation already
   * had is kept in `config` (never rewritten) and surfaced as a notice so the
   * missing tools are explained rather than silently gone.
   */
  let legacyMcpUrls = $state<string[]>([]);
  let legacyMcpHeaders = $state<Record<string, string>>({});
  let availableMCPSets = $state<MCPSet[]>([]);
  let selectedMCPSetNames = $state<string[]>([]);
  let mcpSetStatus = $state<Record<string, { tools: string[]; warnings: string[]; error: string; busy: boolean }>>({});
  let skills = $state<Skill[]>([]);
  let selectedSkillNames = $state<string[]>([]);
  // Per-account preset bookkeeping: never save one back before it loaded, or
  // an empty initial state would overwrite the stored preset.
  let defaultsLoaded = $state(false);
  let accountDefaults = initialWorkbenchSetup(FRONTEND_TOOL_NAMES);
  let setupRevision = 0;
  let disposed = false;
  const defaultsSave = createDebouncedSave<WorkbenchSetup>(async setup => {
    await savePlaygroundDefaults(setup).catch(() => {});
  }, 1200);

  // ─── Named presets ───
  //
  // Several saved setups the reader can switch between. The singleton default
  // above still seeds a NEW conversation automatically; a preset is applied
  // deliberately, including to the conversation already open — switching
  // between setups mid-session is the reason for having more than one.
  let personalPresets = $state<ChatPreset[]>([]);
  let workspacePresets = $state<ChatPreset[]>([]);
  let presets = $derived([...personalPresets, ...workspacePresets]);
  /** The preset last applied. Only *reported* while the setup still matches. */
  let appliedPresetId = $state('');
  /**
   * The reader's own unsaved setup, captured the moment a preset replaces it,
   * so cycling or picking presets can always come back to it.
   */
  let customSetup = $state<WorkbenchSetup | null>(null);
  let presetDraftName = $state('');
  let presetSaveScope = $state<'personal' | 'workspace'>('personal');
  let presetSaving = $state(false);

  // Built-in server tools
  let builtinTools = $state<BuiltinToolDef[]>([]);
  let enabledBuiltinTools = $state<string[]>([...accountDefaults.builtin_tools]);


  // Frontend-only tools
  let enabledFrontendTools = $state<string[]>([...accountDefaults.frontend_tools]);

  // ─── Local MCP servers ───
  //
  // MCP endpoints running on this person's own machine. Unlike every other
  // tool source on this page, these are dialled from the browser: the server
  // stores the record and never connects to it.

  let localServers = $state<LocalMCPServer[]>([]);
  let localServersLoaded = $state(false);
  /** Per-record discovery state, keyed by record id. */
  let localStatus = $state<Record<string, { tools: string[]; error: string; hint: string; busy: boolean }>>({});
  /** Approval is per device, so it is read from local storage, not the record. */
  let localApprovedIds = $state<string[]>([]);
  let localMCPAvailable = $derived(isFeatureEnabled(FEATURE_CHAT_LOCAL_MCP));
  let activeLocalServers = $derived(
    localServers.filter(s => localApprovedIds.includes(s.id)),
  );

  /**
   * Editor for the registry, which lives in the tools panel.
   *
   * `localEditorOpen` is explicit rather than derived from whether a record is
   * selected: a new record has none, so inferring it left the Add action with
   * no visible effect. `localEditorKey` remounts the editor per record.
   */
  let localEditorOpen = $state(false);
  let localEditing = $state<LocalMCPServer | undefined>(undefined);
  let localEditorKey = $state(0);
  /** Approval dialog: the tools are shown before the server may be used. */
  let localApprovalFor = $state<LocalMCPServer | null>(null);
  let localApprovalTools = $state<LocalMCPTool[]>([]);

  /**
   * One client per record for the page session, so a stateful server keeps its
   * Mcp-Session-Id across calls. Keyed by id + url: editing the URL must not
   * keep talking to the old address.
   */
  const localClients = new Map<string, LocalMCPClient>();
  const localHeaderCache = new Map<string, Record<string, string>>();

  /**
   * Correlation for the turn in flight. The conversation is the session; each
   * turn is its own trace, matching how the server-side loops record. Set
   * before the first completion so a tool this browser runs lands on the same
   * trace as the generation that asked for it.
   */
  let turnTraceId = $state('');

  function localStorageSafe(): Storage | undefined {
    try {
      return window.localStorage;
    } catch {
      return undefined;
    }
  }

  function refreshLocalApprovals() {
    localApprovedIds = localServers.filter(s => approvalFor(s.id, localStorageSafe())).map(s => s.id);
  }

  /**
   * Headers are revealed per record at the point of dialling rather than being
   * handed out with the list, so the page does not hold every credential for
   * the whole session.
   */
  async function localClientFor(server: LocalMCPServer, signal?: AbortSignal): Promise<LocalMCPClient> {
    const key = `${server.id}|${server.url}`;
    const cached = localClients.get(key);
    if (cached && !signal) return cached;

    let headers = localHeaderCache.get(key);
    if (!headers) {
      const names = Object.keys(server.headers ?? {});
      headers = names.length > 0 ? await revealLocalMCPHeaders(server.id) : {};
      localHeaderCache.set(key, headers);
    }
    const client = new LocalMCPClient(server.url, { headers, signal });
    if (!signal) localClients.set(key, client);

    return client;
  }

  function forgetLocalClient(server: LocalMCPServer) {
    for (const key of [...localClients.keys()]) {
      if (key.startsWith(`${server.id}|`)) localClients.delete(key);
    }
    for (const key of [...localHeaderCache.keys()]) {
      if (key.startsWith(`${server.id}|`)) localHeaderCache.delete(key);
    }
  }

  // ─── Local providers ───
  //
  // OpenAI-compatible endpoints this browser calls directly. The server stores
  // the record and never sends a request to it; a turn on a local model goes
  // from this page to the provider, with tools still dispatched as usual.

  let localProviders = $state<LocalChatProvider[]>([]);
  let localProvidersAvailable = $derived(isFeatureEnabled(FEATURE_CHAT_LOCAL_PROVIDERS));
  /** Enabled on this device (localStorage), by record id. */
  let localProviderEnabledIds = $state<string[]>([]);
  /** Discovered models and last error per record id. */
  let localProviderStatus = $state<Record<string, { models: string[]; error: string; busy: boolean }>>({});
  let localProviderEditorOpen = $state(false);
  let localProviderEditing = $state<LocalChatProvider | undefined>(undefined);
  let localProviderEditorKey = $state(0);
  /** Credentials per id|base_url, revealed when first needed. */
  const localProviderSecrets = new Map<string, LocalChatProviderSecrets>();

  function rebuildModelOptions() {
    const localGroups: Array<{ label: string; models: string[] }> = [];
    if (localProvidersAvailable) {
      for (const p of localProviders) {
        if (!localProviderEnabledIds.includes(p.id)) continue;
        const ids = localProviderStatus[p.id]?.models ?? [];
        if (ids.length === 0) continue;
        localGroups.push({ label: `${p.name} · This device`, models: ids.map(m => localModelRef(p.name, m)) });
      }
    }
    modelGroups = [...serverModelGroups, ...localGroups];
    models = [...serverModels, ...localGroups.flatMap(g => g.models)];
  }

  function refreshLocalProviderEnabled() {
    localProviderEnabledIds = localProviders.filter(p => localProviderEnabled(p.id, localStorageSafe())).map(p => p.id);
  }

  async function localProviderSecretsFor(p: LocalChatProvider): Promise<LocalChatProviderSecrets> {
    const key = `${p.id}|${p.base_url}`;
    const cached = localProviderSecrets.get(key);
    if (cached) return cached;
    const hasSecret = !!p.api_key || Object.keys(p.headers ?? {}).length > 0;
    const secrets = hasSecret ? await revealLocalChatProvider(p.id) : { api_key: '', headers: {} };
    localProviderSecrets.set(key, secrets);

    return secrets;
  }

  function forgetLocalProviderSecrets(id: string) {
    for (const key of [...localProviderSecrets.keys()]) {
      if (key.startsWith(`${id}|`)) localProviderSecrets.delete(key);
    }
  }

  /** Lists the provider's models from <base>/models, directly from this browser. */
  async function discoverLocalProviderModels(p: LocalChatProvider): Promise<void> {
    localProviderStatus = { ...localProviderStatus, [p.id]: { models: localProviderStatus[p.id]?.models ?? [], error: '', busy: true } };
    try {
      const secrets = await localProviderSecretsFor(p);
      let res: Response;
      try {
        res = await fetch(localProviderEndpoint(p.base_url, 'models'), {
          headers: localProviderHeaders(secrets),
          signal: AbortSignal.timeout(15000),
        });
      } catch (e) {
        throw describeLocalProviderError(e, p.name, p.base_url);
      }
      if (!res.ok) throw new Error(`${p.name}: GET /models answered HTTP ${res.status}`);
      const ids = parseModelList(await res.json());
      if (ids.length === 0) throw new Error(`${p.name} reported no models`);
      localProviderStatus = { ...localProviderStatus, [p.id]: { models: ids, error: '', busy: false } };
    } catch (e: any) {
      localProviderStatus = { ...localProviderStatus, [p.id]: { models: [], error: e?.message || 'Could not list models', busy: false } };
    }
    rebuildModelOptions();
  }

  async function loadLocalProviders() {
    if (!localProvidersAvailable) return;
    try {
      localProviders = await listLocalChatProviders();
      refreshLocalProviderEnabled();
      await Promise.all(localProviders.filter(p => localProviderEnabledIds.includes(p.id)).map(discoverLocalProviderModels));
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load local providers', 'alert');
    }
  }

  function editLocalProvider(p?: LocalChatProvider) {
    localProviderEditing = p;
    localProviderEditorKey++;
    localProviderEditorOpen = true;
  }

  function closeLocalProviderEditor() {
    localProviderEditing = undefined;
    localProviderEditorOpen = false;
  }

  function localProviderSaved(stored: LocalChatProvider[], edited?: LocalChatProvider) {
    localProviders = stored;
    if (edited) {
      forgetLocalProviderSecrets(edited.id);
      if (localProviderEnabledIds.includes(edited.id)) void discoverLocalProviderModels(edited);
    }
    refreshLocalProviderEnabled();
    closeLocalProviderEditor();
    rebuildModelOptions();
  }

  async function removeLocalProvider(p: LocalChatProvider) {
    try {
      localProviders = await saveLocalChatProviders(localProviders.filter(x => x.id !== p.id));
      disableLocalProvider(p.id, localStorageSafe());
      forgetLocalProviderSecrets(p.id);
      refreshLocalProviderEnabled();
      if (localProviderEditing?.id === p.id) closeLocalProviderEditor();
      rebuildModelOptions();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove', 'alert');
    }
  }

  function toggleLocalProvider(p: LocalChatProvider) {
    if (localProviderEnabledIds.includes(p.id)) {
      disableLocalProvider(p.id, localStorageSafe());
      refreshLocalProviderEnabled();
      rebuildModelOptions();
      return;
    }
    enableLocalProvider(p.id, localStorageSafe());
    refreshLocalProviderEnabled();
    void discoverLocalProviderModels(p);
  }

  /** The record a `local:<name>/<model>` reference resolves to, enabled on this device. */
  function localProviderFor(ref: string): { provider: LocalChatProvider; model: string } {
    const parsed = parseLocalModelRef(ref);
    const provider = parsed && localProviders.find(p => p.name === parsed.provider);
    if (!parsed || !provider) throw new Error(`Local provider for ${ref} is not configured on this account.`);
    if (!localProvidersAvailable) throw new Error('Local providers are disabled in this installation.');
    if (!localProviderEnabledIds.includes(provider.id)) throw new Error(`Local provider "${provider.name}" is not enabled on this device.`);

    return { provider, model: parsed.model };
  }

  // ─── Browser extensions ───
  //
  // The second tool source this page dials itself, and the only one with no
  // address at all: an extension has no URL, so neither AT nor this page can
  // connect to one. What they share is `window.postMessage` through the
  // extension's content script, which `lib/helper/extension-bridge` speaks.
  //
  // Nothing here is specific to one extension. The page broadcasts and every
  // extension implementing the protocol answers for itself, so a second one
  // needs no change in Chats.

  let extensionsAvailable = $derived(isFeatureEnabled(FEATURE_CHAT_EXTENSIONS));
  let webConnectionEnabled = $state(false);
  let extensions = $state<ExtensionDescriptor[]>([]);
  /** Per-extension discovery state, keyed by extension id. */
  let extensionStatus = $state<Record<string, { tools: string[]; error: string; busy: boolean }>>({});
  /** Approval is per device, so it is read from local storage, not from a record. */
  let extensionApprovedIds = $state<string[]>([]);
  let extensionsScanning = $state(false);
  /** True once a scan has finished, so "none found" is not shown before one ran. */
  let extensionsScanned = $state(false);
  let extensionApprovalTarget = $state<ExtensionDescriptor | null>(null);
  let extensionApprovalTools = $state<ExtensionTool[]>([]);
  let extensionInspectorTarget = $state<ExtensionDescriptor | null>(null);
  let extensionInspectorTools = $state<ExtensionTool[]>([]);
  let extensionInspectorPanel: HTMLDivElement | undefined = $state();
  let activeExtensions = $derived(extensions.filter(e => extensionApprovedIds.includes(e.id)));

  let extensionBridge: ExtensionBridge | null = null;
  let extensionUnsubscribe: (() => void) | null = null;
  let extensionScanRequested = false;
  let extensionScanGeneration = 0;

  /**
   * One bridge for the page session. It is created as soon as the feature is
   * on rather than at the first scan, because an extension announces itself
   * the moment the person connects it from the extension's own UI — and a page
   * that was not listening would show nothing until it was reloaded.
   */
  function ensureExtensionBridge(): ExtensionBridge | null {
    if (!extensionsAvailable || !webConnectionEnabled) return null;
    if (extensionBridge) return extensionBridge;
    try {
      extensionBridge = new ExtensionBridge({ window, origin: window.location.origin });
    } catch {
      return null;
    }
    extensionUnsubscribe = extensionBridge.subscribe(event => {
      // A tool-list change only matters for an extension already in use;
      // anything else is a membership change and needs a fresh scan.
      if (event.event === 'tools_changed' && extensionApprovedIds.includes(event.extension)) {
        void discoverTools();

        return;
      }
      void scanExtensions();
    });

    return extensionBridge;
  }

  function refreshExtensionApprovals() {
    extensionApprovedIds = extensions.filter(e => extensionApprovalFor(e.id, localStorageSafe())).map(e => e.id);
  }

  /**
   * Asks every installed extension to identify itself.
   *
   * Silence is a legitimate answer: an extension that has not been connected to
   * this origin is expected not to reply, so "none found" covers both "none
   * installed" and "none connected here" — the page must not claim to know
   * which, and saying so is what keeps this from being a fingerprinting probe.
   */
  async function scanExtensions() {
    const generation = ++extensionScanGeneration;
    const bridge = ensureExtensionBridge();
    if (!bridge) {
      extensions = [];
      extensionApprovedIds = [];

      return;
    }
    extensionsScanning = true;
    try {
      const found = await bridge.discover();
      if (generation !== extensionScanGeneration || bridge !== extensionBridge) return;
      extensions = found;
      refreshExtensionApprovals();
      void discoverTools();
    } catch {
      // discover() resolves with what it collected; a throw here means the
      // bridge is gone, which the empty list already says.
      if (generation === extensionScanGeneration) extensions = [];
    } finally {
      if (generation === extensionScanGeneration) {
        extensionsScanning = false;
        extensionsScanned = true;
      }
    }
  }

  $effect(() => {
    if (!extensionsAvailable || !webConnectionEnabled) {
      // The feature catalog arrives after mount and `isFeatureEnabled` reports
      // enabled until it does, so this is the path that runs when the
      // installation has it off — a disabled feature must not be left holding
      // a live channel to whatever is listening on the page.
      extensionUnsubscribe?.();
      extensionUnsubscribe = null;
      extensionBridge?.dispose();
      extensionBridge = null;
      extensions = [];
      extensionApprovedIds = [];
      extensionScanRequested = false;
      extensionScanGeneration += 1;
      extensionsScanning = false;
      untrack(() => { void discoverTools(); });

      return;
    }
    if (extensionScanRequested) return;
    extensionScanRequested = true;
    untrack(() => { void scanExtensions(); });
  });

  /** Lists one extension's tools and records why it failed when it did. */
  async function discoverExtensionTools(ext: ExtensionDescriptor): Promise<ExtensionTool[]> {
    const bridge = ensureExtensionBridge();
    if (!bridge) return [];
    extensionStatus = { ...extensionStatus, [ext.id]: { tools: [], error: '', busy: true } };
    try {
      const tools = await bridge.listTools(ext.id);
      extensionStatus = {
        ...extensionStatus,
        [ext.id]: { tools: tools.map(t => t.name), error: '', busy: false },
      };

      return tools;
    } catch (e: any) {
      extensionStatus = {
        ...extensionStatus,
        [ext.id]: { tools: [], error: e?.message || 'the extension did not answer', busy: false },
      };
      throw e;
    }
  }

  /**
   * Opening the approval dialog lists the tools first, so the person decides
   * against what the extension actually offers rather than against its name.
   */
  async function beginExtensionApproval(ext: ExtensionDescriptor) {
    extensionApprovalTarget = ext;
    extensionApprovalTools = [];
    try {
      extensionApprovalTools = await discoverExtensionTools(ext);
    } catch {
      /* the failure is rendered from extensionStatus */
    }
  }

  async function inspectExtensionTools(ext: ExtensionDescriptor) {
    extensionInspectorTarget = ext;
    extensionInspectorTools = [];
    try {
      extensionInspectorTools = await discoverExtensionTools(ext);
    } catch {
      /* the shared extension status renders the failure and retry path */
    }
  }

  function handleWorkbenchTabsKeydown(event: KeyboardEvent) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const current = workbenchTabs.findIndex(tab => tab.id === workbenchTab);
    const next = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? workbenchTabs.length - 1
        : (current + (event.key === 'ArrowRight' ? 1 : -1) + workbenchTabs.length) % workbenchTabs.length;
    workbenchTab = workbenchTabs[next].id;
    requestAnimationFrame(() => {
      workbenchPanel?.querySelector<HTMLButtonElement>(`#workbench-tab-${workbenchTab}`)?.focus();
    });
  }

  function confirmExtensionApproval() {
    const ext = extensionApprovalTarget;
    if (!ext) return;
    approveExtension(ext.id, extensionApprovalTools.map(t => t.name), localStorageSafe());
    refreshExtensionApprovals();
    extensionApprovalTarget = null;
    void discoverTools();
  }

  /** One action, effective immediately: the next turn offers nothing from it. */
  function disableExtension(ext: ExtensionDescriptor) {
    revokeExtension(ext.id, localStorageSafe());
    refreshExtensionApprovals();
    if (extensionInspectorTarget?.id === ext.id) extensionInspectorTarget = null;
    void discoverTools();
  }

  /**
   * Everything this browser may run on the person's own device, in one list.
   *
   * It is one strip rather than two because the question a reader has is "what
   * can this page reach on my machine", not "which subsystem is it".
   */
  let activeBrowserTools = $derived([
    ...activeLocalServers.map(server => ({
      key: `local:${server.id}`,
      name: server.name,
      disable: () => disableLocalServer(server),
    })),
    ...activeExtensions.map(ext => ({
      key: `extension:${ext.id}`,
      name: ext.name,
      disable: () => disableExtension(ext),
    })),
  ]);

  // Todo panel
  let todos = $state<TodoItem[]>([]);
  let showTodoPanel = $state(false);

  // Pending question is answered inline in its originating assistant message.
  let pendingQuestion = $state<PendingQuestion | null>(null);
  let questionSelections = $state<string[]>([]);

  // Discovered tools and dispatch map
  let discoveredTools = $state<ToolDefinition[]>([]);
  let toolSourceMap = $state<Record<string, ToolSource>>({});
  let skillSystemPrompts = $state<string[]>([]);
  let loadingTools = $state(false);
  let toolCount = $derived(discoveredTools.length);
  let todoActiveCount = $derived(todos.filter(t => t.status === 'pending' || t.status === 'in_progress').length);

  // ─── Durable history state ───

  /** Empty string means "unsaved scratch buffer". */
  let conversationId = $state('');
  // Used only while saving is unavailable; keep unsaved turns correlated too.
  let scratchSessionId = '';
  let conversation = $state<PlaygroundConversation | null>(null);
  let parentTitle = $state('');
  let meta = $state<MessageMeta[]>([]);
  let showShareDialog = $state(false);
  let conversations = $state<PlaygroundConversation[]>([]);
  let conversationsLoading = $state(false);
  let conversationsCursor = $state('');
  // Below lg the list is an overlay, so it starts closed on narrow screens.
  let showConversations = $state(typeof window === 'undefined' || window.matchMedia('(min-width: 1024px)').matches);
  let historyLoading = $state(false);
  let historyTruncated = $state(false);
  let historyCursor = $state('');
  let loadingOlderHistory = $state(false);
  let saving = $state(false);
  let confirmClear = $state(false);

  // ─── Media storage ───

  const mediaCache = createChatMediaCache({
    download: getMediaDataURL,
    upload: (dataUrl, name) => uploadMedia(dataUrlToBlob(dataUrl), name),
  });

  /** Set once a 503 proves storage is off: one quiet hint, never a toast per image. */
  let mediaStorageOff = $state(false);
  let mediaHintDismissed = $state(false);

  /** Bytes for a stored image, fetched lazily and cached per id. */
  function mediaDataUrl(id: string): Promise<string> {
    return mediaCache.download(id).catch(() => '');
  }

  /**
   * Upload one attachment for persistence. Returns `''` when it could not be
   * stored, which the caller turns into the omitted descriptor so the turn
   * still persists. 413 and 415 are named per image; 503 only raises the
   * one-time hint, because a disabled backend is a configuration fact, not a
   * per-image error worth repeating.
   */
  async function uploadAttachment(dataUrl: string, name: string): Promise<string> {
    try {
      return await mediaCache.upload(dataUrl, name);
    } catch (e) {
      if (isMediaStorageDisabled(e)) {
        mediaStorageOff = true;
        return '';
      }
      addToast(mediaUploadErrorMessage(e, name), 'alert');
      return '';
    }
  }

  let unsavedCount = $derived(meta.reduce((n, m) => n + (m.sequence === null ? 1 : 0), 0));
  let sharingAvailable = $derived(isFeatureEnabled(FEATURE_CHAT_SHARING));
  let shareBoundaries = $derived.by(() => messages.flatMap((message, index) => {
    const sequence = meta[index]?.sequence;
    if (message.role !== 'assistant' || sequence === null || sequence === undefined || (message.tool_calls?.length ?? 0) > 0) return [];
    return [{ sequence, label: `Message ${sequence} · ${getTextContent(message.content).slice(0, 56) || 'completed response'}` }];
  }));

  /**
   * A restored conversation may name a model the provider list no longer
   * advertises. Keeping it as an option is both honest and keeps the select
   * binding from silently rewriting the conversation's stored pair.
   */
  let modelOptions = $derived(selectedModel && !models.includes(selectedModel) ? [selectedModel, ...models] : models);

  /**
   * Efforts the selected model accepts: its own list when the server knows it
   * (detected from the model family or set on the provider), otherwise what
   * the adapter can express. Empty hides the control.
   */
  let reasoningEffortOptions = $derived.by(() => {
    const { provider_key, model } = splitModel(selectedModel);
    const p = providerReasoning[provider_key];
    return p ? modelReasoningEfforts(p.type, model, p.efforts).efforts : [];
  });
  /** Input modalities the selected model is known to accept; undefined = unknown. */
  let acceptedInputs = $derived.by(() => {
    const { provider_key, model } = splitModel(selectedModel);
    return providerReasoning[provider_key]?.capabilities?.[model]?.input_modalities as InputModality[] | undefined;
  });
  /** Pending attachments the selected model cannot read. */
  let pendingRefusals = $derived(pendingImages.map(a => attachmentRefusal(a, acceptedInputs)).filter(Boolean));

  /** Sent only when the current adapter accepts it; the choice itself is kept across model switches. */
  let effectiveReasoningEffort = $derived(reasoningEffortOptions.includes(reasoningEffort) ? reasoningEffort : '');

  /** Last state successfully written to the conversation row, for diffing. */
  let savedSettings: { system_prompt: string; provider_key: string; model: string; config: string } | null = null;
  let settingsTimer: ReturnType<typeof setTimeout> | null = null;
  let confirmClearTimer: ReturnType<typeof setTimeout> | null = null;
  /** Mirrors the id currently reflected in the hash route. */
  let routedId = '';

  const HISTORY_PAGE_SIZE = 50;

  /** `provider_key/model` — the model half may itself contain slashes. */
  function splitModel(value: string): { provider_key: string; model: string } {
    const at = value.indexOf('/');
    return at < 0 ? { provider_key: value, model: '' } : { provider_key: value.slice(0, at), model: value.slice(at + 1) };
  }

  function joinModel(providerKey: string, model: string): string {
    return model ? `${providerKey}/${model}` : providerKey;
  }

  // ─── Workbench config round-trip ───

  function currentConfig(): Record<string, unknown> {
    const { model, system_prompt, reasoning_effort, ...setup } = currentSetup();
    return {
      // Preserved, not offered: a conversation saved before direct MCP URLs
      // were removed keeps its record instead of having it rewritten away.
      mcp_urls: [...legacyMcpUrls],
      mcp_headers: { ...legacyMcpHeaders },
      ...setup,
      ...(reasoning_effort ? { reasoning_effort } : {}),
    };
  }

  function applyConfig(config: Record<string, unknown> | null | undefined) {
    const c = config ?? {};
    const names = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
    legacyMcpUrls = names(c.mcp_urls);
    applySetup(normalizeWorkbenchSetup({ ...c, model: selectedModel, system_prompt: systemPrompt }));
    const headers = c.mcp_headers;
    legacyMcpHeaders = headers && typeof headers === 'object' && !Array.isArray(headers)
      ? Object.fromEntries(Object.entries(headers as Record<string, unknown>).filter((e): e is [string, string] => typeof e[1] === 'string'))
      : {};
  }

  function applySetup(setup: WorkbenchSetup) {
    if (setup.model && models.includes(setup.model)) selectedModel = setup.model;
    systemPrompt = setup.system_prompt;
    reasoningEffort = setup.reasoning_effort;
    selectedMCPSetNames = [...setup.mcp_sets];
    selectedSkillNames = [...setup.skills];
    enabledBuiltinTools = [...setup.builtin_tools];
    enabledFrontendTools = [...setup.frontend_tools];
    showTodoPanel = enabledFrontendTools.includes('todo_write') || enabledFrontendTools.includes('todo_read');
  }

  function settingsSnapshot() {
    const { provider_key, model } = splitModel(selectedModel);
    return { system_prompt: systemPrompt, provider_key, model, config: JSON.stringify(currentConfig()) };
  }

  /** Debounced: tool toggles and keystrokes must not turn into a PATCH storm. */
  function scheduleSettingsSave() {
    if (!conversationId) return;
    if (settingsTimer) clearTimeout(settingsTimer);
    settingsTimer = setTimeout(() => { settingsTimer = null; void saveSettings(); }, 800);
  }

  async function saveSettings() {
    const id = conversationId;
    const generation = turnLifecycle.generation();
    if (!id || !savedSettings) return;
    const next = settingsSnapshot();
    const patch: PlaygroundConversationInput = {};
    if (next.system_prompt !== savedSettings.system_prompt) patch.system_prompt = next.system_prompt;
    // Never overwrite a stored pair with an empty one: a conversation whose
    // provider was removed still deserves to remember what it ran on.
    if (next.provider_key && next.provider_key !== savedSettings.provider_key) patch.provider_key = next.provider_key;
    if (next.provider_key && next.model !== savedSettings.model) patch.model = next.model;
    if (next.config !== savedSettings.config) patch.config = JSON.parse(next.config);
    try {
      // Returns null without calling the API when nothing changed: PATCH {} is a 400.
      const updated = await patchPlaygroundConversation(id, patch);
      if (!updated || disposed || conversationId !== id || turnLifecycle.generation() !== generation) return;
      savedSettings = next;
      conversation = updated;
      mergeConversation(updated);
    } catch (e) {
      if (disposed || turnLifecycle.generation() !== generation) return;
      addToast(playgroundErrorMessage(e, 'Failed to save conversation settings'), 'alert');
    }
  }

  // ─── Conversation list ───

  function mergeConversation(c: PlaygroundConversation) {
    conversations = sortPlaygroundConversations([c, ...conversations.filter(x => x.id !== c.id)]);
  }

  async function loadConversations(more = false) {
    if (conversationsLoading) return;
    if (more && !conversationsCursor) return;
    conversationsLoading = true;
    try {
      const before = more ? conversationsCursor : '';
      const res = await listPlaygroundConversations({ limit: 50, before: before || undefined });
      const page = res.data ?? [];
      conversations = sortPlaygroundConversations(more
        ? [...conversations, ...page.filter(c => !conversations.some(x => x.id === c.id))]
        : page);
      conversationsCursor = res.meta?.next_before ?? '';
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to load conversations'), 'alert');
    } finally {
      conversationsLoading = false;
    }
  }

  function selectConversation(id: string) {
    if (id === conversationId) return;
    push(playgroundRoute(id));
  }

  function newConversation() {
    // Already on the scratch route: the hash would not change, so reset directly.
    if (!conversationId) { void openConversation(''); return; }
    push('/chats');
  }

  async function renameConversation(id: string, title: string) {
    const previous = conversations.find(c => c.id === id);
    conversations = conversations.map(c => (c.id === id ? { ...c, title } : c));
    try {
      const updated = await patchPlaygroundConversation(id, { title });
      if (!updated) return;
      mergeConversation(updated);
      if (conversationId === id) conversation = updated;
    } catch (e) {
      if (previous) conversations = conversations.map(c => (c.id === id ? previous : c));
      addToast(playgroundErrorMessage(e, 'Failed to rename conversation'), 'alert');
    }
  }

  async function removeConversation(id: string) {
    try {
      await deletePlaygroundConversation(id);
      conversations = conversations.filter(c => c.id !== id);
      if (conversationId === id) push('/chats');
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to delete conversation'), 'alert');
    }
  }

  // ─── Open / restore ───

  function resetBuffer() {
    turnLifecycle.invalidate();
    voiceContext++;
    if (abortController) { abortController.abort(); abortController = null; }
    streaming = false;
    messages = [];
    rawMessages = {};
    meta = [];
    systemPrompt = '';
    pendingImages = [];
    queuedMessages = [];
    todos = [];
    pendingQuestion = null;
    contextTokens = 0;
    completionTokens = 0;
    totalTokens = 0;
    confirmClear = false;
  }

  function storedUsage(data: Record<string, unknown> | undefined): CallUsage | undefined {
    const u = data?.at_usage as CallUsage | undefined;
    if (!u || typeof u.prompt !== 'number') return undefined;
    return { prompt: u.prompt, completion: Number(u.completion) || 0, ...(typeof u.cost_cents === 'number' ? { cost_cents: u.cost_cents } : {}) };
  }

  function toChatMessage(m: PlaygroundMessage): ChatMessage {
    const data = (m.data ?? {}) as Record<string, any>;
    const msg: ChatMessage = { role: m.role, content: (data.content ?? '') as string | ContentPart[] };
    if (Array.isArray(data.tool_calls)) msg.tool_calls = data.tool_calls as ToolCall[];
    if (typeof data.tool_call_id === 'string') msg.tool_call_id = data.tool_call_id;
    return msg;
  }

  function toMessageData(m: ChatMessage): Record<string, unknown> {
    const data: Record<string, unknown> = { content: m.content };
    if (m.tool_calls) data.tool_calls = m.tool_calls;
    if (m.tool_call_id) data.tool_call_id = m.tool_call_id;
    return data;
  }

  async function openConversation(id: string) {
    if (settingsTimer) { clearTimeout(settingsTimer); settingsTimer = null; void saveSettings(); }
    void defaultsSave.flush();
    toolDiscoveryVersion++;
    resetBuffer();
    const generation = turnLifecycle.generation();
    const current = () => !disposed && turnLifecycle.generation() === generation && conversationId === id;
    conversationId = id;
    scratchSessionId = '';
    conversation = null;
    parentTitle = '';
    historyTruncated = false;
    historyCursor = '';
    loadingOlderHistory = false;
    savedSettings = null;
    historyLoading = false;
    appliedPresetId = '';
    customSetup = null;
    if (!id) {
      legacyMcpUrls = [];
      legacyMcpHeaders = {};
      const setup = newWorkbenchSetup(accountDefaults, models);
      selectedModel = setup.model;
      applySetup(setup);
      void discoverTools();
      return;
    }

    historyLoading = true;
    try {
      const c = await getPlaygroundConversation(id);
      if (!current()) return;
      conversation = c;
      systemPrompt = c.system_prompt || '';
      const pair = joinModel(c.provider_key || '', c.model || '');
      if (pair) selectedModel = pair;
      applyConfig(c.config);
      savedSettings = settingsSnapshot();
      mergeConversation(c);

      // Rendering is paged; completions resolve the unseen prefix on the server.
      const res = await listPlaygroundMessages(id, { limit: HISTORY_PAGE_SIZE });
      if (!current()) return;
      const loaded = res.data ?? [];
      historyCursor = res.meta?.next_before ?? '';
      historyTruncated = !!historyCursor;

      messages = loaded.map(toChatMessage);
      todos = latestTodos(messages) ?? [];
      rawMessages = {};
      meta = loaded.map(m => ({ id: m.id, sequence: m.sequence, provider_key: m.provider_key, model: m.model, created_at: m.created_at, imageNames: [], compaction: m.data?.[COMPACTION_FLAG] === true, usage: storedUsage(m.data) }));
      if (c.forked_from_id) void loadParentTitle(c.forked_from_id);
      void discoverTools();
      scrollToBottom(true);
    } catch (e) {
      if (!current()) return;
      addToast(playgroundErrorMessage(e, 'Failed to open conversation'), 'alert');
    } finally {
      if (current()) historyLoading = false;
    }
  }

  async function loadOlderHistory() {
    if (!historyCursor || loadingOlderHistory || streaming || saving || historyLoading) return;
    const id = conversationId, cursor = historyCursor, generation = turnLifecycle.generation();
    loadingOlderHistory = true;
    try {
      const res = await listPlaygroundMessages(id, { limit: HISTORY_PAGE_SIZE, before: cursor });
      if (disposed || generation !== turnLifecycle.generation() || conversationId !== id || streaming || saving) return;
      const previousHeight = chatContainer?.scrollHeight ?? 0;
      const previousTop = chatContainer?.scrollTop ?? 0;
      const known = new Set(meta.map(m => m.id));
      const older = (res.data ?? []).filter(m => !known.has(m.id));
      const hadTodoCall = latestTodos(messages) !== null;
      messages = [...older.map(toChatMessage), ...messages];
      // The newest call wins; older pages matter only when none was loaded yet.
      if (!hadTodoCall) todos = latestTodos(messages) ?? [];
      meta = [...older.map(m => ({ id: m.id, sequence: m.sequence, provider_key: m.provider_key, model: m.model, created_at: m.created_at, imageNames: [], compaction: m.data?.[COMPACTION_FLAG] === true, usage: storedUsage(m.data) })), ...meta];
      rawMessages = Object.fromEntries(Object.entries(rawMessages).map(([key, value]) => [Number(key) + older.length, value]));
      historyCursor = res.meta?.next_before ?? '';
      historyTruncated = !!historyCursor;
      await tick();
      if (conversationId === id && chatContainer) chatContainer.scrollTop = previousTop + chatContainer.scrollHeight - previousHeight;
    } catch (e) {
      if (conversationId === id) addToast(playgroundErrorMessage(e, 'Failed to load older messages'), 'alert');
    } finally {
      if (conversationId === id && generation === turnLifecycle.generation()) loadingOlderHistory = false;
    }
  }

  /** A deleted parent leaves `forked_from_sequence` but drops `forked_from_id`. */
  async function loadParentTitle(id: string) {
    try {
      const parent = await getPlaygroundConversation(id);
      if (conversation?.forked_from_id === id) parentTitle = parent.title?.trim() || 'Untitled conversation';
    } catch {
      parentTitle = '';
    }
  }

  $effect(() => {
    const id = params.id ?? '';
    if (id === routedId) return;
    routedId = id;
    untrack(() => { void openConversation(id); });
  });

  // Move focus into the dialog when it opens: Escape is handled on the panel,
  // so a reader whose focus was still on the page behind it could not close
  // the thing covering the page.
  $effect(() => {
    if (showWorkbench) workbenchPanel?.focus();
  });

  $effect(() => {
    if (extensionInspectorTarget) extensionInspectorPanel?.focus();
  });

  // ─── Persistence ───

  async function ensureConversation(seedTitle: string): Promise<string> {
    if (conversationId) return conversationId;
    const generation = turnLifecycle.generation();
    const snapshot = settingsSnapshot();
    const { provider_key, model } = splitModel(selectedModel);
    const created = await createPlaygroundConversation({
      title: playgroundTitleFrom(seedTitle),
      system_prompt: systemPrompt,
      provider_key,
      model,
      config: currentConfig(),
    });
    if (disposed || generation !== turnLifecycle.generation()) throw new DOMException('Chat changed', 'AbortError');
    conversationId = created.id;
    // Claim the route before pushing so the router effect does not reload and
    // discard the in-flight turn.
    routedId = created.id;
    conversation = created;
    savedSettings = snapshot;
    mergeConversation(created);
    push(playgroundRoute(created.id));
    scheduleSettingsSave();
    return created.id;
  }

  /**
   * Append every message this turn produced in one batched call. Failures are
   * non-destructive: the transcript stays in memory with `sequence === null`,
   * so the toolbar retry re-sends exactly the same, still-unsaved messages.
   */
  interface TranscriptScope { id: string; generation: number }
  interface PendingEntry { index: number; input: PlaygroundMessageInput; imageNames: string[] }
  const transcriptWriter = createTranscriptWriter<TranscriptScope, PendingEntry, PlaygroundMessageInput, PlaygroundMessage>({
    current: scope => !disposed && conversationId === scope.id && turnLifecycle.generation() === scope.generation,
    snapshot: () => messages.flatMap((message, index) => meta[index]?.sequence === null && message.role !== 'system' ? [{
      index,
      input: { client_id: meta[index].client_id ?? (meta[index].client_id = crypto.randomUUID()), role: message.role as PlaygroundRole, provider_key: meta[index].provider_key, model: meta[index].model, data: JSON.parse(JSON.stringify({ ...toMessageData(message), ...(meta[index].compaction ? { [COMPACTION_FLAG]: true } : {}), ...(meta[index].usage ? { at_usage: meta[index].usage } : {}) })) },
      imageNames: [...meta[index].imageNames],
    }] : []),
    prepare: async entry => ({ ...entry.input, data: await persistPlaygroundImages(entry.input.data, entry.imageNames, uploadAttachment) }),
    append: (scope, inputs) => appendPlaygroundMessages(scope.id, inputs),
    adopt: (entry, stored) => {
      // If storage omitted an attachment, the live transcript still has its
      // bytes. Keep sending those rather than silently replacing them by a
      // saved omission descriptor until this conversation is reopened.
      if (meta[entry.index]) meta[entry.index] = { ...meta[entry.index], id: JSON.stringify(stored.data ?? {}).includes('"omitted":true') ? undefined : stored.id, sequence: stored.sequence, created_at: stored.created_at || meta[entry.index].created_at };
    },
    busy: value => { saving = value; },
    batchSize: PLAYGROUND_MESSAGE_BATCH_MAX,
  });

  async function persistPending() {
    const scope = { id: conversationId, generation: turnLifecycle.generation() };
    if (!scope.id) return;
    try {
      await transcriptWriter.write(scope);
      if (disposed || turnLifecycle.generation() !== scope.generation) return;
      const current = conversations.find(c => c.id === scope.id);
      if (current) mergeConversation({ ...current, updated_at: new Date().toISOString() });
    } catch (e) {
      if (disposed || turnLifecycle.generation() !== scope.generation) return;
      addToast(playgroundErrorMessage(e, 'Failed to save messages. They are kept in the transcript — retry from the toolbar.'), 'alert');
    }
  }

  /** Keep the first `keep` messages, and drop the stored tail to match. */
  async function truncateFrom(keep: number) {
    const generation = turnLifecycle.generation();
    const removed = meta.slice(keep).map(m => m.sequence).filter((s): s is number => s !== null);
    messages = messages.slice(0, keep);
    meta = meta.slice(0, keep);
    // Retry/edit drops later todo_write calls; show the list as of what remains.
    const remainingTodos = latestTodos(messages);
    if (remainingTodos || !historyTruncated) todos = remainingTodos ?? [];
    if (!conversationId || removed.length === 0) return;
    try {
      await truncatePlaygroundMessages(conversationId, Math.min(...removed));
    } catch (e) {
      if (disposed || turnLifecycle.generation() !== generation) return;
      addToast(playgroundErrorMessage(e, 'Failed to trim stored history'), 'alert');
    }
  }

  async function forkFrom(index: number) {
    const sequence = meta[index]?.sequence;
    if (!conversationId || sequence === null || sequence === undefined) return;
    try {
      const forked = await forkPlaygroundConversation(conversationId, sequence);
      mergeConversation(forked);
      push(playgroundRoute(forked.id));
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to fork conversation'), 'alert');
    }
  }

  onDestroy(() => {
    turnLifecycle.invalidate();
    abortController?.abort();
    disposed = true;
    toolDiscoveryVersion++;
    void defaultsSave.flush();
    if (confirmClearTimer) clearTimeout(confirmClearTimer);
    if (settingsTimer) { clearTimeout(settingsTimer); settingsTimer = null; void saveSettings(); }
    extensionUnsubscribe?.();
    extensionBridge?.dispose();
    extensionBridge = null;
  });

  // ─── Load providers/models ───

  async function loadInfo() {
    loading = true;
    try {
      const info = await getInfo();

      // Build full model list: provider_key/model
      const allModels: string[] = [];
      const groups: Array<{ label: string; models: string[] }> = [];
      const reasoning: Record<string, { type: string; efforts?: Record<string, string[]>; capabilities?: Record<string, { input_modalities?: string[] }> }> = {};
      for (const p of info.providers ?? []) {
        const reference = p.reference || p.key;
        reasoning[reference] = { type: p.type, efforts: p.reasoning_efforts, capabilities: p.model_capabilities };
        const providerModels: string[] = [];
        if (p.models && p.models.length > 0) {
          for (const m of p.models) {
            providerModels.push(`${reference}/${m}`);
          }
        } else if (p.default_model) {
          providerModels.push(`${reference}/${p.default_model}`);
        }
        if (providerModels.length > 0) {
          const scope = p.scope ? ` · ${p.scope[0].toUpperCase()}${p.scope.slice(1)}` : '';
          groups.push({ label: `${p.key}${scope}`, models: providerModels });
          allModels.push(...providerModels);
        }
      }
      allModels.sort((a, b) => a.localeCompare(b));
      serverModelGroups = groups.sort((a, b) => a.label.localeCompare(b.label));
      serverModels = allModels;
      rebuildModelOptions();
      providerReasoning = reasoning;
      if (models.length > 0 && !selectedModel) {
        selectedModel = models[0];
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load provider info', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadSkills() {
    try {
      const res = await listSkills();
      skills = res.data ?? [];
    } catch {
      // Skills may not be available
    }
  }

  async function loadBuiltinTools() {
    try {
      const res = await listBuiltinTools(true);
      builtinTools = res.tools ?? [];
    } catch {
      // Built-in tools endpoint may not be available
    }
  }

  async function loadMCPSets() {
    try {
      const res = await listMCPSets({ _limit: 500 });
      availableMCPSets = res.data ?? [];
    } catch {
      // MCP sets may not be available
    }
  }

  /**
   * The account's saved starting point. It seeds a NEW conversation only — an
   * opened conversation carries its own config, and overwriting that with a
   * preset would silently rewrite saved history. Applied after the model list
   * resolves so a stale model is not selected.
   */
  async function loadDefaults(revision: number) {
    try {
      const prefs = await getPlaygroundDefaults();
      if (disposed) return;
      defaultsLoaded = true;
      // A late read must not undo selections made while the page was loading.
      if (revision !== setupRevision) {
        defaultsSave.schedule(accountDefaults);
        return;
      }
      accountDefaults = normalizeWorkbenchSetup(prefs, FRONTEND_TOOL_NAMES);
      if (conversationId || params.id) return;
      applySetup(accountDefaults);
    } catch {
      // A deployment without preference storage simply has no preset.
    } finally {
      if (!disposed && !conversationId && !params.id) void discoverTools();
    }
  }

  /**
   * Remembers the current selection for the next new conversation. Debounced
   * and best-effort: this is a convenience, never a precondition for chatting,
   * so a failure is silent rather than a toast on every toggle.
   */
  function saveDefaults() {
    setupRevision++;
    accountDefaults = currentSetup();
    if (!defaultsLoaded) return;
    defaultsSave.schedule(accountDefaults);
  }

  function workbenchChanged() {
    scheduleSettingsSave();
    saveDefaults();
  }

  // ─── Named presets ───

  // ─── Slash commands ───

  let personalCommands = $state<ChatCommand[]>([]);
  let workspaceCommands = $state<ChatCommand[]>([]);
  /** Personal commands win a name clash, as the more specific choice. */
  let customCommands = $derived.by(() => {
    const seen = new Set(personalCommands.map(c => c.name));
    return [...personalCommands, ...workspaceCommands.filter(c => !seen.has(c.name))];
  });

  async function loadCommands() {
    const [personal, workspace] = await Promise.allSettled([listChatCommands(), listWorkspaceChatCommands()]);
    if (personal.status === 'fulfilled') personalCommands = personal.value;
    if (workspace.status === 'fulfilled') workspaceCommands = workspace.value;
  }

  async function loadPresets() {
    const [personal, workspace] = await Promise.allSettled([listChatPresets(), listWorkspaceChatPresets()]);
    if (personal.status === 'fulfilled') personalPresets = personal.value;
    if (workspace.status === 'fulfilled') workspacePresets = workspace.value;
  }

  /** The current workbench state in preset form. */
  function currentSetup(): WorkbenchSetup {
    return normalizeWorkbenchSetup({
      model: selectedModel,
      reasoning_effort: reasoningEffort,
      system_prompt: systemPrompt,
      mcp_sets: [...selectedMCPSetNames],
      skills: [...selectedSkillNames],
      builtin_tools: [...enabledBuiltinTools],
      frontend_tools: [...enabledFrontendTools],
    });
  }

  function presetMatchesCurrent(preset: ChatPreset): boolean {
    return workbenchSetupsEqual(normalizeWorkbenchSetup(preset, FRONTEND_TOOL_NAMES), currentSetup());
  }

  /**
   * The preset the current setup actually is. A name that kept claiming a
   * preset after the reader changed a tool would describe state that is no
   * longer there, so divergence reports "no preset" rather than a stale name.
   */
  const activePresetId = $derived.by(() => {
    const preset = presets.find(p => p.id === appliedPresetId);

    return preset && presetMatchesCurrent(preset) ? preset.id : '';
  });

  /** The entry the name box addresses — what Overwrite and Delete act on. */
  const draftPreset = $derived.by(() => {
    const name = presetDraftName.trim().toLowerCase();
    const source = presetSaveScope === 'workspace' ? workspacePresets : personalPresets;

    return name ? source.find(p => p.name.toLowerCase() === name) : undefined;
  });

  /**
   * Applies a saved setup to the conversation in front of the reader. The
   * transcript is untouched; only the setup changes, and the conversation's
   * own stored config is updated to match so a reload keeps it.
   */
  function applyPreset(id: string) {
    const preset = presets.find(p => p.id === id);
    if (!preset) {
      // "No preset" is a state, not an action: it reports that the setup
      // matches nothing saved, and must not wipe the reader's selections.
      appliedPresetId = '';

      return;
    }

    // Leaving a setup that matches no saved preset: keep it so it can be restored.
    if (!presets.some(presetMatchesCurrent)) customSetup = currentSetup();

    // A missing model is named rather than silently replacing the current one.
    if (preset.model && !models.includes(preset.model)) {
      addToast(`"${preset.name}" names the model ${preset.model}, which this workspace does not offer — kept ${selectedModel}.`, 'warn');
    } else if (preset.model) {
      selectedModel = preset.model;
    }

    applySetup(normalizeWorkbenchSetup(preset, FRONTEND_TOOL_NAMES));

    appliedPresetId = preset.id;
    if (preset.scope === 'workspace' && !preset.can_edit) {
      // Applying a teammate's preset never points Overwrite/Delete at their
      // record. Start a personal copy with a distinct name; the reader may
      // switch the target to Workspace before saving it as another shared one.
      presetSaveScope = 'personal';
      presetDraftName = `${preset.name} copy`;
    } else {
      presetSaveScope = preset.scope;
      presetDraftName = preset.name;
    }
    void refreshTools();
    scheduleSettingsSave();
  }

  /** Whether the current setup is the captured unsaved one. */
  let onCustomSetup = $derived(!!customSetup && !activePresetId && workbenchSetupsEqual(customSetup, currentSetup()));

  /** Returns to the unsaved setup the reader had before applying a preset. */
  function restoreCustomSetup() {
    if (!customSetup) return;
    applySetup(customSetup);
    appliedPresetId = '';
    void refreshTools();
    scheduleSettingsSave();
  }

  /**
   * Saves the current setup under the typed name. An existing name overwrites
   * that entry rather than adding a second one the reader cannot tell apart —
   * the server refuses duplicates case-insensitively anyway.
   */
  async function savePreset() {
    const name = presetDraftName.trim();
    if (!name || presetSaving) return;

    const existing = draftPreset;
    if (existing && !existing.can_edit) {
      addToast('Only the person who shared this workspace preset can overwrite it. Choose a new name to save a copy.', 'warn');
      return;
    }

    presetSaving = true;
    try {
      if (presetSaveScope === 'workspace') {
        const entry = existing
          ? await updateWorkspaceChatPreset(existing.id, { name, ...currentSetup() })
          : await createWorkspaceChatPreset({ name, ...currentSetup() });
        workspacePresets = existing
          ? workspacePresets.map(p => (p.id === entry.id ? entry : p))
          : [...workspacePresets, entry];
        appliedPresetId = entry.id;
      } else {
        const entry: ChatPreset = {
          id: existing?.id ?? '', name, ...currentSetup(), scope: 'personal', can_edit: true,
        };
        const next = existing
          ? personalPresets.map(p => (p.id === existing.id ? entry : p))
          : [...personalPresets, entry];
        personalPresets = await saveChatPresets(next);
        appliedPresetId = personalPresets.find(p => p.name.toLowerCase() === name.toLowerCase())?.id ?? '';
      }
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to save the preset'), 'alert');
    } finally {
      presetSaving = false;
    }
  }

  /** Deleting a preset removes a saved setup, never the current selections. */
  async function deletePreset(preset: ChatPreset) {
    if (!preset.id || presetSaving || !preset.can_edit) return;

    presetSaving = true;
    try {
      if (preset.scope === 'workspace') {
        await deleteWorkspaceChatPreset(preset.id);
        workspacePresets = workspacePresets.filter(p => p.id !== preset.id);
      } else {
        personalPresets = await saveChatPresets(personalPresets.filter(p => p.id !== preset.id));
      }
      if (appliedPresetId === preset.id) appliedPresetId = '';
      presetDraftName = '';
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to delete the preset'), 'alert');
    } finally {
      presetSaving = false;
    }
  }

  // Defaults are applied after the model list so a saved model can be matched
  // against what this deployment actually offers.
  const initialSetupRevision = setupRevision;
  loadInfo().then(() => loadDefaults(initialSetupRevision));
  loadLocalProviders();
  loadPresets();
  loadCommands();
  const catalogsReady = Promise.all([loadSkills(), loadBuiltinTools(), loadMCPSets(), loadLocalServers()]);
  loadConversations();

  // ─── Scroll ───

  let followLatest = true;
  let lastScrollTop = 0;
  let chatContent: HTMLDivElement | undefined = $state();

  // Only a reader moving *up* stops following. Programmatic scrolls only move
  // down, and content growing between our scroll and its event (streaming
  // deltas, late images) must not be mistaken for the reader leaving the end.
  function handleChatScroll() {
    if (!chatContainer) return;
    const top = chatContainer.scrollTop;
    const distance = chatContainer.scrollHeight - top - chatContainer.clientHeight;
    if (distance <= 24) followLatest = true;
    else if (top < lastScrollTop - 1) followLatest = false;
    lastScrollTop = top;
  }

  function scrollToBottom(force = false) {
    if (force) followLatest = true;
    if (chatContainer && followLatest) {
      requestAnimationFrame(() => {
        // Recheck: the reader may have scrolled up since this frame was queued.
        if (chatContainer && followLatest) chatContainer.scrollTop = chatContainer.scrollHeight;
      });
    }
  }

  // Content also grows without a delta (images loading, Markdown/diagrams
  // rendering, tool cards); keep the end in view while following. The
  // viewport shrinks too when the composer area grows (queue, notices,
  // multi-line input), which changes no content size but hides the end.
  $effect(() => {
    if (!chatContent || !chatContainer) return;
    const observer = new ResizeObserver(() => {
      if (chatContainer && followLatest) chatContainer.scrollTop = chatContainer.scrollHeight;
    });
    observer.observe(chatContent);
    observer.observe(chatContainer);
    return () => observer.disconnect();
  });

  // ─── Image handling ───

  function readFileAsDataURL(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = () => reject(new Error('Failed to read file'));
      reader.readAsDataURL(file);
    });
  }

  /** Per-file cap. The request goes through the gateway body limit too. */
  const ATTACHMENT_MAX_BYTES = 20 * 1024 * 1024;
  const TEXT_ATTACHMENT_MAX_BYTES = 512 * 1024;

  async function addImageFiles(files: FileList | File[]) {
    for (const file of files) {
      const modality = attachmentModality(file.name, file.type);
      const refusal = attachmentRefusal({ name: file.name, modality }, acceptedInputs);
      if (modality === null) {
        addToast(refusal, 'alert');
        continue;
      }
      if (file.size > (modality === 'text' ? TEXT_ATTACHMENT_MAX_BYTES : ATTACHMENT_MAX_BYTES)) {
        addToast(`"${file.name}" is too large to attach (${formatFileSize(file.size)}).`, 'alert');
        continue;
      }
      if (refusal) addToast(refusal, 'warn');
      try {
        const dataUrl = await readFileAsDataURL(file);
        const text = modality === 'text' ? await file.text() : undefined;
        pendingImages = [...pendingImages, { name: file.name, mime: file.type || 'application/octet-stream', modality, dataUrl, text, size: file.size }];
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
      if (item.kind === 'file') {
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

  /** Drops the saved direct-MCP record once the user has acknowledged it. */
  function dismissLegacyMcpUrls() {
    legacyMcpUrls = [];
    legacyMcpHeaders = {};
    scheduleSettingsSave();
  }

  function clearAllToolSelections() {
    selectedMCPSetNames = [];
    selectedSkillNames = [];
    enabledBuiltinTools = [];
    enabledFrontendTools = [];
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

  function toggleMCPSet(setName: string) {
    if (selectedMCPSetNames.includes(setName)) {
      selectedMCPSetNames = selectedMCPSetNames.filter(s => s !== setName);
    } else {
      selectedMCPSetNames = [...selectedMCPSetNames, setName];
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

  /**
   * Lists a local server's tools and records why it failed when it did.
   *
   * A browser reports a refused connection and a refused cross-origin request
   * identically, so the recorded hint names the headers the MCP server has to
   * return rather than repeating "failed to fetch", which the reader cannot
   * act on.
   */
  async function discoverLocalTools(server: LocalMCPServer): Promise<LocalMCPTool[]> {
    localStatus = { ...localStatus, [server.id]: { tools: [], error: '', hint: '', busy: true } };
    try {
      const client = await localClientFor(server);
      const tools = await client.listTools();
      localStatus = {
        ...localStatus,
        [server.id]: { tools: tools.map(t => t.name), error: '', hint: '', busy: false },
      };

      return tools;
    } catch (e: any) {
      forgetLocalClient(server);
      localStatus = {
        ...localStatus,
        [server.id]: { tools: [], error: e?.message || 'could not be reached', hint: e?.hint || '', busy: false },
      };
      throw e;
    }
  }

  /** Discover tools from MCP sets, enabled builtins, and frontend tools. Build the dispatch map. */
  let toolDiscoveryVersion = 0;
  function refreshTools() {
    workbenchChanged();
    return discoverTools();
  }

  async function discoverTools() {
    const version = ++toolDiscoveryVersion;
    loadingTools = true;
    await catalogsReady;
    if (disposed || version !== toolDiscoveryVersion) return;
    const selections = {
      mcp_sets: selectedMCPSetNames,
      skills: selectedSkillNames,
      builtin_tools: enabledBuiltinTools,
    };
    const frontendTools = [...enabledFrontendTools];
    const newTools: ToolDefinition[] = [];
    const newSourceMap: Record<string, ToolSource> = {};
    const newSkillPrompts: string[] = [];

    try {
      // Tools come from installation-registered sources only. Direct MCP URLs
      // are no longer discovered here — register the server as an MCP set so it
      // carries credentials and execution admission with it.

      // 2. Discover MCP Set tools (server-side resolution)
      for (const setName of selections.mcp_sets) {
        mcpSetStatus = { ...mcpSetStatus, [setName]: { tools: [], warnings: [], error: '', busy: true } };
        try {
          const res = await listMCPSetTools(setName);
          if (version !== toolDiscoveryVersion) return;
          mcpSetStatus = {
            ...mcpSetStatus,
            [setName]: {
              tools: (res.tools ?? []).map(tool => tool.name),
              warnings: res.warnings ?? [],
              error: '',
              busy: false,
            },
          };
          for (const t of res.tools ?? []) {
            if (newSourceMap[t.name]) continue;
            newTools.push({
              type: 'function',
              function: {
                name: t.name,
                description: t.description,
                parameters: t.inputSchema || { type: 'object', properties: {} },
              },
            });
            newSourceMap[t.name] = { type: 'mcpset', mcpSetName: setName };
          }
        } catch (e: any) {
          if (version !== toolDiscoveryVersion) return;
          const message = e?.response?.data?.message || e.message || 'failed to discover tools';
          mcpSetStatus = { ...mcpSetStatus, [setName]: { tools: [], warnings: [], error: message, busy: false } };
          addToast(`MCP Set "${setName}": ${message}`, 'alert');
        }
      }

      // 3. Load documentation skill instructions. Skills never register tools,
      // except agent-bound (context: fork) skills: those run their agent on the
      // server through load_skill — the same tool and arguments the Sessions
      // loop uses, so a skill behaves the same wherever it is selected.
      const agentSkills: Skill[] = [];
      for (const skillName of selections.skills) {
        const skill = skills.find(s => s.name === skillName);
        if (!skill) continue;

        if (skill.context === 'fork' && skill.agent) {
          agentSkills.push(skill);
          continue;
        }
        if (skill.system_prompt) {
          newSkillPrompts.push(skill.system_prompt);
        }

      }
      if (agentSkills.length > 0 && !newSourceMap[LOAD_SKILL_TOOL]) {
        const catalog = agentSkills.map(s => `- \`${s.name}\` — ${s.description || '(no description)'}\n  Runs in an isolated context using its own agent; pass a self-contained \`task\` and choose \`run_mode\` (\`foreground\` when the result is needed now, \`background\` for independent concurrent work).`).join('\n');
        newSkillPrompts.push(`## Available Skills\n\n${catalog}\n\nCall \`${LOAD_SKILL_TOOL}\` with the skill name to use one; do not attempt its work yourself. Background runs return a run_id; call \`${RUN_STATUS_TOOL}\` with it to wait for the result.`);
        newTools.push({
          type: 'function',
          function: {
            name: LOAD_SKILL_TOOL,
            description: 'Activate an attached skill. A catalog entry marked isolated runs through its configured subagent and requires a self-contained task.',
            parameters: {
              type: 'object',
              properties: {
                skill_name: { type: 'string', enum: agentSkills.map(s => s.name), description: 'The name of the skill to load. Must match one of the catalog entries listed in your system prompt.' },
                task: { type: 'string', description: 'Task for an isolated skill. Required when the selected skill has context: fork.' },
                context: { type: 'string', description: 'Optional background and constraints for the isolated skill run.' },
                run_mode: { type: 'string', enum: ['foreground', 'background'], description: 'Execution mode for an isolated skill. Use background for independent concurrent work. Defaults to foreground.' },
              },
              required: ['skill_name'],
            },
          },
        });
        newSourceMap[LOAD_SKILL_TOOL] = { type: 'skill' };
        newTools.push({
          type: 'function',
          function: {
            name: RUN_STATUS_TOOL,
            description: 'Wait for a background skill run to finish and return its final result.',
            parameters: {
              type: 'object',
              properties: { run_id: { type: 'string' } },
              required: ['run_id'],
            },
          },
        });
        newSourceMap[RUN_STATUS_TOOL] = { type: 'skill' };
      }

      // 4. Add enabled built-in server tools
      for (const toolName of selections.builtin_tools) {
        if (isChatTodoTool(toolName)) continue;
        const def = builtinTools.find(t => t.name === toolName);
        if (!def || builtinDisabledBy(def, isFeatureEnabled) || newSourceMap[def.name]) continue;
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

      // 5. Add enabled frontend tools
      for (const toolName of frontendTools) {
        const def = FRONTEND_TOOLS.find(t => t.function.name === toolName);
        if (!def || newSourceMap[def.function.name]) continue;
        newTools.push(def);
        newSourceMap[def.function.name] = { type: 'frontend' };
      }

      // 6. Local MCP servers, dialled from this browser.
      //
      // Registered last on purpose: a program on somebody's laptop must not be
      // able to take over the name of a built-in or MCP-set tool the
      // conversation already relies on, so it yields the name on collision.
      if (localMCPAvailable) {
        for (const server of localServers) {
          if (!localApprovedIds.includes(server.id)) continue;
          try {
            const tools = await discoverLocalTools(server);
            if (version !== toolDiscoveryVersion) return;
            for (const tool of tools) {
              const exposed = localMCPToolName(tool.name, server.name, name => !!newSourceMap[name]);
              newTools.push({
                type: 'function',
                function: {
                  name: exposed,
                  description: `${tool.description ?? ''}${tool.description ? ' ' : ''}(runs on ${server.name}, your machine)`.trim(),
                  parameters: tool.inputSchema || { type: 'object', properties: {} },
                },
              });
              newSourceMap[exposed] = { type: 'local', localServerId: server.id, localToolName: tool.name };
            }
          } catch {
            // discoverLocalTools records the reason on localStatus; a local
            // server being unreachable must not empty the tool list.
          }
        }
      }

      // 7. Browser extensions, which answer this page directly.
      //
      // Last for the same reason local MCP is late: an extension must not be
      // able to take over the name of a tool the conversation already relies
      // on, so it yields the name on collision.
      if (extensionsAvailable && webConnectionEnabled) {
        for (const ext of extensions) {
          if (!extensionApprovedIds.includes(ext.id)) continue;
          if (!ext.capabilities.includes(CAPABILITY_TOOLS)) continue;
          try {
            const tools = await discoverExtensionTools(ext);
            if (version !== toolDiscoveryVersion) return;
            for (const tool of tools) {
              const exposed = localMCPToolName(tool.name, ext.name, name => !!newSourceMap[name]);
              newTools.push({
                type: 'function',
                function: {
                  name: exposed,
                  description: `${tool.description}${tool.description ? ' ' : ''}(runs in ${ext.name}, your browser)`.trim(),
                  parameters: tool.inputSchema || { type: 'object', properties: {} },
                },
              });
              newSourceMap[exposed] = { type: 'extension', extensionId: ext.id, extensionToolName: tool.name };
            }
          } catch {
            // discoverExtensionTools records the reason on extensionStatus; an
            // extension that stopped answering must not empty the tool list.
          }
        }
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to discover tools', 'alert');
    } finally {
      if (version !== toolDiscoveryVersion) return;
      discoveredTools = newTools;
      toolSourceMap = newSourceMap;
      skillSystemPrompts = newSkillPrompts;
      loadingTools = false;
    }
  }

  /** Execute a tool call by dispatching to the correct backend or frontend handler. */
  async function executeToolCall(tc: ToolCall, turn: TurnContext): Promise<string> {
    turnLifecycle.assert(turn);
    return dispatchChatTool(tc, turn.sources, {
      skill: (_, args) => executeSkillTool(tc, args, turn),
      mcpset: async (source, args) => {
        if (!source.mcpSetName) return 'Error: MCP set is missing';
        const res = await callMCPSetTool(source.mcpSetName, tc.function.name, args, turn.controller.signal);
        turnLifecycle.assert(turn);
        const text = res.content?.map(c => c.text).join('\n') ?? '';
        // generate_image (and other media tools) reached through an MCP set
        // report stored media the same way the built-in path does.
        noteTurnArtifacts(text);
        return text || 'Tool executed successfully (no output)';
      },
      builtin: async (_, args) => {
        const res = await callBuiltinTool(tc.function.name, args, '', turn.traceId, turn.sessionId, turn.controller.signal);
        turnLifecycle.assert(turn);
        if (res.error) return `Error: ${res.error}`;
        noteTurnArtifacts(res.result);
        return res.result;
      },
      local: (source, args) => executeLocalTool(source, tc.function.name, args, turn),
      extension: (source, args) => executeExtensionTool(source, tc.function.name, args, turn),
      frontend: (_, args) => executeFrontendTool(tc.function.name, args),
    });
  }

  /**
   * Agent-bound skills. Foreground runs block this tool call until the agent
   * answers; background runs are tracked per turn so their results are always
   * collected before the turn ends, even when the model forgets to wait.
   */
  async function executeSkillTool(tc: ToolCall, args: Record<string, any>, turn: TurnContext): Promise<string> {
    const signal = turn.controller.signal;
    if (tc.function.name === RUN_STATUS_TOOL) {
      const id = String(args.run_id ?? '').trim();
      if (!id) return 'Error: run_id is required';
      return await collectSkillRuns([id], tc.id, turn);
    }
    const skill = String(args.skill_name ?? '');
    const background = args.run_mode === 'background';
    if (!String(args.task ?? '').trim()) return `Error: load_skill: task is required for skill "${skill}"`;
    skillRunProgress = { ...skillRunProgress, [tc.id]: background ? `starting ${skill} in the background…` : `${skill} is working…` };
    const res = await runSkill({ skill, task: String(args.task ?? ''), context: args.context ? String(args.context) : undefined, background, trace_id: turn.traceId }, signal);
    turnLifecycle.assert(turn);
    noteTurnArtifacts(res.result);
    if (res.error) return `Error: ${res.error}`;
    if (background) {
      try {
        const started = JSON.parse(res.result) as SkillRunStatus;
        if (started.run_id) turnSkillRuns = [...turnSkillRuns, { id: started.run_id, skill, reported: false }];
      } catch { /* Result is still returned to the model below. */ }
    }
    return res.result;
  }

  async function collectSkillRuns(ids: string[], toolCallId: string, turn: TurnContext): Promise<string> {
    const signal = turn.controller.signal;
    const results: SkillRunStatus[] = [];
    for (const id of ids) {
      const name = turnSkillRuns.find(r => r.id === id)?.skill || id;
      let status: SkillRunStatus;
      while (true) {
        signal?.throwIfAborted();
        const done = results.length;
        skillRunProgress = { ...skillRunProgress, [toolCallId]: `waiting for ${name} (${done}/${ids.length} finished)…` };
        status = await waitSkillRun(id, 20, signal);
        turnLifecycle.assert(turn);
        if (['completed', 'failed', 'cancelled'].includes(status.status)) break;
      }
      results.push(status);
      turnSkillRuns = turnSkillRuns.map(r => r.id === id ? { ...r, reported: true } : r);
      if (status.artifacts?.length) turnArtifacts = [...turnArtifacts, ...status.artifacts];
    }
    return results.map(r => {
      const files = r.artifacts?.length
        ? `\nDelivered files (to show one, copy its markdown into your answer; never write a server path):\n${r.artifacts.map(a => `- ${a.name}${a.markdown ? `: ${a.markdown}` : ''}`).join('\n')}`
        : '';
      const note = r.artifacts_note ? `\n${r.artifacts_note}` : '';
      return `## ${r.agent_name} (${r.run_id}) — ${r.status}\n${r.error ? `Error: ${r.error}` : r.result || '(no result)'}${files}${note}`;
    }).join('\n\n');
  }

  /** Records files a foreground skill run delivered, from its tool result. */
  function noteTurnArtifacts(result: string) {
    try {
      const parsed = JSON.parse(result);
      if (Array.isArray(parsed?.artifacts)) {
        const valid = parsed.artifacts.filter((a: any) => typeof a?.media_id === 'string' && a.media_id && typeof a?.content_type === 'string');
        if (valid.length) turnArtifacts = [...turnArtifacts, ...valid];
      }
    } catch { /* Plain-text results carry no artifacts. */ }
  }

  /**
   * Run a tool on an MCP server on this machine.
   *
   * The result is bounded here because Chats is not governed by loopgov — that
   * governs the three server-side loops — so nothing else would stop a local
   * tool from filling the context window.
   *
   * The call is reported to Traces as a client-asserted observation. Without
   * it a conversation's trace shows its generations with the tool steps
   * between them missing, which reads as an unexplained gap rather than an
   * absence.
   */
  async function executeLocalTool(source: ToolSource, exposedName: string, args: Record<string, any>, turn: TurnContext): Promise<string> {
    const server = localServers.find(s => s.id === source.localServerId);
    if (!server) return `Error: local MCP server is no longer configured`;
    // Re-checked every call: the switch may have been turned off mid-turn.
    if (!localApprovedIds.includes(server.id)) {
      return `Error: ${server.name} is not enabled on this device`;
    }

    const remoteName = source.localToolName || exposedName;
    const started = Date.now();
    let status: 'ok' | 'error' = 'ok';
    let output = '';
    let failure = '';
    try {
      const client = await localClientFor(server, turn.controller.signal);
      output = clipLocalToolResult(await client.callTool(remoteName, args));
    } catch (e: any) {
      status = 'error';
      failure = [e?.message, e?.hint].filter(Boolean).join(' — ') || 'local tool call failed';
      forgetLocalClient(server);
    }

    void reportLocalToolObservation({
      trace_id: turn.traceId,
      session_id: turn.sessionId,
      name: remoteName,
      server: server.name,
      status,
      latency_ms: Date.now() - started,
      error: failure,
      input: JSON.stringify(args ?? {}),
      output,
    });

    // A local failure is returned to the model as a tool error rather than
    // ending the turn: it can try a different tool or explain itself.
    if (status === 'error') return `Error: ${failure}`;

    return output || 'Tool executed successfully (no output)';
  }

  /**
   * Run a tool inside a browser extension.
   *
   * Bounded and traced exactly like a local MCP call, and for the same
   * reasons: Chats is not governed by loopgov, and a trace that shows the
   * generations without the tool steps between them reads as an unexplained
   * gap rather than an absence. The extension is named in the observation
   * because the work did not happen in this process.
   */
  async function executeExtensionTool(source: ToolSource, exposedName: string, args: Record<string, any>, turn: TurnContext): Promise<string> {
    const ext = extensions.find(e => e.id === source.extensionId);
    if (!ext) return `Error: that browser extension is no longer connected`;
    // Re-checked every call: the switch may have been turned off mid-turn.
    if (!webConnectionEnabled || !extensionApprovedIds.includes(ext.id)) {
      return `Error: ${ext.name} is not enabled on this device`;
    }
    const bridge = ensureExtensionBridge();
    if (!bridge) return `Error: the extension bridge is unavailable on this page`;

    const remoteName = source.extensionToolName || exposedName;
    const started = Date.now();
    let status: 'ok' | 'error' = 'ok';
    let output = '';
    let failure = '';
    try {
      output = clipLocalToolResult(await bridge.callTool(ext.id, remoteName, args, turn.controller.signal));
    } catch (e: any) {
      status = 'error';
      failure = e?.message || 'the extension did not complete the call';
    }

    void reportLocalToolObservation({
      trace_id: turn.traceId,
      session_id: turn.sessionId,
      name: remoteName,
      server: ext.name,
      status,
      latency_ms: Date.now() - started,
      error: failure,
      input: JSON.stringify(args ?? {}),
      output,
    });

    // Returned to the model as a tool error rather than ending the turn: it
    // can try another tool or explain itself.
    if (status === 'error') return `Error: ${failure}`;

    return output || 'Tool executed successfully (no output)';
  }

  // ─── Local MCP registry editing ───

  async function loadLocalServers() {
    if (!localMCPAvailable) {
      localServersLoaded = true;

      return;
    }
    try {
      localServers = await listLocalMCPServers();
      refreshLocalApprovals();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load local MCP servers', 'alert');
    } finally {
      localServersLoaded = true;
    }
  }

  /** Opens the editor on an existing record, or empty for a new one. */
  function editLocalServer(server?: LocalMCPServer) {
    localEditing = server;
    localEditorKey++;
    localEditorOpen = true;
  }

  function closeLocalEditor() {
    localEditing = undefined;
    localEditorOpen = false;
  }

  function localServerSaved(stored: LocalMCPServer[], edited?: LocalMCPServer) {
    localServers = stored;
    // The address may have changed; the old session must not be reused.
    if (edited) forgetLocalClient(edited);
    refreshLocalApprovals();
    closeLocalEditor();
    void discoverTools();
  }

  async function removeLocalServer(server: LocalMCPServer) {
    try {
      localServers = await saveLocalMCPServers(localServers.filter(s => s.id !== server.id));
      revokeLocalMCP(server.id, localStorageSafe());
      forgetLocalClient(server);
      refreshLocalApprovals();
      if (localEditing?.id === server.id) closeLocalEditor();
      void discoverTools();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove', 'alert');
    }
  }

  /**
   * Opening the approval dialog connects first, so the person decides against
   * the tools the server actually offers rather than a name and a URL.
   */
  async function beginLocalApproval(server: LocalMCPServer) {
    localApprovalFor = server;
    localApprovalTools = [];
    try {
      localApprovalTools = await discoverLocalTools(server);
    } catch {
      /* the failure is rendered from localStatus */
    }
  }

  function confirmLocalApproval() {
    const server = localApprovalFor;
    if (!server) return;
    approveLocalMCP(server.id, localApprovalTools.map(t => t.name), localStorageSafe());
    refreshLocalApprovals();
    localApprovalFor = null;
    void discoverTools();
  }

  /** One action, effective immediately: the next turn offers nothing from it. */
  function disableLocalServer(server: LocalMCPServer) {
    revokeLocalMCP(server.id, localStorageSafe());
    forgetLocalClient(server);
    refreshLocalApprovals();
    void discoverTools();
  }

  /** Execute a frontend-only tool (runs entirely in the browser). */
  async function executeFrontendTool(name: string, args: Record<string, any>): Promise<string> {
    switch (name) {
      case 'todo_write': {
        const items = args.todos;
        if (!Array.isArray(items)) return 'Error: todos must be an array';
        todos = normalizeTodos(items);
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
        questionSelections = [];
        const signal = abortController?.signal;
        const answer = await new Promise<string>((resolve, reject) => {
          const cancel = () => {
            pendingQuestion = null;
            reject(new DOMException('Question cancelled', 'AbortError'));
          };
          if (signal?.aborted) { cancel(); return; }
          signal?.addEventListener('abort', cancel, { once: true });
          pendingQuestion = {
            question,
            header,
            options,
            multiple,
            custom,
            resolve: answer => {
              signal?.removeEventListener('abort', cancel);
              resolve(answer);
            },
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

  function setupReady(): boolean {
    if (loadingTools) {
      addToast('Wait for tool discovery to finish before sending.', 'warn');
      return false;
    }
    return true;
  }

  async function sendMessage() {
    if (chatRecording || chatTranscribing) return;
    const text = userInput.trim();
    // `/name args` runs a command instead of sending the text. An unknown name
    // is sent as ordinary text, so a message that merely starts with a slash
    // is never swallowed.
    const slash = pendingImages.length === 0 && !streaming ? parseSlashInput(text) : null;
    if (slash && runSlashCommand(slash.name, slash.args)) return;
    if ((!text && pendingImages.length === 0) || !selectedModel) return;
    if (pendingRefusals.length > 0) {
      addToast(pendingRefusals[0], 'alert');
      return;
    }
    const item = queuedMessage(text, pendingImages);
    // A running turn takes the message at its next step boundary, so the
    // composer stays usable while the agent works.
    if (streaming) {
      queuedMessages = [...queuedMessages, item];
      userInput = '';
      pendingImages = [];
      return;
    }
    if (!setupReady()) return;
    userInput = '';
    pendingImages = [];
    // Messages left waiting by a stopped turn go first, in the order written.
    const items = [...queuedMessages, item];
    queuedMessages = [];
    await runUserTurn(items);
  }

  // ─── Commands ───

  /** The browser sees `at_cost_cents` only from the Chats endpoint, and only for priced models. */
  function callUsage(u: ChatUsage): CallUsage {
    const cost = (u as ChatUsage & { at_cost_cents?: number }).at_cost_cents;
    return { prompt: u.prompt_tokens, completion: u.completion_tokens, ...(typeof cost === 'number' ? { cost_cents: cost } : {}) };
  }

  /** Spend of the calls in this page: loaded history plus this session. */
  let conversationCost = $derived.by(() => {
    let cents = 0, priced = 0, unpriced = 0;
    for (const m of meta) {
      if (!m?.usage) continue;
      if (typeof m.usage.cost_cents === 'number') { cents += m.usage.cost_cents; priced++; } else unpriced++;
    }
    return { cents, priced, unpriced };
  });

  function formatCost(cents: number): string {
    const dollars = cents / 100;
    if (dollars === 0) return '$0.00';
    if (dollars < 0.01) return `$${dollars.toFixed(4)}`;
    return `$${dollars.toFixed(dollars < 10 ? 3 : 2)}`;
  }

  let compacting = $state(false);
  let canCompact = $derived(!streaming && !saving && !compacting && !!selectedModel && messages.length - Math.max(0, compactionStart(meta.map(m => m?.compaction))) > 2);

  interface BuiltinCommand { name: string; description: string; args?: string; disabled?: boolean; run: (args: string) => void }

  let builtinCommands = $derived<BuiltinCommand[]>([
    { name: 'compact', args: '[focus]', description: 'Summarize the conversation so far and continue from the summary', disabled: !canCompact, run: args => void compactConversation(args) },
    { name: 'new', description: 'Start a new chat', run: () => newConversation() },
    { name: 'clear', description: 'Clear the transcript (asks to confirm)', disabled: streaming || saving || messages.length === 0, run: () => { showSessionPanel = true; requestClear(); } },
    { name: 'model', description: 'Switch model', disabled: models.length === 0, run: () => (palette = 'models') },
    { name: 'preset', description: 'Apply a preset', disabled: presets.length === 0, run: () => (palette = 'presets') },
    { name: 'effort', description: 'Set reasoning effort', disabled: reasoningEffortOptions.length === 0, run: () => (palette = 'effort') },
    { name: 'tools', description: 'Open the workbench', run: () => openWorkbench('tools') },
    { name: 'share', description: 'Share a snapshot of this chat', disabled: !(sharingAvailable && conversationId && shareBoundaries.length > 0), run: () => (showShareDialog = true) },
    { name: 'commands', description: 'Create and edit your own commands', run: () => openWorkbench('commands') },
  ]);

  interface CommandSuggestion { name: string; args?: string; description: string; scope: 'built-in' | 'personal' | 'workspace'; disabled?: boolean }

  let commandIndex = $state(0);
  let commandMenuDismissed = $state('');
  let commandQuery = $derived(pendingImages.length === 0 ? slashQuery(userInput) : null);
  let commandSuggestions = $derived.by((): CommandSuggestion[] => {
    if (commandQuery === null || commandMenuDismissed === userInput) return [];
    const all: CommandSuggestion[] = [
      ...builtinCommands.map(c => ({ name: c.name, args: c.args, description: c.description, scope: 'built-in' as const, disabled: c.disabled })),
      ...customCommands.map(c => ({ name: c.name, args: /\$(ARGUMENTS|[1-9])/.test(c.template) ? '[args]' : undefined, description: c.description || c.template.split('\n')[0], scope: c.scope })),
    ];
    const q = commandQuery;
    return all
      .filter(c => !q || c.name.includes(q))
      .sort((a, b) => Number(!b.name.startsWith(q)) - Number(!a.name.startsWith(q)));
  });
  $effect(() => { void commandQuery; commandIndex = 0; });

  /** Fill the composer with the chosen command, ready for arguments. */
  function pickCommand(c: CommandSuggestion | undefined, run: boolean) {
    if (!c || c.disabled) return;
    // `[x]` arguments are optional, so Enter runs the command at once; Tab (or
    // a required `<x>`) fills the composer so arguments can follow.
    const needsArgs = !!c.args?.startsWith('<');
    if (run && !needsArgs) {
      userInput = `/${c.name}`;
      void sendMessage();
      return;
    }
    userInput = `/${c.name} `;
    tick().then(() => composerInput?.focus());
  }

  /** Runs a built-in or custom command. Returns false when the name is unknown. */
  function runSlashCommand(name: string, args: string): boolean {
    const builtin = builtinCommands.find(c => c.name === name);
    if (builtin) {
      if (builtin.disabled) { addToast(`/${name} is not available right now.`, 'warn'); return true; }
      userInput = '';
      builtin.run(args);
      return true;
    }
    const custom = customCommands.find(c => c.name === name);
    if (!custom) return false;
    if (!setupReady()) return true;
    const text = expandCommandTemplate(custom.template, args).trim();
    if (!text) { addToast(`/${name} expanded to an empty message.`, 'warn'); return true; }
    userInput = '';
    const previousModel = selectedModel;
    // A command bound to a model runs that one message on it, then hands the
    // conversation back to the model it was on.
    if (custom.model && custom.model !== selectedModel) {
      if (!models.includes(custom.model)) { addToast(`/${name} uses ${custom.model}, which is not available.`, 'alert'); userInput = `/${name}${args ? ' ' + args : ''}`; return true; }
      selectedModel = custom.model;
    }
    const items = [...queuedMessages, queuedMessage(text, [])];
    queuedMessages = [];
    void runUserTurn(items).finally(() => { if (custom.model) selectedModel = previousModel; });
    return true;
  }

  // ─── Compaction ───


  /**
   * /compact: ask the current model for a summary of everything the model
   * would currently see, store it as an assistant message marked as a
   * compaction, and start later model calls from it. The transcript above
   * stays visible and stored; only the context sent upstream shrinks.
   */
  async function compactConversation(instructions: string) {
    if (!canCompact) return;
    if (!setupReady()) return;
    const turn = beginTurn();
    if (!turn) return;
    compacting = true;
    const pair = splitModel(turn.model);
    const localTarget = isLocalModelRef(turn.model) ? localProviderFor(turn.model) : null;
    try {
      repairInterruptedTail(turn.model);
      if (localTarget && historyTruncated && !meta.some(m => m.compaction)) {
        throw new Error('Load older messages first: a local provider needs the whole conversation in this page.');
      }
      await persistPending();
      turnLifecycle.assert(turn);
      const { reqMessages, needsPrefix } = await buildRequestMessages(turn, messages.slice(), !!localTarget);
      reqMessages.push({ role: 'user', content: compactionRequest(instructions) });
      const before = contextTokens;
      let summary = '';
      let usage: ChatUsage | null = null;
      const callbacks: StreamCallbacks = {
        requireComplete: true,
        onDelta: delta => { summary = mergeDeltaContent(summary, delta) as string; },
        onToolCalls: () => {},
        onError: error => addToast(error, 'alert'),
        onUsage: u => { usage = u; },
      };
      const request = { messages: reqMessages, tool_choice: 'none' as const, reasoning_effort: turn.reasoning || undefined, stream: true, stream_options: { include_usage: true } };
      if (localTarget) {
        await runLocalProviderCompletion({ ...turn, tools: [] }, localTarget, reqMessages, callbacks);
      } else {
        await streamChatCompletion('api/v1/chats/completions', {
          ...request,
          model: turn.model,
          at_conversation_id: conversationId || undefined,
          at_history_before: historyTruncated && needsPrefix ? (meta[0]?.id || historyCursor) : undefined,
          metadata: { session_id: turn.sessionId },
        }, callbacks, turn.controller.signal, { 'x-at-trace-id': turn.traceId, 'x-at-trace-name': 'chats compaction' });
      }
      turnLifecycle.assert(turn);
      const text = getTextContent(summary).trim();
      if (!text) throw new Error('The model returned an empty summary; nothing was compacted.');
      messages = [...messages, { role: 'assistant', content: compactionMessageText(text, instructions) }];
      meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, created_at: new Date().toISOString(), imageNames: [], compaction: true, ...(usage ? { usage: callUsage(usage) } : {}) }];
      const after = (usage as ChatUsage | null)?.completion_tokens ?? 0;
      contextTokens = after;
      completionTokens = 0;
      totalTokens = after;
      scrollToBottom();
      await persistPending();
      addToast(before > 0 ? `Compacted ${before.toLocaleString()} tokens of context into a ${after.toLocaleString()}-token summary.` : 'Conversation compacted.', 'info');
    } catch (e) {
      if (turnLifecycle.current(turn) && !turn.controller.signal.aborted && (e as Error).name !== 'AbortError') {
        turn.failed = true;
        addToast((e as Error).message || 'Compaction failed', 'alert');
      }
    } finally {
      compacting = false;
      finishTurn(turn);
      continueWithQueue(turn);
    }
  }

  /** Append queued entries as user messages, in the order they were written. */
  function appendUserMessages(items: QueuedChatMessage<PendingAttachment>[], model: string) {
    const pair = splitModel(model);
    for (const item of items) {
      const content = userMessageContent(item.text, item.attachments, a => attachmentPart(a) as ContentPart) as string | ContentPart[];
      messages = [...messages, { role: 'user', content }];
      meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, created_at: new Date().toISOString(), imageNames: item.attachments.filter(i => i.modality !== 'text').map(i => i.name) }];
    }
  }

  /**
   * Deliver everything queued into the running turn. Called between steps —
   * after tool results or once the model has answered — so the next model
   * call sees the new messages. Returns whether anything was delivered.
   */
  async function drainQueue(turn: TurnContext): Promise<boolean> {
    if (queuedMessages.length === 0) return false;
    const items = queuedMessages;
    queuedMessages = [];
    appendUserMessages(items, turn.model);
    scrollToBottom();
    await persistPending();
    turnLifecycle.assert(turn);
    return true;
  }

  /** After a turn: a message queued too late to join it starts the next one. */
  function continueWithQueue(turn: TurnContext) {
    if ((turn.controller.signal.aborted && !turn.interrupted) || turn.failed || disposed) return;
    if (turnLifecycle.generation() !== turn.generation) return;
    if (queuedMessages.length === 0 || streaming) return;
    const items = queuedMessages;
    queuedMessages = [];
    void runUserTurn(items);
  }

  /**
   * Stop the running step and continue at once with everything queued (and
   * whatever is in the composer). Unlike waiting for the next step boundary,
   * this cuts a long generation or tool call short.
   */
  function interruptAndSend() {
    if (chatRecording || chatTranscribing) return;
    if (userInput.trim() || pendingImages.length > 0) {
      if (pendingRefusals.length > 0) { addToast(pendingRefusals[0], 'alert'); return; }
      queuedMessages = [...queuedMessages, queuedMessage(userInput.trim(), pendingImages)];
      userInput = '';
      pendingImages = [];
    }
    if (queuedMessages.length === 0) return;
    if (!streaming || !activeTurn) { sendQueuedNow(); return; }
    activeTurn.interrupted = true;
    activeTurn.controller.abort();
  }

  /**
   * Close what an interrupted turn left open so the next model call is valid:
   * unanswered tool calls get an explicit "interrupted" result (providers
   * reject unpaired calls) and an empty unsaved placeholder is dropped.
   */
  function repairInterruptedTail(model: string) {
    const last = messages.length - 1;
    if (isEmptyAssistant(messages[last]) && meta[last]?.sequence === null) {
      messages = messages.slice(0, -1);
      meta = meta.slice(0, -1);
    }
    const open = unansweredToolCalls(messages);
    if (open.length === 0) return;
    const pair = splitModel(model);
    for (const id of open) {
      messages = [...messages, { role: 'tool', content: INTERRUPTED_TOOL_RESULT, tool_call_id: id }];
      meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, created_at: new Date().toISOString(), imageNames: [] }];
    }
  }

  function removeQueued(id: string) {
    queuedMessages = takeQueued(queuedMessages, id)[0];
  }

  /** Move a queued entry back into the composer for editing. */
  function editQueued(id: string) {
    const [rest, item] = takeQueued(queuedMessages, id);
    if (!item) return;
    queuedMessages = rest;
    userInput = userInput.trim() ? `${userInput}\n${item.text}` : item.text;
    pendingImages = [...pendingImages, ...item.attachments];
  }

  /** Send what is queued now — after Stop or a failed turn left it waiting. */
  function sendQueuedNow() {
    if (streaming || queuedMessages.length === 0 || !selectedModel) return;
    if (!setupReady()) return;
    const items = queuedMessages;
    queuedMessages = [];
    void runUserTurn(items);
  }

  async function runUserTurn(items: QueuedChatMessage<PendingAttachment>[]) {
    const turn = beginTurn();
    if (!turn) {
      queuedMessages = [...items, ...queuedMessages];
      return;
    }
    const first = items[0];
    try {
      repairInterruptedTail(turn.model);
      appendUserMessages(items, turn.model);
      confirmClear = false;
      // Sending is a request to see the answer: follow it even if the reader
      // had scrolled up.
      scrollToBottom(true);

      // Lazily promote the scratch buffer. A failure here is survivable: the
      // turn still runs, it just stays unsaved.
      if (!conversationId) {
        try {
          await ensureConversation(first?.text || first?.attachments[0]?.name || 'Chat conversation');
        } catch (e) {
          turnLifecycle.assert(turn);
          addToast(playgroundErrorMessage(e, 'Could not start a saved conversation — this turn runs unsaved'), 'alert');
        }
      }
      turnLifecycle.assert(turn);
      turn.sessionId = conversationId || scratchSessionId;

      // Persist the question BEFORE answering it, preserving its send time and
      // keeping it in history even if the turn never finishes. Failed writes
      // remain unsaved in memory and ride the next append.
      await persistPending();
      turnLifecycle.assert(turn);
      await runCompletion(turn);
      if (!turnLifecycle.current(turn)) return;
      await persistPending();
    } catch (e) {
      if (turnLifecycle.current(turn) && !turn.controller.signal.aborted && (e as Error).name !== 'AbortError') {
        turn.failed = true;
        addToast((e as Error).message || 'Chat request failed', 'alert');
      }
    } finally {
      finishTurn(turn);
      continueWithQueue(turn);
    }
  }

  /**
   * Restored history carries image descriptors that no provider accepts. A
   * stored one is re-inlined from media storage — lazily, only for the messages
   * actually going upstream, and cached per id for the page session. Anything
   * that cannot be recovered becomes text, so a missing image never fails the
   * turn.
   */
  async function outgoingContent(content: string | ContentPart[], role = 'user'): Promise<string | ContentPart[]> {
    if (typeof content === 'string') return content;
    if (role === 'assistant') return assistantOutgoingContent(content);
    const parts: ContentPart[] = [];
    for (const part of content) {
      if (part.type === 'file' && part.attachment) {
        // A user attachment (PDF, audio, video): re-inline from media storage.
        const label = part.name || 'attachment';
        const url = part.media_id ? await mediaDataUrl(part.media_id) : '';
        const modality = attachmentModality(label, part.mime_type || '');
        if (url && modality && modality !== 'text') {
          parts.push(attachmentPart({ name: label, mime: part.mime_type || '', modality, dataUrl: url, size: part.bytes || 0 }) as ContentPart);
        } else {
          parts.push({ type: 'text', text: `[attachment "${label}" ${part.media_id ? 'could not be loaded from history' : 'was not saved to history'}]` });
        }
        continue;
      }
      if (part.type === 'file') {
        // Delivered artifacts are for the reader; the model already saw them
        // named in the tool result, so only a short reference goes upstream.
        parts.push({ type: 'text', text: `[file "${part.name || 'file'}" (${part.mime_type || 'unknown type'}) delivered to the user]` });
        continue;
      }
      if (part.type !== 'image') {
        parts.push(part);
        continue;
      }
      const label = part.name || 'attachment';
      const url = part.media_id ? await mediaDataUrl(part.media_id) : '';
      if (url) parts.push({ type: 'image_url', image_url: { url } });
      else if (part.media_id) parts.push({ type: 'text', text: `[image "${label}" could not be loaded from history]` });
      else parts.push({ type: 'text', text: `[image "${label}" was not saved to history]` });
    }
    return parts;
  }

  function beginTurn(): TurnContext | null {
    const base = turnLifecycle.begin();
    if (!base) return null;
    if (!conversationId && !scratchSessionId) {
      scratchSessionId = `chats-${Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')}`;
    }
    const turn = Object.assign(base, {
      model: selectedModel,
      reasoning: effectiveReasoningEffort,
      systemPrompt: [systemPrompt.trim(), ...skillSystemPrompts].filter(Boolean).join('\n\n'),
      tools: JSON.parse(JSON.stringify(discoveredTools)) as ToolDefinition[],
      sources: { ...toolSourceMap },
      sessionId: conversationId || scratchSessionId,
    });
    turnTraceId = turn.traceId;
    turnSkillRuns = []; skillRunProgress = {}; turnArtifacts = [];
    streaming = true;
    abortController = turn.controller;
    activeTurn = turn;
    return turn;
  }

  function finishTurn(turn: TurnContext) {
    if (!turnLifecycle.current(turn)) return;
    streaming = false;
    abortController = null;
    activeTool = null;
    activeTurn = null;
    turnLifecycle.finish(turn);
  }

  async function runCompletion(turn: TurnContext) {
    try {
      const complete = await runChatIterations(MAX_TOOL_ITERATIONS, () => runCompletionStep(turn), () => turnLifecycle.assert(turn));
      if (!complete) {
        const pair = splitModel(turn.model);
        messages = [...messages, { role: 'assistant', content: `Stopped after ${MAX_TOOL_ITERATIONS} tool call iterations to prevent infinite loops.` }];
        meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, created_at: new Date().toISOString(), imageNames: [] }];
      }
    } catch (e) {
      if (turnLifecycle.current(turn) && !turn.controller.signal.aborted && (e as Error).name !== 'AbortError') addToast((e as Error).message || 'Chat request failed', 'alert');
    }
  }

  /**
   * One model call to a local provider, made by this browser. The stream is
   * parsed by the same reader as server completions; the call is reported to
   * Traces as a client-asserted, unpriced generation.
   */
  async function runLocalProviderCompletion(
    turn: TurnContext,
    target: { provider: LocalChatProvider; model: string },
    reqMessages: Array<Record<string, any>>,
    callbacks: StreamCallbacks,
  ): Promise<void> {
    const { provider, model } = target;
    const started = performance.now();
    let usage: ChatUsage | null = null;
    let finish = '';
    let output = '';
    let failure = '';
    try {
      const secrets = await localProviderSecretsFor(provider);
      turnLifecycle.assert(turn);
      const body: Record<string, unknown> = {
        model,
        messages: reqMessages,
        stream: true,
        stream_options: { include_usage: true },
      };
      if (turn.tools.length > 0) body.tools = turn.tools;
      if (turn.reasoning) body.reasoning_effort = turn.reasoning;
      try {
        await streamChatCompletion(
          localProviderEndpoint(provider.base_url, 'chat/completions'),
          body as any,
          {
            ...callbacks,
            onDelta: (delta) => {
              if (typeof delta === 'string') output += delta;
              callbacks.onDelta(delta);
            },
            onUsage: (u) => { usage = u; callbacks.onUsage?.(u); },
            onFinish: (reason) => { finish = reason; },
          },
          turn.controller.signal,
          localProviderHeaders(secrets),
        );
      } catch (e) {
        if ((e as Error)?.name === 'AbortError') throw e;
        throw describeLocalProviderError(e, provider.name, provider.base_url);
      }
    } catch (e) {
      if ((e as Error)?.name !== 'AbortError') failure = (e as Error)?.message || 'Local provider request failed';
      throw e;
    } finally {
      const u = usage as ChatUsage | null;
      void reportLocalGeneration({
        trace_id: turn.traceId,
        session_id: turn.sessionId,
        provider: provider.name,
        model,
        status: failure ? 'error' : 'ok',
        latency_ms: Math.round(performance.now() - started),
        finish_reason: finish || undefined,
        error: failure || undefined,
        input_tokens: u?.prompt_tokens ?? 0,
        output_tokens: u?.completion_tokens ?? 0,
        input: JSON.stringify(reqMessages.slice(-4)),
        output,
      });
    }
  }

  type RequestMessage = { role: string; content: any; tool_calls?: any[]; tool_call_id?: string } | { at_message_id: string };

  /**
   * The messages a model call sends: the system prompt, then the transcript
   * from the latest compaction summary on. Earlier messages stay on screen but
   * no longer reach the model. Saved rows go by reference unless the call is
   * made by this browser (a local provider), which needs them inline.
   */
  async function buildRequestMessages(turn: TurnContext, history: ChatMessage[], inline: boolean) {
    const reqMessages: RequestMessage[] = [];
    if (turn.systemPrompt) reqMessages.push({ role: 'system', content: turn.systemPrompt });
    const boundary = compactionStart(meta.slice(0, history.length).map(m => m?.compaction));
    const contextStart = Math.max(0, boundary);
    for (const [index, m] of history.entries()) {
      if (index < contextStart) continue;
      if (conversationId && meta[index]?.id && !inline) {
        reqMessages.push({ at_message_id: meta[index].id! });
        continue;
      }
      const msg: any = { role: m.role, content: await outgoingContent(m.content, m.role) };
      turnLifecycle.assert(turn);
      if (m.tool_calls) msg.tool_calls = m.tool_calls;
      if (m.tool_call_id) msg.tool_call_id = m.tool_call_id;
      reqMessages.push(msg);
    }
    // Unloaded older rows are fetched by the server only when no summary in
    // this page already replaces them.
    return { reqMessages, contextStart, needsPrefix: boundary < 0 };
  }

  async function runCompletionStep(turn: TurnContext): Promise<boolean> {
    turnLifecycle.assert(turn);
    const turnPair = splitModel(turn.model);
    const history = messages.slice();
    const sessionId = turn.sessionId;

    // Add assistant placeholder. It records the pair selected right now, so a
    // mid-conversation switch is attributed to the turn that used it.
    // The stamp stays empty until the response finishes: an entry that is
    // still streaming has no completion time, and showing the start time under
    // a growing answer would date it minutes early.
    messages = [...messages, { role: 'assistant', content: '' }];
    meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, created_at: '', imageNames: [] }];
    const placeholderIdx = messages.length - 1;
    const controller = turn.controller;

    // Accumulate tool calls from the stream
    let pendingToolCalls: ToolCall[] = [];

    try {
      // A local model is called by this browser, so the server cannot fill in
      // history by reference: every message goes inline, and an unloaded
      // prefix is loaded first rather than silently dropped from context.
      const localTarget = isLocalModelRef(turn.model) ? localProviderFor(turn.model) : null;
      if (localTarget && historyTruncated && !meta.some(m => m.compaction)) {
        throw new Error('Load older messages first: a local provider needs the whole conversation in this page.');
      }

      const { reqMessages, needsPrefix } = await buildRequestMessages(turn, history, !!localTarget);

      const callbacks: StreamCallbacks = {
        requireComplete: true,
        mintMissingToolCallIds: !!localTarget,
        onDelta: (deltaContent) => {
          turnLifecycle.assert(turn);
          const lastIdx = messages.length - 1;
          const prev = messages[lastIdx];
          messages[lastIdx] = {
            ...prev,
            content: mergeDeltaContent(prev.content, deltaContent),
          };
          scrollToBottom();
        },
        onToolCalls: (toolCalls) => {
          turnLifecycle.assert(turn);
          pendingToolCalls = toolCalls;
        },
        onError: (error) => {
          addToast(error, 'alert');
        },
        onUsage: (usage) => {
          turnLifecycle.assert(turn);
          contextTokens = usage.prompt_tokens;
          completionTokens += usage.completion_tokens;
          totalTokens = contextTokens + completionTokens;
          if (meta[placeholderIdx]) meta[placeholderIdx] = { ...meta[placeholderIdx], usage: callUsage(usage) };
        },
      };

      if (localTarget) {
        await runLocalProviderCompletion(turn, localTarget, reqMessages, callbacks);
      } else {
        await streamChatCompletion(
          'api/v1/chats/completions',
          {
            model: turn.model,
            at_conversation_id: conversationId || undefined,
            at_history_before: historyTruncated && needsPrefix ? (meta[0]?.id || historyCursor) : undefined,
            metadata: { session_id: sessionId },
            messages: reqMessages,
            tools: turn.tools.length > 0 ? turn.tools : undefined,
            reasoning_effort: turn.reasoning || undefined,
            stream: true,
            stream_options: { include_usage: true },
          },
          callbacks,
          controller.signal,
          { 'x-at-trace-id': turn.traceId },
        );
      }
      turnLifecycle.assert(turn);

      // The response is complete — stamp it. A turn that goes on to call tools
      // stamps here too: this assistant message is finished, the tool results
      // and the follow-up are separate entries with their own times.
      const answeredIdx = messages.length - 1;
      if (meta[answeredIdx]) meta[answeredIdx] = { ...meta[answeredIdx], created_at: new Date().toISOString() };

      // After streaming completes, check if there are tool calls to execute
      if (pendingToolCalls.length > 0) {
        // Attach tool calls to the assistant message
        const lastIdx = messages.length - 1;
        messages[lastIdx] = { ...messages[lastIdx], tool_calls: pendingToolCalls };

        // Execute each tool call and add tool result messages
        for (const tc of pendingToolCalls) {
          activeTool = { messageIndex: lastIdx, callID: tc.id };
          const result = await executeToolCall(tc, turn);
          turnLifecycle.assert(turn);
          messages = [
            ...messages,
            {
              role: 'tool',
              content: result,
              tool_call_id: tc.id,
            },
          ];
          meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, created_at: new Date().toISOString(), imageNames: [] }];
        }
        activeTool = null;
        scrollToBottom();
        // Messages written while the tools ran join before the next model call.
        await drainQueue(turn);

        return true;
      }

      // The model answered while background skill runs are still unreported.
      // Ending here would drop their results, so wait for them and let the
      // model answer again with what they produced.
      const unreported = turnSkillRuns.filter(r => !r.reported);
      if (unreported.length > 0) {
        streaming = true;
        const waitKey = `wait-${turn.traceId}`;
        const report = await collectSkillRuns(unreported.map(r => r.id), waitKey, turn);
        turnLifecycle.assert(turn);
        skillRunProgress = Object.fromEntries(Object.entries(skillRunProgress).filter(([k]) => k !== waitKey));
        messages = [...messages, { role: 'user', content: `Background skill runs finished:\n\n${report}\n\nReply to me using these results.` }];
        meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, created_at: new Date().toISOString(), imageNames: [] }];
        await drainQueue(turn);
        return true;
      }

      // The turn is complete: attach the files its skill runs delivered to the
      // final answer, so they are shown there and saved with the transcript.
      if (turnArtifacts.length > 0) {
        const lastIdx = messages.length - 1;
        const last = messages[lastIdx];
        if (last?.role === 'assistant') {
          const existing: ContentPart[] = typeof last.content === 'string'
            ? (last.content ? [{ type: 'text', text: last.content }] : [])
            : [...last.content];
          const files: ContentPart[] = turnArtifacts.map(a => INLINE_IMAGE_TYPES.includes(a.content_type)
            ? { type: 'image', media_id: a.media_id, name: a.name, bytes: a.size_bytes }
            : { type: 'file', media_id: a.media_id, name: a.name, bytes: a.size_bytes, mime_type: a.content_type });
          messages[lastIdx] = { ...last, content: [...existing, ...files] };
        }
        turnArtifacts = [];
      }

      // The model answered; anything queued meanwhile continues this turn.
      if (await drainQueue(turn)) return true;
    } catch (e: any) {
      if (!turnLifecycle.current(turn)) throw e;
      if (controller.signal.aborted || e.name === 'AbortError') {
        // User cancelled — don't show error
      } else {
        turn.failed = true;
        addToast(e.message || 'Chat request failed', 'alert');
        // Remove empty assistant message on error
        const lastIdx = messages.length - 1;
        if (messages[lastIdx]?.role === 'assistant' && !getTextContent(messages[lastIdx].content)) {
          messages = messages.slice(0, -1);
          meta = meta.slice(0, -1);
        }
      }
    } finally {
      // An interrupted or failed response that kept its partial text is still
      // an entry in the transcript, so it is stamped when it stopped rather
      // than left undated. Idempotent: a completed response already has one.
      const endedIdx = messages.length - 1;
      if (turnLifecycle.current(turn) && messages[endedIdx]?.role === 'assistant' && meta[endedIdx] && !meta[endedIdx].created_at) {
        meta[endedIdx] = { ...meta[endedIdx], created_at: new Date().toISOString() };
      }
    }
    return false;
  }

  function stopStreaming() {
    if (abortController) {
      abortController.abort();
    }
  }

  /** Two-step confirm — clearing a saved transcript deletes stored rows. */
  function requestClear() {
    if (streaming || saving) return;
    if (!confirmClear) {
      confirmClear = true;
      if (confirmClearTimer) clearTimeout(confirmClearTimer);
      confirmClearTimer = setTimeout(() => { confirmClear = false; }, 5000);
      return;
    }
    if (confirmClearTimer) { clearTimeout(confirmClearTimer); confirmClearTimer = null; }
    confirmClear = false;
    void clearChat();
  }

  async function clearChat() {
    const generation = turnLifecycle.generation();
    await truncateFrom(0);
    if (disposed || turnLifecycle.generation() !== generation) return;
    pendingImages = [];
    todos = [];
    pendingQuestion = null;
    contextTokens = 0;
    completionTokens = 0;
    totalTokens = 0;
    // A saved conversation keeps its system prompt; a scratch buffer resets fully.
    if (!conversationId) systemPrompt = '';
  }

  /** Retry from a specific user message index. */
  async function retryFromIndex(index: number) {
    // Truncating while an append is in flight would desync stored sequences.
    if (streaming || saving) return;
    if (!setupReady()) return;
    const turn = beginTurn();
    if (!turn) return;
    try {
      // Trim stored history first so it matches what the user sees.
      await truncateFrom(index + 1);
      turnLifecycle.assert(turn);
      scrollToBottom();
      await runCompletion(turn);
      if (!turnLifecycle.current(turn)) return;
      await persistPending();
    } catch (e) {
      if (turnLifecycle.current(turn) && !turn.controller.signal.aborted && (e as Error).name !== 'AbortError') {
        turn.failed = true;
        addToast((e as Error).message || 'Chat retry failed', 'alert');
      }
    } finally {
      finishTurn(turn);
      continueWithQueue(turn);
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (commandSuggestions.length > 0 && !e.isComposing) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const n = commandSuggestions.length;
        commandIndex = (commandIndex + (e.key === 'ArrowDown' ? 1 : -1) + n) % n;
        return;
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault();
        if (!e.repeat) pickCommand(commandSuggestions[commandIndex], e.key === 'Enter');
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        commandMenuDismissed = userInput;
        return;
      }
    }
    // Ctrl/Cmd+Enter while the agent works: stop it and continue with the queue.
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && streaming && !e.isComposing) {
      e.preventDefault();
      if (!e.repeat) interruptAndSend();
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      if (!e.repeat) void sendMessage();
      return;
    }
    // Shift+Tab on an empty composer cycles presets; plain Tab keeps moving focus.
    if (e.key === 'Tab' && e.shiftKey && !userInput && pendingImages.length === 0 && presets.length > 0) {
      e.preventDefault();
      cyclePreset();
      return;
    }
    // Recall the newest queued message for editing, like a shell history.
    if (e.key === 'ArrowUp' && !userInput && pendingImages.length === 0 && queuedMessages.length > 0) {
      e.preventDefault();
      editQueued(queuedMessages[queuedMessages.length - 1].id);
      return;
    }
    if (e.key === 'Escape' && streaming && !e.isComposing) {
      e.preventDefault();
      stopStreaming();
    }
  }

  // ─── Terminal layout: command palette and session sidebar ───

  let palette = $state<'' | 'commands' | 'models' | 'presets' | 'effort'>('');
  let showSessionPanel = $state(typeof window === 'undefined' || window.matchMedia('(min-width: 1280px)').matches);
  let composerInput: HTMLTextAreaElement | undefined = $state();

  // Sidebars become overlays when the window narrows; close them rather than
  // letting a desktop choice cover the transcript.
  $effect(() => {
    const lg = window.matchMedia('(min-width: 1024px)');
    const xl = window.matchMedia('(min-width: 1280px)');
    const onLg = () => { if (!lg.matches) showConversations = false; };
    const onXl = () => { if (!xl.matches) showSessionPanel = false; };
    lg.addEventListener('change', onLg);
    xl.addEventListener('change', onXl);
    return () => { lg.removeEventListener('change', onLg); xl.removeEventListener('change', onXl); };
  });

  const modelLabel = (ref: string) => ref.slice(ref.indexOf('/') + 1) || ref;
  const providerLabel = (ref: string) => (ref.includes('/') ? ref.slice(0, ref.indexOf('/')) : '');
  let activePresetName = $derived(presets.find(p => p.id === activePresetId)?.name ?? '');
  let conversationTitle = $derived(conversation?.title?.trim() || (conversationId ? 'Untitled conversation' : 'New chat'));

  function setModel(ref: string) {
    selectedModel = ref;
    workbenchChanged();
  }

  function cycleReasoningEffort() {
    if (reasoningEffortOptions.length === 0) return;
    const options = ['', ...reasoningEffortOptions];
    reasoningEffort = options[(options.indexOf(effectiveReasoningEffort) + 1) % options.length];
    workbenchChanged();
  }

  function cyclePreset() {
    if (presets.length === 0) return;
    // An unsaved setup is a stop in the cycle, so cycling never loses it.
    const stops: string[] = [...(customSetup ? [''] : []), ...presets.map(p => p.id)];
    let index = activePresetId ? stops.indexOf(activePresetId) : onCustomSetup ? 0 : -1;
    if (index < 0 && !presets.some(presetMatchesCurrent)) {
      // A setup changed since the last capture: it becomes the unsaved stop.
      customSetup = currentSetup();
      if (stops[0] !== '') stops.unshift('');
      index = 0;
    }
    const next = stops[(index + 1) % stops.length];
    if (next) applyPreset(next);
    else restoreCustomSetup();
  }

  function openWorkbench(tab: WorkbenchTab = 'prompt') {
    workbenchTab = tab;
    showWorkbench = true;
  }

  let paletteTitle = $derived(palette === 'models' ? 'Select model' : palette === 'presets' ? 'Apply preset' : palette === 'effort' ? 'Reasoning effort' : 'Commands');

  let paletteGroups = $derived.by((): PaletteGroup[] => {
    if (palette === 'models') {
      return modelGroups.map(group => ({
        label: group.label,
        items: group.models.map(ref => ({ label: modelLabel(ref), current: ref === selectedModel, run: () => setModel(ref) })),
      }));
    }
    if (palette === 'presets') {
      const toItem = (p: ChatPreset) => ({ label: p.name, current: p.id === activePresetId, run: () => applyPreset(p.id) });
      return [
        ...(customSetup ? [{ label: 'Current', items: [{ label: 'My unsaved setup', current: onCustomSetup, run: restoreCustomSetup }] }] : []),
        { label: 'My presets', items: personalPresets.map(toItem) },
        { label: 'Workspace presets', items: workspacePresets.map(toItem) },
        { label: 'Manage', items: [{ label: 'Save or edit presets…', run: () => openWorkbench('prompt') }] },
      ];
    }
    if (palette === 'effort') {
      return [{
        label: modelLabel(selectedModel),
        items: ['', ...reasoningEffortOptions].map(effort => ({
          label: effort || 'default',
          current: effort === effectiveReasoningEffort,
          run: () => { reasoningEffort = effort; workbenchChanged(); },
        })),
      }];
    }
    const lastAssistant = messages.findLastIndex(m => m.role === 'assistant');
    return [
      {
        label: 'Session',
        items: [
          { label: 'New chat', hint: 'ctrl+alt+n', run: newConversation },
          { label: 'Share snapshot', disabled: !(sharingAvailable && conversationId && shareBoundaries.length > 0), run: () => (showShareDialog = true) },
          { label: 'Compact conversation', hint: '/compact', disabled: !canCompact, run: () => void compactConversation('') },
          { label: 'Copy last answer', disabled: lastAssistant < 0, run: () => copyMessage(lastAssistant) },
          { label: 'Fork from last message', disabled: messages.length === 0 || meta[messages.length - 1]?.sequence == null, run: () => forkFrom(messages.length - 1) },
          { label: unsavedCount > 0 ? `Retry saving ${unsavedCount} message${unsavedCount === 1 ? '' : 's'}` : 'Retry saving', disabled: !(unsavedCount > 0 && conversationId), run: () => persistPending() },
          { label: 'Clear transcript…', disabled: streaming || saving || (messages.length === 0 && !systemPrompt && pendingImages.length === 0), run: () => { showSessionPanel = true; requestClear(); } },
        ],
      },
      {
        label: 'Model',
        items: [
          { label: 'Switch model', hint: 'ctrl+m', disabled: models.length === 0, run: () => (palette = 'models') },
          { label: 'Reasoning effort', hint: 'ctrl+t', disabled: reasoningEffortOptions.length === 0, run: () => (palette = 'effort') },
          { label: 'Apply preset', hint: 'shift+tab', disabled: presets.length === 0, run: () => (palette = 'presets') },
        ],
      },
      {
        label: 'Workbench',
        items: [
          { label: 'System prompt', run: () => openWorkbench('prompt') },
          { label: 'Skills', run: () => openWorkbench('skills') },
          { label: 'Server tools & MCP sets', run: () => openWorkbench('tools') },
          { label: 'Chat tools, local MCP & extensions', run: () => openWorkbench('chat') },
          ...(localProvidersAvailable ? [{ label: 'Local providers', run: () => openWorkbench('providers') }] : []),
        ],
      },
      {
        label: 'Commands',
        items: [
          ...customCommands.map(c => ({ label: `/${c.name}${c.description ? ` — ${c.description}` : ''}`, hint: c.scope === 'workspace' ? 'workspace' : undefined, run: () => { userInput = `/${c.name} `; tick().then(() => composerInput?.focus()); } })),
          { label: 'Manage commands…', run: () => openWorkbench('commands') },
        ],
      },
      {
        label: 'View',
        items: [
          { label: showConversations ? 'Hide chats sidebar' : 'Show chats sidebar', hint: 'ctrl+b', run: () => (showConversations = !showConversations) },
          { label: showSessionPanel ? 'Hide session sidebar' : 'Show session sidebar', hint: 'ctrl+.', run: () => (showSessionPanel = !showSessionPanel) },
        ],
      },
    ];
  });

  function closePalette() {
    palette = '';
    tick().then(() => composerInput?.focus());
  }

  /** Page-level shortcuts. Ignored while another dialog owns the keyboard. */
  function handleGlobalKeydown(e: KeyboardEvent) {
    if (e.isComposing || palette || showWorkbench || showShareDialog || localApprovalFor || extensionApprovalTarget || extensionInspectorTarget) return;
    if (!(e.ctrlKey || e.metaKey) || e.shiftKey) return;
    const key = e.key.toLowerCase();
    if (key === 'p' && !e.altKey) { e.preventDefault(); palette = 'commands'; }
    else if (key === 'm' && !e.altKey && models.length > 0) { e.preventDefault(); palette = 'models'; }
    else if (key === 't' && !e.altKey && reasoningEffortOptions.length > 0 && document.activeElement === composerInput) { e.preventDefault(); cycleReasoningEffort(); }
    else if (key === 'b' && !e.altKey) { e.preventDefault(); showConversations = !showConversations; }
    else if (key === '.' && !e.altKey) { e.preventDefault(); showSessionPanel = !showSessionPanel; }
    else if (key === 'n' && e.altKey) { e.preventDefault(); newConversation(); }
  }

  function growComposer(node: HTMLTextAreaElement, _value: string) {
    let active = true;
    let width = 0;
    const resize = () => {
      if (!active) return;
      node.style.height = 'auto';
      node.style.height = `${node.scrollHeight + node.offsetHeight - node.clientHeight}px`;
    };
    const observer = new ResizeObserver(entries => {
      const nextWidth = entries[0]?.contentRect.width;
      if (nextWidth !== width) { width = nextWidth; resize(); }
    });
    observer.observe(node);
    queueMicrotask(resize);
    return {
      // Also grow for paste/voice input and shrink when a sent draft is cleared.
      update(_value: string) { queueMicrotask(resize); },
      destroy() { active = false; observer.disconnect(); },
    };
  }

</script>

<svelte:head>
  <title>AT | Chats</title>
</svelte:head>

{#snippet copyAction(index: number)}
  {@const hasText = !!messageText(messages[index]?.content ?? '').trim()}
  {#if hasText}
    <button
      onclick={() => copyMessage(index)}
      aria-label={copiedIndex === index ? 'Copied' : 'Copy message'}
      title="Copy message"
      class="oc-link opacity-0 group-hover:opacity-100 group-has-[:focus-visible]:opacity-100 focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent"
    >{#if copiedIndex === index}<span class="text-[var(--oc-green)]">copied</span>{:else}copy{/if}</button>
  {/if}
{/snippet}

{#snippet rawToggle(index: number)}
  {#if messageText(messages[index]?.content ?? '').trim()}
    <button
      onclick={() => { rawMessages = { ...rawMessages, [index]: !rawMessages[index] }; }}
      aria-pressed={!!rawMessages[index]}
      aria-label={rawMessages[index] ? 'Show rendered markdown' : 'Show raw text'}
      title={rawMessages[index] ? 'Show rendered markdown' : 'Show raw markdown source'}
      class="oc-link opacity-0 group-hover:opacity-100 group-has-[:focus-visible]:opacity-100 focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent"
    >{rawMessages[index] ? 'rendered' : 'raw'}</button>
  {/if}
{/snippet}

{#snippet forkAction(index: number)}
  {@const sequence = meta[index]?.sequence ?? null}
  <button
    onclick={() => forkFrom(index)}
    disabled={sequence === null}
    aria-label={sequence === null ? 'Fork unavailable: this message is not saved yet' : `Fork a new conversation from message ${sequence}`}
    title={sequence === null ? 'Fork becomes available once this message is saved to history' : 'Fork a new conversation from here'}
    class="oc-link opacity-0 group-hover:opacity-100 group-has-[:focus-visible]:opacity-100 focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent disabled:opacity-0 disabled:group-hover:opacity-40 disabled:cursor-not-allowed"
  >fork</button>
{/snippet}

{#snippet modelBadge(index: number)}
  {@const m = meta[index]}
  {#if m && (m.model || m.provider_key)}
    <span title={`Produced by ${joinModel(m.provider_key, m.model)}`}>{m.model || m.provider_key}</span>
  {/if}
{/snippet}

{#snippet messageTime(index: number)}
  {@const stamp = meta[index]?.created_at ?? ''}
  {#if stamp}
    <span class="tabular-nums whitespace-nowrap" title={formatLocalDateTime(stamp)}>{formatMessageTime(stamp)}</span>
  {/if}
{/snippet}

<svelte:window onkeydown={handleGlobalKeydown} />

<div class="oc-theme flex h-full min-h-0 text-sm leading-relaxed">
  {#if showConversations}
    <!-- Below lg the list overlays the transcript instead of squeezing it. -->
    <div class="fixed inset-y-0 left-0 z-40 shadow-[12px_0_40px_-8px_rgb(0_0_0/0.8)] lg:static lg:z-auto lg:shadow-none">
      <ConversationList
        {conversations}
        activeId={conversationId}
        loading={conversationsLoading}
        hasMore={!!conversationsCursor}
        scratchDirty={!conversationId && messages.length > 0}
        onSelect={id => { selectConversation(id); if (!window.matchMedia('(min-width: 1024px)').matches) showConversations = false; }}
        onNew={newConversation}
        onRename={renameConversation}
        onDelete={removeConversation}
        onLoadMore={() => loadConversations(true)}
      />
    </div>
    <button class="fixed inset-0 z-30 bg-black/50 lg:hidden" aria-label="Close chats sidebar" onclick={() => (showConversations = false)}></button>
  {/if}

<div
  class="flex flex-col flex-1 min-w-0 h-full relative"
  ondragover={handleDragOver}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
  role="application"
>
  <!-- Drag overlay -->
  {#if dragging}
    <div class="absolute inset-0 z-50 bg-dark-base/70 border border-dashed border-accent flex items-center justify-center pointer-events-none">
      <div class="bg-dark-surface px-4 py-2 text-dark-text">drop files to attach — saved to history when media storage is configured</div>
    </div>
  {/if}

  {#if showShareDialog && conversationId}
    <ShareDialog conversationId={conversationId} boundaries={shareBoundaries} onclose={() => (showShareDialog = false)} />
  {/if}

  <!-- Workbench.
       One dialog rather than two strips under the toolbar. The strips were
       capped at a fixed height inside the chat column, so between them they
       took a third of the page while still scrolling their own contents —
       the setup was cramped and the transcript was too. Everything here
       answers one question: what this conversation runs with. -->
  {#if showWorkbench}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 sm:pt-8"
      onpointerdown={handleWorkbenchBackdropPointerDown}
      onpointercancel={() => (workbenchBackdropPressStarted = false)}
      onclick={handleWorkbenchBackdropClick}
    >
      <div
        bind:this={workbenchPanel}
        tabindex="-1"
        role="dialog"
        aria-modal="true"
        aria-label="Workbench"
        onkeydown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); showWorkbench = false; } }}
        class="flex w-full max-w-3xl max-h-[calc(100dvh-2rem)] flex-col border border-dark-border bg-dark-surface focus:outline-none sm:max-h-[calc(100dvh-4rem)]"
      >
        <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-dark-border shrink-0">
          <div class="min-w-0">
            <h2 class="text-sm font-medium text-dark-text">Workbench</h2>
            <p class="mt-0.5 text-dark-text-muted">
              What this conversation runs with. Changes take effect on the next message and are saved with the conversation.
            </p>
          </div>
          <button
            onclick={() => (showWorkbench = false)}
            aria-label="Close"
            class="p-1 shrink-0 text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent"
          >
            <X size={16} />
          </button>
        </div>

        <!-- Presets stay above the tabs: they describe and replace the complete
             setup, so burying them inside one category makes their scope look
             smaller than it is. -->
        <div class="shrink-0 border-b border-dark-border px-4 py-3">
          <div class="flex items-center justify-between gap-3 mb-1.5">
            <div>
              <h3 class="text-xs font-medium text-dark-text">Preset</h3>
              <p class="text-dark-text-muted">Save or replace the complete setup shown below.</p>
            </div>
            {#if activePresetId}
              <span class="shrink-0 border border-emerald-900/60 px-1.5 py-0.5 text-emerald-300">Applied</span>
            {/if}
          </div>
          <div class="flex flex-wrap gap-2">
            <select
              bind:value={presetSaveScope}
              aria-label="Preset visibility"
              class="border border-dark-border-subtle bg-dark-elevated text-dark-text px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/10 focus:border-dark-border-subtle"
            >
              <option value="personal">Only me</option>
              <option value="workspace">Workspace</option>
            </select>
            <input
              bind:value={presetDraftName}
              placeholder="Preset name"
              aria-label="Preset name"
              maxlength={80}
              class="min-w-0 flex-1 basis-40 border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/10 focus:border-dark-border-subtle"
            />
            <button
              onclick={savePreset}
              title={draftPreset && !draftPreset.can_edit
                ? 'Only its creator can overwrite this workspace preset'
                : draftPreset
                  ? `Replace "${draftPreset.name}" with the current setup`
                  : `Save the current setup for ${presetSaveScope === 'workspace' ? 'this workspace' : 'yourself'}`}
              class="px-3 py-1.5 text-sm bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-30"
              disabled={!presetDraftName.trim() || presetSaving || !!(draftPreset && !draftPreset.can_edit)}
            >
              {draftPreset && !draftPreset.can_edit ? 'Owned by teammate' : draftPreset ? 'Overwrite' : presetSaveScope === 'workspace' ? 'Share setup' : 'Save setup'}
            </button>
            {#if draftPreset?.can_edit}
              <button
                onclick={() => deletePreset(draftPreset)}
                disabled={presetSaving}
                title={`Delete the saved setup "${draftPreset.name}". The current selections stay.`}
                class="px-3 py-1.5 text-sm border border-dark-border-subtle text-dark-text-secondary hover:border-red-800 hover:text-red-400 disabled:opacity-30"
              >
                Delete
              </button>
            {/if}
          </div>
          {#if draftPreset && !draftPreset.can_edit}
            <p class="mt-1.5 text-xs text-amber-300">This workspace preset belongs to another member. Change the name to save a derived preset; the original stays unchanged.</p>
          {/if}
        </div>

        <div
          role="tablist"
          tabindex="-1"
          aria-label="Workbench sections"
          onkeydown={handleWorkbenchTabsKeydown}
          class="shrink-0 flex overflow-x-auto border-b border-dark-border bg-dark-base px-2"
        >
          {#each workbenchTabs as tab}
            <button
              id={`workbench-tab-${tab.id}`}
              role="tab"
              aria-selected={workbenchTab === tab.id}
              aria-controls={`workbench-panel-${tab.id}`}
              tabindex={workbenchTab === tab.id ? 0 : -1}
              onclick={() => (workbenchTab = tab.id)}
              class={[
                'min-h-10 shrink-0 border-b-2 px-3 text-xs font-medium focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent',
                workbenchTab === tab.id
                  ? 'border-accent text-dark-text'
                  : 'border-transparent text-dark-text-muted hover:text-dark-text',
              ]}
            >
              {tab.label}
            </button>
          {/each}
        </div>

        <div
          id={`workbench-panel-${workbenchTab}`}
          role="tabpanel"
          aria-labelledby={`workbench-tab-${workbenchTab}`}
          class="flex-1 overflow-y-auto px-4 py-4 space-y-4"
        >
          {#if workbenchTab === 'commands'}
            <ChatCommandsEditor personal={personalCommands} workspace={workspaceCommands} {models} reserved={builtinCommands.map(c => c.name)} onchange={loadCommands} />
          {/if}

          <!-- System prompt leads because it is the instruction the selected
               skills and tools serve. -->
          {#if workbenchTab === 'prompt'}
          <div class="block max-w-2xl">
            <span class="text-xs font-medium text-dark-text-muted mb-1 block">System prompt</span>
            <textarea
              value={systemPrompt}
              oninput={(e) => { systemPrompt = e.currentTarget.value; workbenchChanged(); }}
              aria-label="System prompt"
              placeholder="System prompt (optional)"
              rows={8}
              class="w-full border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted px-3 py-1.5 text-sm resize-y focus:outline-none focus:ring-2 focus:ring-accent/10 focus:border-dark-border-subtle"
            ></textarea>
          </div>
          <p class="text-xs text-dark-text-muted">
            The prompt guides the whole conversation. Presets also capture the model, prompt, skills and every tool selection without changing the transcript.
          </p>
          {/if}

          {#if workbenchTab === 'providers' && localProvidersAvailable}
          <!-- Local providers: OpenAI-compatible endpoints this browser calls
               directly. The server stores the record and never sends a
               request to it. -->
          <div role="group" aria-label="Local providers" class="block max-w-3xl">
            <div class="flex items-center gap-2 mb-1">
              <span class="text-xs font-medium text-dark-text-muted">Called by this browser</span>
              <button
                onclick={() => editLocalProvider()}
                class="ml-auto px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated"
              >
                Add provider
              </button>
            </div>
            <p class="mb-2 text-dark-text-muted">
              An OpenAI-compatible endpoint — a model server on your computer or an API with your own key. Your browser calls it directly, so it must allow this page (CORS). Provider budgets and pricing do not apply; see <a href="#/docs?g=local-providers" class="underline underline-offset-2 hover:text-dark-text">Documentation → Local providers</a>.
            </p>

            <div class="space-y-1.5">
              {#each localProviders as provider (provider.id)}
                {@const enabled = localProviderEnabledIds.includes(provider.id)}
                {@const status = localProviderStatus[provider.id]}
                <div class="border border-dark-border-subtle px-2.5 py-1.5">
                  <div class="flex items-center gap-2 flex-wrap">
                    <span class="text-xs font-medium text-dark-text">{provider.name}</span>
                    <code class="font-mono text-dark-text-muted truncate">{provider.base_url}</code>
                    {#if enabled}
                      <span class="px-1.5 py-0.5 border border-emerald-900/60 text-emerald-300">
                        Enabled here{status?.busy ? ' · listing models…' : status?.models?.length ? ` · ${status.models.length} models` : ''}
                      </span>
                    {:else}
                      <span class="px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-muted">
                        Not enabled on this device
                      </span>
                    {/if}
                    <div class="ml-auto flex items-center gap-1">
                      {#if enabled}
                        <button onclick={() => discoverLocalProviderModels(provider)} disabled={status?.busy} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated disabled:opacity-40">Refresh models</button>
                        <button onclick={() => toggleLocalProvider(provider)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:text-red-400">Disable</button>
                      {:else}
                        <button onclick={() => toggleLocalProvider(provider)} class="px-2 py-0.5 text-[10px] border border-accent bg-accent text-dark-base">Enable here</button>
                      {/if}
                      <button onclick={() => editLocalProvider(provider)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated">Edit</button>
                      <button onclick={() => removeLocalProvider(provider)} class="p-0.5 text-dark-text-muted hover:text-red-500" aria-label={`Remove ${provider.name}`}><X size={12} /></button>
                    </div>
                  </div>
                  {#if status?.error}
                    <p class="mt-1 text-red-400">{status.error}</p>
                  {/if}
                </div>
              {/each}
            </div>

            {#if localProviders.length === 0 && !localProviderEditorOpen}
              <p class="text-dark-text-muted">No local providers yet. {LOCAL_PROVIDER_CORS_HINT}</p>
            {/if}

            {#if localProviderEditorOpen}
              {#key localProviderEditorKey}
                <LocalProviderEditor
                  provider={localProviderEditing}
                  providers={localProviders}
                  onsaved={localProviderSaved}
                  oncancel={closeLocalProviderEditor}
                />
              {/key}
            {/if}
          </div>
          {/if}

          {#if workbenchTab === 'tools'}
          <!-- MCP Sets (Internal MCPs) -->
          {#if availableMCPSets.length > 0}
            <div role="group" aria-label="MCP" class="block">
              <span class="text-xs font-medium text-dark-text-muted mb-1 block">MCP</span>
              <div class="flex flex-wrap gap-1.5">
                {#each availableMCPSets as mcpSet}
                  {@const status = mcpSetStatus[mcpSet.name]}
                  <button
                    onclick={() => toggleMCPSet(mcpSet.name)}
                    aria-pressed={selectedMCPSetNames.includes(mcpSet.name)}
                    aria-label={`${mcpSet.name}${selectedMCPSetNames.includes(mcpSet.name) ? ' · Selected' : ''}`}
                    class="px-2.5 py-1 text-xs border {selectedMCPSetNames.includes(mcpSet.name)
                      ? 'bg-purple-600 text-white border-purple-600'
                      : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated'}"
                    title={mcpSet.description || mcpSet.name}
                  >
                    {mcpSet.name}
                    {#if status?.busy}
                      <Loader2 size={10} class="ml-1 inline animate-spin" />
                    {:else if status?.error || status?.warnings.length}
                      <span class="ml-1 border px-1 border-red-800 bg-red-900/30 text-red-300" title={status.error || status.warnings.join('\n')}>{status.tools.length > 0 ? `Partial · ${status.tools.length}` : 'Failed'}</span>
                    {:else if status}
                      <span class="ml-1 opacity-70">({status.tools.length})</span>
                    {/if}
                  </button>
                  {#if selectedMCPSetNames.includes(mcpSet.name) && !status?.busy && (status?.error || status?.warnings.length)}
                    <p role="status" class="basis-full text-red-400">
                      {mcpSet.name}: {status.error || status.warnings[0]}
                    </p>
                  {/if}
                {/each}
              </div>
            </div>
          {/if}

          <!-- Direct MCP URLs were removed: register the server as an MCP set so it
               carries its credentials, processes and execution admission. A saved
               conversation keeps its old record until it is dismissed. -->
          {#if legacyMcpUrls.length > 0}
            <div class="border border-amber-900/50 bg-amber-900/20 px-3 py-2 space-y-1.5">
              <p class="text-xs text-amber-200">
                This conversation referenced {legacyMcpUrls.length} direct MCP server URL{legacyMcpUrls.length === 1 ? '' : 's'}, which Chats no longer calls. Add the server under MCP sets to use its tools again.
              </p>
              <div class="flex flex-wrap gap-1.5">
                {#each legacyMcpUrls as url}
                  <code class="border border-amber-900/50 bg-dark-elevated px-2 py-0.5 font-mono text-amber-200 truncate max-w-full">{url}</code>
                {/each}
              </div>
              <button onclick={dismissLegacyMcpUrls} class="text-amber-300 underline hover:no-underline">Dismiss</button>
            </div>
          {/if}

          <!-- Server Tools (built-in) -->
          {#if builtinTools.length > 0 || enabledBuiltinTools.length > 0}
            <div role="group" aria-label="Server Tools" class="block">
              <span class="text-xs font-medium text-dark-text-muted mb-1 block">Server Tools</span>
              <BuiltinToolPicker tools={builtinTools.filter(tool => !isChatTodoTool(tool.name))} bind:selected={enabledBuiltinTools} collapsed onchange={refreshTools} />
            </div>
          {/if}
          {/if}

          <!-- Skills are a first-class part of the Chat setup. -->
          {#if workbenchTab === 'skills'}
          {#if skills.length > 0}
            <div role="group" aria-label="Skills" class="block">
              <span class="text-xs font-medium text-dark-text-muted mb-1 block">Skills</span>
              <p class="mb-2 text-xs text-dark-text-muted">Add reusable Markdown instructions and reference resources. Skills do not grant tools.</p>
              <div class="flex flex-wrap gap-1.5">
                {#each skills as skill}
                  <button
                    onclick={() => toggleSkill(skill.name)}
                    aria-pressed={selectedSkillNames.includes(skill.name)}
                    aria-label={`${skill.name}${selectedSkillNames.includes(skill.name) ? ' · Selected' : ''}`}
                    class="px-2.5 py-1 text-xs border {selectedSkillNames.includes(skill.name)
                      ? 'bg-accent text-dark-base border-accent'
                      : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated'}"
                    title={skill.description || skill.name}
                  >
                    {skill.name}
                  </button>
                {/each}
              </div>
            </div>
          {:else}
            <p class="text-xs text-dark-text-muted">No skills are available. Create or import a skill from the Skills page, then return here to add it.</p>
          {/if}
          {/if}

          <!-- Chat Tools (frontend-only) -->
          {#if workbenchTab === 'chat'}
          <div role="group" aria-label="Chat Tools" class="block">
            <span class="text-xs font-medium text-dark-text-muted mb-1 block">Chat Tools</span>
            <div class="flex flex-wrap gap-1.5">
              {#each FRONTEND_TOOLS as tool}
                <button
                  onclick={() => toggleFrontendTool(tool.function.name)}
                  aria-pressed={enabledFrontendTools.includes(tool.function.name)}
                  class="px-2.5 py-1 text-xs border {enabledFrontendTools.includes(tool.function.name)
                    ? 'bg-accent text-dark-base border-accent'
                    : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated'}"
                  title={tool.function.description}
                >
                  {tool.function.name}
                </button>
              {/each}
            </div>
            <p class="mt-2 max-w-2xl text-xs text-dark-text-muted">These tools run in this chat interface and help the model manage the conversation while you are here.</p>
          </div>

          <!-- Local MCP servers.
               Unlike every other source here these are dialled by this browser:
               the server stores the address and never connects to it, which is
               what makes a loopback URL mean this machine. -->
          {#if localMCPAvailable}
            <div role="group" aria-label="Local MCP servers" class="block">
              <div class="flex items-center gap-2 mb-1">
                <span class="text-xs font-medium text-dark-text-muted">On this machine</span>
                <button
                  onclick={() => editLocalServer()}
                  class="ml-auto px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated"
                >
                  Add server
                </button>
              </div>

              {#if localServers.length === 0 && !localEditorOpen}
                <p class="text-dark-text-muted">
                  An MCP server running on your own computer. Your browser connects to it directly, so it must allow this page (CORS) — and it is reachable only from this device.
                </p>
              {/if}

              <div class="space-y-1.5">
                {#each localServers as server}
                  {@const approved = localApprovedIds.includes(server.id)}
                  {@const status = localStatus[server.id]}
                  {@const added = toolsAddedSinceApproval(approvalFor(server.id, localStorageSafe()), status?.tools ?? [])}
                  <div class="border border-dark-border-subtle px-2.5 py-1.5">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="text-xs font-medium text-dark-text">{server.name}</span>
                      <code class="font-mono text-dark-text-muted truncate">{server.url}</code>
                      {#if approved}
                        <span class="px-1.5 py-0.5 border border-emerald-900/60 text-emerald-300">
                          Enabled here{status?.tools?.length ? ` · ${status.tools.length} tools` : ''}
                        </span>
                      {:else}
                        <span class="px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-muted">
                          Not enabled on this device
                        </span>
                      {/if}
                      <div class="ml-auto flex items-center gap-1">
                        {#if approved}
                          <button onclick={() => disableLocalServer(server)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:text-red-400">Disable</button>
                        {:else}
                          <button onclick={() => beginLocalApproval(server)} class="px-2 py-0.5 text-[10px] border border-accent bg-accent text-dark-base">Enable…</button>
                        {/if}
                        <button onclick={() => editLocalServer(server)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated">Edit</button>
                        <button onclick={() => removeLocalServer(server)} class="p-0.5 text-dark-text-muted hover:text-red-500" aria-label={`Remove ${server.name}`}><X size={12} /></button>
                      </div>
                    </div>
                    {#if added.length > 0}
                      <p class="mt-1 text-amber-300">
                        New since you enabled it: {added.join(', ')}
                      </p>
                    {/if}
                    {#if status?.error}
                      <p class="mt-1 text-red-400">{status.error}</p>
                      {#if status.hint}
                        <p class="mt-0.5 text-dark-text-muted">{status.hint}</p>
                      {/if}
                    {/if}
                  </div>
                {/each}
              </div>

              {#if localEditorOpen}
                {#key localEditorKey}
                  <LocalMCPServerEditor
                    server={localEditing}
                    servers={localServers}
                    onsaved={localServerSaved}
                    oncancel={closeLocalEditor}
                  />
                {/key}
              {/if}
            </div>
          {:else}
            <p class="text-xs text-dark-text-muted">Tools on this machine are not available in this installation.</p>
          {/if}

          <!-- Browser extensions.
               Discovery is generic: the page asks and every extension that
               implements the bridge answers for itself, so nothing here names
               a vendor. An extension that was never connected to this origin
               stays silent by design, which is why "none found" is phrased as
               an instruction rather than as a verdict. -->
          {#if extensionsAvailable}
            <div role="group" aria-label="Browser extensions" class="block">
              <div class="flex items-center gap-2 mb-1">
                <span class="text-xs font-medium text-dark-text-muted">Browser extensions</span>
                <label class="flex items-center gap-1 text-xs">
                  <input type="checkbox" bind:checked={webConnectionEnabled} />
                  Web connection
                </label>
                <button
                  onclick={() => scanExtensions()}
                  disabled={!webConnectionEnabled || extensionsScanning}
                  class="ml-auto px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated disabled:opacity-50"
                >
                  {extensionsScanning ? 'Scanning…' : 'Scan again'}
                </button>
              </div>

              {#if extensions.length === 0}
                <p class="text-dark-text-muted">
                  {!webConnectionEnabled
                    ? 'Turn on Web connection to make this chat available in your extension’s Agent list.'
                    : extensionsScanned && !extensionsScanning
                    ? 'In your extension, choose Agent, select this chat, and connect a tab. Then enable its tools here.'
                    : 'Looking for extensions that are connected to this site…'}
                </p>
              {/if}

              <div class="space-y-1.5">
                {#each extensions as ext}
                  {@const approved = extensionApprovedIds.includes(ext.id)}
                  {@const status = extensionStatus[ext.id]}
                  {@const added = toolsAddedSinceApproval(extensionApprovalFor(ext.id, localStorageSafe()), status?.tools ?? [])}
                  {@const serves = ext.capabilities.includes(CAPABILITY_TOOLS)}
                  <div class="border border-dark-border-subtle px-2.5 py-1.5">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="text-xs font-medium text-dark-text">{ext.name}</span>
                      {#if ext.version}
                        <code class="font-mono text-dark-text-muted">{ext.version}</code>
                      {/if}
                      {#if approved}
                        <button
                          onclick={() => inspectExtensionTools(ext)}
                          class="px-1.5 py-0.5 border border-emerald-900/60 text-emerald-300 hover:bg-emerald-900/20 focus-visible:outline-2 focus-visible:outline-accent"
                          title={`View tools connected through ${ext.name}`}
                        >
                          Enabled here{status?.tools?.length ? ` · ${status.tools.length} tools` : ' · View tools'}
                        </button>
                      {:else}
                        <span class="px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-muted">
                          Not enabled on this device
                        </span>
                      {/if}
                      <div class="ml-auto flex items-center gap-1">
                        {#if approved}
                          <button onclick={() => disableExtension(ext)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:text-red-400">Disable</button>
                        {:else if serves}
                          <button onclick={() => beginExtensionApproval(ext)} class="px-2 py-0.5 text-[10px] border border-accent bg-accent text-dark-base">Enable…</button>
                        {/if}
                      </div>
                    </div>
                    {#if ext.description}
                      <p class="mt-1 text-dark-text-muted">{ext.description}</p>
                    {/if}
                    <!-- The extension's own words for why it is connected but
                         idle. Without it, "0 tools" is indistinguishable from a
                         fault the reader would go looking for. -->
                    {#if ext.notice}
                      <p class="mt-1 text-amber-300">{ext.notice}</p>
                    {/if}
                    {#if !serves}
                      <p class="mt-1 text-dark-text-muted">This extension offers no tools to Chats.</p>
                    {/if}
                    {#if added.length > 0}
                      <p class="mt-1 text-amber-300">
                        New since you enabled it: {added.join(', ')}
                      </p>
                    {/if}
                    {#if status?.error}
                      <p class="mt-1 text-red-400">{status.error}</p>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <p class="text-xs text-dark-text-muted">Browser extensions are not available in this installation.</p>
          {/if}
          {/if}
        </div>

        <!-- What the selections above actually produced. It sits outside the
             scrolling body because it is the answer to "did that work?", and
             a reader who has scrolled to the bottom of the tool catalogues is
             exactly who needs to read it. -->
        <div class="shrink-0 flex items-center gap-2 px-4 py-3 border-t border-dark-border text-xs text-dark-text-muted">
          <div class="flex min-w-0 flex-1 items-center gap-2">
            {#if loadingTools}
              <Loader2 size={12} class="shrink-0 animate-spin" />
              <span>Discovering tools...</span>
            {:else if toolCount > 0}
              <Wrench size={12} class="shrink-0" />
              <span class="shrink-0">{toolCount} tool{toolCount !== 1 ? 's' : ''} available</span>
              <span class="text-dark-border">|</span>
              <span class="truncate" title={discoveredTools.map(t => t.function.name).join(', ')}>{discoveredTools.map(t => t.function.name).join(', ')}</span>
            {:else if selectedMCPSetNames.length > 0 || selectedSkillNames.length > 0 || enabledBuiltinTools.length > 0 || enabledFrontendTools.length > 0}
              <span>No tools discovered</span>
            {:else}
              <span>Select skills, MCP sets, or tools above</span>
            {/if}
          </div>
          {#if toolCount > 0 || selectedMCPSetNames.length > 0 || selectedSkillNames.length > 0 || enabledBuiltinTools.length > 0 || enabledFrontendTools.length > 0}
            <button
              onclick={clearAllToolSelections}
              class="shrink-0 px-2 py-1 border border-dark-border-subtle text-dark-text-muted hover:text-red-400 hover:border-red-800 focus-visible:outline-2 focus-visible:outline-accent"
              title="Clear all selected skills and tools"
            >
              Clear my selections
            </button>
          {/if}
          <button
            onclick={() => (showWorkbench = false)}
            class="shrink-0 px-3 py-1 text-xs bg-accent text-dark-base hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-accent"
          >
            Done
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Chat messages -->
  <div
    bind:this={chatContainer}
    onscroll={handleChatScroll}
    class="flex-1 min-h-0 overflow-y-auto"
  >
    <div bind:this={chatContent} class="mx-auto w-full max-w-[1200px] px-3 pt-4 pb-2 sm:px-8 sm:pt-6">
      <!-- Title block: the conversation and what it has cost so far. -->
      <header class="mb-5 flex items-center gap-4 bg-dark-surface px-4 py-3 sm:px-[22px] sm:py-4">
        <button
          onclick={() => (showConversations = !showConversations)}
          aria-label={showConversations ? 'Hide chats sidebar' : 'Show chats sidebar'}
          aria-expanded={showConversations}
          title="Chats (ctrl+b)"
          class="oc-link -ml-1 shrink-0 focus-visible:outline-1 focus-visible:outline-accent"
        ><PanelLeft size={15} /></button>
        <h1 class="min-w-0 flex-1 truncate font-bold text-dark-text"><span class="text-dark-text-faint">#</span> {conversationTitle}</h1>
        <div class="flex shrink-0 items-center gap-[2ch] text-dark-text-muted">
          {#if saving}
            <span>saving…</span>
          {:else if unsavedCount > 0 && conversationId}
            <button
              onclick={() => persistPending()}
              aria-label={`Retry saving ${unsavedCount} unsaved message${unsavedCount === 1 ? '' : 's'}`}
              title="These messages are only in this browser tab. Click to retry saving them."
              class="text-[var(--oc-peach)] hover:underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent"
            >{unsavedCount} unsaved</button>
          {/if}
          {#if totalTokens > 0}
            <span class="hidden tabular-nums sm:inline" title="Context: {contextTokens.toLocaleString()} prompt + {completionTokens.toLocaleString()} completion = {totalTokens.toLocaleString()} total tokens">{totalTokens.toLocaleString()}</span>
          {/if}
          <button
            onclick={() => (showSessionPanel = !showSessionPanel)}
            aria-label={showSessionPanel ? 'Hide session sidebar' : 'Show session sidebar'}
            aria-expanded={showSessionPanel}
            title="Session (ctrl+.)"
            class="oc-link focus-visible:outline-1 focus-visible:outline-accent"
          ><PanelRight size={15} /></button>
        </div>
      </header>

      {#if conversation?.forked_from_sequence}
        <p class="-mt-2 mb-5 flex items-center gap-[1ch] px-1 text-dark-text-muted">
          <GitBranch size={12} class="shrink-0 text-[var(--oc-violet)]" />
          {#if conversation.forked_from_id}
            <span>forked from <a href={`#${playgroundRoute(conversation.forked_from_id)}`} class="text-dark-text-secondary underline underline-offset-4 decoration-dark-text-faint hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent">{parentTitle || 'the source conversation'}</a> at message {conversation.forked_from_sequence}</span>
          {:else}
            <span>forked at message {conversation.forked_from_sequence} — the source conversation was deleted</span>
          {/if}
        </p>
      {/if}
      {#if conversation?.imported_from_share_id}
        <p class="-mt-2 mb-5 px-1 text-dark-text-muted">Independent copy of shared snapshot version {conversation.imported_from_share_version}. Changes here never affect the source.</p>
      {/if}

    {#if loading || historyLoading}
      <p class="px-[22px] py-10 text-dark-text-muted">{historyLoading ? 'loading conversation…' : 'loading providers…'}</p>
    {:else if models.length === 0}
      <div class="px-[22px] py-10">
        <p class="text-dark-text">No providers configured.</p>
        <p class="mt-1 text-dark-text-muted">
          Add providers on the <a href="#/providers" class="text-dark-text-secondary underline underline-offset-4 decoration-dark-text-faint hover:text-dark-text">Providers</a> page first.
          {#if localProvidersAvailable}
            Or <button onclick={() => openWorkbench('providers')} class="text-dark-text-secondary underline underline-offset-4 decoration-dark-text-faint hover:text-dark-text">add a local provider</button> that this browser calls directly.
          {/if}
        </p>
      </div>
    {:else if messages.length === 0}
      <div class="px-[22px] py-10">
        <p class="text-dark-text">Send a message to start chatting.</p>
        <p class="mt-1 text-dark-text-muted">
          Using <span class="text-dark-text-secondary">{selectedModel}</span>{toolCount > 0 ? ` with ${toolCount} tool${toolCount !== 1 ? 's' : ''}` : ''}.
          <span class="hidden sm:inline">Press <kbd class="text-dark-text-secondary">ctrl+p</kbd> for commands.</span>
        </p>
      </div>
    {:else}
      {#if historyTruncated}
        <p class="mb-4 border-l border-[var(--oc-peach)] bg-dark-surface px-[22px] py-2 text-dark-text-muted">
          <button onclick={loadOlderHistory} disabled={loadingOlderHistory || streaming || saving} class="text-[var(--oc-peach)] underline underline-offset-4 disabled:opacity-40">{loadingOlderHistory ? 'loading older messages…' : 'load older messages'}</button>
          <span class="ml-[1ch]">— earlier context is included by the server when you send.</span>
        </p>
      {/if}
      {#each messages as msg, i}
        {#if msg.role === 'user'}
          <div class="group my-5 border-l-2 border-accent bg-dark-surface px-4 py-3 sm:px-[22px] sm:py-3.5">
            <div class="text-dark-text">
              <MessageContent message={msg} workspace={workspaceTransport.selected} formatSize={formatFileSize} />
            </div>
            <div class="mt-1.5 flex items-center gap-[2ch] text-dark-text-faint">
              {@render messageTime(i)}
              <span class="flex-1"></span>
              {@render copyAction(i)}
              {@render forkAction(i)}
              {#if !streaming}
                <button
                  onclick={() => retryFromIndex(i)}
                  disabled={saving}
                  aria-label="Retry from this message"
                  title="Retry from this message"
                  class="oc-link opacity-0 group-hover:opacity-100 group-has-[:focus-visible]:opacity-100 focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent"
                >retry</button>
              {/if}
            </div>
          </div>
        {:else if msg.role === 'assistant'}
          {@const finished = !(streaming && i === messages.length - 1)}
          {@const closesTurn = finished && !msg.tool_calls?.length && messages[i + 1]?.role !== 'assistant' && messages[i + 1]?.role !== 'tool'}
          {#if meta[i]?.compaction}
            <div class="my-5 flex items-center gap-[1ch] text-dark-text-faint" role="separator" aria-label="Conversation compacted">
              <span class="h-px flex-1 bg-dark-border"></span>
              <span class="text-[var(--oc-violet)]">compacted</span>
              <span>· earlier messages are no longer sent to the model</span>
              <span class="h-px flex-1 bg-dark-border"></span>
            </div>
          {/if}
          <div class={['group mb-4 min-w-0 px-4 sm:px-[22px]', meta[i]?.compaction ? 'border-l border-[var(--oc-violet)]' : '']}>
            {#if meta[i]?.compaction}
              <details class="text-dark-text-secondary">
                <summary class="cursor-pointer text-[var(--oc-violet)] focus-visible:outline-1 focus-visible:outline-accent">summary · {messageText(msg.content).length.toLocaleString()} chars</summary>
                <div class="mt-2 max-w-[110ch] text-dark-text">
                  <MessageContent message={msg} workspace={workspaceTransport.selected} raw={!!rawMessages[i]} formatSize={formatFileSize} />
                </div>
              </details>
            {:else if messageText(msg.content).trim() || typeof msg.content !== 'string' || !finished}
              <div class="max-w-[110ch] text-dark-text">
                <MessageContent message={msg} workspace={workspaceTransport.selected} raw={!!rawMessages[i]} thinking={!finished} formatSize={formatFileSize} />
              </div>
            {/if}
            <!-- Results remain attached to their originating call. -->
            {#if msg.tool_calls && msg.tool_calls.length > 0}
              <div class="my-2.5 min-w-0">
                {#each msg.tool_calls as tc}
                  {@const source = toolSourceMap[tc.function.name]}
                  {#if pendingQuestion && activeTool?.messageIndex === i && activeTool?.callID === tc.id && tc.function.name === 'question'}
                    {@render questionPrompt()}
                  {:else}
                    <ToolActivity
                      compact
                      call={tc}
                      result={toolResults.get(i)?.get(tc.id)}
                      running={activeTool?.messageIndex === i && activeTool?.callID === tc.id}
                      queued={activeTool?.messageIndex === i && activeTool?.callID !== tc.id}
                      source={source?.type === 'skill' ? 'Agent skill' : source?.type === 'mcpset' ? `MCP: ${source.mcpSetName}` : source?.type === 'builtin' ? 'Built-in' : source?.type === 'local' ? `This machine: ${localServers.find(s => s.id === source.localServerId)?.name ?? 'local MCP'}` : source?.type === 'extension' ? `Extension: ${extensions.find(e => e.id === source.extensionId)?.name ?? source.extensionId}` : source?.type === 'frontend' ? 'Chat' : ''}
                    />
                    {#if skillRunProgress[tc.id] && activeTool?.messageIndex === i && activeTool?.callID === tc.id}
                      <p role="status" class="ml-[2ch] truncate text-[var(--oc-peach)]">~ {skillRunProgress[tc.id]}</p>
                    {/if}
                  {/if}
                {/each}
              </div>
            {/if}
            <div class={['flex flex-wrap items-center gap-x-[1ch] text-dark-text-faint', closesTurn ? 'mt-2' : 'mt-0.5']}>
              {#if closesTurn}
                <span class="text-accent" aria-hidden="true">▣</span>
                <span class="text-dark-text">{activePresetName || 'Chat'}</span>
                <span aria-hidden="true">·</span>
                <span class="text-dark-text-muted">{@render modelBadge(i)}</span>
                {#if meta[i]?.created_at}<span aria-hidden="true">·</span>{/if}
              {/if}
              {#if closesTurn || meta[i]?.created_at}
                <span class={closesTurn ? '' : 'opacity-0 group-hover:opacity-100'}>{@render messageTime(i)}</span>
              {/if}
              <span class="ml-[1ch] flex gap-[2ch]">
                {@render copyAction(i)}
                {@render rawToggle(i)}
                {@render forkAction(i)}
              </span>
            </div>
          </div>
        {/if}
        <!-- Tool messages are rendered in the originating assistant's cards. -->
      {/each}
      {#if skillRunProgress[`wait-${turnTraceId}`]}
        <p role="status" class="px-4 text-[var(--oc-peach)] sm:px-[22px]">~ background skills: {skillRunProgress[`wait-${turnTraceId}`]}</p>
      {/if}
    {/if}
    </div>
  </div>

  <!-- Input area -->
  <div class="mx-auto w-full max-w-[1200px] shrink-0 px-3 pb-3 sm:px-8 sm:pb-3.5">
    <!-- Local MCP servers and browser extensions are each approved once, so the
         fact that a model can run something on this computer has to stay
         visible and revocable while it is true — not only at the moment it was
         granted. One strip, because the reader's question is "what can this
         page reach on my machine", not "which subsystem is it". -->
    {#if activeBrowserTools.length > 0}
      <div role="status" class="mb-0.5 flex flex-wrap items-center gap-x-[2ch] gap-y-1 border-l-2 border-[var(--oc-green)] bg-dark-surface px-4 py-1.5 text-dark-text-muted sm:px-[22px]">
        <span class="min-w-0 flex-1"><span class="text-[var(--oc-green)]">●</span> tools active on this device: <span class="text-dark-text-secondary">{activeBrowserTools.map(t => t.name).join(', ')}</span></span>
        {#each activeBrowserTools as active (active.key)}
          <button onclick={active.disable} class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent">disable {active.name}</button>
        {/each}
      </div>
    {/if}

    <!-- Media storage hint: one quiet, dismissible notice, never a toast per image -->
    {#if mediaStorageOff && !mediaHintDismissed}
      <div role="status" class="mb-0.5 flex items-start gap-[2ch] border-l-2 border-[var(--oc-peach)] bg-dark-surface px-4 py-1.5 text-dark-text-muted sm:px-[22px]">
        <span class="flex-1">
          Media storage is not configured, so attached images stay in this tab only and conversation history keeps a
          placeholder instead.
          <a href="#/settings/storage" class="text-[var(--oc-peach)] underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent">Configure media storage</a>
        </span>
        <button onclick={() => (mediaHintDismissed = true)} aria-label="Dismiss the media storage notice" class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent"><X size={13} /></button>
      </div>
    {/if}

    <!-- Messages waiting for the running turn -->
    {#if queuedMessages.length > 0}
      <div class="mb-0.5 border-l-2 border-[var(--oc-peach)] bg-dark-surface" aria-live="polite">
        <div class="flex flex-wrap items-center gap-x-[2ch] gap-y-1 px-4 pt-2 pb-1 sm:px-[22px]">
          <span class="text-[var(--oc-peach)]">{queuedMessages.length} queued</span>
          <span class="text-dark-text-muted">{streaming ? 'sent to the agent at its next step' : 'waiting — the last turn stopped'}</span>
          <span class="flex-1"></span>
          {#if !streaming}
            <button onclick={sendQueuedNow} disabled={!selectedModel || loadingTools} class="text-dark-text hover:underline underline-offset-4 disabled:opacity-40 focus-visible:outline-1 focus-visible:outline-accent">send now</button>
          {:else}
            <button onclick={interruptAndSend} class="text-dark-text hover:underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent" title="Stop the current step and continue with the queued messages now (Ctrl+Enter)">interrupt &amp; send</button>
          {/if}
          <button onclick={() => (queuedMessages = [])} class="oc-link focus-visible:outline-1 focus-visible:outline-accent">clear</button>
        </div>
        <ol class="max-h-40 overflow-y-auto pb-1.5">
          {#each queuedMessages as item, i (item.id)}
            <li class="flex items-center gap-[1ch] px-4 py-0.5 sm:px-[22px]">
              <span class="shrink-0 text-dark-text-faint">{i + 1}.</span>
              <span class="min-w-0 flex-1 truncate text-dark-text-secondary" title={item.text}>{queuedPreview(item)}</span>
              <button onclick={() => editQueued(item.id)} class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent" title="Move back to the composer">edit</button>
              <button onclick={() => removeQueued(item.id)} aria-label="Remove queued message" class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent"><X size={12} /></button>
            </li>
          {/each}
        </ol>
      </div>
    {/if}

    {#if commandSuggestions.length > 0}
      <div id="command-suggestions" role="listbox" aria-label="Commands" class="mb-0.5 max-h-[min(18rem,40dvh)] overflow-y-auto bg-dark-surface py-1.5">
        {#each commandSuggestions as c, index (c.scope + c.name)}
          <button
            id={`command-option-${index}`}
            role="option"
            aria-selected={index === commandIndex}
            aria-disabled={c.disabled}
            tabindex="-1"
            onmousedown={e => e.preventDefault()}
            onmousemove={() => (commandIndex = index)}
            onclick={() => pickCommand(c, true)}
            class={['flex w-full items-baseline gap-[2ch] px-4 py-[3px] text-left sm:px-[22px]', index === commandIndex ? 'bg-[var(--oc-peach)] text-[#1b1414]' : c.disabled ? 'text-dark-text-faint' : 'text-dark-text-secondary']}
          >
            <span class="shrink-0"><span class={index === commandIndex ? '' : 'text-dark-text'}>/{c.name}</span>{#if c.args}<span class={['ml-[1ch]', index === commandIndex ? '' : 'text-dark-text-faint']}>{c.args}</span>{/if}</span>
            <span class={['min-w-0 flex-1 truncate', index === commandIndex ? '' : 'text-dark-text-muted']}>{c.description}</span>
            {#if c.scope !== 'built-in'}<span class={['shrink-0', index === commandIndex ? '' : 'text-[var(--oc-violet)]']}>{c.scope}</span>{/if}
          </button>
        {/each}
        <p class="px-4 pt-1 text-dark-text-faint sm:px-[22px]">↑↓ select · enter run · tab fill · esc close</p>
      </div>
    {/if}

    <div class={['border-l-2 bg-dark-elevated px-4 pt-3 pb-2.5 sm:px-[22px] sm:pt-3.5', streaming ? 'border-[var(--oc-peach)]' : 'border-accent']}>
      <!-- Pending attachments -->
      {#if pendingImages.length > 0}
        <div class="mb-2.5 flex flex-wrap gap-2">
          {#each pendingImages as att, i}
            {@const refused = attachmentRefusal(att, acceptedInputs)}
            <div class={['group flex max-w-full items-center gap-[1ch] bg-dark-base py-1 pr-1 pl-2', refused ? 'text-[var(--oc-red)]' : 'text-dark-text-secondary']} title={refused || att.name}>
              {#if att.modality === 'image'}
                <img src={att.dataUrl} alt={att.name} class={['size-7 object-cover', refused ? 'opacity-60' : '']} />
              {:else if att.modality === 'audio'}
                <FileAudio size={14} class="shrink-0" />
              {:else if att.modality === 'video'}
                <FileVideo size={14} class="shrink-0" />
              {:else}
                <FileText size={14} class="shrink-0" />
              {/if}
              <span class="min-w-0 max-w-48 truncate">{att.name}</span>
              <span class="shrink-0 text-dark-text-muted">{formatFileSize(att.size)}</span>
              <button
                onclick={() => removeImage(i)}
                aria-label={`Remove ${att.name}`}
                title="Remove"
                class="oc-link shrink-0 p-0.5 focus-visible:outline-1 focus-visible:outline-accent"
              ><X size={12} /></button>
            </div>
          {/each}
        </div>
        {#if pendingRefusals.length > 0}
          <p role="alert" class="mb-2 text-[var(--oc-red)]">{pendingRefusals[0]} Remove it or choose another model.</p>
        {/if}
      {/if}

      <!-- Hidden file input -->
      <input
        bind:this={fileInput}
        type="file"
        accept={acceptAttribute(acceptedInputs)}
        multiple
        class="hidden"
        onchange={handleFilePick}
      />

      <textarea
        bind:this={composerInput}
        bind:value={userInput}
        use:growComposer={userInput}
        onkeydown={handleKeydown}
        onpaste={handlePaste}
        role="combobox"
        aria-expanded={commandSuggestions.length > 0}
        aria-controls="command-suggestions"
        aria-autocomplete="list"
        aria-activedescendant={commandSuggestions.length > 0 ? `command-option-${commandIndex}` : undefined}
        aria-label="Message"
        placeholder={models.length === 0 ? 'No models available' : compacting ? 'Compacting the conversation…' : streaming ? 'Queue a message for the agent…  (ctrl+enter interrupts)' : 'Write a message…  (/ for commands)'}
        disabled={models.length === 0}
        rows={1}
        class="block min-h-[1.65em] max-h-[min(16rem,35dvh)] w-full resize-none overflow-y-auto bg-transparent text-base leading-relaxed text-dark-text placeholder:text-dark-text-muted focus:outline-none disabled:text-dark-text-muted sm:text-sm"
      ></textarea>

      <!-- Status line: mode, model, provider and effort, each opens its picker. -->
      <div class="mt-2.5 flex min-w-0 flex-wrap items-center gap-x-[2ch] gap-y-1">
        <button
          onclick={() => (presets.length > 0 ? (palette = 'presets') : openWorkbench('prompt'))}
          title={presets.length > 0 ? 'Preset (shift+tab on an empty composer cycles)' : 'Save the current setup as a preset in the workbench'}
          class={['shrink-0 hover:underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent', streaming ? 'text-[var(--oc-peach)]' : 'text-accent']}
        >{activePresetName || 'Chat'}</button>
        <button
          onclick={() => (palette = 'models')}
          disabled={loading || models.length === 0}
          aria-label={`Model: ${selectedModel || 'none'}`}
          title="Model (ctrl+m)"
          class="min-w-0 truncate text-dark-text hover:underline underline-offset-4 decoration-dark-text-faint disabled:text-dark-text-muted focus-visible:outline-1 focus-visible:outline-accent"
        >{selectedModel ? modelLabel(selectedModel) : 'no model'}{#if selectedModel && !models.includes(selectedModel)}<span class="text-[var(--oc-red)]"> · unavailable</span>{/if}</button>
        {#if providerLabel(selectedModel)}
          <span class="hidden min-w-0 truncate text-dark-text-muted sm:inline">{providerLabel(selectedModel)}</span>
        {/if}
        {#if reasoningEffortOptions.length > 0}
          <button
            onclick={() => (palette = 'effort')}
            aria-label={`Reasoning effort: ${effectiveReasoningEffort || 'default'}`}
            title="Reasoning effort — models without reasoning may reject it (ctrl+t cycles)"
            class="shrink-0 text-dark-text-muted hover:text-dark-text hover:underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent"
          >{effectiveReasoningEffort || 'default'}</button>
        {/if}
        <span class="flex-1"></span>
        <button
          onclick={() => openWorkbench()}
          aria-label={`Workbench${toolCount > 0 ? ` (${toolCount} tools)` : ''}`}
          aria-expanded={showWorkbench}
          aria-haspopup="dialog"
          title="Workbench — system prompt, skills, presets and tools"
          class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent"
        >{toolCount > 0 ? `tools ${toolCount}` : 'tools'}{#if systemPrompt.trim()}<span class="text-accent" title="A system prompt is set"> •</span>{/if}</button>
        <button
          onclick={() => fileInput?.click()}
          disabled={models.length === 0}
          aria-label="Attach files"
          title={`Attach files — paste or drop works too. This model reads: ${acceptedInputs ? acceptedInputs.join(', ') : 'unknown (the provider decides)'}. Text files are sent as text to any model.`}
          class="oc-link shrink-0 disabled:opacity-40 focus-visible:outline-1 focus-visible:outline-accent"
        >+ attach</button>
        <VoiceInput compact contextKey={voiceContext} disabled={models.length === 0} bind:recording={chatRecording} bind:transcribing={chatTranscribing} ontext={text => { userInput = (userInput ? userInput + ' ' : '') + text; }} />
        {#if streaming}
          <button
            onclick={sendMessage}
            disabled={(!userInput.trim() && pendingImages.length === 0) || chatRecording || chatTranscribing}
            title="Queue (Enter) — delivered to the agent at its next step. Ctrl+Enter interrupts and sends now."
            aria-label="Queue message"
            class="shrink-0 text-dark-text hover:underline underline-offset-4 disabled:text-dark-text-faint disabled:no-underline focus-visible:outline-1 focus-visible:outline-accent"
          >queue</button>
          <button
            onclick={stopStreaming}
            title="Stop (Esc) — queued messages are kept"
            aria-label="Stop response"
            class="shrink-0 text-[var(--oc-red)] hover:underline underline-offset-4 focus-visible:outline-1 focus-visible:outline-accent"
          >■ stop</button>
        {:else}
          <button
            onclick={sendMessage}
            disabled={(!userInput.trim() && pendingImages.length === 0) || !selectedModel || models.length === 0 || chatRecording || chatTranscribing || loadingTools}
            title="Send (Enter) — Shift+Enter for a new line"
            aria-label="Send message"
            class="shrink-0 text-accent hover:underline underline-offset-4 disabled:text-dark-text-faint disabled:no-underline focus-visible:outline-1 focus-visible:outline-accent"
          >send ↵</button>
        {/if}
      </div>
    </div>

    <!-- Key hints, as in a terminal agent's footer. -->
    <div class="hidden justify-between gap-6 px-0.5 pt-2.5 whitespace-nowrap text-dark-text-muted sm:flex">
      <div class="flex gap-[2ch]">
        {#if streaming}
          <span class="text-[var(--oc-peach)]">working…</span>
          <span><kbd class="text-dark-text">esc</kbd> interrupt</span>
        {:else}
          <span><kbd class="text-dark-text">enter</kbd> send</span>
          <span class="hidden md:inline"><kbd class="text-dark-text">shift+enter</kbd> newline</span>
        {/if}
      </div>
      <div class="flex gap-[2ch]">
        {#if reasoningEffortOptions.length > 0}<span class="hidden lg:inline"><kbd class="text-dark-text">ctrl+t</kbd> effort</span>{/if}
        {#if presets.length > 0}<span class="hidden lg:inline"><kbd class="text-dark-text">shift+tab</kbd> presets</span>{/if}
        <span><kbd class="text-dark-text">ctrl+m</kbd> model</span>
        <span><kbd class="text-dark-text">ctrl+p</kbd> commands</span>
      </div>
    </div>
  </div>

  {#snippet questionPrompt()}
    {#if pendingQuestion}
      <details open class="my-2 w-full border-l-2 border-[var(--oc-violet)] bg-dark-surface">
        <summary class="cursor-pointer px-4 py-3 hover:bg-dark-elevated focus-visible:outline-1 focus-visible:outline-accent">
          <span class="inline-flex items-center gap-2">
            <MessageCircleQuestion size={15} class="shrink-0 text-[var(--oc-violet)]" />
            {#if pendingQuestion.header}
              <span class="text-sm font-medium text-dark-text">{pendingQuestion.header}</span>
            {:else}
              <span class="text-sm font-medium text-dark-text">Question</span>
            {/if}
          </span>
          <span class="block mt-2 text-sm text-dark-text-secondary whitespace-pre-wrap break-words">{pendingQuestion.question}</span>
          <span class="block mt-1 text-xs text-dark-text-muted">Waiting for your answer · Click to answer</span>
        </summary>
        <div class="px-4 py-3">
          {#if pendingQuestion.multiple}<p class="mb-3 text-xs text-dark-text-muted">Select one or more options, then submit.</p>{/if}
          <div class="space-y-1.5">
            {#each pendingQuestion.options as opt}
              <button
                aria-pressed={pendingQuestion.multiple ? questionSelections.includes(opt.label) : undefined}
                onclick={() => { const q = pendingQuestion; if (!q) return; if (q.multiple) { questionSelections = questionSelections.includes(opt.label) ? questionSelections.filter(label => label !== opt.label) : [...questionSelections, opt.label]; } else { pendingQuestion = null; q.resolve(opt.label); } }}
                class="w-full text-left px-3 py-2 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary"
              >
                <div class="font-medium">{opt.label}{#if pendingQuestion.multiple && questionSelections.includes(opt.label)}<span class="ml-2 text-xs">Selected</span>{/if}</div>
                {#if opt.description}
                  <div class="text-xs text-dark-text-muted mt-0.5">{opt.description}</div>
                {/if}
              </button>
            {/each}
            {#if pendingQuestion.multiple}
              <button
                disabled={questionSelections.length === 0}
                onclick={() => { const q = pendingQuestion; if (q && questionSelections.length) { pendingQuestion = null; q.resolve(questionSelections.join(', ')); } }}
                class="px-3 py-2 text-sm bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >Submit selected answers</button>
            {/if}
            {#if pendingQuestion.custom !== false}
              <div class="pt-1.5">
                <form
                  onsubmit={(e) => { e.preventDefault(); const input = (e.target as HTMLFormElement).elements.namedItem('custom_answer') as HTMLInputElement; const val = input?.value?.trim(); if (val && pendingQuestion) { const q = pendingQuestion; pendingQuestion = null; q.resolve(val); } }}
                  class="flex gap-2"
                >
                  <input
                    name="custom_answer"
                    aria-label="Your answer"
                    type="text"
                    placeholder="Type your own answer..."
                    class="min-w-0 flex-1 border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/10 focus:border-dark-border-subtle"
                  />
                  <button
                    type="submit"
                    class="px-3 py-1.5 text-sm bg-accent text-dark-base hover:bg-accent-hover"
                  >
                    Submit
                  </button>
                </form>
              </div>
            {/if}
          </div>
        </div>
      </details>
    {/if}
  {/snippet}
</div>

  {#if showSessionPanel}
    <!-- Session sidebar: what this conversation runs with and has produced. -->
    <aside aria-label="Session" class="fixed inset-y-0 right-0 z-40 w-72 shrink-0 overflow-y-auto border-l border-dark-border bg-dark-base px-5 py-5 shadow-[-12px_0_40px_-8px_rgb(0_0_0/0.8)] xl:static xl:z-auto xl:shadow-none">
      <div class="mb-6 flex items-baseline justify-between xl:hidden">
        <span class="font-bold text-dark-text">Session</span>
        <button onclick={() => (showSessionPanel = false)} aria-label="Close session sidebar" class="oc-link focus-visible:outline-1 focus-visible:outline-accent">esc</button>
      </div>

      <section class="mb-6">
        <h2 class="mb-1.5 font-bold text-dark-text">Context</h2>
        <div class="flex justify-between text-dark-text-muted"><span>prompt</span><span class="tabular-nums text-dark-text-secondary">{contextTokens.toLocaleString()}</span></div>
        <div class="flex justify-between text-dark-text-muted"><span>completion</span><span class="tabular-nums text-dark-text-secondary">{completionTokens.toLocaleString()}</span></div>
        <div class="flex justify-between text-dark-text-muted"><span>total</span><span class="tabular-nums text-dark-text-secondary">{totalTokens.toLocaleString()}</span></div>
        <div
          class="mt-1.5 flex justify-between border-t border-dark-border pt-1.5 text-dark-text-muted"
          title={conversationCost.unpriced > 0
            ? `${conversationCost.unpriced} call${conversationCost.unpriced === 1 ? '' : 's'} used a model without a price; set prices on the Pricing page.`
            : 'Cost of the model calls in this conversation, from installation pricing.'}
        >
          <span>cost</span>
          <span class="tabular-nums">
            {#if conversationCost.priced > 0}<span class="text-[var(--oc-peach)]">{formatCost(conversationCost.cents)}</span>{:else}<span class="text-dark-text-faint">—</span>{/if}
            {#if conversationCost.unpriced > 0}<span class="text-dark-text-faint"> +{conversationCost.unpriced} unpriced</span>{/if}
          </span>
        </div>
        {#if historyTruncated}<p class="mt-1 text-dark-text-faint">cost covers loaded messages</p>{/if}
      </section>

      <section class="mb-6">
        <div class="mb-1.5 flex items-baseline justify-between">
          <h2 class="font-bold text-dark-text">Workbench</h2>
          <button onclick={() => openWorkbench()} class="oc-link focus-visible:outline-1 focus-visible:outline-accent">edit</button>
        </div>
        <ul class="space-y-0.5 text-dark-text-muted">
          <li class="flex gap-[1ch]"><span class={systemPrompt.trim() ? 'text-[var(--oc-green)]' : 'text-dark-text-faint'}>{systemPrompt.trim() ? '●' : '○'}</span>system prompt</li>
          {#each selectedSkillNames as name (name)}
            <li class="flex min-w-0 gap-[1ch]"><span class="text-[var(--oc-green)]">●</span><span class="truncate">{name} <span class="text-dark-text-faint">skill</span></span></li>
          {/each}
          {#each selectedMCPSetNames as name (name)}
            <li class="flex min-w-0 gap-[1ch]"><span class="text-[var(--oc-green)]">●</span><span class="truncate">{name} <span class="text-dark-text-faint">mcp</span></span></li>
          {/each}
          {#each activeBrowserTools as active (active.key)}
            <li class="flex min-w-0 gap-[1ch]"><span class="text-[var(--oc-green)]">●</span><span class="truncate">{active.name} <span class="text-dark-text-faint">this device</span></span></li>
          {/each}
          <li class="flex gap-[1ch]"><span class={toolCount > 0 ? 'text-[var(--oc-green)]' : 'text-dark-text-faint'}>{toolCount > 0 ? '●' : '○'}</span><span><span class="tabular-nums">{toolCount}</span> tool{toolCount === 1 ? '' : 's'} available</span></li>
        </ul>
      </section>

      {#if todos.length > 0}
        <section class="mb-6">
          <h2 class="mb-1.5 font-bold text-dark-text">Todo <span class="font-normal text-dark-text-muted tabular-nums">{todos.filter(t => t.status === 'completed').length}/{todos.length}</span></h2>
          <ul class="space-y-0.5">
            {#each todos as todo}
              <li class={['flex gap-[1ch]', todo.status === 'completed' || todo.status === 'cancelled' ? 'text-dark-text-faint line-through' : todo.status === 'in_progress' ? 'text-[var(--oc-peach)]' : 'text-dark-text-secondary']}>
                <span class="shrink-0">{todo.status === 'completed' ? '[✓]' : todo.status === 'in_progress' ? '[•]' : todo.status === 'cancelled' ? '[×]' : '[ ]'}</span>
                <span class="min-w-0">{todo.content}{#if todo.priority === 'high' && todo.status !== 'completed' && todo.status !== 'cancelled'}<span class="text-[var(--oc-red)] no-underline"> !</span>{/if}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      <section class="mb-6">
          <h2 class="mb-1.5 font-bold text-dark-text">Conversation</h2>
          <div class="flex flex-col items-start gap-0.5">
            {#if sharingAvailable && conversationId && shareBoundaries.length > 0}
              <button onclick={() => (showShareDialog = true)} aria-haspopup="dialog" class="oc-link focus-visible:outline-1 focus-visible:outline-accent">share snapshot</button>
            {/if}
            <button
              onclick={requestClear}
              onblur={() => (confirmClear = false)}
              disabled={streaming || saving || (messages.length === 0 && !systemPrompt && pendingImages.length === 0)}
              aria-label={confirmClear ? 'Confirm clearing the transcript' : 'Clear transcript'}
              title="Clear transcript (deletes saved messages)"
              class={['focus-visible:outline-1 focus-visible:outline-accent disabled:opacity-40', confirmClear ? 'text-[var(--oc-red)]' : 'oc-link']}
            >{confirmClear ? (conversationId ? 'confirm: delete saved messages?' : 'confirm: clear?') : 'clear transcript'}</button>
          </div>
      </section>
    </aside>
    <button class="fixed inset-0 z-30 bg-black/50 xl:hidden" aria-label="Close session sidebar" onclick={() => (showSessionPanel = false)}></button>
  {/if}

  {#if palette}
    <CommandPalette title={paletteTitle} groups={paletteGroups} placeholder={palette === 'models' ? 'Search models' : 'Search'} onclose={closePalette} />
  {/if}
</div>

<!-- Approval for a local MCP server.
     Approval is per device and deliberately not part of the stored record:
     the same loopback URL is a different program on a laptop and a desktop,
     so a synced approval would authorize, here, something inspected there.
     The tools are listed first because a name and a URL are not enough to
     decide with — after this, the model calls them without asking again. -->
{#if localApprovalFor}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-lg border border-dark-border bg-dark-surface">
      <div class="px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text">Enable {localApprovalFor.name} on this device</h2>
        <p class="mt-0.5 text-dark-text-muted">
          Your browser will call <code class="font-mono">{localApprovalFor.url}</code> on this computer. The model chooses the arguments, and after this it runs these tools without asking again.
        </p>
      </div>
      <div class="p-4 space-y-3 max-h-80 overflow-y-auto">
        {#if localStatus[localApprovalFor.id]?.busy}
          <p class="text-xs text-dark-text-muted">Connecting…</p>
        {:else if localStatus[localApprovalFor.id]?.error}
          <p class="text-xs text-red-400">{localStatus[localApprovalFor.id].error}</p>
          {#if localStatus[localApprovalFor.id].hint}
            <p class="text-dark-text-muted">{localStatus[localApprovalFor.id].hint}</p>
          {/if}
        {:else if localApprovalTools.length === 0}
          <p class="text-xs text-dark-text-muted">This server advertises no tools.</p>
        {:else}
          <p class="text-xs text-dark-text-secondary">{localApprovalTools.length} tool{localApprovalTools.length === 1 ? '' : 's'}:</p>
          <ul class="space-y-1.5">
            {#each localApprovalTools as tool}
              <li class="border border-dark-border-subtle px-2.5 py-1.5">
                <code class="font-mono text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-0.5 text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="px-4 py-3 border-t border-dark-border flex items-center gap-2">
        <button
          onclick={confirmLocalApproval}
          disabled={localApprovalTools.length === 0}
          class="px-3 py-1.5 text-xs border border-accent bg-accent text-dark-base disabled:opacity-50"
        >
          Enable on this device
        </button>
        <button
          onclick={() => { localApprovalFor = null; }}
          class="px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-secondary"
        >
          Cancel
        </button>
        <button
          onclick={() => localApprovalFor && beginLocalApproval(localApprovalFor)}
          class="ml-auto px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-muted"
        >
          Retry
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Approval for a browser extension.
     Per device for the same reason the local-MCP one is: the person installed
     this extension on this machine, and an approval carried to another one
     would authorize something they never inspected there. The tools are listed
     first because a name is not enough to decide with — after this, the model
     calls them without asking again, with arguments the reader never sees. -->
{#if extensionApprovalTarget}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-lg border border-dark-border bg-dark-surface">
      <div class="px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text">Enable {extensionApprovalTarget.name} on this device</h2>
        <p class="mt-0.5 text-dark-text-muted">
          This extension runs in your browser with the access you granted it when you installed it. The model chooses the arguments, and after this it calls these tools without asking again.
        </p>
      </div>
      <div class="p-4 space-y-3 max-h-80 overflow-y-auto">
        {#if extensionStatus[extensionApprovalTarget.id]?.busy}
          <p class="text-xs text-dark-text-muted">Asking the extension…</p>
        {:else if extensionStatus[extensionApprovalTarget.id]?.error}
          <p class="text-xs text-red-400">{extensionStatus[extensionApprovalTarget.id].error}</p>
        {:else if extensionApprovalTools.length === 0}
          <p class="text-xs text-dark-text-muted">
            {extensionApprovalTarget.notice || 'This extension offers no tools right now.'}
          </p>
        {:else}
          <p class="text-xs text-dark-text-secondary">{extensionApprovalTools.length} tool{extensionApprovalTools.length === 1 ? '' : 's'}:</p>
          <ul class="space-y-1.5">
            {#each extensionApprovalTools as tool}
              <li class="border border-dark-border-subtle px-2.5 py-1.5">
                <code class="font-mono text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-0.5 text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="px-4 py-3 border-t border-dark-border flex items-center gap-2">
        <button
          onclick={confirmExtensionApproval}
          disabled={extensionApprovalTools.length === 0}
          class="px-3 py-1.5 text-xs border border-accent bg-accent text-dark-base disabled:opacity-50"
        >
          Enable on this device
        </button>
        <button
          onclick={() => { extensionApprovalTarget = null; }}
          class="px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-secondary"
        >
          Cancel
        </button>
        <button
          onclick={() => extensionApprovalTarget && beginExtensionApproval(extensionApprovalTarget)}
          class="ml-auto px-3 py-1.5 text-xs border border-dark-border-subtle text-dark-text-muted"
        >
          Retry
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Connected extension tool details. Unlike the approval dialog, this is
     informational: opening it never changes device approval. -->
{#if extensionInspectorTarget}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4"
    onclick={(e) => { if (e.target === e.currentTarget) extensionInspectorTarget = null; }}
  >
    <div
      bind:this={extensionInspectorPanel}
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-labelledby="extension-tools-title"
      onkeydown={(e) => { if (e.key === 'Escape') extensionInspectorTarget = null; }}
      class="flex w-full max-w-lg max-h-[80vh] flex-col border border-dark-border bg-dark-surface"
    >
      <div class="flex items-start justify-between gap-3 px-4 py-3 border-b border-dark-border">
        <div class="min-w-0">
          <h2 id="extension-tools-title" class="text-sm font-medium text-dark-text">{extensionInspectorTarget.name} tools</h2>
          <p class="mt-0.5 text-dark-text-muted">Tools currently connected to this chat through the browser extension.</p>
        </div>
        <button
          onclick={() => (extensionInspectorTarget = null)}
          aria-label="Close extension tools"
          class="shrink-0 p-1 text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent"
        ><X size={16} /></button>
      </div>
      <div class="flex-1 overflow-y-auto p-4">
        {#if extensionStatus[extensionInspectorTarget.id]?.busy}
          <div class="flex items-center gap-2 text-xs text-dark-text-muted"><Loader2 size={13} class="animate-spin" /> Asking the extension…</div>
        {:else if extensionStatus[extensionInspectorTarget.id]?.error}
          <div class="space-y-2">
            <p class="text-xs text-red-400">{extensionStatus[extensionInspectorTarget.id].error}</p>
            <button onclick={() => extensionInspectorTarget && inspectExtensionTools(extensionInspectorTarget)} class="px-2.5 py-1 text-xs border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Retry</button>
          </div>
        {:else if extensionInspectorTools.length === 0}
          <p class="text-xs text-dark-text-muted">{extensionInspectorTarget.notice || 'This extension offers no tools right now.'}</p>
        {:else}
          <div class="mb-2 flex items-center justify-between gap-3">
            <p class="text-xs text-dark-text-secondary">{extensionInspectorTools.length} connected tool{extensionInspectorTools.length === 1 ? '' : 's'}</p>
            <button onclick={() => extensionInspectorTarget && inspectExtensionTools(extensionInspectorTarget)} class="px-2 py-0.5 border border-dark-border-subtle text-dark-text-muted hover:bg-dark-elevated">Refresh</button>
          </div>
          <ul class="divide-y divide-dark-border border border-dark-border-subtle">
            {#each extensionInspectorTools as tool}
              <li class="px-3 py-2.5">
                <code class="font-mono text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-1 leading-relaxed text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="shrink-0 flex justify-end px-4 py-3 border-t border-dark-border">
        <button onclick={() => (extensionInspectorTarget = null)} class="px-3 py-1.5 text-xs bg-accent text-dark-base hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-accent">Done</button>
      </div>
    </div>
  </div>
{/if}


<!-- Markdown typography is provided globally via `.markdown-body` rules in
     src/style/global.css. No component-local overrides needed. -->
