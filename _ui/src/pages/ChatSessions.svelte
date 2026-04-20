<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import {
    listChatSessions,
    createChatSession,
    deleteChatSession,
    updateChatSession,
    listChatMessages,
    clearChatMessages,
    sendMessage,
    confirmToolCall,
    type ChatSession,
    type ChatMessage,
  } from '@/lib/api/chat-sessions';
  import { Send, Square, Plus, Loader2, Trash2, RotateCcw, Bot, ChevronDown, ShieldCheck, ShieldX, Mic, MicOff, User, Wrench, Brain, Terminal, Check, Code, Eye } from 'lucide-svelte';
  import axios from 'axios';
  import { agentAvatar } from '@/lib/helper/avatar';
  import Markdown from '@/lib/components/Markdown.svelte';

  storeNavbar.title = 'Sessions';

  // ─── State ───

  let sessions = $state<ChatSession[]>([]);
  let agents = $state<Agent[]>([]);
  let selectedSessionId = $state<string | null>(null);
  let messages = $state<ChatMessage[]>([]);
  let streamContent = $state('');
  let toolEvents = $state<any[]>([]);
  let expandedTools = $state<Record<string, boolean>>({});
  // Per-message toggle: when true, render the raw markdown source instead
  // of the rendered HTML so users can inspect / copy the original content.
  let rawSourceMode = $state<Record<string, boolean>>({});

  // Voice recording
  let voiceMethod = $state(typeof localStorage !== 'undefined' ? (localStorage.getItem('at-voice-method') || 'openai') : 'openai');
  let voiceModel = $state(typeof localStorage !== 'undefined' ? (localStorage.getItem('at-voice-model') || 'tiny') : 'tiny');
  let showVoiceSettings = $state(false);

  function voiceLabel(): string {
    if (voiceMethod === 'openai') return 'API';
    if (voiceMethod === 'faster-whisper') return `fw:${voiceModel}`;
    return voiceModel;
  }
  let recording = $state(false);
  let transcribing = $state(false);
  let mediaRecorder = $state<MediaRecorder | null>(null);
  let recordingTimer = $state<ReturnType<typeof setInterval> | null>(null);
  let recordingDuration = $state(0);

  async function startRecording() {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });

      // Pick a supported mime type
      let mimeType = 'audio/webm';
      if (!MediaRecorder.isTypeSupported(mimeType)) {
        mimeType = 'audio/mp4';
        if (!MediaRecorder.isTypeSupported(mimeType)) {
          mimeType = ''; // let browser pick default
        }
      }

      const recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      const chunks: BlobPart[] = [];

      recorder.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data);
      };

      recorder.onstop = () => {
        stream.getTracks().forEach(t => t.stop());
        const type = recorder.mimeType || 'audio/webm';
        const ext = type.includes('mp4') ? '.m4a' : '.webm';
        const blob = new Blob(chunks, { type });
        transcribeBlob(blob, ext);
      };

      recorder.start(1000); // request data every second for reliability
      mediaRecorder = recorder;
      recording = true;
      recordingDuration = 0;
      recordingTimer = setInterval(() => { recordingDuration++; }, 1000);
    } catch (e) {
      addToast('Microphone access denied', 'alert');
    }
  }

  function stopRecording() {
    if (mediaRecorder && mediaRecorder.state === 'recording') {
      mediaRecorder.stop();
    }
    recording = false;
    if (recordingTimer) { clearInterval(recordingTimer); recordingTimer = null; }
    recordingDuration = 0;
  }

  async function transcribeBlob(blob: Blob, ext: string) {
    transcribing = true;
    try {
      const form = new FormData();
      form.append('file', blob, `voice${ext}`);
      const params = voiceMethod !== 'openai' ? `?method=${voiceMethod}&model=${voiceModel}` : '';
      const res = await axios.post(`api/v1/audio/transcribe${params}`, form);
      const text = res.data?.text;
      if (text) {
        inputText = (inputText ? inputText + ' ' : '') + text;
        inputEl?.focus();
      } else {
        addToast('Transcription returned empty', 'warn');
      }
    } catch (e: any) {
      addToast('Transcription failed: ' + (e?.response?.data || e.message), 'alert');
    } finally {
      transcribing = false;
    }
  }

  function formatRecordingTime(seconds: number): string {
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}:${s.toString().padStart(2, '0')}`;
  }
  let inputText = $state('');
  let loading = $state(false);
  let sending = $state(false);
  let showAgentPicker = $state(false);
  let showSlashMenu = $state(false);
  let slashFilter = $state('');
  let pendingConfirmation = $state<{
    toolName: string;
    toolId: string;
    arguments: string;
  } | null>(null);
  let abortController: AbortController | null = null;
  let messagesEnd: HTMLDivElement;
  let inputEl: HTMLTextAreaElement;

  // ─── Derived ───

  let selectedSession = $derived(sessions.find(s => s.id === selectedSessionId) || null);
  let currentAgent = $derived(selectedSession ? agents.find(a => a.id === selectedSession.agent_id) : null);
  // Agent pre-selected for new session creation (before a session exists).
  let pendingAgentId = $state<string | null>(null);
  let pendingAgent = $derived(pendingAgentId ? agents.find(a => a.id === pendingAgentId) : null);

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
      addToast(e.message || 'Failed to load sessions', 'error');
    } finally {
      loading = false;
    }
  }

  async function loadAgents() {
    try {
      const res = await listAgents();
      agents = res.data || [];
    } catch {
      // Agents may not be configured
    }
  }

  async function loadMessages(sessionId: string) {
    try {
      messages = await listChatMessages(sessionId);
    } catch (e: any) {
      addToast(e.message || 'Failed to load messages', 'error');
    }
  }

  async function selectSession(id: string) {
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    selectedSessionId = id;
    streamContent = '';
    toolEvents = [];
    sending = false;
    showAgentPicker = false;
    showSlashMenu = false;
    await loadMessages(id);
    scrollToBottom();
    inputEl?.focus();
  }

  function scrollToBottom() {
    setTimeout(() => {
      messagesEnd?.scrollIntoView({ behavior: 'smooth' });
    }, 50);
  }

  // ─── Actions ───

  /** Create a new session with the first available agent (or specified). */
  async function quickCreateSession(agentId?: string) {
    const aid = agentId || (agents.length > 0 ? agents[0].id : '');
    if (!aid) {
      addToast('No agents configured. Create an agent first.', 'error');
      return;
    }
    try {
      const session = await createChatSession({
        agent_id: aid,
        name: 'New Session',
      });
      sessions = [session, ...sessions];
      await selectSession(session.id);
    } catch (e: any) {
      addToast(e.message || 'Failed to create session', 'error');
    }
  }

  async function handleDeleteSession(id: string) {
    try {
      await deleteChatSession(id);
      sessions = sessions.filter(s => s.id !== id);
      if (selectedSessionId === id) {
        selectedSessionId = null;
        messages = [];
      }
    } catch (e: any) {
      addToast(e.message || 'Failed to delete session', 'error');
    }
  }

  async function switchAgent(agentId: string) {
    if (!selectedSessionId) return;
    showAgentPicker = false;

    try {
      const updated = await updateChatSession(selectedSessionId, { agent_id: agentId } as any);
      sessions = sessions.map(s => s.id === updated.id ? updated : s);
      addToast(`Switched to ${agents.find(a => a.id === agentId)?.name || 'agent'}`, 'success');
    } catch (e: any) {
      addToast(e.message || 'Failed to switch agent', 'error');
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
        quickCreateSession();
        break;
      case '/clear':
        if (selectedSessionId) {
          clearChatMessages(selectedSessionId).then(() => {
            messages = [];
            addToast('Messages cleared');
          }).catch(() => addToast('Failed to clear messages', 'error'));
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
    if (!inputText.trim() || sending) return;

    // Handle slash commands.
    if (inputText.startsWith('/')) {
      const match = slashCommands.find(c => c.cmd === inputText.trim());
      if (match) {
        handleSlashCommand(match.cmd);
        return;
      }
    }

    // Auto-create session if none selected — use the pre-selected agent if any.
    if (!selectedSessionId) {
      await quickCreateSession(pendingAgentId || undefined);
      pendingAgentId = null;
      if (!selectedSessionId) return;
    }

    const content = inputText.trim();
    inputText = '';
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
        data: { content },
        created_at: nowIso,
      },
    ];
    // Optimistically bump the session's updated_at so it floats to the top
    // of the sidebar immediately (the backend will persist the same bump
    // when the message is stored).
    bumpSessionToTop(selectedSessionId!, nowIso);
    scrollToBottom();

    abortController = sendMessage(
      selectedSessionId!,
      content,
      (event) => {
        if (event.type === 'content') {
          streamContent += event.content;
          scrollToBottom();
        } else if (event.type === 'tool_call') {
          toolEvents = [...toolEvents, { type: 'call', name: event.tool_name, id: event.tool_id }];
          scrollToBottom();
        } else if (event.type === 'tool_result') {
          toolEvents = [...toolEvents, { type: 'result', name: event.tool_name, id: event.tool_id, result: event.result }];
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
        addToast(error, 'error');
        sending = false;
        abortController = null;
        pendingConfirmation = null;
      },
      async () => {
        sending = false;
        abortController = null;
        pendingConfirmation = null;
        if (selectedSessionId) {
          await loadMessages(selectedSessionId);
        }
        streamContent = '';
        toolEvents = [];
        scrollToBottom();
      },
    );
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      // If slash menu is open and there's an exact or single match, select it.
      if (showSlashMenu && filteredSlashCommands.length > 0) {
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
    if (abortController) {
      abortController.abort();
      abortController = null;
      sending = false;
      pendingConfirmation = null;
    }
  }

  async function retryLastMessage() {
    if (sending || !selectedSessionId) return;

    // Find the last user message
    const lastUserMsg = [...messages].reverse().find(m => m.role === 'user');
    if (!lastUserMsg) return;

    const content = getMessageText(lastUserMsg.data);
    if (!content) return;

    sending = true;
    streamContent = '';
    toolEvents = [];
    bumpSessionToTop(selectedSessionId!);

    abortController = sendMessage(
      selectedSessionId!,
      content,
      (event) => {
        if (event.type === 'content') {
          streamContent += event.content;
          scrollToBottom();
        } else if (event.type === 'tool_call') {
          toolEvents = [...toolEvents, { type: 'call', name: event.tool_name, id: event.tool_id }];
          scrollToBottom();
        } else if (event.type === 'tool_result') {
          toolEvents = [...toolEvents, { type: 'result', name: event.tool_name, id: event.tool_id, result: event.result }];
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
        addToast(error, 'error');
        sending = false;
        abortController = null;
        pendingConfirmation = null;
      },
      async () => {
        sending = false;
        abortController = null;
        pendingConfirmation = null;
        if (selectedSessionId) {
          await loadMessages(selectedSessionId);
        }
        streamContent = '';
        toolEvents = [];
        scrollToBottom();
      },
    );
  }

  async function handleConfirmation(approved: boolean) {
    if (!pendingConfirmation || !selectedSessionId) return;
    const { toolId } = pendingConfirmation;
    pendingConfirmation = null;
    try {
      await confirmToolCall(selectedSessionId, toolId, approved);
    } catch (err: any) {
      addToast(err.message || 'Failed to send confirmation', 'error');
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
    if (typeof data.content === 'string') return data.content;
    if (Array.isArray(data.content)) {
      return data.content.filter((b: any) => b.type === 'text').map((b: any) => b.text).join('');
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

  // Init
  $effect(() => {
    loadSessions().then(() => {
      // Auto-select session from URL query param (e.g., ?session=abc from task chat).
      const hash = window.location.hash;
      const qIdx = hash.indexOf('?');
      if (qIdx !== -1) {
        const params = new URLSearchParams(hash.slice(qIdx + 1));
        const sessionParam = params.get('session');
        if (sessionParam) {
          selectSession(sessionParam);
          // Clean up the URL.
          window.location.hash = hash.slice(0, qIdx);
        }
      }
    });
    loadAgents();
  });
</script>

<svelte:head>
  <title>AT | Sessions</title>
</svelte:head>

<div class="flex h-full bg-gray-50 dark:bg-dark-base">
  <!-- Left: Session list -->
  <div class="w-56 flex-shrink-0 border-r border-gray-200 dark:border-dark-border flex flex-col bg-white dark:bg-dark-surface">
    <div class="flex items-center justify-between px-2 h-8 border-b border-gray-200 dark:border-dark-border">
      <span class="text-[11px] font-semibold text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Sessions</span>
      <button
        onclick={() => quickCreateSession()}
        class="p-0.5 text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary"
        title="New session (or type /new)"
      >
        <Plus size={13} />
      </button>
    </div>

    <div class="flex-1 overflow-y-auto">
      {#if loading}
        <div class="flex items-center justify-center py-6">
          <Loader2 size={14} class="animate-spin text-gray-400" />
        </div>
      {:else if sessions.length === 0}
        <div class="px-2 py-6 text-[11px] text-gray-400 dark:text-dark-text-muted text-center">
          Type to start chatting
        </div>
      {:else}
        {#each sessions as session (session.id)}
          <div
            onclick={() => selectSession(session.id)}
            onkeydown={(e) => { if (e.key === 'Enter') selectSession(session.id); }}
            role="button"
            tabindex="0"
            class={[
              'w-full text-left px-2 py-1.5 text-[11px] border-b border-gray-100 dark:border-dark-border/50 group flex items-center gap-1 cursor-pointer transition-colors',
              selectedSessionId === session.id
                ? 'bg-gray-100 dark:bg-dark-elevated border-l-2 border-l-gray-900 dark:border-l-accent'
                : 'hover:bg-gray-50 dark:hover:bg-dark-elevated/50 border-l-2 border-l-transparent',
            ]}
          >
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-1.5">
                <span class="truncate text-gray-700 dark:text-dark-text font-medium flex-1 min-w-0">{session.name || 'Untitled'}</span>
                <span class="text-[9px] text-gray-400 dark:text-dark-text-muted shrink-0 tabular-nums" title={session.updated_at || session.created_at}>
                  {formatRelative(session.updated_at || session.created_at)}
                </span>
              </div>
              <div class="truncate text-[10px] text-gray-400 dark:text-dark-text-muted">{getAgentName(session.agent_id)}</div>
            </div>
            <button
              onclick={(e) => { e.stopPropagation(); clearChatMessages(session.id).then(() => { if (selectedSessionId === session.id) messages = []; addToast('Messages cleared'); }).catch(() => addToast('Failed to clear', 'error')); }}
              class="opacity-0 group-hover:opacity-100 p-0.5 text-gray-300 hover:text-orange-400 transition-opacity"
              title="Clear messages"
            >
              <RotateCcw size={11} />
            </button>
            <button
              onclick={(e) => { e.stopPropagation(); handleDeleteSession(session.id); }}
              class="opacity-0 group-hover:opacity-100 p-0.5 text-gray-300 hover:text-red-400 transition-opacity"
              title="Delete"
            >
              <Trash2 size={11} />
            </button>
          </div>
        {/each}
      {/if}
    </div>
  </div>

  <!-- Right: Chat area -->
  <div class="flex-1 flex flex-col min-w-0">
    <!-- Top bar: current agent indicator -->
    {#if selectedSession}
      <div class="flex items-center h-8 px-3 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-dark-text-muted">
          <Bot size={12} />
          <span class="font-medium text-gray-700 dark:text-dark-text">{currentAgent?.name || 'Unknown agent'}</span>
          {#if currentAgent?.config?.model}
            <span class="text-gray-400 dark:text-dark-text-muted">· {currentAgent.config.model}</span>
          {/if}
          {#if currentAgent?.config?.provider}
            <span class="text-gray-400 dark:text-dark-text-muted">· {currentAgent.config.provider}</span>
          {/if}
        </div>
      </div>
    {/if}

    <!-- Messages area -->
    <div class="flex-1 overflow-y-auto text-[13px] leading-relaxed">
      {#if !selectedSessionId}
        <div class="flex items-center justify-center h-full text-gray-400 dark:text-dark-text-muted">
          <div class="text-center text-sm max-w-md">
            <p class="text-gray-500 dark:text-dark-text-secondary font-medium mb-2">Select an agent to start</p>
            {#if agents.length > 0}
              <div class="flex flex-wrap gap-2 justify-center mb-3">
                {#each agents as agent (agent.id)}
                  <button
                    onclick={() => { pendingAgentId = agent.id; inputEl?.focus(); }}
                    class={[
                      'flex items-center gap-1.5 px-3 py-1.5 text-[11px] rounded-lg border transition-all',
                      pendingAgentId === agent.id
                        ? 'border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white shadow-sm'
                        : 'border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:border-gray-400 dark:hover:border-dark-text-muted hover:bg-gray-50 dark:hover:bg-dark-elevated',
                    ]}
                  >
                    <img src={agentAvatar(agent.config.avatar_seed, agent.name, 16)} alt="" class="w-4 h-4 rounded-full bg-gray-100 dark:bg-dark-elevated" />
                    {agent.name}
                  </button>
                {/each}
              </div>
              <p class="text-[11px] text-gray-400 dark:text-dark-text-muted">{pendingAgentId ? 'Type a message to start chatting' : 'Pick an agent, then type a message'}</p>
            {:else}
              <p class="text-[11px]">No agents configured. Create an agent first.</p>
            {/if}
          </div>
        </div>
      {:else}
        <div class="max-w-3xl mx-auto px-4 py-4 space-y-4">
          {#each messages as msg (msg.id)}
            {#if msg.role === 'user'}
              <!-- User bubble -->
              <div class="flex gap-3 justify-end group">
                <div class="max-w-[80%] flex flex-col items-end">
                  <div class="flex items-center gap-2 mb-1">
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums">{formatTime(msg.created_at)}</span>
                    <span class="text-[11px] font-semibold text-blue-600 dark:text-blue-400">You</span>
                  </div>
                  <div class="px-3 py-2 rounded-2xl rounded-tr-sm bg-blue-600 text-white shadow-sm whitespace-pre-wrap break-words">{getMessageText(msg.data)}</div>
                </div>
                <div class="shrink-0 w-7 h-7 rounded-full bg-blue-600 text-white flex items-center justify-center shadow-sm">
                  <User size={14} />
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
                <div class="shrink-0 w-7 h-7 rounded-full bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 flex items-center justify-center shadow-sm">
                  <Bot size={14} />
                </div>
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2 mb-1">
                    <span class="text-[11px] font-semibold text-emerald-700 dark:text-emerald-400">{currentAgent?.name || 'Assistant'}</span>
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted tabular-nums">{formatTime(msg.created_at)}</span>
                    {#if text}
                      <button
                        type="button"
                        onclick={() => { rawSourceMode[msg.id] = !rawSourceMode[msg.id]; }}
                        class="ml-auto flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded border border-gray-200 dark:border-dark-border text-gray-500 dark:text-dark-text-muted hover:text-gray-800 dark:hover:text-dark-text hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors opacity-0 group-hover:opacity-100 focus:opacity-100"
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
                    <div class="mb-1.5 border-l-2 border-purple-300 dark:border-purple-700/60 pl-2 py-1 bg-purple-50/50 dark:bg-purple-950/20 rounded-r">
                      <div class="flex items-center gap-1 text-[11px] text-purple-700 dark:text-purple-300 cursor-pointer select-none" onclick={() => { expandedTools[reasoningId] = !expandedTools[reasoningId]; }}>
                        <Brain size={12} />
                        <span class="font-medium">Reasoning</span>
                        <span class="ml-auto text-[10px] opacity-60">{expandedTools[reasoningId] ? '▼' : '▶'}</span>
                      </div>
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
                        class="px-3 py-2 rounded-2xl rounded-tl-sm bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-[13px]"
                      />
                    {/if}
                  {/if}

                  <!-- Tool call summary -->
                  {#if toolCalls.length > 0}
                    <div class="mt-1.5 space-y-1">
                      {#each toolCalls as tc (tc.id || tc.name)}
                        {@const tcId = `tc-${msg.id}-${tc.id || tc.name}`}
                        <!-- svelte-ignore a11y_click_events_have_key_events -->
                        <!-- svelte-ignore a11y_no_static_element_interactions -->
                        <div class="rounded-md border border-yellow-300 dark:border-yellow-700/60 bg-yellow-50/60 dark:bg-yellow-950/20">
                          <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-yellow-800 dark:text-yellow-300 cursor-pointer" onclick={() => { expandedTools[tcId] = !expandedTools[tcId]; }}>
                            <Wrench size={11} />
                            <span class="font-mono font-semibold">{tc.name || '(tool)'}</span>
                            {#if tc.id}<span class="text-[10px] opacity-60 font-mono">{tc.id.slice(0, 10)}</span>{/if}
                            <span class="ml-auto text-[10px] opacity-60">{expandedTools[tcId] ? '▼' : '▶'}</span>
                          </div>
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
                  <div class="flex items-center gap-2 mb-1 cursor-pointer select-none" onclick={() => { expandedTools[toolId] = !expandedTools[toolId]; }}>
                    <span class="text-[11px] font-semibold text-gray-600 dark:text-dark-text-secondary">Tool result</span>
                    {#if msg.data.tool_call_id}
                      <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{msg.data.tool_call_id.slice(0, 12)}</span>
                    {/if}
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">· {toolText.length} chars{pretty.isJSON ? ' · json' : ''}</span>
                    <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted">{expandedTools[toolId] ? '▼ collapse' : '▶ expand'}</span>
                  </div>
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

          <!-- Retry button (shown after last message when not streaming) -->
          {#if messages.length > 0 && !sending && !streamContent}
            <div class="flex justify-start py-1">
              <button
                onclick={retryLastMessage}
                class="flex items-center gap-1 px-2 py-0.5 text-[11px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                title="Retry last message"
              >
                <RotateCcw size={11} />
                Retry
              </button>
            </div>
          {/if}

          <!-- Live tool events (while streaming) -->
          {#if toolEvents.length > 0}
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
                      <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] cursor-pointer select-none" onclick={() => { expandedTools[evtId] = !expandedTools[evtId]; }}>
                        <Check size={11} class="text-green-600 dark:text-green-400" />
                        <span class="font-mono font-semibold text-gray-700 dark:text-dark-text">{evt.name}</span>
                        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">· {evtResult.length} chars{evtPretty.isJSON ? ' · json' : ''}</span>
                        <span class="ml-auto text-[10px] text-gray-400 dark:text-dark-text-muted">{expandedTools[evtId] ? '▼' : '▶'}</span>
                      </div>
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
            <div class="py-2 px-3 my-1 border-l-2 border-orange-400 dark:border-orange-500 bg-orange-50 dark:bg-orange-950/30 rounded-r">
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
              <div class="shrink-0 w-7 h-7 rounded-full bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 flex items-center justify-center shadow-sm">
                <Bot size={14} />
              </div>
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2 mb-1">
                  <span class="text-[11px] font-semibold text-emerald-700 dark:text-emerald-400">{currentAgent?.name || 'Assistant'}</span>
                  {#if sending}
                    <Loader2 size={11} class="animate-spin text-gray-400" />
                  {/if}
                  <button
                    type="button"
                    onclick={() => { rawSourceMode['__streaming'] = !rawSourceMode['__streaming']; }}
                    class="ml-auto flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded border border-gray-200 dark:border-dark-border text-gray-500 dark:text-dark-text-muted hover:text-gray-800 dark:hover:text-dark-text hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors opacity-0 group-hover:opacity-100 focus:opacity-100"
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
                    class="px-3 py-2 rounded-2xl rounded-tl-sm bg-white dark:bg-dark-elevated border border-gray-200 dark:border-dark-border text-[13px]"
                  />
                {/if}
              </div>
            </div>
          {/if}

          <div bind:this={messagesEnd}></div>
        </div>
      {/if}
    </div>

    <!-- Input bar -->
    <div class="border-t border-gray-200 dark:border-dark-border bg-white dark:bg-dark-elevated relative">
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
      {#if showAgentPicker}
        <div class="absolute bottom-full left-0 right-0 mx-3 mb-1 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded-lg shadow-lg overflow-hidden z-10">
          <div class="px-3 py-1.5 text-[10px] font-semibold text-gray-400 dark:text-dark-text-muted uppercase tracking-wider border-b border-gray-100 dark:border-dark-border/50">Switch Agent</div>
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

      <div class="flex items-center gap-2 px-3 py-2">
        <!-- Agent pill -->
        {#if selectedSession}
          <button
            onclick={() => { showAgentPicker = !showAgentPicker; showSlashMenu = false; }}
            class="flex items-center gap-1 px-2 py-1 text-[11px] rounded-md border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors shrink-0"
            title="Switch agent (/agents)"
          >
            <Bot size={11} />
            <span class="max-w-[80px] truncate">{currentAgent?.name || '?'}</span>
            <ChevronDown size={10} />
          </button>
        {:else if pendingAgent}
          <span class="flex items-center gap-1 px-2 py-1 text-[11px] rounded-md border border-gray-900 dark:border-accent text-gray-700 dark:text-dark-text shrink-0">
            <Bot size={11} />
            <span class="max-w-[80px] truncate">{pendingAgent.name}</span>
          </span>
        {/if}

        <textarea
          bind:this={inputEl}
          bind:value={inputText}
          oninput={handleInput}
          onkeydown={handleKeydown}
          placeholder={selectedSessionId ? 'Message… (/ for commands)' : 'Start typing to create a session…'}
          rows={1}
          disabled={sending}
          class="flex-1 resize-none bg-transparent px-2 py-1 text-[13px] font-mono text-gray-800 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none disabled:opacity-50"
        ></textarea>

        <!-- Mic button with settings -->
        <div class="relative shrink-0">
          {#if transcribing}
            <div class="flex items-center gap-1 p-1 text-blue-500">
              <Loader2 size={14} class="animate-spin" />
              <span class="text-[10px]">...</span>
            </div>
          {:else if recording}
            <button
              onclick={stopRecording}
              class="flex items-center gap-1 p-1 text-red-500 hover:text-red-600 animate-pulse"
              title="Stop recording"
            >
              <MicOff size={14} />
              <span class="text-[10px] font-mono">{formatRecordingTime(recordingDuration)}</span>
            </button>
          {:else}
            <div class="flex items-center h-[22px]">
              <button
                onclick={startRecording}
                disabled={sending}
                class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-20"
                title="Voice input (click to record)"
              >
                <Mic size={14} />
              </button>
              <button
                onclick={() => { showVoiceSettings = !showVoiceSettings; }}
                class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary px-0.5 rounded hover:bg-gray-100 dark:hover:bg-dark-elevated"
                title="Voice settings"
              >
                {voiceLabel()}
              </button>
            </div>
          {/if}

          {#if showVoiceSettings}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div
              class="fixed inset-0 z-40"
              onclick={() => { showVoiceSettings = false; }}
            ></div>
            <div class="absolute bottom-full right-0 mb-1 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded shadow-lg p-2 z-50 w-52">
              <div class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider mb-1">Method</div>
              {#each [
                { value: 'openai', label: 'OpenAI API (cloud)' },
                { value: 'local', label: 'Local Whisper' },
                { value: 'faster-whisper', label: 'Faster-Whisper' },
              ] as opt}
                <button
                  onclick={() => { voiceMethod = opt.value; localStorage.setItem('at-voice-method', opt.value); }}
                  class="w-full text-left px-2 py-1 text-[11px] rounded transition-colors {voiceMethod === opt.value ? 'bg-gray-900 dark:bg-accent text-white' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
                >
                  {opt.label}
                </button>
              {/each}
              {#if voiceMethod !== 'openai'}
                <div class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider mt-2 mb-1">Model</div>
                {#each [
                  { value: 'tiny', label: 'tiny (39M, fastest)' },
                  { value: 'base', label: 'base (74M, fast)' },
                  { value: 'small', label: 'small (244M, good)' },
                  { value: 'medium', label: 'medium (769M, better)' },
                ] as opt}
                  <button
                    onclick={() => { voiceModel = opt.value; localStorage.setItem('at-voice-model', opt.value); showVoiceSettings = false; }}
                    class="w-full text-left px-2 py-1 text-[11px] rounded transition-colors {voiceModel === opt.value ? 'bg-gray-700 dark:bg-dark-highest text-white' : 'text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
                  >
                    {opt.label}
                  </button>
                {/each}
              {/if}
            </div>
          {/if}
        </div>

        {#if sending}
          <button onclick={stopGeneration} class="p-1 text-red-400 hover:text-red-500 shrink-0" title="Stop">
            <Square size={14} />
          </button>
        {:else}
          <button
            onclick={handleSend}
            disabled={!inputText.trim()}
            class="p-1 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary disabled:opacity-20 shrink-0"
            title="Send"
          >
            <Send size={14} />
          </button>
        {/if}
      </div>
    </div>
  </div>
</div>
