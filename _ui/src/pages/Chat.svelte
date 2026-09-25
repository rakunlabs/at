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
    getTextContent,
    mergeDeltaContent,
    streamChatCompletion,
  } from '@/lib/helper/chat';
  import { listBuiltinTools, callBuiltinTool, type BuiltinToolDef } from '@/lib/api/mcp';
  import BuiltinToolPicker from '@/lib/components/BuiltinToolPicker.svelte';
  import { builtinDisabledBy } from '@/lib/helper/builtin-tools';
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
    REDACTED,
    approvalFor,
    approveLocalMCP,
    clipLocalToolResult,
    localMCPToolName,
    localMCPUrlProblem,
    revokeLocalMCP,
    toolsAddedSinceApproval,
  } from '@/lib/helper/local-mcp';
  import { LocalMCPClient, type LocalMCPTool } from '@/lib/helper/local-mcp-client';
  import {
    CAPABILITY_TOOLS,
    ExtensionBridge,
    approveExtension,
    extensionApprovalFor,
    revokeExtension,
    type ExtensionDescriptor,
    type ExtensionTool,
  } from '@/lib/helper/extension-bridge';
  import { FEATURE_CHAT_EXTENSIONS, FEATURE_CHAT_LOCAL_MCP, FEATURE_CHAT_SHARING } from '@/lib/api/features';
  import {
    type PlaygroundConversation,
    type PlaygroundConversationInput,
    type PlaygroundDefaults,
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
    MEDIA_ALLOWED_LABEL,
    MEDIA_MAX_UPLOAD_BYTES,
    dataUrlToBlob,
    getMediaDataURL,
    isMediaStorageDisabled,
    mediaImageURL,
    mediaUploadErrorMessage,
    uploadMedia,
  } from '@/lib/api/media';
  import ConversationList from '@/lib/components/playground/ConversationList.svelte';
  import ShareDialog from '@/lib/components/playground/ShareDialog.svelte';
  import { Send, Trash2, ChevronDown, Square, ImagePlus, X, RotateCcw, Wrench, Plus, Loader2, ListChecks, MessageCircleQuestion, PanelLeft, GitBranch, CloudOff, ImageOff, Share2 } from 'lucide-svelte';
  import { onDestroy, untrack } from 'svelte';
  import { push } from 'svelte-spa-router';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import Markdown from '@/lib/components/Markdown.svelte';

  storeNavbar.title = 'Chats';

  // svelte-spa-router yields `{id: null}` for the bare `/chats` route.
  let { params = {} }: { params?: { id?: string | null } } = $props();

  // ─── Types ───

  interface PendingImage {
    name: string;
    dataUrl: string;
    /** Over the 16 MB media-storage cap: sent to the model, not saved to history. */
    oversize?: boolean;
  }

  /** Maps a tool name to its source for dispatch. */
  interface ToolSource {
    type: 'mcp' | 'builtin' | 'frontend' | 'mcpset' | 'local' | 'extension';
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

  type WorkbenchTab = 'prompt' | 'skills' | 'tools' | 'chat';

  /**
   * Durable-history bookkeeping kept strictly parallel to `messages`: index `i`
   * of `meta` always describes `messages[i]`. `sequence` is the single source of
   * truth for "already persisted" — `null` means the message exists only in the
   * browser, a number is the gapless sequence the store assigned. Every append
   * only sends the `null` ones, so a retry after a failed save can never
   * duplicate a row.
   */
  interface MessageMeta {
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
    /** Original file names of attached images, consumed when stripping. */
    imageNames: string[];
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
  let modelGroups = $state<Array<{ label: string; models: string[] }>>([]);
  let selectedModel = $state('');
  let systemPrompt = $state('');
  let userInput = $state('');
  let activeTool = $state<{ messageIndex: number; callID: string } | null>(null);
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
  let pendingImages = $state<PendingImage[]>([]);
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
  const workbenchTabs: Array<{ id: WorkbenchTab; label: string }> = [
    { id: 'prompt', label: 'System prompt' },
    { id: 'skills', label: 'Skills' },
    { id: 'tools', label: 'Server tools' },
    { id: 'chat', label: 'Chat tools' },
  ];

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
  let defaultsTimer: ReturnType<typeof setTimeout> | null = null;

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
  let presetDraftName = $state('');
  let presetSaveScope = $state<'personal' | 'workspace'>('personal');
  let presetSaving = $state(false);

  // Built-in server tools
  let builtinTools = $state<BuiltinToolDef[]>([]);
  let enabledBuiltinTools = $state<string[]>([]);


  // Frontend-only tools
  let enabledFrontendTools = $state<string[]>([...FRONTEND_TOOL_NAMES]);

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
   * Editor state for the registry, which lives in the tools panel.
   *
   * `localEditorOpen` is explicit rather than derived from whether the draft
   * fields have content: a new record starts with every field empty, so
   * inferring it left the Add action with no visible effect.
   */
  let localEditorOpen = $state(false);
  let localDraftId = $state('');
  let localDraftName = $state('');
  let localDraftUrl = $state('');
  let localDraftHeaderKey = $state('');
  let localDraftHeaderValue = $state('');
  let localDraftHeaders = $state<Record<string, string>>({});
  let localDraftError = $state('');
  let localSaving = $state(false);
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
  let turnSessionId = $state('');

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
        void refreshTools();

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
      void refreshTools();
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
      untrack(() => { void refreshTools(); });

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
    void refreshTools();
  }

  /** One action, effective immediately: the next turn offers nothing from it. */
  function disableExtension(ext: ExtensionDescriptor) {
    revokeExtension(ext.id, localStorageSafe());
    refreshExtensionApprovals();
    if (extensionInspectorTarget?.id === ext.id) extensionInspectorTarget = null;
    void refreshTools();
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
  let showConversations = $state(true);
  let historyLoading = $state(false);
  let historyTruncated = $state(false);
  let saving = $state(false);
  let saveQueued = false;
  let confirmClear = $state(false);

  // ─── Media storage ───

  /**
   * `media_id` → the re-inlined data URI. A restored transcript only carries
   * ids, but every provider needs image content in the request body, so the
   * bytes are fetched once and reused for the rest of the page session. Media
   * objects are immutable server-side, so an entry can never go stale; a failed
   * fetch is cached as `''` so a deleted object is not refetched on every
   * message of every turn. A page reload starts from empty.
   */
  const mediaDataUrls = new Map<string, Promise<string>>();

  /**
   * Inline data URI → the media id it was stored as. A save retry after a
   * partial failure then re-uses the existing object instead of uploading the
   * same bytes twice.
   */
  const uploadedMedia = new Map<string, string>();

  /** Set once a 503 proves storage is off: one quiet hint, never a toast per image. */
  let mediaStorageOff = $state(false);
  let mediaHintDismissed = $state(false);

  /** Bytes for a stored image, fetched lazily and cached per id. */
  function mediaDataUrl(id: string): Promise<string> {
    let pending = mediaDataUrls.get(id);
    if (!pending) {
      pending = getMediaDataURL(id).catch(() => '');
      mediaDataUrls.set(id, pending);
    }
    return pending;
  }

  /**
   * Upload one attachment for persistence. Returns `''` when it could not be
   * stored, which the caller turns into the omitted descriptor so the turn
   * still persists. 413 and 415 are named per image; 503 only raises the
   * one-time hint, because a disabled backend is a configuration fact, not a
   * per-image error worth repeating.
   */
  async function uploadAttachment(dataUrl: string, name: string): Promise<string> {
    const existing = uploadedMedia.get(dataUrl);
    if (existing) return existing;
    try {
      const object = await uploadMedia(dataUrlToBlob(dataUrl), name);
      uploadedMedia.set(dataUrl, object.id);
      // The bytes are already inline in this tab: skip the round trip later.
      mediaDataUrls.set(object.id, Promise.resolve(dataUrl));
      return object.id;
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

  /** Last state successfully written to the conversation row, for diffing. */
  let savedSettings: { system_prompt: string; provider_key: string; model: string; config: string } | null = null;
  let settingsTimer: ReturnType<typeof setTimeout> | null = null;
  let confirmClearTimer: ReturnType<typeof setTimeout> | null = null;
  /** Mirrors the id currently reflected in the hash route. */
  let routedId = '';

  const HISTORY_MAX_PAGES = 10;
  const HISTORY_PAGE_SIZE = 200;

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
    return {
      // Preserved, not offered: a conversation saved before direct MCP URLs
      // were removed keeps its record instead of having it rewritten away.
      mcp_urls: [...legacyMcpUrls],
      mcp_headers: { ...legacyMcpHeaders },
      mcp_sets: [...selectedMCPSetNames],
      skills: [...selectedSkillNames],
      builtin_tools: [...enabledBuiltinTools],
      frontend_tools: [...enabledFrontendTools],
    };
  }

  function applyConfig(config: Record<string, unknown> | null | undefined) {
    const c = config ?? {};
    const names = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
    legacyMcpUrls = names(c.mcp_urls);
    selectedMCPSetNames = names(c.mcp_sets);
    selectedSkillNames = names(c.skills);
    enabledBuiltinTools = names(c.builtin_tools);
    enabledFrontendTools = names(c.frontend_tools);
    const headers = c.mcp_headers;
    legacyMcpHeaders = headers && typeof headers === 'object' && !Array.isArray(headers)
      ? Object.fromEntries(Object.entries(headers as Record<string, unknown>).filter((e): e is [string, string] => typeof e[1] === 'string'))
      : {};
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
      if (!updated || conversationId !== id) return;
      savedSettings = next;
      conversation = updated;
      mergeConversation(updated);
    } catch (e) {
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
    voiceContext++;
    if (abortController) { abortController.abort(); abortController = null; }
    streaming = false;
    messages = [];
    meta = [];
    systemPrompt = '';
    pendingImages = [];
    todos = [];
    pendingQuestion = null;
    contextTokens = 0;
    completionTokens = 0;
    totalTokens = 0;
    confirmClear = false;
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
    if (settingsTimer) { clearTimeout(settingsTimer); settingsTimer = null; }
    resetBuffer();
    conversationId = id;
    scratchSessionId = '';
    conversation = null;
    parentTitle = '';
    historyTruncated = false;
    savedSettings = null;
    if (!id) return;

    historyLoading = true;
    try {
      const c = await getPlaygroundConversation(id);
      if (conversationId !== id) return;
      conversation = c;
      systemPrompt = c.system_prompt || '';
      const pair = joinModel(c.provider_key || '', c.model || '');
      if (pair) selectedModel = pair;
      applyConfig(c.config);
      savedSettings = settingsSnapshot();
      mergeConversation(c);

      // Load the WHOLE transcript. A partial one would silently truncate the
      // context sent upstream on the next turn, which is worse than slow.
      const loaded: PlaygroundMessage[] = [];
      let cursor = '';
      let pages = 0;
      while (pages < HISTORY_MAX_PAGES) {
        const res = await listPlaygroundMessages(id, { limit: HISTORY_PAGE_SIZE, before: cursor || undefined });
        if (conversationId !== id) return;
        loaded.unshift(...(res.data ?? []));
        cursor = res.meta?.next_before ?? '';
        pages += 1;
        if (!cursor) break;
      }
      historyTruncated = !!cursor;

      messages = loaded.map(toChatMessage);
      meta = loaded.map(m => ({ sequence: m.sequence, provider_key: m.provider_key, model: m.model, created_at: m.created_at, imageNames: [] }));
      if (c.forked_from_id) void loadParentTitle(c.forked_from_id);
      void refreshTools();
      scrollToBottom(true);
    } catch (e) {
      if (conversationId !== id) return;
      addToast(playgroundErrorMessage(e, 'Failed to open conversation'), 'alert');
    } finally {
      if (conversationId === id) historyLoading = false;
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
    const { provider_key, model } = splitModel(selectedModel);
    const created = await createPlaygroundConversation({
      title: playgroundTitleFrom(seedTitle),
      system_prompt: systemPrompt,
      provider_key,
      model,
      config: currentConfig(),
    });
    conversationId = created.id;
    // Claim the route before pushing so the router effect does not reload and
    // discard the in-flight turn.
    routedId = created.id;
    conversation = created;
    savedSettings = settingsSnapshot();
    mergeConversation(created);
    push(playgroundRoute(created.id));
    return created.id;
  }

  /**
   * Append every message this turn produced in one batched call. Failures are
   * non-destructive: the transcript stays in memory with `sequence === null`,
   * so the toolbar retry re-sends exactly the same, still-unsaved messages.
   */
  async function persistPending() {
    const id = conversationId;
    if (!id) return;
    // A second turn can finish while the first batch is still in flight; queue
    // instead of dropping it, so nothing silently stays unsaved.
    if (saving) { saveQueued = true; return; }
    const indexes: number[] = [];
    for (let i = 0; i < messages.length; i += 1) {
      if (meta[i]?.sequence === null && messages[i].role !== 'system') indexes.push(i);
    }
    if (indexes.length === 0) return;

    saving = true;
    try {
      // An inline data-URI is never stored in `data`: it goes to media storage
      // and leaves a `media_id` behind, or degrades to a visible descriptor.
      const batch: PlaygroundMessageInput[] = [];
      for (const i of indexes) {
        batch.push({
          role: messages[i].role as PlaygroundRole,
          provider_key: meta[i].provider_key,
          model: meta[i].model,
          data: await persistPlaygroundImages(toMessageData(messages[i]), meta[i].imageNames, uploadAttachment),
        });
      }

      for (let offset = 0; offset < batch.length; offset += PLAYGROUND_MESSAGE_BATCH_MAX) {
        const stored = await appendPlaygroundMessages(id, batch.slice(offset, offset + PLAYGROUND_MESSAGE_BATCH_MAX));
        if (conversationId !== id) return;
        stored.forEach((s, k) => {
          const index = indexes[offset + k];
          // Adopt the stored stamp over the optimistic one: the transcript
          // must not change what it says about a message after a reload.
          if (meta[index]) meta[index] = { ...meta[index], sequence: s.sequence, created_at: s.created_at || meta[index].created_at };
        });
      }
      // Keep the local row fresh without changing its creation-time position.
      const current = conversations.find(c => c.id === id);
      if (current) mergeConversation({ ...current, updated_at: new Date().toISOString() });
    } catch (e) {
      addToast(playgroundErrorMessage(e, 'Failed to save messages. They are kept in the transcript — retry from the toolbar.'), 'alert');
      saveQueued = false;
    } finally {
      saving = false;
      if (saveQueued) { saveQueued = false; void persistPending(); }
    }
  }

  /** Keep the first `keep` messages, and drop the stored tail to match. */
  async function truncateFrom(keep: number) {
    const removed = meta.slice(keep).map(m => m.sequence).filter((s): s is number => s !== null);
    messages = messages.slice(0, keep);
    meta = meta.slice(0, keep);
    if (!conversationId || removed.length === 0) return;
    try {
      await truncatePlaygroundMessages(conversationId, Math.min(...removed));
    } catch (e) {
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
      for (const p of info.providers ?? []) {
        const reference = p.reference || p.key;
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
      modelGroups = groups.sort((a, b) => a.label.localeCompare(b.label));
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
  async function loadDefaults() {
    try {
      const prefs = await getPlaygroundDefaults();
      defaultsLoaded = true;
      if (conversationId || params.id) return;
      if (prefs.model && models.includes(prefs.model)) selectedModel = prefs.model;
      if (prefs.system_prompt && !systemPrompt.trim()) systemPrompt = prefs.system_prompt;
      if (prefs.mcp_sets?.length) selectedMCPSetNames = [...prefs.mcp_sets];
      if (prefs.skills?.length) selectedSkillNames = [...prefs.skills];
      if (prefs.builtin_tools?.length) enabledBuiltinTools = [...prefs.builtin_tools];
      enabledFrontendTools = [...(prefs.frontend_tools ?? FRONTEND_TOOL_NAMES)];
      showTodoPanel = enabledFrontendTools.includes('todo_write') || enabledFrontendTools.includes('todo_read');
    } catch {
      // A deployment without preference storage simply has no preset.
    } finally {
      if (!conversationId && !params.id) void refreshTools();
    }
  }

  /**
   * Remembers the current selection for the next new conversation. Debounced
   * and best-effort: this is a convenience, never a precondition for chatting,
   * so a failure is silent rather than a toast on every toggle.
   */
  async function saveDefaults() {
    if (!defaultsLoaded) return;
    if (defaultsTimer) clearTimeout(defaultsTimer);
    defaultsTimer = setTimeout(() => {
      defaultsTimer = null;
      void savePlaygroundDefaults({
        model: selectedModel,
        system_prompt: systemPrompt,
        mcp_sets: [...selectedMCPSetNames],
        skills: [...selectedSkillNames],
        builtin_tools: [...enabledBuiltinTools],
        frontend_tools: [...enabledFrontendTools],
      }).catch(() => {});
    }, 1200);
  }

  // ─── Named presets ───

  async function loadPresets() {
    const [personal, workspace] = await Promise.allSettled([listChatPresets(), listWorkspaceChatPresets()]);
    if (personal.status === 'fulfilled') personalPresets = personal.value;
    if (workspace.status === 'fulfilled') workspacePresets = workspace.value;
  }

  /** Order-insensitive: a tool selection is a set, not a sequence. */
  function sameSelection(a: string[] | undefined, b: string[] | undefined): boolean {
    const left = [...(a ?? [])].sort();
    const right = [...(b ?? [])].sort();

    return left.length === right.length && left.every((v, i) => v === right[i]);
  }

  /** The current workbench state in preset form. */
  function currentSetup(): PlaygroundDefaults {
    return {
      model: selectedModel,
      system_prompt: systemPrompt,
      mcp_sets: [...selectedMCPSetNames],
      skills: [...selectedSkillNames],
      builtin_tools: [...enabledBuiltinTools],
      frontend_tools: [...enabledFrontendTools],
    };
  }

  function presetMatchesCurrent(preset: ChatPreset): boolean {
    return (preset.model ?? '') === selectedModel
      && (preset.system_prompt ?? '') === systemPrompt
      && sameSelection(preset.mcp_sets, selectedMCPSetNames)
      && sameSelection(preset.skills, selectedSkillNames)
      && sameSelection(preset.builtin_tools, enabledBuiltinTools)
      && sameSelection(preset.frontend_tools ?? FRONTEND_TOOL_NAMES, enabledFrontendTools);
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

    // A missing model is named rather than silently replacing the current one.
    if (preset.model && !models.includes(preset.model)) {
      addToast(`"${preset.name}" names the model ${preset.model}, which this workspace does not offer — kept ${selectedModel}.`, 'warn');
    } else if (preset.model) {
      selectedModel = preset.model;
    }

    systemPrompt = preset.system_prompt ?? '';
    selectedMCPSetNames = [...(preset.mcp_sets ?? [])];
    selectedSkillNames = [...(preset.skills ?? [])];
    enabledBuiltinTools = [...(preset.builtin_tools ?? [])];
    enabledFrontendTools = [...(preset.frontend_tools ?? FRONTEND_TOOL_NAMES)];
    showTodoPanel = enabledFrontendTools.includes('todo_write') || enabledFrontendTools.includes('todo_read');

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
  loadInfo().then(loadDefaults);
  loadPresets();
  const catalogsReady = Promise.all([loadSkills(), loadBuiltinTools(), loadMCPSets(), loadLocalServers()]);
  loadConversations();

  // ─── Scroll ───

  let followLatest = true;

  function handleChatScroll() {
    if (chatContainer) {
      followLatest = chatContainer.scrollHeight - chatContainer.scrollTop - chatContainer.clientHeight <= 4;
    }
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
      // The model still accepts it; media storage does not. Say so up front
      // rather than reporting a 413 after the turn has already been sent.
      const oversize = file.size > MEDIA_MAX_UPLOAD_BYTES;
      if (oversize) addToast(`"${file.name}" is over the 16 MB storage limit — it is sent to the model but not saved to history`, 'warn');
      try {
        const dataUrl = await readFileAsDataURL(file);
        pendingImages = [...pendingImages, { name: file.name, dataUrl, oversize }];
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
  async function refreshTools() {
    const version = ++toolDiscoveryVersion;
    loadingTools = true;
    await catalogsReady;
    if (version !== toolDiscoveryVersion) return;
    const selections = {
      mcp_sets: selectedMCPSetNames,
      skills: selectedSkillNames,
      builtin_tools: enabledBuiltinTools,
    };
    const frontendTools = [...enabledFrontendTools];
    void saveDefaults();
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

      // 3. Load documentation skill instructions. Skills never register tools.
      for (const skillName of selections.skills) {
        const skill = skills.find(s => s.name === skillName);
        if (!skill) continue;

        if (skill.system_prompt) {
          newSkillPrompts.push(skill.system_prompt);
        }

      }

      // 4. Add enabled built-in server tools
      for (const toolName of selections.builtin_tools) {
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
      // Single funnel for every tool/MCP/skill mutation. During a restore the
      // snapshot already matches, so this resolves to no request at all.
      scheduleSettingsSave();
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
      if (source.type === 'mcpset' && source.mcpSetName) {
        const res = await callMCPSetTool(source.mcpSetName, tc.function.name, args);
        const text = res.content?.map(c => c.text).join('\n') ?? '';
        return text || 'Tool executed successfully (no output)';
      } else if (source.type === 'builtin') {
        const res = await callBuiltinTool(tc.function.name, args, '', turnTraceId);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'local') {
        return await executeLocalTool(source, tc.function.name, args);
      } else if (source.type === 'extension') {
        return await executeExtensionTool(source, tc.function.name, args);
      } else if (source.type === 'frontend') {
        return await executeFrontendTool(tc.function.name, args);
      }
      return `Error: unknown tool source type`;
    } catch (e: any) {
      return `Error: ${e?.response?.data?.message || e?.response?.data?.error?.message || e.message || 'tool execution failed'}`;
    }
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
  async function executeLocalTool(source: ToolSource, exposedName: string, args: Record<string, any>): Promise<string> {
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
      const client = await localClientFor(server, abortController?.signal);
      output = clipLocalToolResult(await client.callTool(remoteName, args));
    } catch (e: any) {
      status = 'error';
      failure = [e?.message, e?.hint].filter(Boolean).join(' — ') || 'local tool call failed';
      forgetLocalClient(server);
    }

    void reportLocalToolObservation({
      trace_id: turnTraceId,
      session_id: turnSessionId,
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
  async function executeExtensionTool(source: ToolSource, exposedName: string, args: Record<string, any>): Promise<string> {
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
      output = clipLocalToolResult(await bridge.callTool(ext.id, remoteName, args, abortController?.signal));
    } catch (e: any) {
      status = 'error';
      failure = e?.message || 'the extension did not complete the call';
    }

    void reportLocalToolObservation({
      trace_id: turnTraceId,
      session_id: turnSessionId,
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

  function resetLocalDraft() {
    localDraftError = '';
    localDraftHeaderKey = '';
    localDraftHeaderValue = '';
    localDraftId = '';
    localDraftName = '';
    localDraftUrl = '';
    localDraftHeaders = {};
  }

  /** Opens the editor on an existing record, or empty for a new one. */
  function editLocalServer(server?: LocalMCPServer) {
    resetLocalDraft();
    if (server) {
      localDraftId = server.id;
      localDraftName = server.name;
      localDraftUrl = server.url;
      // Values arrive redacted; replaying the sentinel preserves the stored one.
      localDraftHeaders = { ...(server.headers ?? {}) };
    }
    localEditorOpen = true;
  }

  function closeLocalEditor() {
    resetLocalDraft();
    localEditorOpen = false;
  }

  function addLocalDraftHeader() {
    const key = localDraftHeaderKey.trim();
    if (!key) return;
    localDraftHeaders = { ...localDraftHeaders, [key]: localDraftHeaderValue };
    localDraftHeaderKey = '';
    localDraftHeaderValue = '';
  }

  async function saveLocalServer() {
    const problem = localMCPUrlProblem(localDraftUrl);
    if (!localDraftName.trim()) {
      localDraftError = 'Name is required';

      return;
    }
    if (problem) {
      localDraftError = problem;

      return;
    }

    const entry: LocalMCPServer = {
      id: localDraftId,
      name: localDraftName.trim(),
      url: localDraftUrl.trim(),
      headers: Object.keys(localDraftHeaders).length > 0 ? localDraftHeaders : undefined,
    };
    const next = localDraftId
      ? localServers.map(s => (s.id === localDraftId ? entry : s))
      : [...localServers, entry];

    localSaving = true;
    try {
      localServers = await saveLocalMCPServers(next);
      if (localDraftId) {
        const edited = localServers.find(s => s.id === localDraftId);
        // The address may have changed; the old session must not be reused.
        if (edited) forgetLocalClient(edited);
      }
      refreshLocalApprovals();
      closeLocalEditor();
      void refreshTools();
    } catch (e: any) {
      localDraftError = e?.response?.data?.message || 'Failed to save';
    } finally {
      localSaving = false;
    }
  }

  async function removeLocalServer(server: LocalMCPServer) {
    try {
      localServers = await saveLocalMCPServers(localServers.filter(s => s.id !== server.id));
      revokeLocalMCP(server.id, localStorageSafe());
      forgetLocalClient(server);
      refreshLocalApprovals();
      if (localDraftId === server.id) closeLocalEditor();
      void refreshTools();
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
    void refreshTools();
  }

  /** One action, effective immediately: the next turn offers nothing from it. */
  function disableLocalServer(server: LocalMCPServer) {
    revokeLocalMCP(server.id, localStorageSafe());
    forgetLocalClient(server);
    refreshLocalApprovals();
    void refreshTools();
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
    if ((!text && pendingImages.length === 0) || !selectedModel) return;
    if (streaming) return;
    if (!setupReady()) return;

    // Build user message content
    const images = pendingImages;
    let userContent: string | ContentPart[];
    if (images.length > 0) {
      const parts: ContentPart[] = [];
      for (const img of images) {
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
    const pair = splitModel(selectedModel);
    messages = [...messages, { role: 'user', content: userContent }];
    meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, created_at: new Date().toISOString(), imageNames: images.map(i => i.name) }];
    userInput = '';
    pendingImages = [];
    confirmClear = false;
    scrollToBottom();

    // Lazily promote the scratch buffer. A failure here is survivable: the turn
    // still runs, it just stays unsaved.
    if (!conversationId) {
      try {
        await ensureConversation(text || images[0]?.name || 'Chat conversation');
      } catch (e) {
        addToast(playgroundErrorMessage(e, 'Could not start a saved conversation — this turn runs unsaved'), 'alert');
      }
    }

    // Persist the question BEFORE answering it. The store stamps one
    // clock_timestamp per append, so saving the whole turn at the end gave the
    // user's message the completion time — a question and its answer recorded
    // as having happened at the same instant. It also means a turn that never
    // finishes still leaves the question in history. A failure here is
    // non-destructive: the message stays unsaved and rides the next append.
    await persistPending();
    await runCompletion();
    await persistPending();
  }

  /**
   * Restored history carries image descriptors that no provider accepts. A
   * stored one is re-inlined from media storage — lazily, only for the messages
   * actually going upstream, and cached per id for the page session. Anything
   * that cannot be recovered becomes text, so a missing image never fails the
   * turn.
   */
  async function outgoingContent(content: string | ContentPart[]): Promise<string | ContentPart[]> {
    if (typeof content === 'string') return content;
    const parts: ContentPart[] = [];
    for (const part of content) {
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

  /** Recursive completion loop that handles tool calls. */
  async function runCompletion(depth: number = 0) {
    if (depth === 0 && !setupReady()) return;
    // Guard against infinite tool-call loops
    const turnPair = splitModel(selectedModel);

    if (depth >= MAX_TOOL_ITERATIONS) {
      messages = [...messages, {
        role: 'assistant',
        content: `Stopped after ${MAX_TOOL_ITERATIONS} tool call iterations to prevent infinite loops.`,
      }];
      meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, created_at: new Date().toISOString(), imageNames: [] }];
      return;
    }

    // Snapshot the history synchronously. Re-inlining stored images is async,
    // so the turn has to be claimed (`streaming`) before the first await —
    // otherwise a second Enter could start a concurrent completion — and the
    // placeholder pushed below must not end up in the request.
    const history = messages.slice();
    if (!conversationId && !scratchSessionId) {
      scratchSessionId = `chats-${Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')}`;
    }
    const sessionId = conversationId || scratchSessionId;
    // One trace per turn, the conversation as the session — the same shape the
    // server-side loops record, so a browser-run tool can be placed on it.
    turnSessionId = sessionId;
    turnTraceId = `chats-turn-${Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, '0')).join('')}`;

    // Add assistant placeholder. It records the pair selected right now, so a
    // mid-conversation switch is attributed to the turn that used it.
    // The stamp stays empty until the response finishes: an entry that is
    // still streaming has no completion time, and showing the start time under
    // a growing answer would date it minutes early.
    messages = [...messages, { role: 'assistant', content: '' }];
    meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, created_at: '', imageNames: [] }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    // Accumulate tool calls from the stream
    let pendingToolCalls: ToolCall[] = [];

    try {
      // Build request messages
      const reqMessages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string }> = [];

      // Conversation prompt plus the prompts contributed by selected skills.
      const fullSystemPrompt = [systemPrompt.trim(), ...skillSystemPrompts].filter(Boolean).join('\n\n');
      if (fullSystemPrompt) {
        reqMessages.push({ role: 'system', content: fullSystemPrompt });
      }

      for (const m of history) {
        const msg: any = { role: m.role, content: await outgoingContent(m.content) };
        if (m.tool_calls) msg.tool_calls = m.tool_calls;
        if (m.tool_call_id) msg.tool_call_id = m.tool_call_id;
        reqMessages.push(msg);
      }

      await streamChatCompletion(
        'api/v1/chats/completions',
        {
          model: selectedModel,
          metadata: { session_id: sessionId },
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
        { 'x-at-trace-id': turnTraceId },
      );

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
          const result = await executeToolCall(tc);
          controller.signal.throwIfAborted();
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
          meta = meta.slice(0, -1);
        }
      }
    } finally {
      // An interrupted or failed response that kept its partial text is still
      // an entry in the transcript, so it is stamped when it stopped rather
      // than left undated. Idempotent: a completed response already has one.
      const endedIdx = messages.length - 1;
      if (messages[endedIdx]?.role === 'assistant' && meta[endedIdx] && !meta[endedIdx].created_at) {
        meta[endedIdx] = { ...meta[endedIdx], created_at: new Date().toISOString() };
      }
      streaming = false;
      abortController = null;
      activeTool = null;
    }
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
    await truncateFrom(0);
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
    // Trim the stored transcript first so history matches what the user sees.
    await truncateFrom(index + 1);
    scrollToBottom();
    await runCompletion();
    await persistPending();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.isComposing) {
      e.preventDefault();
      if (!e.repeat) void sendMessage();
    }
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

{#snippet forkAction(index: number)}
  {@const sequence = meta[index]?.sequence ?? null}
  <button
    onclick={() => forkFrom(index)}
    disabled={sequence === null}
    aria-label={sequence === null ? 'Fork unavailable: this message is not saved yet' : `Fork a new conversation from message ${sequence}`}
    title={sequence === null ? 'Fork becomes available once this message is saved to history' : 'Fork a new conversation from here'}
    class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text flex items-center gap-1 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-0 disabled:group-hover:opacity-40 disabled:cursor-not-allowed disabled:hover:text-gray-400 "
  >
    <GitBranch size={11} />
    Fork
  </button>
{/snippet}

{#snippet modelBadge(index: number)}
  {@const m = meta[index]}
  {#if m && joinModel(m.provider_key, m.model) && joinModel(m.provider_key, m.model) !== selectedModel}
    <span
      class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted border border-gray-200 dark:border-dark-border px-1 py-px"
      title={`Produced by ${joinModel(m.provider_key, m.model)}`}
    >
      {m.model || m.provider_key}
    </span>
  {/if}
{/snippet}

{#snippet messageTime(index: number)}
  {@const stamp = meta[index]?.created_at ?? ''}
  {#if stamp}
    <span
      class="text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums whitespace-nowrap"
      title={formatLocalDateTime(stamp)}
    >{formatMessageTime(stamp)}</span>
  {/if}
{/snippet}

{#snippet omittedImage(part: ContentPart, tone: string)}
  <div class="mb-2 flex items-center gap-1.5 border border-dashed px-2 py-1 text-[11px] {tone}">
    <ImageOff size={11} class="shrink-0" />
    <span class="truncate">{part.name || 'image'} — image not saved to history</span>
  </div>
{/snippet}

<div class="flex h-full">
  {#if showConversations}
    <ConversationList
      {conversations}
      activeId={conversationId}
      loading={conversationsLoading}
      hasMore={!!conversationsCursor}
      scratchDirty={!conversationId && messages.length > 0}
      onSelect={selectConversation}
      onNew={newConversation}
      onRename={renameConversation}
      onDelete={removeConversation}
      onLoadMore={() => loadConversations(true)}
    />
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
    <div class="absolute inset-0 z-50 bg-gray-900/10 dark:bg-dark-base/30 border-2 border-dashed border-gray-400 dark:border-dark-border-subtle flex items-center justify-center pointer-events-none">
      <div class="bg-white dark:bg-dark-surface px-4 py-2 text-sm text-gray-600 dark:text-dark-text-secondary shadow-sm">Drop images here — saved to history up to 16 MB</div>
    </div>
  {/if}

  <!-- Toolbar -->
  <div class="border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-1 flex flex-wrap items-center gap-1.5 shrink-0">
    <!-- Conversation panel toggle -->
    <button
      onclick={() => (showConversations = !showConversations)}
      aria-label={showConversations ? 'Hide conversation list' : 'Show conversation list'}
      aria-expanded={showConversations}
      title={showConversations ? 'Hide conversations' : 'Show conversations'}
      class="h-9 w-9 shrink-0 inline-flex items-center justify-center border border-gray-300 hover:bg-gray-50 text-gray-600 hover:text-gray-900 dark:border-dark-border-subtle dark:hover:bg-dark-elevated dark:text-dark-text-secondary dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent "
    >
      <PanelLeft size={14} />
    </button>

    <!-- Model selector -->
    <div class="relative min-w-0 flex-1 basis-40 max-w-xs">
      <select
        bind:value={selectedModel}
        onchange={scheduleSettingsSave}
        aria-label="Model"
        disabled={loading || models.length === 0}
        class="h-9 w-full truncate border border-gray-300 dark:border-dark-border-subtle pl-2.5 pr-8 text-xs appearance-none bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted "
      >
        {#if modelOptions.length === 0}
          <option value="">No models available</option>
        {/if}
        {#if selectedModel && !models.includes(selectedModel)}
          <option value={selectedModel}>{selectedModel} · unavailable</option>
        {/if}
        {#each modelGroups as group}
          <optgroup label={group.label}>
            {#each group.models as model}
              <option value={model}>{model.slice(model.indexOf('/') + 1)}</option>
            {/each}
          </optgroup>
        {/each}
      </select>
      <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-muted" />
    </div>

    <!-- Preset switcher. Controlled by the derived id, not bound: once the
         setup diverges from the applied preset it reports none. -->
    {#if presets.length > 0}
      <div class="relative min-w-0 shrink basis-28 max-w-44">
        <select
          value={activePresetId}
          onchange={(e) => applyPreset(e.currentTarget.value)}
          aria-label="Preset"
          title="Apply a saved setup to this conversation"
          class="h-9 w-full truncate border border-gray-300 dark:border-dark-border-subtle pl-2.5 pr-8 text-xs appearance-none bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent "
        >
          <option value="">No preset</option>
          {#if personalPresets.length > 0}
            <optgroup label="My presets">
              {#each personalPresets as preset}
                <option value={preset.id}>{preset.name}</option>
              {/each}
            </optgroup>
          {/if}
          {#if workspacePresets.length > 0}
            <optgroup label="Workspace presets">
              {#each workspacePresets as preset}
                <option value={preset.id}>{preset.name}{preset.can_edit ? ' · yours' : ''}</option>
              {/each}
            </optgroup>
          {/if}
        </select>
        <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-muted" />
      </div>
    {/if}

    <!-- Workbench: one entry point for the setup, system prompt included. A
         dot marks a prompt that is set, because the prompt is now behind a
         dialog and its presence is not otherwise visible from the page. -->
    <button
      onclick={() => (showWorkbench = true)}
      aria-label={`Workbench${toolCount > 0 ? ` (${toolCount} tools)` : ''}`}
      aria-expanded={showWorkbench}
      aria-haspopup="dialog"
      class={['h-9 min-w-9 px-2 shrink-0 inline-flex items-center justify-center gap-1.5 border text-xs focus-visible:outline-2 focus-visible:outline-accent ', showWorkbench ? 'bg-gray-100 border-gray-400 text-gray-900 dark:bg-dark-elevated dark:border-dark-text-muted dark:text-dark-text' : 'border-gray-300 text-gray-600 hover:bg-gray-50 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}
      title="Workbench — system prompt, skills, presets and tools"
    >
      <Wrench size={14} />
      {#if toolCount > 0}
        <span class="tabular-nums">{toolCount}</span>
      {/if}
      {#if systemPrompt.trim()}
        <span class="w-1.5 h-1.5 bg-gray-400 dark:bg-dark-text-muted" title="A system prompt is set"></span>
      {/if}
    </button>

    <!-- Todo panel toggle -->
    {#if todos.length > 0}
      <button
        onclick={() => (showTodoPanel = !showTodoPanel)}
        aria-label={`Todo list${todoActiveCount > 0 ? ` (${todoActiveCount} active)` : ''}`}
        aria-expanded={showTodoPanel}
        class={['h-9 min-w-9 px-2 shrink-0 inline-flex items-center justify-center gap-1.5 border text-xs focus-visible:outline-2 focus-visible:outline-accent ', showTodoPanel ? 'bg-gray-100 border-gray-400 text-gray-900 dark:bg-dark-elevated dark:border-dark-text-muted dark:text-dark-text' : 'border-gray-300 text-gray-600 hover:bg-gray-50 dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}
        title="Todo list"
      >
        <ListChecks size={14} />
        {#if todoActiveCount > 0}
          <span class="tabular-nums">{todoActiveCount}</span>
        {/if}
      </button>
    {/if}

    <!-- Unsaved / saving indicator (right-aligned) -->
    <div class="ml-auto flex flex-wrap min-w-0 items-center justify-end gap-1.5">
      {#if saving}
        <span class="flex items-center gap-1 text-[11px] text-gray-400 dark:text-dark-text-muted">
          <Loader2 size={11} class="animate-spin" />
          Saving
        </span>
      {:else if unsavedCount > 0 && conversationId}
        <button
          onclick={() => persistPending()}
          aria-label={`Retry saving ${unsavedCount} unsaved message${unsavedCount === 1 ? '' : 's'}`}
          title="These messages are only in this browser tab. Click to retry saving them."
          class="h-9 inline-flex shrink-0 items-center justify-center gap-1.5 px-2 text-xs border border-amber-300 dark:border-amber-900/60 text-amber-700 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-900/20 focus-visible:outline-2 focus-visible:outline-accent "
        >
          <CloudOff size={14} />
          {unsavedCount} unsaved
        </button>
      {/if}

      <!-- Token usage -->
      {#if totalTokens > 0}
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted font-mono tabular-nums" title="Context: {contextTokens.toLocaleString()} prompt + {completionTokens.toLocaleString()} completion = {totalTokens.toLocaleString()} total tokens">
          {totalTokens.toLocaleString()} tok
        </div>
      {/if}

      {#if sharingAvailable && conversationId && shareBoundaries.length > 0}
        <button onclick={() => (showShareDialog = true)} aria-label="Share conversation snapshot" aria-haspopup="dialog" aria-expanded={showShareDialog} class="h-9 inline-flex items-center justify-center gap-1.5 border border-gray-300 px-2.5 text-xs text-gray-600 hover:bg-gray-50 hover:text-gray-900 focus-visible:outline-2 focus-visible:outline-accent dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:hover:text-dark-text">
          <Share2 size={14} /> Share
        </button>
      {/if}

      <!-- Clear transcript (two-step confirm: this deletes stored messages) -->
      <button
        onclick={requestClear}
        onblur={() => (confirmClear = false)}
        disabled={streaming || saving || (messages.length === 0 && !systemPrompt && pendingImages.length === 0)}
        aria-label={confirmClear ? 'Confirm clearing the transcript' : 'Clear transcript'}
        title={conversationId ? 'Clear transcript (deletes saved messages)' : 'Clear transcript'}
        class={['h-9 min-w-9 shrink-0 inline-flex items-center justify-center gap-1.5 px-2 border text-xs focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-30 disabled:cursor-not-allowed', confirmClear ? 'border-red-600 text-red-600 dark:text-red-400 hover:border-red-700' : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:border-red-300 dark:hover:border-red-800 hover:text-red-600 dark:hover:text-red-400']}
      >
        <Trash2 size={14} />
        {#if confirmClear}Confirm?{/if}
      </button>
    </div>
  </div>

  <!-- Fork lineage -->
  {#if conversation?.forked_from_sequence}
    <div class="border-b border-gray-200 dark:border-dark-border bg-purple-50/60 dark:bg-purple-900/10 px-4 py-1.5 shrink-0 flex items-center gap-1.5 text-[11px] text-purple-800 dark:text-purple-300">
      <GitBranch size={12} class="shrink-0" />
      {#if conversation.forked_from_id}
        <span>
          Forked from
          <a
            href={`#${playgroundRoute(conversation.forked_from_id)}`}
            class="underline underline-offset-2 hover:text-purple-950 dark:hover:text-purple-200 focus-visible:outline-2 focus-visible:outline-accent"
          >{parentTitle || 'the source conversation'}</a>
          at message {conversation.forked_from_sequence}
        </span>
      {:else}
        <span>Forked at message {conversation.forked_from_sequence} — the source conversation was deleted</span>
      {/if}
    </div>
  {/if}

  {#if conversation?.imported_from_share_id}
    <div class="border-b border-gray-200 bg-blue-50/70 px-4 py-1.5 text-[11px] text-blue-800 dark:border-dark-border dark:bg-blue-900/10 dark:text-blue-300">
      Independent copy of shared snapshot version {conversation.imported_from_share_version}. Changes here never affect the source.
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
        class="flex w-full max-w-3xl max-h-[calc(100dvh-2rem)] flex-col border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface focus:outline-none sm:max-h-[calc(100dvh-4rem)]"
      >
        <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base shrink-0">
          <div class="min-w-0">
            <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Workbench</h2>
            <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
              What this conversation runs with. Changes take effect on the next message and are saved with the conversation.
            </p>
          </div>
          <button
            onclick={() => (showWorkbench = false)}
            aria-label="Close"
            class="p-1 shrink-0 text-gray-400 dark:text-dark-text-muted hover:bg-gray-200 dark:hover:bg-dark-elevated hover:text-gray-600 dark:hover:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent"
          >
            <X size={16} />
          </button>
        </div>

        <!-- Presets stay above the tabs: they describe and replace the complete
             setup, so burying them inside one category makes their scope look
             smaller than it is. -->
        <div class="shrink-0 border-b border-gray-200 dark:border-dark-border px-4 py-3">
          <div class="flex items-center justify-between gap-3 mb-1.5">
            <div>
              <h3 class="text-xs font-medium text-gray-800 dark:text-dark-text">Preset</h3>
              <p class="text-[11px] text-gray-500 dark:text-dark-text-muted">Save or replace the complete setup shown below.</p>
            </div>
            {#if activePresetId}
              <span class="shrink-0 border border-emerald-300 dark:border-emerald-900/60 px-1.5 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-300">Applied</span>
            {/if}
          </div>
          <div class="flex flex-wrap gap-2">
            <select
              bind:value={presetSaveScope}
              aria-label="Preset visibility"
              class="border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400"
            >
              <option value="personal">Only me</option>
              <option value="workspace">Workspace</option>
            </select>
            <input
              bind:value={presetDraftName}
              placeholder="Preset name"
              aria-label="Preset name"
              maxlength={80}
              class="min-w-0 flex-1 basis-40 border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400"
            />
            <button
              onclick={savePreset}
              title={draftPreset && !draftPreset.can_edit
                ? 'Only its creator can overwrite this workspace preset'
                : draftPreset
                  ? `Replace "${draftPreset.name}" with the current setup`
                  : `Save the current setup for ${presetSaveScope === 'workspace' ? 'this workspace' : 'yourself'}`}
              class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30"
              disabled={!presetDraftName.trim() || presetSaving || !!(draftPreset && !draftPreset.can_edit)}
            >
              {draftPreset && !draftPreset.can_edit ? 'Owned by teammate' : draftPreset ? 'Overwrite' : presetSaveScope === 'workspace' ? 'Share setup' : 'Save setup'}
            </button>
            {#if draftPreset?.can_edit}
              <button
                onclick={() => deletePreset(draftPreset)}
                disabled={presetSaving}
                title={`Delete the saved setup "${draftPreset.name}". The current selections stay.`}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:border-red-300 dark:hover:border-red-800 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-30"
              >
                Delete
              </button>
            {/if}
          </div>
          {#if draftPreset && !draftPreset.can_edit}
            <p class="mt-1.5 text-xs text-amber-700 dark:text-amber-300">This workspace preset belongs to another member. Change the name to save a derived preset; the original stays unchanged.</p>
          {/if}
        </div>

        <div
          role="tablist"
          tabindex="-1"
          aria-label="Workbench sections"
          onkeydown={handleWorkbenchTabsKeydown}
          class="shrink-0 flex overflow-x-auto border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-2"
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
                  ? 'border-gray-900 text-gray-900 dark:border-accent dark:text-dark-text'
                  : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-dark-text-muted dark:hover:text-dark-text',
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
          <!-- System prompt leads because it is the instruction the selected
               skills and tools serve. -->
          {#if workbenchTab === 'prompt'}
          <div class="block max-w-2xl">
            <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">System prompt</span>
            <textarea
              value={systemPrompt}
              oninput={(e) => { systemPrompt = e.currentTarget.value; scheduleSettingsSave(); void saveDefaults(); }}
              aria-label="System prompt"
              placeholder="System prompt (optional)"
              rows={8}
              class="w-full border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm resize-y focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400"
            ></textarea>
          </div>
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">
            The prompt guides the whole conversation. Presets also capture the model, prompt, skills and every tool selection without changing the transcript.
          </p>
          {/if}

          {#if workbenchTab === 'tools'}
          <!-- MCP Sets (Internal MCPs) -->
          {#if availableMCPSets.length > 0}
            <div role="group" aria-label="MCP" class="block">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">MCP</span>
              <div class="flex flex-wrap gap-1.5">
                {#each availableMCPSets as mcpSet}
                  {@const status = mcpSetStatus[mcpSet.name]}
                  <button
                    onclick={() => toggleMCPSet(mcpSet.name)}
                    aria-pressed={selectedMCPSetNames.includes(mcpSet.name)}
                    aria-label={`${mcpSet.name}${selectedMCPSetNames.includes(mcpSet.name) ? ' · Selected' : ''}`}
                    class="px-2.5 py-1 text-xs border {selectedMCPSetNames.includes(mcpSet.name)
                      ? 'bg-purple-700 dark:bg-purple-600 text-white border-purple-700 dark:border-purple-600'
                      : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                    title={mcpSet.description || mcpSet.name}
                  >
                    {mcpSet.name}
                    {#if status?.busy}
                      <Loader2 size={10} class="ml-1 inline animate-spin" />
                    {:else if status?.error || status?.warnings.length}
                      <span class="ml-1 border border-red-300 bg-red-50 px-1 text-[10px] text-red-700 dark:border-red-800 dark:bg-red-900/30 dark:text-red-300" title={status.error || status.warnings.join('\n')}>{status.tools.length > 0 ? `Partial · ${status.tools.length}` : 'Failed'}</span>
                    {:else if status}
                      <span class="ml-1 opacity-70">({status.tools.length})</span>
                    {/if}
                  </button>
                  {#if selectedMCPSetNames.includes(mcpSet.name) && !status?.busy && (status?.error || status?.warnings.length)}
                    <p role="status" class="basis-full text-[10px] text-red-600 dark:text-red-400">
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
            <div class="border border-amber-300 dark:border-amber-900/50 bg-amber-50 dark:bg-amber-900/20 px-3 py-2 space-y-1.5">
              <p class="text-xs text-amber-900 dark:text-amber-200">
                This conversation referenced {legacyMcpUrls.length} direct MCP server URL{legacyMcpUrls.length === 1 ? '' : 's'}, which Chats no longer calls. Add the server under MCP sets to use its tools again.
              </p>
              <div class="flex flex-wrap gap-1.5">
                {#each legacyMcpUrls as url}
                  <code class="border border-amber-300 dark:border-amber-900/50 bg-white/60 dark:bg-dark-elevated px-2 py-0.5 text-[10px] font-mono text-amber-900 dark:text-amber-200 truncate max-w-full">{url}</code>
                {/each}
              </div>
              <button onclick={dismissLegacyMcpUrls} class="text-[10px] text-amber-800 dark:text-amber-300 underline hover:no-underline">Dismiss</button>
            </div>
          {/if}

          <!-- Server Tools (built-in) -->
          {#if builtinTools.length > 0 || enabledBuiltinTools.length > 0}
            <div role="group" aria-label="Server Tools" class="block">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Server Tools</span>
              <BuiltinToolPicker tools={builtinTools} bind:selected={enabledBuiltinTools} onchange={refreshTools} />
            </div>
          {/if}
          {/if}

          <!-- Skills are a first-class part of the Chat setup. -->
          {#if workbenchTab === 'skills'}
          {#if skills.length > 0}
            <div role="group" aria-label="Skills" class="block">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Skills</span>
              <p class="mb-2 text-xs text-gray-500 dark:text-dark-text-muted">Add reusable Markdown instructions and reference resources. Skills do not grant tools.</p>
              <div class="flex flex-wrap gap-1.5">
                {#each skills as skill}
                  <button
                    onclick={() => toggleSkill(skill.name)}
                    aria-pressed={selectedSkillNames.includes(skill.name)}
                    aria-label={`${skill.name}${selectedSkillNames.includes(skill.name) ? ' · Selected' : ''}`}
                    class="px-2.5 py-1 text-xs border {selectedSkillNames.includes(skill.name)
                      ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                      : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                    title={skill.description || skill.name}
                  >
                    {skill.name}
                  </button>
                {/each}
              </div>
            </div>
          {:else}
            <p class="text-xs text-gray-500 dark:text-dark-text-muted">No skills are available. Create or import a skill from the Skills page, then return here to add it.</p>
          {/if}
          {/if}

          <!-- Chat Tools (frontend-only) -->
          {#if workbenchTab === 'chat'}
          <div role="group" aria-label="Chat Tools" class="block">
            <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Chat Tools</span>
            <div class="flex flex-wrap gap-1.5">
              {#each FRONTEND_TOOLS as tool}
                <button
                  onclick={() => toggleFrontendTool(tool.function.name)}
                  aria-pressed={enabledFrontendTools.includes(tool.function.name)}
                  class="px-2.5 py-1 text-xs border {enabledFrontendTools.includes(tool.function.name)
                    ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
                    : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                  title={tool.function.description}
                >
                  {tool.function.name}
                </button>
              {/each}
            </div>
            <p class="mt-2 max-w-2xl text-xs text-gray-500 dark:text-dark-text-muted">These tools run in this chat interface and help the model manage the conversation while you are here.</p>
          </div>

          <!-- Local MCP servers.
               Unlike every other source here these are dialled by this browser:
               the server stores the address and never connects to it, which is
               what makes a loopback URL mean this machine. -->
          {#if localMCPAvailable}
            <div role="group" aria-label="Local MCP servers" class="block">
              <div class="flex items-center gap-2 mb-1">
                <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide">On this machine</span>
                <button
                  onclick={() => editLocalServer()}
                  class="ml-auto px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated"
                >
                  Add server
                </button>
              </div>

              {#if localServers.length === 0 && !localEditorOpen}
                <p class="text-[11px] text-gray-400 dark:text-dark-text-muted">
                  An MCP server running on your own computer. Your browser connects to it directly, so it must allow this page (CORS) — and it is reachable only from this device.
                </p>
              {/if}

              <div class="space-y-1.5">
                {#each localServers as server}
                  {@const approved = localApprovedIds.includes(server.id)}
                  {@const status = localStatus[server.id]}
                  {@const added = toolsAddedSinceApproval(approvalFor(server.id, localStorageSafe()), status?.tools ?? [])}
                  <div class="border border-gray-200 dark:border-dark-border-subtle px-2.5 py-1.5">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="text-xs font-medium text-gray-700 dark:text-dark-text">{server.name}</span>
                      <code class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted truncate">{server.url}</code>
                      {#if approved}
                        <span class="px-1.5 py-0.5 text-[10px] border border-emerald-300 dark:border-emerald-900/60 text-emerald-700 dark:text-emerald-300">
                          Enabled here{status?.tools?.length ? ` · ${status.tools.length} tools` : ''}
                        </span>
                      {:else}
                        <span class="px-1.5 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted">
                          Not enabled on this device
                        </span>
                      {/if}
                      <div class="ml-auto flex items-center gap-1">
                        {#if approved}
                          <button onclick={() => disableLocalServer(server)} class="px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400">Disable</button>
                        {:else}
                          <button onclick={() => beginLocalApproval(server)} class="px-2 py-0.5 text-[10px] border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white">Enable…</button>
                        {/if}
                        <button onclick={() => editLocalServer(server)} class="px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated">Edit</button>
                        <button onclick={() => removeLocalServer(server)} class="p-0.5 text-gray-400 hover:text-red-500" aria-label={`Remove ${server.name}`}><X size={12} /></button>
                      </div>
                    </div>
                    {#if added.length > 0}
                      <p class="mt-1 text-[10px] text-amber-700 dark:text-amber-300">
                        New since you enabled it: {added.join(', ')}
                      </p>
                    {/if}
                    {#if status?.error}
                      <p class="mt-1 text-[10px] text-red-600 dark:text-red-400">{status.error}</p>
                      {#if status.hint}
                        <p class="mt-0.5 text-[10px] text-gray-500 dark:text-dark-text-muted">{status.hint}</p>
                      {/if}
                    {/if}
                  </div>
                {/each}
              </div>

              {#if localEditorOpen}
                <div class="mt-1.5 border border-gray-300 dark:border-dark-border-subtle p-2.5 space-y-2">
                  <div class="grid grid-cols-4 gap-2 items-center">
                    <label class="contents">
                      <span class="text-xs text-gray-600 dark:text-dark-text-secondary">Name</span>
                      <input bind:value={localDraftName} placeholder="laptop" class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text" />
                    </label>
                    <label class="contents">
                      <span class="text-xs text-gray-600 dark:text-dark-text-secondary">URL</span>
                      <input bind:value={localDraftUrl} placeholder="http://127.0.0.1:3000/mcp" class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-xs font-mono dark:bg-dark-elevated dark:text-dark-text" />
                    </label>
                    <div class="col-start-2 col-span-3 text-[10px] text-gray-400 dark:text-dark-text-muted">
                      Full endpoint URL, used exactly as entered. Loopback and private addresses only — a reachable server belongs in an MCP set, where execution policy and tracing apply.
                    </div>
                  </div>

                  <div class="grid grid-cols-4 gap-2 items-start">
                    <span class="text-xs text-gray-600 dark:text-dark-text-secondary pt-1">Headers</span>
                    <div class="col-span-3 space-y-1">
                      {#each Object.entries(localDraftHeaders) as [hk, hv]}
                        <div class="flex items-center gap-1">
                          <span class="text-[10px] font-mono text-gray-600 dark:text-dark-text-secondary">{hk}:</span>
                          <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted truncate">{hv === REDACTED ? 'stored' : hv}</span>
                          <button
                            onclick={() => { const next = { ...localDraftHeaders }; delete next[hk]; localDraftHeaders = next; }}
                            class="ml-auto p-0.5 text-gray-400 hover:text-red-500"
                            aria-label={`Remove header ${hk}`}
                          ><X size={10} /></button>
                        </div>
                      {/each}
                      <div class="flex items-center gap-1">
                        <input bind:value={localDraftHeaderKey} placeholder="Authorization" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-[11px] font-mono dark:bg-dark-elevated dark:text-dark-text" />
                        <input bind:value={localDraftHeaderValue} placeholder="value" type="password" class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-[11px] font-mono dark:bg-dark-elevated dark:text-dark-text" />
                        <button onclick={addLocalDraftHeader} class="px-2 py-1 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted">Add</button>
                      </div>
                      <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">Stored encrypted and never shown again.</p>
                    </div>
                  </div>

                  {#if localDraftError}
                    <p class="text-[11px] text-red-600 dark:text-red-400">{localDraftError}</p>
                  {/if}
                  <div class="flex items-center gap-2">
                    <button onclick={saveLocalServer} disabled={localSaving} class="px-2.5 py-1 text-xs border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white disabled:opacity-50">
                      {localSaving ? 'Saving…' : 'Save'}
                    </button>
                    <button onclick={closeLocalEditor} class="px-2.5 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary">Cancel</button>
                  </div>
                </div>
              {/if}
            </div>
          {:else}
            <p class="text-xs text-gray-500 dark:text-dark-text-muted">Tools on this machine are not available in this installation.</p>
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
                <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide">Browser extensions</span>
                <label class="flex items-center gap-1 text-xs">
                  <input type="checkbox" bind:checked={webConnectionEnabled} />
                  Web connection
                </label>
                <button
                  onclick={() => scanExtensions()}
                  disabled={!webConnectionEnabled || extensionsScanning}
                  class="ml-auto px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50"
                >
                  {extensionsScanning ? 'Scanning…' : 'Scan again'}
                </button>
              </div>

              {#if extensions.length === 0}
                <p class="text-[11px] text-gray-400 dark:text-dark-text-muted">
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
                  <div class="border border-gray-200 dark:border-dark-border-subtle px-2.5 py-1.5">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="text-xs font-medium text-gray-700 dark:text-dark-text">{ext.name}</span>
                      {#if ext.version}
                        <code class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{ext.version}</code>
                      {/if}
                      {#if approved}
                        <button
                          onclick={() => inspectExtensionTools(ext)}
                          class="px-1.5 py-0.5 text-[10px] border border-emerald-300 dark:border-emerald-900/60 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-50 dark:hover:bg-emerald-900/20 focus-visible:outline-2 focus-visible:outline-accent"
                          title={`View tools connected through ${ext.name}`}
                        >
                          Enabled here{status?.tools?.length ? ` · ${status.tools.length} tools` : ' · View tools'}
                        </button>
                      {:else}
                        <span class="px-1.5 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted">
                          Not enabled on this device
                        </span>
                      {/if}
                      <div class="ml-auto flex items-center gap-1">
                        {#if approved}
                          <button onclick={() => disableExtension(ext)} class="px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400">Disable</button>
                        {:else if serves}
                          <button onclick={() => beginExtensionApproval(ext)} class="px-2 py-0.5 text-[10px] border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white">Enable…</button>
                        {/if}
                      </div>
                    </div>
                    {#if ext.description}
                      <p class="mt-1 text-[11px] text-gray-500 dark:text-dark-text-muted">{ext.description}</p>
                    {/if}
                    <!-- The extension's own words for why it is connected but
                         idle. Without it, "0 tools" is indistinguishable from a
                         fault the reader would go looking for. -->
                    {#if ext.notice}
                      <p class="mt-1 text-[11px] text-amber-700 dark:text-amber-300">{ext.notice}</p>
                    {/if}
                    {#if !serves}
                      <p class="mt-1 text-[11px] text-gray-400 dark:text-dark-text-muted">This extension offers no tools to Chats.</p>
                    {/if}
                    {#if added.length > 0}
                      <p class="mt-1 text-[10px] text-amber-700 dark:text-amber-300">
                        New since you enabled it: {added.join(', ')}
                      </p>
                    {/if}
                    {#if status?.error}
                      <p class="mt-1 text-[10px] text-red-600 dark:text-red-400">{status.error}</p>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <p class="text-xs text-gray-500 dark:text-dark-text-muted">Browser extensions are not available in this installation.</p>
          {/if}
          {/if}
        </div>

        <!-- What the selections above actually produced. It sits outside the
             scrolling body because it is the answer to "did that work?", and
             a reader who has scrolled to the bottom of the tool catalogues is
             exactly who needs to read it. -->
        <div class="shrink-0 flex items-center gap-2 px-4 py-3 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-xs text-gray-500 dark:text-dark-text-muted">
          <div class="flex min-w-0 flex-1 items-center gap-2">
            {#if loadingTools}
              <Loader2 size={12} class="shrink-0 animate-spin" />
              <span>Discovering tools...</span>
            {:else if toolCount > 0}
              <Wrench size={12} class="shrink-0" />
              <span class="shrink-0">{toolCount} tool{toolCount !== 1 ? 's' : ''} available</span>
              <span class="text-gray-300 dark:text-dark-border">|</span>
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
              class="shrink-0 px-2 py-1 text-[11px] border border-gray-300 dark:border-dark-border-subtle text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 hover:border-red-300 dark:hover:border-red-800 focus-visible:outline-2 focus-visible:outline-accent "
              title="Clear all selected skills and tools"
            >
              Clear my selections
            </button>
          {/if}
          <button
            onclick={() => (showWorkbench = false)}
            class="shrink-0 px-3 py-1 text-xs bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-accent "
          >
            Done
          </button>
        </div>
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
          class="p-0.5 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary "
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
    onscroll={handleChatScroll}
    class="flex-1 overflow-y-auto px-4 py-4 space-y-4"
  >
    {#if loading || historyLoading}
      <div class="flex items-center justify-center gap-2 py-12 text-gray-400 dark:text-dark-text-muted text-sm">
        <Loader2 size={14} class="animate-spin" />
        {historyLoading ? 'Loading conversation…' : 'Loading providers...'}
      </div>
    {:else if models.length === 0}
      <div class="text-center py-12">
        <div class="text-gray-400 dark:text-dark-text-muted mb-2">No providers configured</div>
        <div class="text-xs text-gray-400 dark:text-dark-text-muted">
          Add providers on the <a href="#/providers" class="underline underline-offset-2 hover:text-gray-700 dark:hover:text-dark-text ">Providers</a> page first.
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
      {#if historyTruncated}
        <div class="text-center text-[11px] text-amber-700 dark:text-amber-400 border border-amber-300 dark:border-amber-900/60 bg-amber-50 dark:bg-amber-900/10 px-3 py-1.5">
          Only the most recent part of this transcript is loaded. Fork from a message to continue from a bounded prefix.
        </div>
      {/if}
      {#each messages as msg, i}
        {#if msg.role === 'user'}
          <div class="flex justify-end group">
            <div class="max-w-[75%]">
              <div class="px-4 py-2.5 text-sm leading-relaxed bg-gray-900 dark:bg-[#2B2D42] text-white">
                {#if typeof msg.content === 'string'}
                  <span class="whitespace-pre-wrap">{msg.content}</span>
                {:else}
                  {#each msg.content as part}
                    {#if part.type === 'image_url' && part.image_url?.url}
                      <img src={part.image_url.url} alt="" class="max-w-full max-h-64 mb-2 border border-gray-600 dark:border-accent/50" />
                    {:else if part.type === 'image' && part.media_id}
                      <img
                        src={mediaImageURL(part.media_id, workspaceTransport.selected)}
                        alt={part.name || 'Stored image attachment'}
                        loading="lazy"
                        class="max-w-full max-h-64 mb-2 border border-gray-600 dark:border-accent/50"
                      />
                    {:else if part.type === 'image'}
                      {@render omittedImage(part, 'border-white/40 text-white/80')}
                    {:else if part.type === 'text' && part.text}
                      <span class="whitespace-pre-wrap">{part.text}</span>
                    {/if}
                  {/each}
                {/if}
              </div>
              <div class="mt-1 flex justify-end items-center gap-3">
                {@render forkAction(i)}
                {#if !streaming}
                  <button
                    onclick={() => retryFromIndex(i)}
                    disabled={saving}
                    aria-label="Retry from this message"
                    class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text flex items-center gap-1 focus-visible:outline-2 focus-visible:outline-accent "
                    title="Retry from this message"
                  >
                    <RotateCcw size={11} />
                    Retry
                  </button>
                {/if}
                {@render messageTime(i)}
              </div>
            </div>
          </div>
        {:else if msg.role === 'assistant'}
          <div class="flex justify-start group">
            <div class={msg.tool_calls?.length ? 'min-w-0 w-full sm:max-w-[85%]' : 'max-w-[75%]'}>
              <div class="px-4 py-2.5 text-sm leading-relaxed bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border-subtle shadow-sm text-gray-800 dark:text-dark-text">
                {#if typeof msg.content === 'string'}
                  {#if !msg.content && streaming && i === messages.length - 1}
                    <span class="text-gray-400 dark:text-dark-text-muted italic">Thinking...</span>
                  {:else}
                    <Markdown source={msg.content} />
                  {/if}
                {:else}
                  {#each msg.content as part}
                    {#if part.type === 'image_url' && part.image_url?.url}
                      <img src={part.image_url.url} alt="" class="max-w-full max-h-64 mb-2 border border-gray-200 dark:border-dark-border" />
                    {:else if part.type === 'image' && part.media_id}
                      <img
                        src={mediaImageURL(part.media_id, workspaceTransport.selected)}
                        alt={part.name || 'Stored image attachment'}
                        loading="lazy"
                        class="max-w-full max-h-64 mb-2 border border-gray-200 dark:border-dark-border"
                      />
                    {:else if part.type === 'image'}
                      {@render omittedImage(part, 'border-gray-300 dark:border-dark-border text-gray-500 dark:text-dark-text-muted')}
                    {:else if part.type === 'text' && part.text}
                      <Markdown source={part.text} />
                    {/if}
                  {/each}
                {/if}
                <!-- Results remain attached to their originating call. -->
                {#if msg.tool_calls && msg.tool_calls.length > 0}
                  <div class="mt-2 pt-2 border-t border-gray-200 dark:border-dark-border space-y-1">
                    {#each msg.tool_calls as tc}
                      {@const source = toolSourceMap[tc.function.name]}
                      {#if pendingQuestion && activeTool?.messageIndex === i && activeTool?.callID === tc.id && tc.function.name === 'question'}
                        {@render questionPrompt()}
                      {:else}
                        <ToolActivity
                          call={tc}
                          result={toolResults.get(i)?.get(tc.id)}
                          running={activeTool?.messageIndex === i && activeTool?.callID === tc.id}
                          queued={activeTool?.messageIndex === i && activeTool?.callID !== tc.id}
                          source={source?.type === 'mcpset' ? `MCP: ${source.mcpSetName}` : source?.type === 'builtin' ? 'Built-in' : source?.type === 'local' ? `This machine: ${localServers.find(s => s.id === source.localServerId)?.name ?? 'local MCP'}` : source?.type === 'extension' ? `Extension: ${extensions.find(e => e.id === source.extensionId)?.name ?? source.extensionId}` : source?.type === 'frontend' ? 'Chat' : ''}
                        />
                      {/if}
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="mt-1 flex items-center gap-3">
                {@render messageTime(i)}
                {@render modelBadge(i)}
                {@render forkAction(i)}
              </div>
            </div>
          </div>
        {/if}
        <!-- Tool messages are rendered in the originating assistant's cards. -->
      {/each}
    {/if}
  </div>

  <!-- Input area -->
  <div class="border-t border-gray-200 dark:border-dark-border bg-white dark:bg-dark-elevated px-4 py-3 shrink-0">
    <!-- Local MCP servers and browser extensions are each approved once, so the
         fact that a model can run something on this computer has to stay
         visible and revocable while it is true — not only at the moment it was
         granted. One strip, because the reader's question is "what can this
         page reach on my machine", not "which subsystem is it". -->
    {#if activeBrowserTools.length > 0}
      <div role="status" class="mb-2 flex flex-wrap items-center gap-2 border border-emerald-300 dark:border-emerald-900/60 bg-emerald-50 dark:bg-emerald-900/10 px-2.5 py-1.5 text-[11px] text-emerald-900 dark:text-emerald-300">
        <Wrench size={12} class="shrink-0" />
        <span class="flex-1 min-w-0">
          Tools active on this device: {activeBrowserTools.map(t => t.name).join(', ')}
        </span>
        {#each activeBrowserTools as active (active.key)}
          <button
            onclick={active.disable}
            class="shrink-0 px-2 py-0.5 border border-emerald-300 dark:border-emerald-900/60 hover:bg-emerald-100 dark:hover:bg-emerald-900/30 focus-visible:outline-2 focus-visible:outline-accent"
          >
            Disable {active.name}
          </button>
        {/each}
      </div>
    {/if}

    <!-- Media storage hint: one quiet, dismissible notice, never a toast per image -->
    {#if mediaStorageOff && !mediaHintDismissed}
      <div role="status" class="mb-2 flex items-start gap-2 border border-amber-300 dark:border-amber-900/60 bg-amber-50 dark:bg-amber-900/10 px-2.5 py-1.5 text-[11px] text-amber-800 dark:text-amber-300">
        <ImageOff size={12} class="shrink-0 mt-0.5" />
        <span class="flex-1">
          Media storage is not configured, so attached images stay in this tab only and conversation history keeps a
          placeholder instead.
          <a
            href="#/settings/media"
            class="underline underline-offset-2 hover:text-amber-950 dark:hover:text-amber-200 focus-visible:outline-2 focus-visible:outline-accent"
          >Configure media storage</a>
        </span>
        <button
          onclick={() => (mediaHintDismissed = true)}
          aria-label="Dismiss the media storage notice"
          class="shrink-0 hover:text-amber-950 dark:hover:text-amber-200 focus-visible:outline-2 focus-visible:outline-accent"
        >
          <X size={12} />
        </button>
      </div>
    {/if}

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
              aria-label={`Remove ${img.name}`}
              class="absolute -top-1.5 -right-1.5 w-5 h-5 bg-gray-900 dark:bg-accent text-white flex items-center justify-center opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent "
              title="Remove"
            >
              <X size={12} />
            </button>
            {#if img.oversize}
              <span
                class="absolute top-0 left-0 bg-amber-500 text-white text-[9px] px-1"
                title="Over the 16 MB storage limit — sent to the model but not saved to history"
              >
                &gt;16MB
              </span>
            {/if}
            <div class="absolute bottom-0 left-0 right-0 bg-black/50 text-white text-[9px] px-1 truncate">
              {img.name}
            </div>
          </div>
        {/each}
      </div>
    {/if}

    <div class="flex flex-wrap sm:flex-nowrap items-end gap-2">
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
        aria-label="Attach image"
        class="inline-flex size-11 sm:size-10 shrink-0 items-center justify-center border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-500 focus-visible:outline-2 focus-visible:outline-accent "
        title={`Attach image — paste or drop works too. Saved to history up to 16 MB (${MEDIA_ALLOWED_LABEL}).`}
      >
        <ImagePlus size={18} />
      </button>

      <textarea
        bind:value={userInput}
        use:growComposer={userInput}
        onkeydown={handleKeydown}
        onpaste={handlePaste}
        aria-label="Message"
        placeholder={models.length === 0 ? 'No models available' : 'Write a message…'}
        disabled={models.length === 0}
        rows={1}
        class="order-first sm:order-none basis-full sm:basis-auto min-w-0 min-h-11 sm:min-h-10 max-h-[min(16rem,35dvh)] overflow-y-auto flex-1 border border-gray-300 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-[9px] sm:py-2 text-base sm:text-sm leading-6 sm:leading-[22px] resize-none focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted "
      ></textarea>
      <VoiceInput contextKey={voiceContext} disabled={models.length === 0 || streaming} bind:recording={chatRecording} bind:transcribing={chatTranscribing} ontext={text => { userInput = (userInput ? userInput + ' ' : '') + text; }} />

      {#if streaming}
        <button
          onclick={stopStreaming}
          class="ml-auto inline-flex size-11 sm:size-10 shrink-0 items-center justify-center bg-red-600 text-white hover:bg-red-700 focus-visible:outline-2 focus-visible:outline-accent"
          title="Stop"
          aria-label="Stop response"
        >
          <Square size={18} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={(!userInput.trim() && pendingImages.length === 0) || !selectedModel || models.length === 0 || chatRecording || chatTranscribing || loadingTools}
          class="ml-auto inline-flex size-11 sm:size-10 shrink-0 items-center justify-center bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 disabled:hover:bg-gray-900 focus-visible:outline-2 focus-visible:outline-accent"
          title="Send (Ctrl+Enter / ⌘+Enter)"
          aria-label="Send message"
        >
          <Send size={18} />
        </button>
      {/if}
    </div>
  </div>

  {#snippet questionPrompt()}
    {#if pendingQuestion}
      <details class="bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border w-full">
        <summary class="px-4 py-3 cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent">
          <span class="inline-flex items-center gap-2">
            <MessageCircleQuestion size={16} class="text-blue-500 shrink-0" />
            {#if pendingQuestion.header}
              <span class="text-sm font-medium text-gray-800 dark:text-dark-text">{pendingQuestion.header}</span>
            {:else}
              <span class="text-sm font-medium text-gray-800 dark:text-dark-text">Question</span>
            {/if}
          </span>
          <span class="block mt-2 text-sm text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap break-words">{pendingQuestion.question}</span>
          <span class="block mt-1 text-xs text-gray-500 dark:text-dark-text-muted">Waiting for your answer · Click to answer</span>
        </summary>
        <div class="px-4 py-3">
          {#if pendingQuestion.multiple}<p class="mb-3 text-xs text-gray-500 dark:text-dark-text-muted">Select one or more options, then submit.</p>{/if}
          <div class="space-y-1.5">
            {#each pendingQuestion.options as opt}
              <button
                aria-pressed={pendingQuestion.multiple ? questionSelections.includes(opt.label) : undefined}
                onclick={() => { const q = pendingQuestion; if (!q) return; if (q.multiple) { questionSelections = questionSelections.includes(opt.label) ? questionSelections.filter(label => label !== opt.label) : [...questionSelections, opt.label]; } else { pendingQuestion = null; q.resolve(opt.label); } }}
                class="w-full text-left px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary "
              >
                <div class="font-medium">{opt.label}{#if pendingQuestion.multiple && questionSelections.includes(opt.label)}<span class="ml-2 text-xs">Selected</span>{/if}</div>
                {#if opt.description}
                  <div class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{opt.description}</div>
                {/if}
              </button>
            {/each}
            {#if pendingQuestion.multiple}
              <button
                disabled={questionSelections.length === 0}
                onclick={() => { const q = pendingQuestion; if (q && questionSelections.length) { pendingQuestion = null; q.resolve(questionSelections.join(', ')); } }}
                class="px-3 py-2 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
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
                    class="min-w-0 flex-1 border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 "
                  />
                  <button
                    type="submit"
                    class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
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
</div>

<!-- Approval for a local MCP server.
     Approval is per device and deliberately not part of the stored record:
     the same loopback URL is a different program on a laptop and a desktop,
     so a synced approval would authorize, here, something inspected there.
     The tools are listed first because a name and a URL are not enough to
     decide with — after this, the model calls them without asking again. -->
{#if localApprovalFor}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-lg border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Enable {localApprovalFor.name} on this device</h2>
        <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
          Your browser will call <code class="font-mono">{localApprovalFor.url}</code> on this computer. The model chooses the arguments, and after this it runs these tools without asking again.
        </p>
      </div>
      <div class="p-4 space-y-3 max-h-80 overflow-y-auto">
        {#if localStatus[localApprovalFor.id]?.busy}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">Connecting…</p>
        {:else if localStatus[localApprovalFor.id]?.error}
          <p class="text-xs text-red-600 dark:text-red-400">{localStatus[localApprovalFor.id].error}</p>
          {#if localStatus[localApprovalFor.id].hint}
            <p class="text-[11px] text-gray-500 dark:text-dark-text-muted">{localStatus[localApprovalFor.id].hint}</p>
          {/if}
        {:else if localApprovalTools.length === 0}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">This server advertises no tools.</p>
        {:else}
          <p class="text-xs text-gray-600 dark:text-dark-text-secondary">{localApprovalTools.length} tool{localApprovalTools.length === 1 ? '' : 's'}:</p>
          <ul class="space-y-1.5">
            {#each localApprovalTools as tool}
              <li class="border border-gray-200 dark:border-dark-border-subtle px-2.5 py-1.5">
                <code class="text-[11px] font-mono text-gray-800 dark:text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="px-4 py-3 border-t border-gray-200 dark:border-dark-border flex items-center gap-2">
        <button
          onclick={confirmLocalApproval}
          disabled={localApprovalTools.length === 0}
          class="px-3 py-1.5 text-xs border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white disabled:opacity-50"
        >
          Enable on this device
        </button>
        <button
          onclick={() => { localApprovalFor = null; }}
          class="px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary"
        >
          Cancel
        </button>
        <button
          onclick={() => localApprovalFor && beginLocalApproval(localApprovalFor)}
          class="ml-auto px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted"
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
    <div class="w-full max-w-lg border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Enable {extensionApprovalTarget.name} on this device</h2>
        <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
          This extension runs in your browser with the access you granted it when you installed it. The model chooses the arguments, and after this it calls these tools without asking again.
        </p>
      </div>
      <div class="p-4 space-y-3 max-h-80 overflow-y-auto">
        {#if extensionStatus[extensionApprovalTarget.id]?.busy}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">Asking the extension…</p>
        {:else if extensionStatus[extensionApprovalTarget.id]?.error}
          <p class="text-xs text-red-600 dark:text-red-400">{extensionStatus[extensionApprovalTarget.id].error}</p>
        {:else if extensionApprovalTools.length === 0}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">
            {extensionApprovalTarget.notice || 'This extension offers no tools right now.'}
          </p>
        {:else}
          <p class="text-xs text-gray-600 dark:text-dark-text-secondary">{extensionApprovalTools.length} tool{extensionApprovalTools.length === 1 ? '' : 's'}:</p>
          <ul class="space-y-1.5">
            {#each extensionApprovalTools as tool}
              <li class="border border-gray-200 dark:border-dark-border-subtle px-2.5 py-1.5">
                <code class="text-[11px] font-mono text-gray-800 dark:text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="px-4 py-3 border-t border-gray-200 dark:border-dark-border flex items-center gap-2">
        <button
          onclick={confirmExtensionApproval}
          disabled={extensionApprovalTools.length === 0}
          class="px-3 py-1.5 text-xs border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white disabled:opacity-50"
        >
          Enable on this device
        </button>
        <button
          onclick={() => { extensionApprovalTarget = null; }}
          class="px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary"
        >
          Cancel
        </button>
        <button
          onclick={() => extensionApprovalTarget && beginExtensionApproval(extensionApprovalTarget)}
          class="ml-auto px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted"
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
      class="flex w-full max-w-lg max-h-[80vh] flex-col border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface"
    >
      <div class="flex items-start justify-between gap-3 px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <div class="min-w-0">
          <h2 id="extension-tools-title" class="text-sm font-medium text-gray-900 dark:text-dark-text">{extensionInspectorTarget.name} tools</h2>
          <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-text-muted">Tools currently connected to this chat through the browser extension.</p>
        </div>
        <button
          onclick={() => (extensionInspectorTarget = null)}
          aria-label="Close extension tools"
          class="shrink-0 p-1 text-gray-400 dark:text-dark-text-muted hover:bg-gray-200 dark:hover:bg-dark-elevated hover:text-gray-600 dark:hover:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent"
        ><X size={16} /></button>
      </div>
      <div class="flex-1 overflow-y-auto p-4">
        {#if extensionStatus[extensionInspectorTarget.id]?.busy}
          <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted"><Loader2 size={13} class="animate-spin" /> Asking the extension…</div>
        {:else if extensionStatus[extensionInspectorTarget.id]?.error}
          <div class="space-y-2">
            <p class="text-xs text-red-600 dark:text-red-400">{extensionStatus[extensionInspectorTarget.id].error}</p>
            <button onclick={() => extensionInspectorTarget && inspectExtensionTools(extensionInspectorTarget)} class="px-2.5 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated">Retry</button>
          </div>
        {:else if extensionInspectorTools.length === 0}
          <p class="text-xs text-gray-500 dark:text-dark-text-muted">{extensionInspectorTarget.notice || 'This extension offers no tools right now.'}</p>
        {:else}
          <div class="mb-2 flex items-center justify-between gap-3">
            <p class="text-xs text-gray-600 dark:text-dark-text-secondary">{extensionInspectorTools.length} connected tool{extensionInspectorTools.length === 1 ? '' : 's'}</p>
            <button onclick={() => extensionInspectorTarget && inspectExtensionTools(extensionInspectorTarget)} class="px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-500 dark:text-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated">Refresh</button>
          </div>
          <ul class="divide-y divide-gray-100 dark:divide-dark-border border border-gray-200 dark:border-dark-border-subtle">
            {#each extensionInspectorTools as tool}
              <li class="px-3 py-2.5">
                <code class="text-[11px] font-mono text-gray-800 dark:text-dark-text">{tool.name}</code>
                {#if tool.description}
                  <p class="mt-1 text-[11px] leading-relaxed text-gray-500 dark:text-dark-text-muted">{tool.description}</p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      <div class="shrink-0 flex justify-end px-4 py-3 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <button onclick={() => (extensionInspectorTarget = null)} class="px-3 py-1.5 text-xs bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-accent">Done</button>
      </div>
    </div>
  </div>
{/if}

<!-- Markdown typography is provided globally via `.markdown-body` rules in
     src/style/global.css. No component-local overrides needed. -->
