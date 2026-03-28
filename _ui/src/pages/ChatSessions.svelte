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
  import { Send, Square, Plus, Loader2, Trash2, RotateCcw, Bot, ChevronDown, ShieldCheck, ShieldX } from 'lucide-svelte';
  import { agentAvatar } from '@/lib/helper/avatar';
  import { md, renderMarkdown } from '@/lib/helper/markdown';

  storeNavbar.title = 'Sessions';

  // ─── State ───

  let sessions = $state<ChatSession[]>([]);
  let agents = $state<Agent[]>([]);
  let selectedSessionId = $state<string | null>(null);
  let messages = $state<ChatMessage[]>([]);
  let streamContent = $state('');
  let toolEvents = $state<any[]>([]);
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
      const res = await listChatSessions({ _sort: '-created_at' });
      sessions = res.data || [];
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

    // Auto-create session if none selected.
    if (!selectedSessionId) {
      await quickCreateSession();
      if (!selectedSessionId) return;
    }

    const content = inputText.trim();
    inputText = '';
    showSlashMenu = false;
    sending = true;
    streamContent = '';
    toolEvents = [];

    // Optimistic user message.
    messages = [
      ...messages,
      {
        id: 'pending-' + Date.now(),
        session_id: selectedSessionId!,
        role: 'user',
        data: { content },
        created_at: new Date().toISOString(),
      },
    ];
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

  function getMessageText(data: any): string {
    if (typeof data.content === 'string') return data.content;
    if (Array.isArray(data.content)) {
      return data.content.filter((b: any) => b.type === 'text').map((b: any) => b.text).join('');
    }
    return '';
  }

  function formatTime(iso: string): string {
    try {
      const d = new Date(iso);
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
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
  <div class="w-48 flex-shrink-0 border-r border-gray-200 dark:border-dark-border flex flex-col bg-white dark:bg-dark-surface">
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
              <div class="truncate text-gray-700 dark:text-dark-text font-medium">{session.name || 'Untitled'}</div>
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
    <div class="flex-1 overflow-y-auto font-mono text-[13px]">
      {#if !selectedSessionId}
        <div class="flex items-center justify-center h-full text-gray-400 dark:text-dark-text-muted">
          <div class="text-center text-sm">
            <p class="text-gray-500 dark:text-dark-text-secondary font-medium mb-1">No session selected</p>
            <p class="text-[11px]">Click <strong>+</strong> or type a message to start</p>
            <p class="text-[11px] mt-1 text-gray-400">Type <kbd class="px-1 py-0.5 bg-gray-200 dark:bg-dark-elevated rounded text-[10px]">/</kbd> for commands</p>
          </div>
        </div>
      {:else}
        <div class="max-w-3xl mx-auto px-4 py-3 space-y-1">
          {#each messages as msg (msg.id)}
            {#if msg.role === 'user'}
              <div class="py-1.5">
                <div class="flex items-baseline gap-2">
                  <span class="text-[11px] font-bold text-blue-600 dark:text-blue-400 select-none shrink-0">you</span>
                  <span class="text-[10px] text-gray-300 dark:text-dark-text-muted select-none">{formatTime(msg.created_at)}</span>
                </div>
                <div class="pl-0 mt-0.5 text-gray-800 dark:text-dark-text whitespace-pre-wrap">{getMessageText(msg.data)}</div>
              </div>
            {:else if msg.role === 'assistant'}
              <div class="py-1.5">
                <div class="flex items-baseline gap-2">
                  <span class="text-[11px] font-bold text-green-600 dark:text-green-400 select-none shrink-0">assistant</span>
                  <span class="text-[10px] text-gray-300 dark:text-dark-text-muted select-none">{formatTime(msg.created_at)}</span>
                  {#if msg.data.tool_calls}
                    <span class="text-[10px] text-yellow-600 dark:text-yellow-400">
                      [{Array.isArray(msg.data.tool_calls) ? msg.data.tool_calls.map((tc: any) => tc.Name || tc.name).join(', ') : 'tools'}]
                    </span>
                  {/if}
                </div>
                <div class="pl-0 mt-0.5 prose prose-sm dark:prose-invert max-w-none prose-p:my-1 prose-pre:my-1 prose-code:text-[12px]" use:renderMarkdown>
                  {@html md(getMessageText(msg.data))}
                </div>
              </div>
            {:else if msg.role === 'tool'}
              <div class="py-0.5 pl-4 border-l-2 border-gray-200 dark:border-dark-border">
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                  tool {#if msg.data.tool_call_id}<span class="text-gray-500">{msg.data.tool_call_id.slice(0, 12)}</span>{/if}
                </div>
                <pre class="text-[11px] text-gray-500 dark:text-dark-text-secondary whitespace-pre-wrap break-all mt-0.5 max-h-32 overflow-y-auto">{getMessageText(msg.data)}</pre>
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

          <!-- Streaming output -->
          {#if toolEvents.length > 0}
            <div class="py-0.5 pl-4 border-l-2 border-yellow-300 dark:border-yellow-600">
              {#each toolEvents as evt}
                {#if evt.type === 'call'}
                  <div class="flex items-center gap-1 text-[11px] text-yellow-700 dark:text-yellow-400">
                    <Loader2 size={10} class="animate-spin" />
                    <span>{evt.name}</span>
                  </div>
                {:else}
                  <div class="text-[10px] text-gray-500 dark:text-dark-text-muted">
                    <span class="text-green-600 dark:text-green-400">{evt.name}</span> → <span class="font-mono">{(evt.result || '').slice(0, 150)}{(evt.result || '').length > 150 ? '…' : ''}</span>
                  </div>
                {/if}
              {/each}
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
            <div class="py-1.5">
              <div class="flex items-baseline gap-2">
                <span class="text-[11px] font-bold text-green-600 dark:text-green-400 select-none">assistant</span>
                {#if sending}
                  <Loader2 size={10} class="animate-spin text-gray-400" />
                {/if}
              </div>
              <div class="pl-0 mt-0.5 prose prose-sm dark:prose-invert max-w-none prose-p:my-1 prose-pre:my-1 prose-code:text-[12px]" use:renderMarkdown>
                {@html md(streamContent)}
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

      <div class="flex items-end gap-2 px-3 py-2">
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
