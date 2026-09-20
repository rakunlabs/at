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
  import { callSkillTool, listBuiltinTools, callBuiltinTool, type BuiltinToolDef } from '@/lib/api/mcp';
  import { workspaceTransport } from '@/lib/api/transport';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listAgents, createAgent, updateAgent, type Agent, type AgentScope, type SkillRef } from '@/lib/api/agents';
  import { isNativeAdmin } from '@/lib/store/auth.svelte';
  import { listMCPSets, listMCPSetTools, callMCPSetTool, type MCPSet } from '@/lib/api/mcp-sets';
  import {
    type PlaygroundConversation,
    type PlaygroundConversationInput,
    type PlaygroundMessage,
    type PlaygroundMessageInput,
    type PlaygroundRole,
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
  } from '@/lib/api/playground';
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
  import { Send, Trash2, ChevronDown, Square, Settings, ImagePlus, X, RotateCcw, Wrench, Plus, Loader2, ListChecks, MessageCircleQuestion, PanelLeft, GitBranch, CloudOff, ImageOff, Bot } from 'lucide-svelte';
  import { onDestroy, untrack } from 'svelte';
  import { push } from 'svelte-spa-router';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import Markdown from '@/lib/components/Markdown.svelte';

  storeNavbar.title = 'Playground';

  // svelte-spa-router yields `{id: null}` for the bare `/playground` route.
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
    type: 'mcp' | 'skill' | 'builtin' | 'frontend' | 'mcpset';
    /** MCP server URL (when type === 'mcp') */
    serverUrl?: string;
    /** Skill name (when type === 'skill') */
    skillName?: string;
    /** MCP Set name (when type === 'mcpset') */
    mcpSetName?: string;
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
  let agents = $state<Agent[]>([]);
  /** Agent bound to this conversation; supplies the base setup. */
  let boundAgentId = $state('');
  let agentPickerId = $state('');
  let skills = $state<Skill[]>([]);
  let selectedSkillNames = $state<string[]>([]);
  // "Save as agent" dialog.
  let showSaveAgent = $state(false);
  let saveAgentName = $state('');
  let saveAgentScope = $state<AgentScope>('personal');
  let saveAgentMode = $state<'create' | 'update'>('create');
  let savingAgent = $state(false);
  // Per-account preset bookkeeping: never save one back before it loaded, or
  // an empty initial state would overwrite the stored preset.
  let defaultsLoaded = $state(false);
  let defaultsTimer: ReturnType<typeof setTimeout> | null = null;

  // Built-in server tools
  let builtinTools = $state<BuiltinToolDef[]>([]);
  let enabledBuiltinTools = $state<string[]>([]);


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

  // ─── Durable history state ───

  /** Empty string means "unsaved scratch buffer". */
  let conversationId = $state('');
  let conversation = $state<PlaygroundConversation | null>(null);
  let parentTitle = $state('');
  let meta = $state<MessageMeta[]>([]);
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
      agent_id: boundAgentId,
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
    boundAgentId = typeof c.agent_id === 'string' ? c.agent_id : '';
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
    conversations = [c, ...conversations.filter(x => x.id !== c.id)];
  }

  async function loadConversations(more = false) {
    if (conversationsLoading) return;
    if (more && !conversationsCursor) return;
    conversationsLoading = true;
    try {
      const before = more ? conversationsCursor : '';
      const res = await listPlaygroundConversations({ limit: 50, before: before || undefined });
      const page = res.data ?? [];
      conversations = more
        ? [...conversations, ...page.filter(c => !conversations.some(x => x.id === c.id))]
        : page;
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
    push('/playground');
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
      if (conversationId === id) push('/playground');
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
      meta = loaded.map(m => ({ sequence: m.sequence, provider_key: m.provider_key, model: m.model, imageNames: [] }));
      if (c.forked_from_id) void loadParentTitle(c.forked_from_id);
      void refreshTools();
      scrollToBottom();
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
          if (meta[index]) meta[index] = { ...meta[index], sequence: s.sequence };
        });
      }
      // An append bumps `updated_at` server-side, so mirror the promotion.
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
  });

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
      allModels.sort((a, b) => a.localeCompare(b));
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
      if (prefs.agent_id) boundAgentId = prefs.agent_id;
      if (prefs.mcp_sets?.length) selectedMCPSetNames = [...prefs.mcp_sets];
      if (prefs.skills?.length) selectedSkillNames = [...prefs.skills];
      if (prefs.builtin_tools?.length) enabledBuiltinTools = [...prefs.builtin_tools];
      if (prefs.frontend_tools?.length) enabledFrontendTools = [...prefs.frontend_tools];
      showTodoPanel = enabledFrontendTools.includes('todo_write') || enabledFrontendTools.includes('todo_read');
      if (selectedMCPSetNames.length || selectedSkillNames.length || enabledBuiltinTools.length || enabledFrontendTools.length) {
        void refreshTools();
      }
    } catch {
      // A deployment without preference storage simply has no preset.
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
        agent_id: boundAgentId,
        system_prompt: systemPrompt,
        mcp_sets: [...selectedMCPSetNames],
        skills: [...selectedSkillNames],
        builtin_tools: [...enabledBuiltinTools],
        frontend_tools: [...enabledFrontendTools],
      }).catch(() => {});
    }, 1200);
  }

  // Defaults are applied after the model list so a saved model can be matched
  // against what this deployment actually offers.
  loadInfo().then(loadDefaults);
  loadAgents();
  loadSkills();
  loadBuiltinTools();
  loadMCPSets();
  loadConversations();

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

  function skillRefId(skill: string | SkillRef): string {
    return typeof skill === 'string' ? skill : skill.id;
  }

  const boundAgent = $derived(boundAgentId ? agents.find(a => a.id === boundAgentId) : undefined);

  /**
   * Binding an agent makes its configuration the conversation's base — model,
   * system prompt and tools — and records which agent that was, so reopening
   * the conversation shows it. Everything stays editable afterwards: the point
   * of the workbench is to start from an agent and add to it. Selections are
   * merged rather than replaced so a half-configured conversation is not
   * discarded by picking an agent.
   */
  function bindAgent(agentId: string) {
    const agent = agents.find(a => a.id === agentId);
    if (!agent) return;
    boundAgentId = agent.id;
    agentPickerId = '';

    const model = joinModel(agent.config.provider, agent.config.model);
    // Only adopt a model this deployment actually offers; an agent may point
    // at a provider the caller's workspace cannot reach.
    if (model && models.includes(model)) selectedModel = model;

    const newSets = agent.config.mcp_sets?.filter(s => !selectedMCPSetNames.includes(s)) || [];
    if (newSets.length > 0) selectedMCPSetNames = [...selectedMCPSetNames, ...newSets];

    const newSkills = agent.config.skills?.map(skillRefId).filter(s => s && !selectedSkillNames.includes(s)) || [];
    if (newSkills.length > 0) selectedSkillNames = [...selectedSkillNames, ...newSkills];

    const newBuiltins = agent.config.builtin_tools?.filter(t => !enabledBuiltinTools.includes(t)) || [];
    if (newBuiltins.length > 0) enabledBuiltinTools = [...enabledBuiltinTools, ...newBuiltins];

    if (agent.config.system_prompt && !systemPrompt.trim()) {
      systemPrompt = agent.config.system_prompt;
    }

    refreshTools();
    scheduleSettingsSave();
    void saveDefaults();
  }

  /** Unbinds without touching the setup it contributed: this is a label, not a lock. */
  function clearBoundAgent() {
    boundAgentId = '';
    scheduleSettingsSave();
    void saveDefaults();
  }

  function openSaveAgent(mode: 'create' | 'update') {
    saveAgentMode = mode;
    saveAgentName = mode === 'update' ? (boundAgent?.name ?? '') : (boundAgent ? `${boundAgent.name}_v2` : '');
    saveAgentScope = 'personal';
    showSaveAgent = true;
  }

  /**
   * Turns the current workbench into an agent. The mapping is exact for
   * everything AgentConfig has a field for; the browser-only chat tools
   * (todo bookkeeping, the question prompt) have no server-side equivalent and
   * are dropped with a notice rather than silently.
   *
   * Worth knowing: the Playground runs its tool loop in the browser, while a
   * saved agent runs server-side (Sessions, delegation, workflows) under the
   * loop governor. The configuration transfers exactly; the execution
   * environment is not identical.
   */
  async function saveAsAgent() {
    const name = saveAgentName.trim();
    if (!name) {
      addToast('Agent name is required', 'warn');
      return;
    }
    const { provider_key, model } = splitModel(selectedModel);
    if (!provider_key) {
      addToast('Select a model before saving an agent', 'warn');
      return;
    }
    savingAgent = true;
    try {
      const config = {
        description: '',
        provider: provider_key,
        model,
        system_prompt: systemPrompt,
        skills: [...selectedSkillNames],
        mcp_sets: [...selectedMCPSetNames],
        mcp_urls: [],
        builtin_tools: [...enabledBuiltinTools],
        max_iterations: 10,
        tool_timeout: 60,
      };
      let saved: Agent;
      if (saveAgentMode === 'update' && boundAgent) {
        saved = await updateAgent(boundAgent.id, { name, config: { ...boundAgent.config, ...config } });
        addToast(`Agent "${name}" updated`);
      } else {
        saved = await createAgent({ name, scope: saveAgentScope, config });
        addToast(`Agent "${name}" created`);
      }
      await loadAgents();
      boundAgentId = saved.id;
      showSaveAgent = false;
      if (enabledFrontendTools.length > 0) {
        addToast('Chat tools (todo, question) run only in the Playground and were not saved to the agent', 'warn');
      }
      scheduleSettingsSave();
      void saveDefaults();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save agent', 'alert');
    } finally {
      savingAgent = false;
    }
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

  /** Discover tools from MCP sets, selected skills, enabled builtins, and frontend tools. Build the dispatch map. */
  async function refreshTools() {
    loadingTools = true;
    void saveDefaults();
    const newTools: ToolDefinition[] = [];
    const newSourceMap: Record<string, ToolSource> = {};
    const newSkillPrompts: string[] = [];

    try {
      // Tools come from installation-registered sources only. Direct MCP URLs
      // are no longer discovered here — register the server as an MCP set so it
      // carries credentials and execution admission with it.

      // 2. Discover MCP Set tools (server-side resolution)
      for (const setName of selectedMCPSetNames) {
        try {
          const res = await listMCPSetTools(setName);
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
          // Also load system prompts from MCP Set's enabled skills
          const mcpSet = availableMCPSets.find(s => s.name === setName);
          if (mcpSet?.config?.enabled_skills) {
            for (const skillName of mcpSet.config.enabled_skills) {
              const skill = skills.find(s => s.name === skillName);
              if (skill?.system_prompt) {
                newSkillPrompts.push(skill.system_prompt);
              }
            }
          }
        } catch (e: any) {
          addToast(`MCP Set "${setName}": ${e?.response?.data?.message || e.message || 'failed to discover tools'}`, 'alert');
        }
      }

      // 3. Discover skill tools
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

      // 4. Add enabled built-in server tools
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

      // 5. Add enabled frontend tools
      for (const toolName of enabledFrontendTools) {
        const def = FRONTEND_TOOLS.find(t => t.function.name === toolName);
        if (!def || newSourceMap[def.function.name]) continue;
        newTools.push(def);
        newSourceMap[def.function.name] = { type: 'frontend' };
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to discover tools', 'alert');
    } finally {
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
      } else if (source.type === 'skill' && source.skillName) {
        const res = await callSkillTool(source.skillName, tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'builtin') {
        const res = await callBuiltinTool(tc.function.name, args);
        if (res.error) return `Error: ${res.error}`;
        return res.result;
      } else if (source.type === 'frontend') {
        return await executeFrontendTool(tc.function.name, args);
      }
      return `Error: unknown tool source type`;
    } catch (e: any) {
      return `Error: ${e?.response?.data?.message || e?.response?.data?.error?.message || e.message || 'tool execution failed'}`;
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
    if (chatRecording || chatTranscribing) return;
    const text = userInput.trim();
    if ((!text && pendingImages.length === 0) || !selectedModel) return;
    if (streaming) return;

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
    meta = [...meta, { sequence: null, provider_key: pair.provider_key, model: pair.model, imageNames: images.map(i => i.name) }];
    userInput = '';
    pendingImages = [];
    confirmClear = false;
    scrollToBottom();

    // Lazily promote the scratch buffer. A failure here is survivable: the turn
    // still runs, it just stays unsaved.
    if (!conversationId) {
      try {
        await ensureConversation(text || images[0]?.name || 'Playground conversation');
      } catch (e) {
        addToast(playgroundErrorMessage(e, 'Could not start a saved conversation — this turn runs unsaved'), 'alert');
      }
    }

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
    // Guard against infinite tool-call loops
    const turnPair = splitModel(selectedModel);

    if (depth >= MAX_TOOL_ITERATIONS) {
      messages = [...messages, {
        role: 'assistant',
        content: `Stopped after ${MAX_TOOL_ITERATIONS} tool call iterations to prevent infinite loops.`,
      }];
      meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, imageNames: [] }];
      return;
    }

    // Snapshot the history synchronously. Re-inlining stored images is async,
    // so the turn has to be claimed (`streaming`) before the first await —
    // otherwise a second Enter could start a concurrent completion — and the
    // placeholder pushed below must not end up in the request.
    const history = messages.slice();

    // Add assistant placeholder. It records the pair selected right now, so a
    // mid-conversation switch is attributed to the turn that used it.
    messages = [...messages, { role: 'assistant', content: '' }];
    meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, imageNames: [] }];
    streaming = true;
    const controller = new AbortController();
    abortController = controller;

    // Accumulate tool calls from the stream
    let pendingToolCalls: ToolCall[] = [];

    try {
      // Build request messages
      const reqMessages: Array<{ role: string; content: any; tool_calls?: any[]; tool_call_id?: string }> = [];

      // System prompt: combine user system prompt + skill system prompts
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
          activeTool = { messageIndex: lastIdx, callID: tc.id };
          const result = await executeToolCall(tc);
          messages = [
            ...messages,
            {
              role: 'tool',
              content: result,
              tool_call_id: tc.id,
            },
          ];
          meta = [...meta, { sequence: null, provider_key: turnPair.provider_key, model: turnPair.model, imageNames: [] }];
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
      node.style.height = `${node.scrollHeight}px`;
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
  <title>AT | Playground</title>
</svelte:head>

{#snippet forkAction(index: number)}
  {@const sequence = meta[index]?.sequence ?? null}
  <button
    onclick={() => forkFrom(index)}
    disabled={sequence === null}
    aria-label={sequence === null ? 'Fork unavailable: this message is not saved yet' : `Fork a new conversation from message ${sequence}`}
    title={sequence === null ? 'Fork becomes available once this message is saved to history' : 'Fork a new conversation from here'}
    class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text flex items-center gap-1 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-0 disabled:group-hover:opacity-40 disabled:cursor-not-allowed disabled:hover:text-gray-400 transition-opacity"
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
  <div class="border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-2 flex items-center gap-2 shrink-0">
    <!-- Conversation panel toggle -->
    <button
      onclick={() => (showConversations = !showConversations)}
      aria-label={showConversations ? 'Hide conversation list' : 'Show conversation list'}
      aria-expanded={showConversations}
      title={showConversations ? 'Hide conversations' : 'Show conversations'}
      class="p-1.5 border border-gray-300 hover:bg-gray-50 text-gray-500 hover:text-gray-700 dark:border-dark-border-subtle dark:hover:bg-dark-elevated dark:text-dark-text-muted dark:hover:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent transition-colors"
    >
      <PanelLeft size={14} />
    </button>

    <!-- Model selector -->
    <div class="relative flex-1 max-w-xs">
      <select
        bind:value={selectedModel}
        onchange={scheduleSettingsSave}
        aria-label="Model"
        disabled={loading || models.length === 0}
        class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted transition-colors"
      >
        {#if modelOptions.length === 0}
          <option value="">No models available</option>
        {/if}
        {#each modelOptions as model}
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

    <!-- Unsaved / saving indicator (right-aligned) -->
    <div class="ml-auto flex items-center gap-2">
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
          class="flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-amber-300 dark:border-amber-900/60 text-amber-700 dark:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-900/20 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
        >
          <CloudOff size={11} />
          {unsavedCount} unsaved
        </button>
      {/if}

      <!-- Token usage -->
      {#if totalTokens > 0}
        <div class="text-[11px] text-gray-400 dark:text-dark-text-muted font-mono tabular-nums" title="Context: {contextTokens.toLocaleString()} prompt + {completionTokens.toLocaleString()} completion = {totalTokens.toLocaleString()} total tokens">
          {totalTokens.toLocaleString()} tok
        </div>
      {/if}

      <!-- Clear transcript (two-step confirm: this deletes stored messages) -->
      <button
        onclick={requestClear}
        onblur={() => (confirmClear = false)}
        disabled={streaming || saving || (messages.length === 0 && !systemPrompt && pendingImages.length === 0)}
        aria-label={confirmClear ? 'Confirm clearing the transcript' : 'Clear transcript'}
        title={conversationId ? 'Clear transcript (deletes saved messages)' : 'Clear transcript'}
        class={['flex items-center gap-1 px-1.5 py-1 text-[11px] focus-visible:outline-2 focus-visible:outline-accent transition-colors disabled:opacity-30 disabled:cursor-not-allowed', confirmClear ? 'bg-red-600 text-white hover:bg-red-700' : 'text-gray-400 dark:text-dark-text-muted hover:bg-red-50 dark:hover:bg-red-900/20 hover:text-red-600 dark:hover:text-red-400']}
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

  <!-- System prompt -->
  {#if showSystemPrompt}
    <div class="border-b border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/50 px-4 py-2.5 shrink-0">
      <textarea
        bind:value={systemPrompt}
        oninput={scheduleSettingsSave}
        aria-label="System prompt"
        placeholder="System prompt (optional)"
        rows={2}
        class="w-full border border-gray-300 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm resize-y focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
      ></textarea>
    </div>
  {/if}

  <!-- Tools configuration panel -->
  {#if showToolsConfig}
    <div class="border-b border-gray-200 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/50 px-4 py-3 shrink-0 space-y-3 max-h-80 overflow-y-auto">
      <!-- Agent: bound to this conversation, and saveable back as a new agent -->
      <label class="block">
        <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Agent</span>
        {#if boundAgentId}
          <div class="flex flex-wrap items-center gap-2">
            <span class="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs border border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white">
              <Bot size={12} />
              {boundAgent?.name ?? boundAgentId}
              {#if boundAgent?.scope === 'personal'}<span class="opacity-70">· personal</span>{:else if boundAgent?.scope === 'global'}<span class="opacity-70">· global</span>{/if}
            </span>
            <button onclick={clearBoundAgent} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Unbind</button>
            {#if boundAgent?.scope === 'personal'}
              <button onclick={() => openSaveAgent('update')} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Update agent</button>
            {/if}
            <button onclick={() => openSaveAgent('create')} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Save as new agent</button>
          </div>
          <p class="mt-1 text-[10px] text-gray-400 dark:text-dark-text-muted">Everything below stays editable — add tools on top, then save it as your own agent.</p>
        {:else}
          <div class="flex gap-2">
            <div class="relative flex-1">
              <select
                bind:value={agentPickerId}
                class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm appearance-none bg-white dark:bg-dark-elevated dark:text-dark-text pr-8 focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
              >
                <option value="">Start from an agent…</option>
                {#each agents as agent}
                  <option value={agent.id}>{agent.name}{agent.scope === 'personal' ? ' (personal)' : agent.scope === 'global' ? ' (global)' : ''}{agent.config.description ? ` — ${agent.config.description}` : ''}</option>
                {/each}
              </select>
              <ChevronDown size={14} class="absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none text-gray-400 dark:text-dark-text-muted" />
            </div>
            <button
              onclick={() => bindAgent(agentPickerId)}
              disabled={!agentPickerId}
              class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 transition-colors"
            >
              Use
            </button>
            <button onclick={() => openSaveAgent('create')} class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Save as agent</button>
          </div>
        {/if}
      </label>

      <!-- MCP Sets (Internal MCPs) -->
      {#if availableMCPSets.length > 0}
        <label class="block">
          <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">MCP</span>
          <div class="flex flex-wrap gap-1.5">
            {#each availableMCPSets as mcpSet}
              <button
                onclick={() => toggleMCPSet(mcpSet.name)}
                class="px-2.5 py-1 text-xs border transition-colors {selectedMCPSetNames.includes(mcpSet.name)
                  ? 'bg-purple-700 dark:bg-purple-600 text-white border-purple-700 dark:border-purple-600'
                  : 'border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated'}"
                title={mcpSet.description || mcpSet.name}
              >
                {mcpSet.name}
              </button>
            {/each}
          </div>
        </label>
      {/if}

      <!-- Direct MCP URLs were removed: register the server as an MCP set so it
           carries its credentials, processes and execution admission. A saved
           conversation keeps its old record until it is dismissed. -->
      {#if legacyMcpUrls.length > 0}
        <div class="border border-amber-300 dark:border-amber-900/50 bg-amber-50 dark:bg-amber-900/20 px-3 py-2 space-y-1.5">
          <p class="text-xs text-amber-900 dark:text-amber-200">
            This conversation referenced {legacyMcpUrls.length} direct MCP server URL{legacyMcpUrls.length === 1 ? '' : 's'}, which the Playground no longer calls. Add the server under MCP sets to use its tools again.
          </p>
          <div class="flex flex-wrap gap-1.5">
            {#each legacyMcpUrls as url}
              <code class="border border-amber-300 dark:border-amber-900/50 bg-white/60 dark:bg-dark-elevated px-2 py-0.5 text-[10px] font-mono text-amber-900 dark:text-amber-200 truncate max-w-full">{url}</code>
            {/each}
          </div>
          <button onclick={dismissLegacyMcpUrls} class="text-[10px] text-amber-800 dark:text-amber-300 underline hover:no-underline">Dismiss</button>
        </div>
      {/if}

      <!-- Skills -->
      {#if skills.length > 0}
        <label class="block">
          <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Skills</span>
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
        </label>
      {/if}

      <!-- Server Tools (built-in) -->
      {#if builtinTools.length > 0}
        <label class="block">
          <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Server Tools</span>
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
        </label>
      {/if}

      <!-- Chat Tools (frontend-only) -->
      <label class="block">
        <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wide mb-1 block">Chat Tools</span>
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
      </label>

      <!-- Discovered tools summary + clear button -->
      <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted pt-1 border-t border-gray-200 dark:border-dark-border">
        {#if loadingTools}
          <Loader2 size={12} class="animate-spin" />
          <span>Discovering tools...</span>
        {:else if toolCount > 0}
          <Wrench size={12} />
          <span>{toolCount} tool{toolCount !== 1 ? 's' : ''} available</span>
          <span class="text-gray-300 dark:text-dark-border">|</span>
          <span class="truncate flex-1">{discoveredTools.map(t => t.function.name).join(', ')}</span>
        {:else if selectedMCPSetNames.length > 0 || selectedSkillNames.length > 0 || enabledBuiltinTools.length > 0 || enabledFrontendTools.length > 0}
          <span>No tools discovered</span>
        {:else}
          <span>Pick an agent, select MCP sets or skills, or toggle tools above</span>
        {/if}
        {#if toolCount > 0 || selectedMCPSetNames.length > 0 || selectedSkillNames.length > 0 || enabledBuiltinTools.length > 0 || enabledFrontendTools.length > 0}
          <button
            onclick={clearAllToolSelections}
            class="ml-auto shrink-0 px-2 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 hover:border-red-300 dark:hover:border-red-800 transition-colors"
            title="Clear all tool selections"
          >
            Clear All
          </button>
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
    {#if loading || historyLoading}
      <div class="flex items-center justify-center gap-2 py-12 text-gray-400 dark:text-dark-text-muted text-sm">
        <Loader2 size={14} class="animate-spin" />
        {historyLoading ? 'Loading conversation…' : 'Loading providers...'}
      </div>
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
      {#if historyTruncated}
        <div class="text-center text-[11px] text-amber-700 dark:text-amber-400 border border-amber-300 dark:border-amber-900/60 bg-amber-50 dark:bg-amber-900/10 px-3 py-1.5">
          Only the most recent part of this transcript is loaded. Fork from a message to continue from a bounded prefix.
        </div>
      {/if}
      {#each messages as msg, i}
        {#if msg.role === 'user'}
          <div class="flex justify-end group">
            <div class="max-w-[75%]">
              <div class="px-4 py-2.5 text-sm leading-relaxed bg-gray-900 dark:bg-accent text-white">
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
                    class="text-xs text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text flex items-center gap-1 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
                    title="Retry from this message"
                  >
                    <RotateCcw size={11} />
                    Retry
                  </button>
                {/if}
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
                      <ToolActivity
                        call={tc}
                        result={toolResults.get(i)?.get(tc.id)}
                        running={activeTool?.messageIndex === i && activeTool?.callID === tc.id}
                        queued={activeTool?.messageIndex === i && activeTool?.callID !== tc.id}
                        source={source?.type === 'mcpset' ? `MCP: ${source.mcpSetName}` : source?.type === 'skill' ? `Skill: ${source.skillName}` : source?.type === 'builtin' ? 'Built-in' : source?.type === 'frontend' ? 'Chat' : ''}
                      />
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="mt-1 flex items-center gap-3">
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
              class="absolute -top-1.5 -right-1.5 w-5 h-5 bg-gray-900 dark:bg-accent text-white flex items-center justify-center opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent transition-opacity"
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
        class="inline-flex size-11 sm:size-10 shrink-0 items-center justify-center border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-gray-500 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
        title={`Attach image — paste or drop works too. Saved to history up to 16 MB (${MEDIA_ALLOWED_LABEL}).`}
      >
        <ImagePlus size={14} />
      </button>

      <textarea
        bind:value={userInput}
        use:growComposer={userInput}
        onkeydown={handleKeydown}
        onpaste={handlePaste}
        aria-label="Message"
        aria-describedby="playground-composer-hint"
        placeholder={models.length === 0 ? 'No models available' : 'Write a message…'}
        disabled={models.length === 0}
        rows={3}
        class="order-first sm:order-none basis-full sm:basis-auto min-w-0 min-h-24 max-h-[min(16rem,35dvh)] overflow-y-auto flex-1 border border-gray-300 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text dark:placeholder:text-dark-text-muted px-4 py-2 text-base sm:text-sm resize-none focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle disabled:bg-gray-50 dark:disabled:bg-dark-base disabled:text-gray-400 dark:disabled:text-dark-text-muted transition-colors"
      ></textarea>
      <VoiceInput contextKey={voiceContext} disabled={models.length === 0 || streaming} bind:recording={chatRecording} bind:transcribing={chatTranscribing} ontext={text => { userInput = (userInput ? userInput + ' ' : '') + text; }} />

      {#if streaming}
        <button
          onclick={stopStreaming}
          class="ml-auto min-h-11 min-w-11 sm:min-h-10 sm:min-w-10 px-3 py-2 bg-red-600 text-white hover:bg-red-700 flex items-center justify-center gap-1.5 transition-colors"
          title="Stop"
        >
          <Square size={14} />
        </button>
      {:else}
        <button
          onclick={sendMessage}
          disabled={(!userInput.trim() && pendingImages.length === 0) || !selectedModel || models.length === 0 || chatRecording || chatTranscribing}
          class="ml-auto min-h-11 min-w-11 sm:min-h-10 sm:min-w-10 px-3 py-2 bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 disabled:hover:bg-gray-900 flex items-center justify-center gap-1.5 transition-colors"
          title="Send (Ctrl+Enter / ⌘+Enter)"
          aria-label="Send message"
        >
          <Send size={14} />
        </button>
      {/if}
    </div>
    <p id="playground-composer-hint" class="mt-1.5 text-xs text-gray-500 dark:text-dark-text-muted">Enter for a new line · Ctrl+Enter / ⌘+Enter to send</p>
  </div>

  <!-- Save-as-agent overlay: turns the current workbench into a reusable agent -->
  {#if showSaveAgent}
    <div class="absolute inset-0 z-40 bg-gray-900/30 dark:bg-black/50 flex items-center justify-center p-4">
      <div class="bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shadow-lg max-w-md w-full">
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text">{saveAgentMode === 'update' ? 'Update agent' : 'Save as agent'}</span>
          <button onclick={() => showSaveAgent = false} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted transition-colors"><X size={14} /></button>
        </div>
        <form onsubmit={(e) => { e.preventDefault(); void saveAsAgent(); }} class="px-4 py-3 space-y-3">
          <label class="block">
            <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1 block">Name</span>
            <input
              type="text"
              bind:value={saveAgentName}
              placeholder="e.g., research_buddy"
              class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
          </label>
          {#if saveAgentMode === 'create'}
            <label class="block">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1 block">Availability</span>
              <select
                bind:value={saveAgentScope}
                class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 transition-colors"
              >
                <option value="personal">Personal — only you</option>
                <option value="workspace">Workspace — shared with this workspace</option>
                {#if isNativeAdmin()}
                  <option value="global">Global — every workspace</option>
                {/if}
              </select>
            </label>
          {/if}
          <p class="text-[10px] text-gray-400 dark:text-dark-text-muted">
            Saves the model, system prompt, skills, MCP sets and server tools. Chat tools (todo, question) run only here and are not included. A saved agent runs server-side in Sessions, so its tool loop is governed differently than this workbench.
          </p>
          <div class="flex justify-end gap-2 pt-1">
            <button type="button" onclick={() => showSaveAgent = false} class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Cancel</button>
            <button type="submit" disabled={savingAgent || !saveAgentName.trim()} class="px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-30 transition-colors">
              {savingAgent ? 'Saving…' : saveAgentMode === 'update' ? 'Update' : 'Create agent'}
            </button>
          </div>
        </form>
      </div>
    </div>
  {/if}

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
</div>

<!-- Markdown typography is provided globally via `.markdown-body` rules in
     src/style/global.css. No component-local overrides needed. -->
