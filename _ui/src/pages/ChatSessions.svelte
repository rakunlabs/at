<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { querystring } from 'svelte-spa-router';
  import { updateRouteQuery } from '@/lib/helper/route-query';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listBotConfigs, type BotConfig } from '@/lib/api/bots';
  import {
    listChatSessions,
    getChatSession,
    createChatSession,
    deleteChatSession,
    updateChatSession,
    listChatMessages,
    clearChatMessages,
    sendMessage,
    confirmToolCall,
    type ChatSession,
    type ChatMessage,
    type ChatAttachment,
  } from '@/lib/api/chat-sessions';
  import { Send, Square, Plus, Loader2, Trash2, RotateCcw, Bot, ChevronDown, ShieldCheck, ShieldX, Wrench, Brain, Terminal, Check, Code, Eye, GitBranch, Search, ArrowLeft, ArrowDown, Copy, Paperclip, X, FileText, Download, Pencil } from 'lucide-svelte';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import { agentAvatar } from '@/lib/helper/avatar';
  import Markdown from '@/lib/components/Markdown.svelte';
  import { CHAT_ATTACHMENT_COUNT, CHAT_ATTACHMENT_TOTAL, attachmentBytes, attachmentSize, attachmentIsImage, attachmentImageURL, readChatAttachment, downloadChatAttachment } from '@/lib/helper/chat-attachments';

  storeNavbar.title = 'Sessions';

  // ─── State ───

  let sessions = $state<ChatSession[]>([]);
  let agents = $state<Agent[]>([]);
  let organizations = $state<Organization[]>([]);
  let organizationError = $state('');
  let loadingOrganizations = $state(false);
  let showNewSession = $state(false);
  let targetType = $state<'agent' | 'organization'>('agent');
  let newTargetId = $state('');
  let creatingSession = $state(false);
  let bots = $state<BotConfig[]>([]);
  // Sidebar filter: 'all' | 'web' (no bot_config_id) | bot ID
  let botFilter = $state<string>('all');
  let sessionSearch = $state('');
  let showSessionList = $state(true);
  let loadingMessages = $state(false);
  let messageError = $state('');
  let turnError = $state('');
  let nearBottom = $state(true);
  let messageRequest = 0;
  let selectionVersion = 0;
  let turnVersion = 0;
  let selectedSessionId = $state<string | null>(null);
  let editingTitle = $state(false);
  let titleDraft = $state('');
  let savingTitle = $state(false);
  let titleInput = $state<HTMLInputElement>();
  let titleButton = $state<HTMLButtonElement>();
  let messages = $state<ChatMessage[]>([]);
  let streamContent = $state('');
  let toolEvents = $state<any[]>([]);
  let expandedTools = $state<Record<string, boolean>>({});
  let showToolActivity = $state(false);
  let toolActivityCount = $derived(messages.filter(m => m.role === 'tool' || getToolCalls(m.data).length > 0).length + toolEvents.length);
  let visibleMessages = $derived(messages.filter(m => showToolActivity || (m.role !== 'tool' && (m.role !== 'assistant' || !!getMessageText(m.data) || !!getMessageReasoning(m.data) || getToolCalls(m.data).length === 0))));
  let collapsedSessionThreads = $state<Record<string, boolean>>({});
  // Per-message toggle: when true, render the raw markdown source instead
  // of the rendered HTML so users can inspect / copy the original content.
  let rawSourceMode = $state<Record<string, boolean>>({});
  let attachments = $state<ChatAttachment[]>([]);
  let readingAttachments = $state(false);
  let attachmentError = $state('');
  let draggingFiles = $state(false);
  let fileInput = $state<HTMLInputElement>();
  const composerControl = 'inline-flex h-11 min-w-11 sm:h-10 sm:min-w-0 shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-40';
  const secondaryControl = `${composerControl} border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated`;

  async function addAttachments(files: File[]) {
    if (sending || loadingMessages || readingAttachments || !files.length) return;
    const selection = selectionVersion;
    attachmentError = '';
    if (attachments.length + files.length > CHAT_ATTACHMENT_COUNT) { attachmentError = 'Attach up to 4 files per message.'; return; }
    if (attachments.reduce((total, file) => total + attachmentBytes(file), 0) + files.reduce((total, file) => total + file.size, 0) > CHAT_ATTACHMENT_TOTAL) {
      attachmentError = 'Attachments must be at most 8 MB in total.'; return;
    }
    readingAttachments = true;
    try {
      const added = await Promise.all(files.map(readChatAttachment));
      if (selection === selectionVersion) attachments = [...attachments, ...added];
    } catch (e) {
      if (selection === selectionVersion) attachmentError = e instanceof Error ? e.message : 'Could not read the files. Try adding them again.';
    } finally {
      if (selection === selectionVersion) readingAttachments = false;
    }
  }

  function pasteAttachments(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.files || []);
    if (!files.length) return;
    event.preventDefault();
    void addAttachments(files);
  }

  function resetComposer() {
    editingTitle = false;
    savingTitle = false;
    voiceContext++;
    attachments = [];
    readingAttachments = false;
    attachmentError = '';
    draggingFiles = false;
    inputText = '';
    recording = false;
    transcribing = false;
  }

  // Voice lifecycle is shared with Playground and reset on session changes.
  let voiceContext = $state(0);
  let recording = $state(false);
  let transcribing = $state(false);
  let inputText = $state('');
  let loading = $state(false);
  let sending = $state(false);
  let missingFinalReply = $derived(!sending && !loadingMessages && !streamContent && messages.length > 0 && (messages[messages.length - 1].role === 'tool' || (messages[messages.length - 1].role === 'assistant' && !getMessageText(messages[messages.length - 1].data) && getToolCalls(messages[messages.length - 1].data).length > 0)));
  let showAgentPicker = $state(false);
  let showSlashMenu = $state(false);
  let slashFilter = $state('');
  let pendingConfirmation = $state<{
    toolName: string;
    toolId: string;
    arguments: string;
  } | null>(null);
  let abortController: AbortController | null = null;
  let messagesContainer = $state<HTMLDivElement | undefined>(undefined);
  let inputEl = $state<HTMLTextAreaElement | undefined>(undefined);

  // Scroll-up lazy loading: sessions open anchored at the bottom with only
  // the most recent page of messages; older history loads as the user
  // scrolls toward the top.
  const MESSAGE_PAGE = 50;
  let hasOlder = $state(false);
  let loadingOlder = $state(false);

  // ─── Derived ───

  // Filter sessions by the bot dropdown selection. 'all' = show every session,
  // 'web' = only sessions without a bot_config_id (created from this UI),
  // any other value = the specific BotConfig ID.
  let filteredSessions = $derived(
    sessions.filter(s => {
      if (sessionSearch && !`${s.name} ${getAgentName(s.agent_id)} ${getBotName(s.config?.bot_config_id)}`.toLocaleLowerCase().includes(sessionSearch.toLocaleLowerCase())) return false;
      if (botFilter === 'all') return true;
      if (botFilter === 'web') return !s.config?.bot_config_id;
      return baseBotConfigId(s.config?.bot_config_id) === botFilter;
    })
  );

  interface SessionThreadGroup {
    parent: ChatSession;
    children: ChatSession[];
    activity: number;
  }

  // Telegram task conversations are persisted as focused sessions. Group
  // those sessions under their channel session instead of presenting a flat,
  // noisy list where every task looks like an unrelated conversation.
  let sessionThreadGroups = $derived.by<SessionThreadGroup[]>(() => {
    const taskSessions = filteredSessions.filter(isTaskThreadSession);
    const roots = filteredSessions.filter(s => !isTaskThreadSession(s));
    const groupedTaskIds = new Set<string>();
    const groups = roots.map(parent => {
      const children = taskSessions
        .filter(child => taskThreadBelongsTo(child, parent))
        .sort((a, b) => sessionActivityTime(b) - sessionActivityTime(a));
      children.forEach(child => groupedTaskIds.add(child.id));
      return {
        parent,
        children,
        activity: Math.max(sessionActivityTime(parent), ...children.map(sessionActivityTime)),
      };
    });

    // Keep task sessions visible if their original channel session was
    // deleted or is outside the current result page.
    for (const session of taskSessions) {
      if (!groupedTaskIds.has(session.id)) {
        groups.push({ parent: session, children: [], activity: sessionActivityTime(session) });
      }
    }

    return groups.sort((a, b) => b.activity - a.activity);
  });

  let selectedSession = $derived(sessions.find(s => s.id === selectedSessionId) || null);
  let currentAgent = $derived(selectedSession ? agents.find(a => a.id === selectedSession.agent_id) : null);
  // Agent pre-selected for new session creation (before a session exists).
  let pendingAgentId = $state<string | null>(null);
  let pendingAgent = $derived(pendingAgentId ? agents.find(a => a.id === pendingAgentId) : null);
  let groupedAgents = $derived.by(() => {
    const groups = new Map<string, Agent[]>();
    for (const agent of agents) {
      const group = agent.config.group?.trim() || '';
      groups.set(group, [...(groups.get(group) || []), agent]);
    }
    return [...groups.entries()]
      .sort(([a], [b]) => a === '' ? 1 : b === '' ? -1 : a.localeCompare(b))
      .map(([name, members]) => ({ name, agents: members.slice().sort((a, b) => a.name.localeCompare(b.name)) }));
  });
  let hasAgentGroups = $derived(groupedAgents.some(group => !!group.name));

  const slashCommands = [
    { cmd: '/agents', label: 'Switch agent', desc: 'Change the agent for this session' },
    { cmd: '/clear', label: 'Clear messages', desc: 'Start fresh in this session' },
    { cmd: '/new', label: 'New session', desc: 'Create a new chat session' },
    { cmd: '/sessions', label: 'List sessions', desc: 'Show all sessions' },
  ];

  let filteredSlashCommands = $derived(
    slashFilter
      ? slashCommands.filter(c => c.cmd.startsWith('/' + slashFilter))
      : slashCommands
  );

  // ─── Load data ───

  async function loadSessions() {
    loading = true;
    try {
      // Sort by most-recently-active first. The backend bumps updated_at
      // whenever a new message is inserted so live sessions float to top.
      const res = await listChatSessions({ _sort: '-updated_at' });
      sessions = (res.data || []).slice().sort((a, b) => sessionActivityTime(b) - sessionActivityTime(a));
    } catch (e: any) {
      addToast(e.message || 'Failed to load sessions', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadAgents() {
    try {
      const res = await listAgents();
      // Personal agents first (they are "yours"), then workspace, then
      // global; alphabetical inside each tier.
      const tierRank = (a: Agent) => (a.scope === 'personal' ? 0 : a.scope === 'global' ? 2 : 1);
      agents = (res.data || []).slice().sort((a, b) => tierRank(a) - tierRank(b) || a.name.localeCompare(b.name));
    } catch {
      // Agents may not be configured
    }
  }

  async function loadOrganizations() {
    loadingOrganizations = true;
    organizationError = '';
    try {
      const res = await listOrganizations();
      organizations = (res.data || []).slice().sort((a, b) => a.name.localeCompare(b.name));
    } catch (e: any) {
      organizationError = e?.response?.data?.message || 'Could not load organizations. Check your access or retry.';
    } finally {
      loadingOrganizations = false;
    }
  }

  function openNewSession() {
    showNewSession = true;
    showSessionList = true;
    newTargetId = '';
    void loadOrganizations();
  }

  async function createTargetSession() {
    if (!newTargetId || creatingSession) return;
    creatingSession = true;
    try {
      if (targetType === 'agent') {
        if (await quickCreateSession(newTargetId)) showNewSession = false;
      } else {
        const org = organizations.find(o => o.id === newTargetId);
        const session = await createChatSession({ organization_id: newTargetId, name: org?.name || 'Organization chat', config: { organization_chat: true } });
        sessions = [session, ...sessions];
        showNewSession = false;
        await selectSession(session.id);
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not create the conversation.', 'alert');
    } finally {
      creatingSession = false;
    }
  }

  async function loadBots() {
    try {
      const res = await listBotConfigs();
      bots = res.data || [];
    } catch {
      // Bots may not be configured — filter dropdown will just hide
    }
  }

  function getBotName(botId: string | undefined): string {
    if (!botId) return '';
    const baseId = baseBotConfigId(botId);
    const b = bots.find(x => x.id === baseId);
    return b ? `${b.platform} · ${b.name}` : baseId.slice(0, 8);
  }

  function getBotShortName(botId: string | undefined): string {
    if (!botId) return '';
    const baseId = baseBotConfigId(botId);
    const b = bots.find(x => x.id === baseId);
    return b?.name || baseId.slice(0, 8);
  }

  function baseBotConfigId(botId: string | undefined): string {
    return botId?.startsWith('task:') ? botId.slice(5) : (botId || '');
  }

  function isTaskThreadSession(session: ChatSession): boolean {
    return !!session.task_id && session.config?.bot_config_id?.startsWith('task:') === true;
  }

  function taskThreadBelongsTo(child: ChatSession, parent: ChatSession): boolean {
    return isTaskThreadSession(child)
      && baseBotConfigId(child.config?.bot_config_id) === parent.config?.bot_config_id
      && child.config?.platform === parent.config?.platform
      && child.config?.platform_user_id === parent.config?.platform_user_id
      && child.config?.platform_channel_id === parent.config?.platform_channel_id;
  }

  async function loadMessages(sessionId: string, limit: number = MESSAGE_PAGE, preserveHistory = false) {
    const request = ++messageRequest;
    const selection = selectionVersion;
    try {
      const fetched = await listChatMessages(sessionId, { limit });
      if (selectedSessionId !== sessionId || selection !== selectionVersion || request !== messageRequest) return false;
      if (preserveHistory && fetched.length >= limit) {
        const incoming = new Set(fetched.map(m => m.id));
        messages = [...messages.filter(m => !incoming.has(m.id) && m.id < fetched[0].id), ...fetched];
      } else {
        messages = fetched;
        hasOlder = fetched.length >= limit;
      }
      messageError = '';
      return true;
    } catch (e: any) {
      if (selectedSessionId === sessionId && selection === selectionVersion && request === messageRequest) {
        messageError = e?.response?.data?.message || e.message || 'Failed to load messages';
      }
      return false;
    }
  }

  async function refreshMessages() {
    if (!selectedSessionId || sending || loadingMessages || loadingOlder) return;
    const follow = nearBottom;
    if (await loadMessages(selectedSessionId, Math.max(MESSAGE_PAGE, messages.length), true)) {
      if (follow && nearBottom) scrollToBottom();
    }
  }

  /** Load the page of messages older than the current oldest and prepend
   *  it, preserving the visual scroll position. */
  async function loadOlderMessages() {
    if (!selectedSessionId || loadingOlder || !hasOlder || messages.length === 0) return;
    const sessionId = selectedSessionId;
    const selection = selectionVersion;
    ++messageRequest; // A refresh must not replace history being prepended.
    loadingOlder = true;
    try {
      const older = await listChatMessages(sessionId, {
        limit: MESSAGE_PAGE,
        beforeId: messages[0].id,
      });
      // Session switched while we were fetching — drop the result.
      if (selectedSessionId !== sessionId || selection !== selectionVersion) return;
      hasOlder = older.length >= MESSAGE_PAGE;
      if (older.length > 0) {
        const el = messagesContainer;
        const prevHeight = el?.scrollHeight ?? 0;
        const prevTop = el?.scrollTop ?? 0;
        const existing = new Set(messages.map(m => m.id));
        messages = [...older.filter(m => !existing.has(m.id)), ...messages];
        await tick();
        if (el) el.scrollTop = el.scrollHeight - prevHeight + prevTop;
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to load older messages', 'alert');
    } finally {
      if (selection === selectionVersion) loadingOlder = false;
    }
  }

  function handleMessagesScroll() {
    if (messagesContainer) nearBottom = messagesContainer.scrollHeight - messagesContainer.scrollTop - messagesContainer.clientHeight < 120;
    if (messagesContainer && messagesContainer.scrollTop < 100) {
      loadOlderMessages();
    }
  }

  async function selectSession(id: string, fromURL = false) {
    if (!fromURL) updateRouteQuery({ session: id });
    if (selectedSessionId === id) {
      if (!fromURL) showSessionList = false;
      return;
    }
    const selection = ++selectionVersion;
    resetComposer();
    ++turnVersion;
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    selectedSessionId = id;
    showSessionList = false;
    messages = [];
    hasOlder = false;
    loadingOlder = false;
    loadingMessages = true;
    messageError = '';
    turnError = '';
    pendingConfirmation = null;
    expandedTools = {};
    rawSourceMode = {};
    streamContent = '';
    toolEvents = [];
    sending = false;
    showAgentPicker = false;
    showSlashMenu = false;
    await loadMessages(id);
    if (selection !== selectionVersion) return;
    loadingMessages = false;
    nearBottom = true;
    await anchorToBottom();
    inputEl?.focus();
  }

  /** Anchor after Svelte has rendered the selected conversation. */
  async function anchorToBottom() {
    await tick();
    if (messagesContainer) messagesContainer.scrollTop = messagesContainer.scrollHeight;
  }

  function scrollToBottom() {
    if (!nearBottom) return;
    void tick().then(() => {
      if (nearBottom && messagesContainer) messagesContainer.scrollTop = messagesContainer.scrollHeight;
    });
  }

  async function copyMessage(text: string) {
    try { await navigator.clipboard.writeText(text); addToast('Response copied'); }
    catch { addToast('Could not copy. Use Source to select the text.', 'alert'); }
  }

  // ─── Actions ───

  async function editTitle() {
    if (!selectedSession) return;
    titleDraft = selectedSession.name || '';
    editingTitle = true;
    await tick();
    titleInput?.focus();
    titleInput?.select();
  }

  async function cancelTitleEdit() {
    editingTitle = false;
    await tick();
    titleButton?.focus();
  }

  async function saveTitle() {
    const name = titleDraft.trim();
    if (!selectedSession || !name || savingTitle) return;
    if (name === selectedSession.name) {
      await cancelTitleEdit();
      return;
    }
    const sessionId = selectedSession.id;
    const selection = selectionVersion;
    savingTitle = true;
    try {
      const updated = await updateChatSession(sessionId, { name });
      // Merge only the title so a concurrent agent switch stays intact.
      sessions = sessions.map(s => s.id === sessionId ? { ...s, name: updated.name } : s);
      if (selection === selectionVersion) await cancelTitleEdit();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not rename the session. Try again.', 'alert');
    } finally {
      if (selection === selectionVersion) savingTitle = false;
    }
  }

  /** Create a new session with the first available agent (or specified). */
  async function quickCreateSession(agentId?: string) {
    const selection = selectionVersion;
    const aid = agentId || (agents.length > 0 ? agents[0].id : '');
    if (!aid) {
      addToast('No agents configured. Create an agent first.', 'alert');
      return;
    }
    try {
      const session = await createChatSession({
        agent_id: aid,
        name: 'New Session',
      });
      sessions = [session, ...sessions];
      if (selection !== selectionVersion) return null;
      await selectSession(session.id);
      return selectedSessionId === session.id ? session.id : null;
    } catch (e: any) {
      addToast(e.message || 'Failed to create session', 'alert');
      return null;
    }
  }

  async function handleDeleteSession(id: string) {
    try {
      await deleteChatSession(id);
      sessions = sessions.filter(s => s.id !== id);
      if (selectedSessionId === id) {
        clearSessionSelection();
        updateRouteQuery({ session: null }, true);
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to delete session', 'alert');
    }
  }

  async function switchAgent(agentId: string) {
    if (!selectedSessionId) return;
    if (selectedSession?.config?.organization_chat) return;
    showAgentPicker = false;

    try {
      const updated = await updateChatSession(selectedSessionId, { agent_id: agentId } as any);
      sessions = sessions.map(s => s.id === updated.id ? updated : s);
      addToast(`Switched to ${agents.find(a => a.id === agentId)?.name || 'agent'}`);
    } catch (e: any) {
      addToast(e.message || 'Failed to switch agent', 'alert');
    }

    inputEl?.focus();
  }

  function handleSlashCommand(cmd: string) {
    showSlashMenu = false;
    inputText = '';

    switch (cmd) {
      case '/agents':
        showAgentPicker = true;
        break;
      case '/new':
        openNewSession();
        break;
      case '/clear':
        if (selectedSessionId) {
          clearChatMessages(selectedSessionId).then(() => {
            messages = [];
            addToast('Messages cleared');
          }).catch(() => addToast('Failed to clear messages', 'alert'));
        }
        break;
      case '/sessions':
        // Already showing sessions sidebar
        break;
    }
  }

  function handleInput() {
    const text = inputText;
    if (text.startsWith('/')) {
      showSlashMenu = true;
      slashFilter = text.slice(1);
    } else {
      showSlashMenu = false;
      slashFilter = '';
    }
  }

  async function handleSend() {
    if ((!inputText.trim() && !attachments.length) || sending || loadingMessages || readingAttachments || recording || transcribing) return;

    // Handle slash commands.
    if (inputText.startsWith('/') && attachments.length === 0) {
      const match = slashCommands.find(c => c.cmd === inputText.trim());
      if (match) {
        handleSlashCommand(match.cmd);
        return;
      }
    }

    const content = inputText.trim();
    const sentAttachments = attachments.slice();
    // Lock submission before awaiting session creation.
    sending = true;
    if (!selectedSessionId) {
      const createdID = await quickCreateSession(pendingAgentId || undefined);
      if (!createdID || selectedSessionId !== createdID) {
        if (!selectedSessionId) sending = false;
        return;
      }
      pendingAgentId = null;
    }

    inputText = '';
    attachments = [];
    attachmentError = '';
    showSlashMenu = false;
    sending = true;
    streamContent = '';
    toolEvents = [];

    // Optimistic user message.
    const nowIso = new Date().toISOString();
    messages = [
      ...messages,
      {
        id: 'pending-' + Date.now(),
        session_id: selectedSessionId!,
        role: 'user',
        data: { content, attachments: sentAttachments },
        created_at: nowIso,
      },
    ];
    // Optimistically bump the session's updated_at so it floats to the top
    // of the sidebar immediately (the backend will persist the same bump
    // when the message is stored).
    bumpSessionToTop(selectedSessionId!, nowIso);
    nearBottom = true;
    scrollToBottom();

    startTurn(selectedSessionId!, content, sentAttachments);
  }

  function startTurn(sessionId: string, content: string, sentAttachments: ChatAttachment[] = []) {
    const turn = ++turnVersion;
    let lastResponse = '';
    ++messageRequest; // Discard a background refresh started before this turn.
    const active = () => turn === turnVersion && selectedSessionId === sessionId;
    sending = true;
    turnError = '';
    streamContent = '';
    toolEvents = [];
    abortController = sendMessage(
      sessionId,
      content,
      (event) => {
        if (!active()) return;
        if (event.type === 'content') {
          lastResponse = event.content || '';
          if (event.content) streamContent += (streamContent ? '\n\n' : '') + event.content;
          scrollToBottom();
        } else if (event.type === 'tool_call') {
          toolEvents = [...toolEvents, { type: 'call', name: event.tool_name, id: event.tool_id }];
          scrollToBottom();
        } else if (event.type === 'tool_result') {
          toolEvents = [...toolEvents.filter(e => e.id !== event.tool_id), { type: 'result', name: event.tool_name, id: event.tool_id, result: event.result }];
          scrollToBottom();
        } else if (event.type === 'tool_confirm') {
          pendingConfirmation = {
            toolName: event.tool_name,
            toolId: event.tool_id,
            arguments: event.arguments || '{}',
          };
          scrollToBottom();
        }
      },
      (error) => {
        if (!active()) return;
        turnError = error;
        sending = false;
        abortController = null;
        pendingConfirmation = null;
      },
      async () => {
        if (!active()) return;
        abortController = null;
        pendingConfirmation = null;
        const loaded = await loadMessages(sessionId, Math.max(MESSAGE_PAGE, messages.length + 10));
        if (!active()) return;
        sending = false;
        // Keep the received answer visible if persistence/history cannot be read.
        if (loaded && (!lastResponse || messages.some(m => m.role === 'assistant' && getMessageText(m.data) === lastResponse))) {
          streamContent = '';
          toolEvents = [];
        }
        scrollToBottom();
        try {
          const updated = await getChatSession(sessionId);
          sessions = sessions.map(s => s.id === updated.id ? updated : s);
        } catch { /* The transcript remains usable; the next reload refreshes links. */ }
      },
      sentAttachments,
    );
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      // If slash menu is open and there's an exact or single match, select it.
      if (showSlashMenu && filteredSlashCommands.length > 0 && attachments.length === 0) {
        handleSlashCommand(filteredSlashCommands[0].cmd);
        return;
      }
      handleSend();
    }
    if (e.key === 'Escape') {
      showSlashMenu = false;
      showAgentPicker = false;
    }
  }

  function stopGeneration() {
    ++turnVersion;
    if (!abortController && sending) {
      ++selectionVersion; // Cancel an in-flight lazy session creation.
      sending = false;
      return;
    }
    if (abortController) {
      abortController.abort();
      abortController = null;
      sending = false;
      pendingConfirmation = null;
      turnError = 'Generation stopped. Any received response is kept below.';
    }
  }

  async function retryLastMessage() {
    if (sending || !selectedSessionId) return;

    // Find the last user message
    const lastUserMsg = [...messages].reverse().find(m => m.role === 'user');
    if (!lastUserMsg) return;

    const content = getMessageText(lastUserMsg.data);
    const savedAttachments = lastUserMsg.data.attachments || [];
    if (!content && !savedAttachments.length) return;

    bumpSessionToTop(selectedSessionId!);
    nearBottom = true;
    startTurn(selectedSessionId, content, savedAttachments);
  }

  async function handleConfirmation(approved: boolean) {
    if (!pendingConfirmation || !selectedSessionId) return;
    const { toolId } = pendingConfirmation;
    pendingConfirmation = null;
    try {
      await confirmToolCall(selectedSessionId, toolId, approved);
    } catch (err: any) {
      addToast(err.message || 'Failed to send confirmation', 'alert');
    }
  }

  function getAgentName(agentId: string): string {
    return agents.find(a => a.id === agentId)?.name || agentId.slice(0, 8);
  }

  // Move the given session to the top of the sidebar and bump its
  // updated_at so subsequent sorts keep it pinned until another session
  // receives newer activity.
  function bumpSessionToTop(sessionId: string, iso: string = new Date().toISOString()) {
    const idx = sessions.findIndex(s => s.id === sessionId);
    if (idx === -1) return;
    const next = sessions.slice();
    next[idx] = { ...next[idx], updated_at: iso };
    const [s] = next.splice(idx, 1);
    sessions = [s, ...next];
  }

  function getMessageText(data: any): string {
    if (typeof data?.content === 'string') return data.content;
    if (Array.isArray(data?.content)) {
      return data.content.filter((b: any) => b && (b.type === 'text' || b.type === 'output_text')).map((b: any) => b.text || '').join('\n');
    }
    return '';
  }

  // Extract non-text structured content blocks so the UI can render
  // reasoning/thinking alongside the main text body.
  function getMessageReasoning(data: any): string {
    if (!Array.isArray(data?.content)) return '';
    return data.content
      .filter((b: any) => b && (b.type === 'reasoning' || b.type === 'thinking'))
      .map((b: any) => b.text || b.thinking || '')
      .join('\n')
      .trim();
  }

  // Normalise an assistant message's tool calls for display. Handles both
  // OpenAI-style (`tool_calls: [{id, function:{name, arguments}}]`) and
  // Anthropic-style (`content: [{type:'tool_use', id, name, input}]`).
  function getToolCalls(data: any): Array<{ id: string; name: string; args: string }> {
    const out: Array<{ id: string; name: string; args: string }> = [];
    if (Array.isArray(data?.tool_calls)) {
      for (const tc of data.tool_calls) {
        const name = tc.name || tc.Name || tc.function?.name || tc.Function?.Name || '';
        const id = tc.id || tc.ID || '';
        let args = '';
        const raw = tc.function?.arguments ?? tc.Function?.Arguments ?? tc.arguments ?? tc.Arguments;
        if (typeof raw === 'string') args = raw;
        else if (raw !== undefined) {
          try { args = JSON.stringify(raw, null, 2); } catch { args = String(raw); }
        }
        out.push({ id, name, args });
      }
    }
    if (Array.isArray(data?.content)) {
      for (const b of data.content) {
        if (b?.type === 'tool_use') {
          let args = '';
          if (b.input !== undefined) {
            try { args = JSON.stringify(b.input, null, 2); } catch { args = String(b.input); }
          }
          out.push({ id: b.id || '', name: b.name || '', args });
        }
      }
    }
    return out;
  }

  // Try to pretty-print JSON content; otherwise return the original string.
  function prettyJSON(text: string): { pretty: string; isJSON: boolean } {
    const t = (text || '').trim();
    if (!t || (t[0] !== '{' && t[0] !== '[')) return { pretty: text, isJSON: false };
    try {
      return { pretty: JSON.stringify(JSON.parse(t), null, 2), isJSON: true };
    } catch {
      return { pretty: text, isJSON: false };
    }
  }

  // Sort key: prefer updated_at (bumped by backend on every new message),
  // fall back to created_at for sessions that haven't been touched yet.
  function sessionActivityTime(s: ChatSession): number {
    const raw = s.updated_at || s.created_at || '';
    const t = Date.parse(raw);
    return Number.isNaN(t) ? 0 : t;
  }

  function formatTime(iso: string): string {
    try {
      const d = new Date(iso);
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch {
      return '';
    }
  }

  // Human-friendly relative time for the session sidebar (e.g. "2m", "3h", "4d").
  function formatRelative(iso: string): string {
    if (!iso) return '';
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '';
    const diff = Date.now() - t;
    if (diff < 60_000) return 'now';
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
    if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d`;
    try {
      return new Date(t).toLocaleDateString();
    } catch {
      return '';
    }
  }

  function clearSessionSelection() {
    ++selectionVersion;
    resetComposer();
    ++turnVersion;
    abortController?.abort();
    abortController = null;
    sending = false;
    streamContent = '';
    toolEvents = [];
    pendingConfirmation = null;
    selectedSessionId = null;
    showSessionList = true;
    messages = [];
    loadingMessages = false;
    loadingOlder = false;
    hasOlder = false;
    messageError = '';
    turnError = '';
  }

  const routeSession = $derived(new URLSearchParams($querystring).get('session'));
  $effect(() => {
    const id = routeSession;
    untrack(() => {
      if (id) void selectSession(id, true);
      else if (selectedSessionId) clearSessionSelection();
    });
  });

  // Init
  onMount(() => {
    loadSessions();
    loadAgents();
    loadBots();
    let refreshing = false;
    const timer = setInterval(async () => {
      if (document.hidden || refreshing || sending || turnError || streamContent || toolEvents.length) return;
      refreshing = true;
      try { await refreshMessages(); } finally { refreshing = false; }
    }, 3000);
    return () => {
      ++selectionVersion;
      ++turnVersion;
      resetComposer();
      clearInterval(timer);
      abortController?.abort();
    };
  });
</script>

{#snippet sessionRow(session: ChatSession, nested = false, childCount = 0)}
  <div
    onclick={() => selectSession(session.id)}
    onkeydown={(e) => { if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); selectSession(session.id); } }}
    role="button"
    tabindex="0"
    class={[
      'w-full text-left py-1.5 text-xs leading-4 border-b border-gray-100 dark:border-dark-border/50 group flex items-center gap-1.5 cursor-pointer transition-colors focus-visible:outline-2 focus-visible:outline-accent focus-visible:-outline-offset-2',
      nested ? 'pl-7 pr-3 bg-gray-50/60 dark:bg-dark-base/40' : 'pl-5 pr-3',
      selectedSessionId === session.id
        ? 'bg-gray-100 dark:bg-dark-elevated'
        : 'hover:bg-gray-50 dark:hover:bg-dark-elevated/50',
    ]}
  >
    {#if nested}
      <GitBranch size={11} class="shrink-0 text-gray-300 dark:text-dark-text-faint" />
    {/if}
    <div class="min-w-0 flex-1">
      <div class="flex items-baseline gap-1.5">
        <span class="truncate text-gray-700 dark:text-dark-text font-medium flex-1 min-w-0">{session.name || 'Untitled'}</span>
        <span class="text-xs text-gray-500 dark:text-dark-text-secondary shrink-0 tabular-nums" title={session.updated_at || session.created_at}>
          {formatRelative(session.updated_at || session.created_at)}
        </span>
      </div>
      <div class="truncate text-[11px] leading-4 text-gray-500 dark:text-dark-text-secondary flex items-center gap-1">
        {#if nested || isTaskThreadSession(session)}
          <span class="text-violet-500 dark:text-violet-400">Task thread</span>
          <span>·</span>
        {/if}
        <span>{session.config?.organization_chat ? 'Organization · ' : ''}{getAgentName(session.agent_id)}</span>
        {#if !nested && !isTaskThreadSession(session) && session.config?.bot_config_id}
          <span>·</span>
          <span class="text-blue-500 dark:text-blue-400" title={getBotName(session.config.bot_config_id)}>{getBotShortName(session.config.bot_config_id)}</span>
        {/if}
        {#if childCount > 0}
          <span>· {childCount} task{childCount === 1 ? '' : 's'}</span>
        {/if}
      </div>
    </div>
    <button
      onclick={(e) => { e.stopPropagation(); clearChatMessages(session.id).then(() => { if (selectedSessionId === session.id) messages = []; addToast('Messages cleared'); }).catch(() => addToast('Failed to clear', 'alert')); }}
      disabled={sending && selectedSessionId === session.id}
      class="sm:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 p-1.5 text-gray-500 dark:text-dark-text-secondary hover:text-orange-500 transition-opacity disabled:opacity-30"
      title="Clear messages"
    >
      <RotateCcw size={11} />
    </button>
    <button
      onclick={(e) => { e.stopPropagation(); handleDeleteSession(session.id); }}
      class="sm:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 p-1.5 text-gray-500 dark:text-dark-text-secondary hover:text-red-500 transition-opacity"
      title="Delete"
    >
      <Trash2 size={11} />
    </button>
  </div>
{/snippet}

<svelte:head>
  <title>AT | Sessions</title>
</svelte:head>

<div class="flex h-full min-h-0 overflow-hidden bg-gray-50 dark:bg-dark-base">
  <!-- Left: Session list -->
  <div class={[showSessionList ? 'flex' : 'hidden', 'sm:flex w-full sm:w-64 lg:w-72 flex-shrink-0 min-h-0 border-r border-gray-200 dark:border-dark-border flex-col bg-white dark:bg-dark-surface']}>
    <div class="flex items-center justify-between px-3 h-10 shrink-0 border-b border-gray-200 dark:border-dark-border">
      <h1 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Sessions</h1>
      <button
        onclick={openNewSession}
        disabled={sending}
        class="flex items-center gap-1 px-2 py-1 rounded text-xs font-medium bg-gray-900 text-white dark:bg-accent dark:text-gray-950 disabled:opacity-40"
        title="New session (or type /new)"
      >
        <Plus size={16} /> New
      </button>
    </div>

    {#if showNewSession}
      <form onsubmit={(e) => { e.preventDefault(); void createTargetSession(); }} class="space-y-3 border-b border-gray-200 bg-gray-50 p-3 text-xs dark:border-dark-border dark:bg-dark-base">
        <label class="block space-y-1"><span class="font-medium text-gray-900 dark:text-dark-text">Talk to</span>
          <select bind:value={targetType} onchange={() => { newTargetId = ''; }} disabled={creatingSession} class="w-full border border-gray-300 bg-white px-2 py-2 text-sm dark:border-dark-border dark:bg-dark-surface dark:text-dark-text">
            <option value="agent">Agent</option><option value="organization">Organization</option>
          </select>
        </label>
        <label class="block space-y-1"><span class="font-medium text-gray-900 dark:text-dark-text">{targetType === 'agent' ? 'Agent' : 'Organization'}</span>
          <select bind:value={newTargetId} disabled={creatingSession || (targetType === 'organization' && loadingOrganizations)} class="w-full border border-gray-300 bg-white px-2 py-2 text-sm dark:border-dark-border dark:bg-dark-surface dark:text-dark-text">
            <option value="">Select {targetType === 'agent' ? 'an agent' : 'an organization'}…</option>
            {#if targetType === 'agent'}
              {#each agents as agent (agent.id)}<option value={agent.id}>{agent.name}</option>{/each}
            {:else}
              {#each organizations as org (org.id)}<option value={org.id} disabled={!org.head_agent_id}>{org.name}{!org.head_agent_id ? ' — no head agent' : ''}</option>{/each}
            {/if}
          </select>
        </label>
        {#if targetType === 'organization'}
          <p class="text-gray-600 dark:text-dark-text-secondary">Discuss ideas with the team’s head agent. A task starts only when you ask for work to be done.</p>
          {#if loadingOrganizations}<p role="status">Loading organizations…</p>{:else if organizationError}<p role="alert" class="text-red-700 dark:text-red-300">{organizationError}</p><button type="button" onclick={loadOrganizations} class="underline">Retry</button>{:else if !organizations.length}<p>No organizations available in this workspace.</p>{/if}
        {/if}
        <div class="flex gap-2">
          <button type="submit" disabled={!newTargetId || creatingSession} class="min-h-11 border border-gray-900 bg-gray-900 px-3 py-1.5 text-white disabled:opacity-40 dark:border-accent dark:bg-accent dark:text-gray-950">{creatingSession ? 'Starting…' : 'Start chat'}</button>
          <button type="button" onclick={() => { showNewSession = false; }} disabled={creatingSession} class="min-h-11 border border-gray-300 px-3 py-1.5 dark:border-dark-border dark:text-dark-text">Cancel</button>
        </div>
      </form>
    {/if}

    <label class="flex items-center gap-2 m-2 px-2 h-8 shrink-0 rounded border border-gray-200 dark:border-dark-border focus-within:ring-1 focus-within:ring-accent text-gray-500 dark:text-dark-text-secondary">
      <Search size={14} />
      <input bind:value={sessionSearch} aria-label="Search sessions" placeholder="Search sessions…" class="w-full min-w-0 bg-transparent text-base sm:text-xs text-gray-900 dark:text-dark-text placeholder:text-gray-500 dark:placeholder:text-dark-text-secondary outline-none" />
    </label>

    {#if bots.length > 0}
      <div class="px-2 py-1.5 border-b border-gray-200 dark:border-dark-border">
        <select
          bind:value={botFilter}
          class="w-full text-xs px-2 py-1 rounded border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-elevated text-gray-700 dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-accent"
          title="Filter sessions by bot"
        >
          <option value="all">All sessions</option>
          <option value="web">Web only (no bot)</option>
          {#each bots as bot (bot.id)}
            <option value={bot.id}>{bot.platform} · {bot.name}</option>
          {/each}
        </select>
      </div>
    {/if}

    <div class="flex-1 overflow-y-auto">
      {#if loading}
        <div class="flex items-center justify-center py-6">
          <Loader2 size={14} class="animate-spin text-gray-400" />
        </div>
      {:else if filteredSessions.length === 0}
        <div class="px-2 py-6 text-[11px] text-gray-400 dark:text-dark-text-muted text-center">
          {sessions.length === 0 ? 'Type to start chatting' : 'No sessions match this filter'}
        </div>
      {:else}
        {#each sessionThreadGroups as thread (thread.parent.id)}
          <div>
            <div class="relative">
              {@render sessionRow(thread.parent, false, thread.children.length)}
              {#if thread.children.length > 0}
                <button
                  onclick={(e) => { e.stopPropagation(); collapsedSessionThreads[thread.parent.id] = !collapsedSessionThreads[thread.parent.id]; }}
                  class="absolute left-0.5 top-2 p-0.5 text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text z-10"
                  title={collapsedSessionThreads[thread.parent.id] ? 'Show task threads' : 'Hide task threads'}
                >
                  <ChevronDown size={10} class={collapsedSessionThreads[thread.parent.id] ? '-rotate-90' : ''} />
                </button>
              {/if}
            </div>
            {#if !collapsedSessionThreads[thread.parent.id]}
              {#each thread.children as child (child.id)}
                {@render sessionRow(child, true)}
              {/each}
            {/if}
          </div>
        {/each}
      {/if}
    </div>
  </div>

  <!-- Right: Chat area -->
  <div class={[showSessionList ? 'hidden' : 'flex', 'sm:flex flex-1 flex-col min-w-0 min-h-0 relative']}>
    <button onclick={() => { showSessionList = true; }} class="sm:hidden flex items-center gap-2 px-4 py-3 text-sm border-b border-gray-200 dark:border-dark-border">
      <ArrowLeft size={16} /> All sessions
    </button>
    <!-- Top bar: current agent indicator -->
    {#if selectedSession}
      <div class="flex items-center gap-3 h-10 shrink-0 px-4 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="min-w-0 flex-1 flex items-center gap-3">
          {#if editingTitle}
            <form class="flex min-w-0 flex-1 items-center gap-1" onsubmit={(event) => { event.preventDefault(); void saveTitle(); }}>
              <input
                bind:this={titleInput}
                bind:value={titleDraft}
                aria-label="Session name"
                placeholder="Session name"
                required
                disabled={savingTitle}
                onkeydown={(event) => {
                  if (event.key === 'Escape' && !event.isComposing && !savingTitle) {
                    event.preventDefault();
                    void cancelTitleEdit();
                  }
                  if (event.key === 'Enter' && event.isComposing) event.preventDefault();
                }}
                class="h-8 min-w-0 flex-1 rounded border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface px-2 text-sm text-gray-900 dark:text-dark-text focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
              />
              <button type="submit" disabled={savingTitle || !titleDraft.trim()} aria-label={savingTitle ? 'Saving session name' : 'Save session name'} title="Save (Enter)" class="flex h-9 w-9 shrink-0 items-center justify-center rounded text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40">
                {#if savingTitle}<Loader2 size={15} class="animate-spin" />{:else}<Check size={15} />{/if}
              </button>
              <button type="button" onclick={cancelTitleEdit} disabled={savingTitle} aria-label="Cancel rename" title="Cancel (Escape)" class="flex h-9 w-9 shrink-0 items-center justify-center rounded text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40"><X size={15} /></button>
            </form>
          {:else}
            <h2 class="min-w-0 text-sm font-medium text-gray-900 dark:text-dark-text">
              <button bind:this={titleButton} onclick={editTitle} title="Rename session" aria-label={`Rename session: ${selectedSession.name || 'Untitled session'}`} class="flex h-9 max-w-full items-center gap-2 rounded px-1 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent">
                <span class="truncate">{selectedSession.name || 'Untitled session'}</span><Pencil size={12} class="shrink-0 text-gray-500 dark:text-dark-text-secondary" />
              </button>
            </h2>
            <span class="hidden md:block truncate text-xs text-gray-500 dark:text-dark-text-secondary" title={`${currentAgent?.name || 'Assistant'} · ${currentAgent?.config?.provider || ''}/${currentAgent?.config?.model || ''}`}>{currentAgent?.name || 'Assistant'}{currentAgent?.config?.model ? ` · ${currentAgent.config.model}` : ''}</span>
          {/if}
        </div>
        {#if toolActivityCount > 0}<button onclick={() => { showToolActivity = !showToolActivity; }} aria-pressed={showToolActivity} class="flex shrink-0 items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text" title={showToolActivity ? 'Hide tool activity' : 'Show tool activity'}><Wrench size={13} /><span class="hidden sm:inline">Activity</span> {toolActivityCount}</button>{/if}
        {#if selectedSession.task_id || selectedSession.config?.active_task_id}<a href={`#/tasks/${selectedSession.task_id || selectedSession.config.active_task_id}`} class="flex shrink-0 items-center gap-1 text-xs text-violet-600 dark:text-violet-400 hover:underline"><GitBranch size={12} /> Task</a>{/if}
        <button onclick={refreshMessages} disabled={sending || loadingMessages} title="Refresh messages" class="p-1.5 rounded text-gray-500 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-40"><RotateCcw size={14} /></button>
      </div>
    {/if}

    <!-- Messages area -->
    <div bind:this={messagesContainer} onscroll={handleMessagesScroll} class="flex-1 min-h-0 overflow-y-auto overscroll-contain text-base leading-7">
      {#if !selectedSessionId}
        <div class="flex min-h-full items-center justify-center px-4 py-8 sm:px-6">
          <div class="w-full max-w-2xl text-sm">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Select an agent to start</h2>
            <p class="mt-1 mb-6 text-sm text-gray-500 dark:text-dark-text-secondary">Choose who to work with, then send a message or attach a file.</p>
            {#if agents.length > 0}
              <div class="space-y-5 mb-4">
                {#each groupedAgents as group (group.name)}
                  <section aria-label={group.name || 'Ungrouped agents'}>
                    {#if hasAgentGroups}<h3 class="mb-2 flex items-baseline gap-2 text-sm font-medium text-gray-700 dark:text-dark-text"><span class="break-words">{group.name || 'Ungrouped'}</span><span class="text-xs font-normal text-gray-500 dark:text-dark-text-secondary">{group.agents.length}</span></h3>{/if}
                    <div class="flex flex-wrap gap-2">
                {#each group.agents as agent (agent.id)}
                  <button
                    onclick={() => { pendingAgentId = agent.id; inputEl?.focus(); }}
                    aria-pressed={pendingAgentId === agent.id}
                    title={agent.config.description || agent.name}
                    class={[
                      'inline-flex min-h-10 max-w-full items-center gap-2 px-3 py-2 text-sm rounded-md border transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent',
                      pendingAgentId === agent.id
                        ? 'border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white dark:text-gray-950'
                        : 'border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:border-gray-400 dark:hover:border-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated',
                    ]}
                  >
                    <img src={agentAvatar(agent.config.avatar_seed, agent.name, 16)} alt="" class="w-4 h-4 rounded-full bg-gray-100 dark:bg-dark-elevated" />
                    <span class="break-words text-left">{agent.name}</span>
                    {#if pendingAgentId === agent.id}<Check size={14} class="shrink-0" />{/if}
                  </button>
                {/each}
                    </div>
                  </section>
                {/each}
              </div>
              {#if pendingAgent}<p role="status" class="text-sm text-gray-500 dark:text-dark-text-secondary">Ready to chat with {pendingAgent.name}.</p>{/if}
            {:else}
              <p class="text-[11px]">No agents configured. Create an agent first.</p>
            {/if}
          </div>
        </div>
      {:else}
        <div class="w-full px-4 py-4 space-y-4">
          {#if messageError}
            <div role="alert" class="flex flex-wrap items-center gap-2 rounded-md border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/30 p-3 text-sm text-red-800 dark:text-red-300">
              <span class="flex-1">Messages could not be refreshed. {messageError}</span>
              <button onclick={refreshMessages} class="font-semibold underline underline-offset-4">Try again</button>
            </div>
          {/if}
          {#if loadingMessages}
            <div role="status" class="flex items-center justify-center gap-2 py-12 text-sm text-gray-500 dark:text-dark-text-secondary"><Loader2 size={18} class="animate-spin motion-reduce:animate-none" /> Loading conversation…</div>
          {:else if messages.length === 0 && !messageError && !sending}
            <div class="py-12 text-center">
              <h3 class="font-semibold text-gray-900 dark:text-dark-text">Start the conversation</h3>
              <p class="mt-2 text-sm text-gray-500 dark:text-dark-text-secondary">Send a message to {currentAgent?.name || 'your agent'}. Responses and tool activity will appear here.</p>
            </div>
          {/if}
          {#if loadingOlder}
            <div class="flex items-center justify-center gap-1.5 py-1 text-[11px] text-gray-400 dark:text-dark-text-muted">
              <Loader2 size={12} class="animate-spin" /> Loading older messages…
            </div>
          {:else if hasOlder}
            <div class="flex items-center justify-center py-1">
              <button
                onclick={loadOlderMessages}
                class="text-[11px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary hover:underline"
              >Load older messages</button>
            </div>
          {/if}
          {#each visibleMessages as msg (msg.id)}
            {#if msg.role === 'user'}
              <!-- User bubble -->
              <div class="flex gap-3 justify-end group">
                <div class="min-w-0 max-w-[85%] flex flex-col items-end">
                  <div class="flex items-center gap-2 mb-1">
                    <span class="text-xs text-gray-500 dark:text-dark-text-secondary tabular-nums whitespace-nowrap">{formatTime(msg.created_at)}</span>
                    <span class="text-xs font-semibold text-blue-600 dark:text-blue-400">You</span>
                  </div>
                  {#if msg.data.attachments?.length}
                    <div class="mb-2 flex max-w-full flex-wrap justify-end gap-2">
                      {#each msg.data.attachments as file}
                        <button onclick={() => downloadChatAttachment(file)} class="max-w-full overflow-hidden rounded-md border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface text-left text-gray-900 dark:text-dark-text focus-visible:outline-2 focus-visible:outline-accent" title={`Download ${file.name}`}>
                          {#if attachmentIsImage(file)}
                            <img src={attachmentImageURL(file)} alt={file.name} class="max-h-56 max-w-full object-contain" loading="lazy" />
                          {/if}
                          <span class="flex items-center gap-2 px-3 py-2 text-sm"><FileText size={16} class="shrink-0" /><span class="min-w-0 max-w-56 truncate">{file.name}</span><Download size={14} class="shrink-0" /></span>
                        </button>
                      {/each}
                    </div>
                  {/if}
                  {#if getMessageText(msg.data)}<div class="px-4 py-2.5 bg-gray-900 dark:bg-accent text-white dark:text-gray-950 whitespace-pre-wrap break-words text-base sm:text-sm leading-relaxed">{getMessageText(msg.data)}</div>{/if}
                </div>
              </div>

            {:else if msg.role === 'assistant'}
              {@const text = getMessageText(msg.data)}
              {@const reasoning = getMessageReasoning(msg.data)}
              {@const toolCalls = getToolCalls(msg.data)}
              {@const reasoningId = `reason-${msg.id}`}
              {@const showSource = rawSourceMode[msg.id]}
              <!-- Assistant bubble -->
              <div class="flex gap-3 group">
                <div class="flex-1 min-w-0">
                  <div class="flex flex-wrap items-center gap-x-2 gap-y-1 mb-1">
                    <span class="text-xs font-semibold text-emerald-700 dark:text-emerald-400 break-words">{currentAgent?.name || 'Assistant'}</span>
                    <span class="text-xs text-gray-500 dark:text-dark-text-secondary tabular-nums whitespace-nowrap">{formatTime(msg.created_at)}</span>
                    {#if text}
                      <button onclick={() => copyMessage(text)} class="ml-auto flex items-center gap-1 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text" title="Copy response"><Copy size={13} /> Copy</button>
                      <button
                        type="button"
                        onclick={() => { rawSourceMode[msg.id] = !rawSourceMode[msg.id]; }}
                        class="flex items-center gap-1 px-2 py-1 text-xs rounded border border-gray-200 dark:border-dark-border text-gray-500 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated"
                        title={showSource ? 'Show rendered markdown' : 'Show raw source'}
                      >
                        {#if showSource}
                          <Eye size={11} />
                          <span>rendered</span>
                        {:else}
                          <Code size={11} />
                          <span>source</span>
                        {/if}
                      </button>
                    {/if}
                  </div>

                  <!-- Reasoning / thinking (collapsible) -->
                  {#if reasoning}
                    <!-- svelte-ignore a11y_click_events_have_key_events -->
                    <!-- svelte-ignore a11y_no_static_element_interactions -->
                    <div class="mb-2 border border-gray-200 dark:border-dark-border px-3 py-2 rounded-md">
                      <button aria-expanded={!!expandedTools[reasoningId]} class="w-full flex items-center gap-2 text-sm text-gray-600 dark:text-dark-text-secondary text-left" onclick={() => { expandedTools[reasoningId] = !expandedTools[reasoningId]; }}>
                        <Brain size={12} />
                        <span class="font-medium">Reasoning</span>
                        <ChevronDown size={14} class={`ml-auto ${expandedTools[reasoningId] ? '' : '-rotate-90'}`} />
                      </button>
                      {#if expandedTools[reasoningId]}
                        <div class="mt-1 text-[12px] text-purple-900 dark:text-purple-200 whitespace-pre-wrap italic opacity-90">{reasoning}</div>
                      {/if}
                    </div>
                  {/if}

                  <!-- Main text body: rendered markdown OR raw source -->
                  {#if text}
                    {#if showSource}
                      <pre class="px-3 py-2 rounded-2xl rounded-tl-sm bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border text-[12px] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-dark-text max-h-[32rem] overflow-y-auto">{text}</pre>
                    {:else}
                      <Markdown
                        source={text}
                        enhance
                        class="px-4 py-2.5 text-base sm:text-sm leading-relaxed bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border-subtle shadow-sm text-gray-900 dark:text-dark-text [overflow-wrap:anywhere]"
                      />
                    {/if}
                  {:else if !reasoning && toolCalls.length === 0}
                    <p class="text-sm text-gray-500 dark:text-dark-text-secondary italic">The model returned no text content.</p>
                  {/if}

                  <!-- Tool call summary -->
                  {#if showToolActivity && toolCalls.length > 0}
                    <div class="mt-1.5 space-y-1">
                      {#each toolCalls as tc (tc.id || tc.name)}
                        {@const tcId = `tc-${msg.id}-${tc.id || tc.name}`}
                        <!-- svelte-ignore a11y_click_events_have_key_events -->
                        <!-- svelte-ignore a11y_no_static_element_interactions -->
                        <div class="rounded-md border border-yellow-300 dark:border-yellow-700/60 bg-yellow-50/60 dark:bg-yellow-950/20">
                          <button aria-expanded={!!expandedTools[tcId]} class="w-full flex items-center gap-1.5 px-3 py-2 text-xs text-left text-yellow-800 dark:text-yellow-300" onclick={() => { expandedTools[tcId] = !expandedTools[tcId]; }}>
                            <Wrench size={11} />
                            <span class="font-mono font-semibold break-all">{tc.name || '(tool)'}</span>
                            {#if tc.id}<span class="text-[10px] opacity-60 font-mono">{tc.id.slice(0, 10)}</span>{/if}
                            <ChevronDown size={14} class={`ml-auto shrink-0 ${expandedTools[tcId] ? '' : '-rotate-90'}`} />
                          </button>
                          {#if expandedTools[tcId] && tc.args}
                            <pre class="mx-2 mb-2 px-2 py-1.5 text-[11px] font-mono whitespace-pre-wrap break-all bg-white dark:bg-dark-base rounded border border-yellow-200 dark:border-yellow-800/40 max-h-64 overflow-y-auto">{tc.args}</pre>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              </div>

            {:else if msg.role === 'tool'}
              {@const toolText = getMessageText(msg.data)}
              {@const pretty = prettyJSON(toolText)}
              {@const toolId = `tool-${msg.id}`}
              <!-- Tool result row -->
              <div class="flex gap-3 group">
                <div class="shrink-0 w-7 h-7 rounded-full bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted flex items-center justify-center">
                  <Terminal size={13} />
                </div>
                <div class="flex-1 min-w-0">
                  <!-- svelte-ignore a11y_click_events_have_key_events -->
                  <!-- svelte-ignore a11y_no_static_element_interactions -->
                  <button aria-expanded={!!expandedTools[toolId]} class="w-full flex flex-wrap items-center gap-2 mb-1 py-1 text-left" onclick={() => { expandedTools[toolId] = !expandedTools[toolId]; }}>
                    <span class="text-[11px] font-semibold text-gray-600 dark:text-dark-text-secondary">Tool result</span>
                    {#if msg.data.tool_call_id}
                      <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{msg.data.tool_call_id.slice(0, 12)}</span>
                    {/if}
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">· {toolText.length} chars{pretty.isJSON ? ' · json' : ''}</span>
                    <ChevronDown size={14} class={`ml-auto text-gray-500 dark:text-dark-text-secondary ${expandedTools[toolId] ? '' : '-rotate-90'}`} />
                  </button>
                  {#if expandedTools[toolId]}
                    <pre class="text-[11px] font-mono text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap break-all bg-gray-50 dark:bg-dark-base p-2.5 rounded-md border border-gray-200 dark:border-dark-border max-h-96 overflow-y-auto">{pretty.pretty}</pre>
                  {:else}
                    <pre class="text-[11px] font-mono text-gray-500 dark:text-dark-text-muted whitespace-pre-wrap break-all bg-gray-50 dark:bg-dark-base/50 px-2.5 py-1.5 rounded-md border border-gray-200 dark:border-dark-border/60 line-clamp-2 overflow-hidden">{toolText.slice(0, 240)}{toolText.length > 240 ? '…' : ''}</pre>
                  {/if}
                </div>
              </div>

            {:else if msg.role === 'system'}
              <!-- System message (hint banner) -->
              <div class="px-3 py-2 rounded-md bg-amber-50 dark:bg-amber-950/20 border border-amber-200 dark:border-amber-800/40 text-[12px] text-amber-800 dark:text-amber-300">
                <div class="text-[10px] font-semibold uppercase tracking-wider mb-0.5 opacity-70">System</div>
                <div class="whitespace-pre-wrap">{getMessageText(msg.data)}</div>
              </div>
            {/if}
          {/each}

          {#if turnError}
            <div role="alert" class="rounded-md border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/30 p-3 text-sm text-red-800 dark:text-red-300">{turnError}</div>
          {/if}
          {#if missingFinalReply}
            <div class="flex flex-wrap items-center gap-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-3 text-sm text-gray-600 dark:text-dark-text-secondary">
              <span>This saved turn has tool activity but no final reply.</span>
              <button disabled={!!inputText.trim() || attachments.length > 0 || readingAttachments} class="font-medium underline underline-offset-4 disabled:opacity-40" onclick={() => { inputText = 'Please summarize the results of the work above in the language of my previous messages. Reply directly to me without running any additional tools.'; void handleSend(); }}>Ask for a summary</button>
            </div>
          {/if}
          {#if sending && !streamContent && toolEvents.length === 0 && !pendingConfirmation}
            <div role="status" class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-text-secondary"><Loader2 size={16} class="animate-spin motion-reduce:animate-none" /> Waiting for {currentAgent?.name || 'the agent'}…</div>
          {/if}

          <!-- Retry button (shown after last message when not streaming) -->
          {#if messages.some(m => m.role === 'user') && !sending && !loadingMessages}
            <div class="flex justify-start py-1">
              <button
                onclick={retryLastMessage}
                class="flex items-center gap-1.5 px-2 py-1.5 text-xs text-gray-500 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                title="Retry last message"
              >
                <RotateCcw size={11} />
                Retry
              </button>
            </div>
          {/if}

          <!-- Live tool events (while streaming) -->
          {#if toolEvents.length > 0 && !showToolActivity}
            <div role="status" class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-text-secondary">
              {#if sending}<Loader2 size={14} class="animate-spin motion-reduce:animate-none" />{:else}<Wrench size={14} />{/if}
              <span>{sending ? 'Working with tools…' : 'Tool activity saved.'}</span>
              <button class="underline underline-offset-4" onclick={() => { showToolActivity = true; }}>Show activity</button>
            </div>
          {/if}
          {#if toolEvents.length > 0 && showToolActivity}
            <div class="flex gap-3">
              <div class="shrink-0 w-7 h-7 rounded-full bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400 flex items-center justify-center">
                <Wrench size={13} />
              </div>
              <div class="flex-1 min-w-0 space-y-1">
                {#each toolEvents as evt}
                  {#if evt.type === 'call'}
                    <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] rounded-md bg-yellow-50 dark:bg-yellow-950/20 border border-yellow-200 dark:border-yellow-800/40 text-yellow-800 dark:text-yellow-300">
                      <Loader2 size={11} class="animate-spin" />
                      <span class="font-mono font-semibold">{evt.name}</span>
                      <span class="text-[10px] opacity-60">running…</span>
                    </div>
                  {:else}
                    {@const evtResult = evt.result || ''}
                    {@const evtPretty = prettyJSON(evtResult)}
                    {@const evtId = `stream-${evt.id || evt.name}`}
                    <!-- svelte-ignore a11y_click_events_have_key_events -->
                    <!-- svelte-ignore a11y_no_static_element_interactions -->
                    <div class="rounded-md border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-elevated">
                      <button aria-expanded={!!expandedTools[evtId]} class="w-full flex flex-wrap items-center gap-1.5 px-3 py-2 text-xs text-left" onclick={() => { expandedTools[evtId] = !expandedTools[evtId]; }}>
                        <Check size={11} class="text-green-600 dark:text-green-400" />
                        <span class="font-mono font-semibold text-gray-700 dark:text-dark-text">{evt.name}</span>
                        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">· {evtResult.length} chars{evtPretty.isJSON ? ' · json' : ''}</span>
                        <ChevronDown size={14} class={`ml-auto text-gray-500 dark:text-dark-text-secondary ${expandedTools[evtId] ? '' : '-rotate-90'}`} />
                      </button>
                      {#if expandedTools[evtId]}
                        <pre class="mx-2 mb-2 px-2 py-1.5 text-[11px] font-mono whitespace-pre-wrap break-all bg-gray-50 dark:bg-dark-base rounded border border-gray-200 dark:border-dark-border max-h-80 overflow-y-auto">{evtPretty.pretty}</pre>
                      {:else if evtResult}
                        <div class="mx-2 mb-1.5 px-2 py-1 text-[11px] font-mono text-gray-500 dark:text-dark-text-muted truncate">{evtResult.slice(0, 200)}{evtResult.length > 200 ? '…' : ''}</div>
                      {/if}
                    </div>
                  {/if}
                {/each}
              </div>
            </div>
          {/if}

          <!-- Tool confirmation prompt -->
          {#if pendingConfirmation}
            <div class="py-3 px-4 my-1 border border-orange-300 dark:border-orange-800 bg-orange-50 dark:bg-orange-950/30 rounded-md">
              <div class="flex items-center gap-1.5 text-[12px] font-semibold text-orange-700 dark:text-orange-400 mb-1.5">
                <ShieldCheck size={14} />
                <span>Tool confirmation required</span>
              </div>
              <div class="text-[11px] text-gray-700 dark:text-dark-text-secondary mb-1">
                The agent wants to execute <span class="font-mono font-bold text-orange-700 dark:text-orange-300">{pendingConfirmation.toolName}</span>
              </div>
              <details class="mb-2">
                <summary class="text-[10px] text-gray-500 dark:text-dark-text-muted cursor-pointer hover:text-gray-700 dark:hover:text-dark-text-secondary">
                  Show arguments
                </summary>
                <pre class="text-[10px] text-gray-600 dark:text-dark-text-secondary whitespace-pre-wrap break-all mt-1 max-h-40 overflow-y-auto bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded p-2 font-mono">{(() => { try { return JSON.stringify(JSON.parse(pendingConfirmation.arguments), null, 2); } catch { return pendingConfirmation.arguments; } })()}</pre>
              </details>
              <div class="flex items-center gap-2">
                <button
                  onclick={() => handleConfirmation(true)}
                  class="flex items-center gap-1 px-3 py-1 text-[11px] font-medium rounded bg-green-600 hover:bg-green-700 text-white transition-colors"
                >
                  <ShieldCheck size={12} />
                  Approve
                </button>
                <button
                  onclick={() => handleConfirmation(false)}
                  class="flex items-center gap-1 px-3 py-1 text-[11px] font-medium rounded bg-red-500 hover:bg-red-600 text-white transition-colors"
                >
                  <ShieldX size={12} />
                  Reject
                </button>
              </div>
            </div>
          {/if}
          {#if streamContent}
            {@const streamShowSource = rawSourceMode['__streaming']}
            <div class="flex gap-3 group">
              <div class="flex-1 min-w-0">
                <div class="flex flex-wrap items-center gap-2 mb-1">
                  <span class="text-xs font-semibold text-emerald-700 dark:text-emerald-400">{currentAgent?.name || 'Assistant'}</span>
                  {#if sending}
                    <Loader2 size={11} class="animate-spin text-gray-400" />
                  {/if}
                  <button
                    type="button"
                    onclick={() => { rawSourceMode['__streaming'] = !rawSourceMode['__streaming']; }}
                    class="ml-auto flex items-center gap-1 px-2 py-1 text-xs rounded border border-gray-200 dark:border-dark-border text-gray-500 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated"
                    title={streamShowSource ? 'Show rendered markdown' : 'Show raw source'}
                  >
                    {#if streamShowSource}
                      <Eye size={11} />
                      <span>rendered</span>
                    {:else}
                      <Code size={11} />
                      <span>source</span>
                    {/if}
                  </button>
                </div>
                {#if streamShowSource}
                  <pre class="px-3 py-2 rounded-2xl rounded-tl-sm bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border text-[12px] font-mono whitespace-pre-wrap break-words text-gray-800 dark:text-dark-text max-h-[32rem] overflow-y-auto">{streamContent}</pre>
                {:else}
                  <Markdown
                    source={streamContent}
                    class="px-4 py-2.5 text-base sm:text-sm leading-relaxed bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border-subtle shadow-sm text-gray-900 dark:text-dark-text [overflow-wrap:anywhere]"
                  />
                {/if}
              </div>
            </div>
          {/if}

        </div>
      {/if}
    </div>

    {#if !nearBottom && selectedSessionId}
      <div class="flex justify-center py-2"><button onclick={() => { nearBottom = true; scrollToBottom(); }} class="flex items-center gap-2 rounded-full border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-elevated px-4 py-2 text-sm shadow-sm"><ArrowDown size={16} /> Latest messages</button></div>
    {/if}
    <!-- Input bar -->
    <section aria-label="Message composer" ondragover={(e) => { if (e.dataTransfer?.types.includes('Files')) { e.preventDefault(); draggingFiles = true; } }} ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) draggingFiles = false; }} ondrop={(e) => { e.preventDefault(); draggingFiles = false; void addAttachments(Array.from(e.dataTransfer?.files || [])); }} class={['relative shrink-0 border-t border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface', draggingFiles ? 'ring-2 ring-inset ring-accent' : '']}>
      <!-- Slash command menu -->
      {#if showSlashMenu && filteredSlashCommands.length > 0}
        <div class="absolute bottom-full left-0 right-0 mx-3 mb-1 bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border rounded-lg shadow-lg overflow-hidden z-10">
          {#each filteredSlashCommands as cmd}
            <button
              onclick={() => handleSlashCommand(cmd.cmd)}
              class="w-full text-left px-3 py-2 text-[12px] hover:bg-gray-50 dark:hover:bg-dark-elevated flex items-center gap-3 transition-colors"
            >
              <span class="font-mono font-bold text-gray-700 dark:text-dark-text w-20">{cmd.cmd}</span>
              <span class="text-gray-500 dark:text-dark-text-secondary">{cmd.desc}</span>
            </button>
          {/each}
        </div>
      {/if}

      <!-- Agent picker dropdown -->
      {#if showAgentPicker && !selectedSession?.config?.organization_chat}
        <div class="absolute bottom-full left-0 right-0 mx-3 mb-1 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded-lg shadow-lg overflow-hidden z-10">
          <div class="px-3 py-2 text-sm font-medium text-gray-600 dark:text-dark-text-secondary border-b border-gray-100 dark:border-dark-border/50">Switch agent</div>
          {#each agents as agent (agent.id)}
            <button
              onclick={() => switchAgent(agent.id)}
              class={[
                'w-full text-left px-3 py-2 text-[12px] hover:bg-gray-50 dark:hover:bg-dark-elevated flex items-center gap-2 transition-colors',
                selectedSession?.agent_id === agent.id ? 'bg-gray-50 dark:bg-dark-elevated' : '',
              ]}
            >
              <img src={agentAvatar(agent.config.avatar_seed, agent.name, 20)} alt="" class="w-5 h-5 rounded-full shrink-0 bg-gray-100 dark:bg-dark-elevated" />
              <div>
                <span class="font-medium text-gray-700 dark:text-dark-text">{agent.name}</span>
                {#if agent.scope === 'personal'}
                  <span class="ml-1.5 px-1 py-0 text-[10px] font-medium bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-400 border border-blue-200 dark:border-blue-900/40" title="Personal agent — only visible to you">personal</span>
                {:else if agent.scope === 'global'}
                  <span class="ml-1.5 px-1 py-0 text-[10px] font-medium bg-purple-50 dark:bg-purple-900/20 text-purple-700 dark:text-purple-400 border border-purple-200 dark:border-purple-900/40" title="Global agent — available in every workspace">global</span>
                {/if}
                {#if agent.config.group}<span class="ml-2 text-xs text-gray-500 dark:text-dark-text-secondary">{agent.config.group}</span>{/if}
                {#if agent.config.description}
                  <span class="text-gray-400 dark:text-dark-text-muted ml-1">— {agent.config.description}</span>
                {/if}
              </div>
              {#if selectedSession?.agent_id === agent.id}
                <span class="ml-auto text-[10px] text-green-500 font-medium">active</span>
              {/if}
            </button>
          {/each}
          {#if agents.length === 0}
            <div class="px-3 py-3 text-[11px] text-gray-400 text-center">No agents configured</div>
          {/if}
        </div>
      {/if}

      <div class="flex flex-wrap items-center gap-2 w-full p-3 sm:p-4">
        <input bind:this={fileInput} type="file" multiple class="hidden" aria-label="Attach photos or files" onchange={(e) => { void addAttachments(Array.from(e.currentTarget.files || [])); e.currentTarget.value = ''; }} />
        {#if attachments.length || readingAttachments || attachmentError || draggingFiles}
          <div class="order-first basis-full space-y-2">
            {#if draggingFiles}<p role="status" class="text-sm text-gray-600 dark:text-dark-text-secondary">Drop files here to attach them.</p>{/if}
            {#if attachments.length}
              <ul class="flex flex-wrap gap-2" aria-label="Attachments ready to send">
                {#each attachments as file, i}
                  <li class="flex min-w-0 max-w-full items-center gap-2 rounded-md border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base py-2 pl-2 pr-1">
                    {#if attachmentIsImage(file)}<img src={attachmentImageURL(file)} alt="" class="size-10 rounded object-cover" />{:else}<FileText size={20} class="mx-2 shrink-0 text-gray-500 dark:text-dark-text-secondary" />{/if}
                    <div class="min-w-0"><p class="max-w-44 truncate text-sm text-gray-900 dark:text-dark-text" title={file.name}>{file.name}</p><p class="text-xs text-gray-500 dark:text-dark-text-secondary">{attachmentSize(attachmentBytes(file))}</p></div>
                    <button class={`${composerControl} w-10 text-gray-500 hover:bg-gray-200 dark:hover:bg-dark-elevated`} disabled={sending || readingAttachments} aria-label={`Remove ${file.name}`} onclick={() => { attachments = attachments.filter((_, index) => index !== i); attachmentError = ''; }}><X size={16} /></button>
                  </li>
                {/each}
              </ul>
            {/if}
            {#if readingAttachments}<p role="status" class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-text-secondary"><Loader2 size={16} class="animate-spin motion-reduce:animate-none" /> Reading files…</p>{/if}
            {#if attachmentError}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{attachmentError}</p>{/if}
          </div>
        {/if}
        <div class="order-first basis-full">
          <textarea
            bind:this={inputEl}
            bind:value={inputText}
            oninput={handleInput}
            onkeydown={handleKeydown}
            onpaste={pasteAttachments}
            aria-label="Message to agent"
            aria-describedby="session-composer-help"
            placeholder={attachments.length ? 'Add a message about these files…' : selectedSessionId ? 'Message… (/ for commands)' : 'Start typing to create a session…'}
            rows={2}
            disabled={sending || loadingMessages}
            class="block w-full min-w-0 resize-y max-h-48 rounded-md border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base px-3 py-2 text-base leading-6 text-gray-900 dark:text-dark-text placeholder:text-gray-500 dark:placeholder:text-dark-text-secondary focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
          ></textarea>
        </div>
        <!-- Agent pill -->
        <div class="min-w-0 basis-full lg:basis-auto lg:max-w-44">
        {#if selectedSession}
          <button
            onclick={() => { showAgentPicker = !showAgentPicker; showSlashMenu = false; }}
            disabled={sending || loadingMessages || !!selectedSession.config?.organization_chat}
            aria-expanded={showAgentPicker}
            class={`${secondaryControl} max-w-full px-3`}
            title="Switch agent (/agents)"
          >
            <Bot size={16} class="shrink-0" />
            <span class="min-w-0 truncate">{currentAgent?.name || 'Agent'}</span>
            <ChevronDown size={14} class="shrink-0" />
          </button>
        {:else if pendingAgent}
          <span class={`${secondaryControl} max-w-full px-3`}>
            <Bot size={16} class="shrink-0" />
            <span class="min-w-0 truncate">{pendingAgent.name}</span>
          </span>
        {/if}
        </div>
        <button onclick={() => fileInput?.click()} disabled={sending || loadingMessages || readingAttachments || attachments.length >= CHAT_ATTACHMENT_COUNT} class={`${secondaryControl} w-10`} title="Attach files" aria-label="Attach photos or files"><Paperclip size={18} /></button>
        <VoiceInput contextKey={voiceContext} disabled={sending || loadingMessages} bind:recording bind:transcribing ontext={text => { inputText = (inputText ? inputText + ' ' : '') + text; inputEl?.focus(); }} />

        {#if sending}
          <button onclick={stopGeneration} class={`${composerControl} ml-auto min-w-20 px-3 bg-red-600 text-white hover:bg-red-700`} title="Stop generation">
            <Square size={16} /> Stop
          </button>
        {:else}
          <button
            onclick={handleSend}
            disabled={(!inputText.trim() && !attachments.length) || loadingMessages || readingAttachments || recording || transcribing || (!selectedSessionId && !agents.length)}
            class={`${composerControl} ml-auto min-w-20 px-3 bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover`}
            title="Send"
          >
            <Send size={16} /> Send
          </button>
        {/if}
        <p id="session-composer-help" class="basis-full text-xs leading-5 text-gray-500 dark:text-dark-text-secondary"><span class="hidden sm:inline">Enter to send · Shift + Enter for a new line.</span>{' '}Up to 4 files · 5 MB each · 8 MB total. Supported file formats depend on the agent’s model.</p>
      </div>
    </section>
  </div>
</div>
