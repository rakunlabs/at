<script lang="ts">
  import { PastedText } from '@/lib/helper/paste-collapse';
  import { onMount, tick } from 'svelte';
  import {
    ArrowUp, Bot, Brain, Check, ChevronDown, Cpu, Eye, FileDiff, FileText, FolderGit2, Gauge, Hammer, ListChecks, LoaderCircle,
    Maximize2, MessageCircleQuestion, Minimize2, Paperclip, Redo2, ShieldAlert, Square, Undo2, X,
  } from 'lucide-svelte';
  import SquareAlert from '@/lib/components/icons/SquareAlert.svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import ToolActivity from '@/lib/components/ToolActivity.svelte';
  import CommandPalette, { type PaletteGroup } from '@/lib/components/playground/CommandPalette.svelte';
  import { createAdaptivePoll } from '@/lib/helper/adaptive-poll';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import {
    getDeveloperGitStatus, getDeveloperSessionPendingTool, getDeveloperActiveStream, resumeDeveloperStream, listDeveloperSessionMessages, streamDeveloperSession, cancelDeveloperSession, updateDeveloperSession,
    listDeveloperProjectFiles, revertDeveloperSession, unrevertDeveloperSession,
    type DeveloperMode, type DeveloperPendingTool, type DeveloperRun, type DeveloperSession, type DeveloperSessionMessage, type DeveloperStreamEvent,
    steerDeveloperSession,
  } from '@/lib/api/developer-spaces';
  import type { ChatAttachment } from '@/lib/api/chat-sessions';
  import {
    buildTranscript, developerAgentSettings, developerAgentValue, mentionQuery, promptMentions, rankFiles, toolSummary, visibleMessages,
    DEVELOPER_COMMANDS, MODE_HINTS, STATUS_LABELS,
    type DeveloperAgentChoice, type TranscriptEntry,
  } from '@/lib/helper/developer-space';
  import { parseSlashInput, slashQuery } from '@/lib/helper/chat-commands';
  import {
    CHAT_ATTACHMENT_COUNT, CHAT_ATTACHMENT_TOTAL, attachmentBytes, attachmentImageURL, attachmentIsImage, attachmentSize, readChatAttachment,
  } from '@/lib/helper/chat-attachments';
  import { formatMessageTime } from '@/lib/helper/format';
  import { addToast } from '@/lib/store/toast.svelte';

  interface ModelGroup { label: string; models: string[] }
  interface Props {
    session: DeveloperSession;
    modelGroups: ModelGroup[];
    /** Reasoning efforts per `provider/model`; missing or empty = none offered. */
    modelEfforts?: Record<string, string[]>;
    /** Built-in profiles plus the workspace's agents. */
    agentChoices: DeveloperAgentChoice[];
    /** Bumped by the page whenever files may have changed (saves, git actions). */
    revision?: number;
    /** The container is running, so files can be listed for @ mentions. */
    runtimeAvailable?: boolean;
    onsession: (session: DeveloperSession) => void;
    /** Files the agent wrote, so open editors can reload them. */
    onfileschanged: (paths: string[], tree: boolean) => void;
    onopenfile: (path: string) => void;
    ondiff: (file: string, opts: { untracked?: boolean; head?: boolean }) => void;
    onnewsession?: () => void;
  }
  let { session, modelGroups, modelEfforts = {}, agentChoices, revision = 0, runtimeAvailable = false, onsession, onfileschanged, onopenfile, ondiff, onnewsession }: Props = $props();

  interface ChangedFile { path: string; additions: number; deletions: number; binary: boolean; untracked: boolean }

  let messages = $state<DeveloperSessionMessage[]>([]);
  let pending = $state<DeveloperPendingTool | null>(null);
  let loading = $state(true);
  let prompt = $state('');
  let answer = $state('');
  let busy = $state(false);
  let error = $state('');
  let durableRun = $state<DeveloperRun | null>(null);
  // Live text of the turn being generated; replaced by the saved message.
  let liveText = $state('');
  let liveThinking = $state('');
  let runningTools = $state<Record<string, boolean>>({});
  let controller: AbortController | null = null;
  let generation = 0;
  let disposed = false;
  let scroller: HTMLDivElement;
  let stickToBottom = true;

  const shown = $derived(visibleMessages(messages, session.revert_message_id));
  const transcript = $derived(buildTranscript(shown));
  const hiddenCount = $derived(messages.length - shown.length);
  const working = $derived(busy || session.status === 'running' || !!durableRun);
  const modelValue = $derived(session.provider ? `${session.provider}/${session.model ?? ''}` : '');
  const modelLabel = $derived(session.model || 'Choose a model');
  const efforts = $derived(modelEfforts[modelValue] ?? []);
  const todos = $derived(session.todos ?? []);
  const openTodos = $derived(todos.filter(t => t.status === 'pending' || t.status === 'in_progress').length);
  const canUndo = $derived(!working && transcript.some(entry => entry.undoable));

  // ─── Changed files strip ───
  let changes = $state<ChangedFile[]>([]);
  let changesOpen = $state(false);
  let todosOpen = $state(true);
  let expandedComposer = $state(false);
  let changesSeq = 0;
  let recording = $state(false);
  let transcribing = $state(false);
  let palette = $state<'model' | 'agent' | 'effort' | ''>('');
  let textarea = $state<HTMLTextAreaElement>();
  // Large pastes appear as `[Pasted ~N lines]` and expand when sent.
  const pasted = new PastedText();
  let fileInput = $state<HTMLInputElement>();
  const totals = $derived(changes.reduce((sum, c) => ({ add: sum.add + c.additions, del: sum.del + c.deletions }), { add: 0, del: 0 }));

  // ─── Attachments ───
  let attachments = $state<ChatAttachment[]>([]);
  let attachmentError = $state('');
  let reading = $state(false);
  let dragging = $state(false);

  async function addAttachments(files: File[]) {
    attachmentError = '';
    if (!files.length) return;
    if (attachments.length + files.length > CHAT_ATTACHMENT_COUNT) { attachmentError = `Attach up to ${CHAT_ATTACHMENT_COUNT} files per message.`; return; }
    if (attachments.reduce((t, f) => t + attachmentBytes(f), 0) + files.reduce((t, f) => t + f.size, 0) > CHAT_ATTACHMENT_TOTAL) {
      attachmentError = 'Attachments must be at most 8 MB in total.'; return;
    }
    reading = true;
    try { attachments = [...attachments, ...(await Promise.all(files.map(readChatAttachment)))]; }
    catch (e) { attachmentError = e instanceof Error ? e.message : 'Could not read the files.'; }
    finally { reading = false; }
  }

  function pasteFiles(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.files ?? []);
    if (!files.length) {
      const value = textarea ? pasted.paste(event, textarea) : null;
      if (value !== null) prompt = value;
      return;
    }
    event.preventDefault();
    void addAttachments(files);
  }

  // ─── @ file mentions ───
  let projectFiles = $state<string[]>([]);
  let filesFor = '';
  let mention = $state<{ start: number; query: string } | null>(null);
  let mentionIndex = $state(0);
  const fileSet = $derived(new Set(projectFiles));
  const mentionMatches = $derived(mention ? rankFiles(projectFiles, mention.query, 12) : []);

  async function ensureProjectFiles() {
    const key = `${session.project_path}:${revision}`;
    if (!runtimeAvailable || filesFor === key) return;
    filesFor = key;
    try { projectFiles = (await listDeveloperProjectFiles(session.project_path)).files; }
    catch { filesFor = ''; }
  }

  function updateMention() {
    if (!textarea) return;
    mention = mentionQuery(prompt, textarea.selectionStart ?? prompt.length);
    mentionIndex = 0;
    if (mention) void ensureProjectFiles();
  }

  function pickMention(path: string) {
    if (!mention || !textarea) return;
    const caret = textarea.selectionStart ?? prompt.length;
    prompt = `${prompt.slice(0, mention.start)}@${path} ${prompt.slice(caret)}`;
    const position = mention.start + path.length + 2;
    mention = null;
    void tick().then(() => { textarea?.focus(); textarea?.setSelectionRange(position, position); });
  }

  // ─── Slash commands ───
  const slash = $derived(attachments.length ? null : slashQuery(prompt));
  const slashMatches = $derived(slash === null ? [] : DEVELOPER_COMMANDS.filter(c => c.name.startsWith(slash)));
  let slashIndex = $state(0);
  $effect(() => { void slash; slashIndex = 0; });

  /** Runs `/name args`. Returns false for an unknown name, which is then sent as text. */
  async function runCommand(name: string, args: string): Promise<boolean> {
    switch (name) {
      case 'compact':
        prompt = '';
        await stream('run', { compact: true, prompt: args });
        return true;
      case 'undo': prompt = ''; await undo(); return true;
      case 'redo': prompt = ''; await redo(); return true;
      case 'new': prompt = ''; onnewsession?.(); return true;
      case 'model': prompt = ''; if (!working) palette = 'model'; return true;
      case 'agent': prompt = ''; if (!working) palette = 'agent'; return true;
      case 'effort':
        prompt = '';
        if (args) await chooseEffort(args === 'default' ? '' : args.toLowerCase());
        else if (!working) palette = 'effort';
        return true;
      case 'todos': prompt = ''; todosOpen = true; if (!todos.length) addToast('The task list is empty.', 'info'); return true;
    }
    return false;
  }

  async function loadChanges() {
    const seq = ++changesSeq;
    try {
      const status = await getDeveloperGitStatus(session.project_path, true);
      if (seq !== changesSeq) return;
      const untracked = new Set(status.untracked);
      changes = (status.changes ?? []).map(c => ({ path: c.path, additions: c.additions, deletions: c.deletions, binary: !!c.binary, untracked: untracked.has(c.path) }));
    } catch {
      // Not a repository, git missing or the space is stopped: the strip just stays hidden.
      if (seq === changesSeq) changes = [];
    }
  }

  $effect(() => {
    void session.project_path;
    void revision;
    void loadChanges();
  });

  const agentValue = $derived(developerAgentValue(session));
  const agentChoice = $derived(agentChoices.find(c => c.value === agentValue));
  // A deleted or no longer visible agent still shows what the session was set to.
  const agentLabel = $derived(agentChoice?.label ?? (session.agent_id ? 'Unavailable agent' : 'Build'));
  const agentHint = $derived(agentChoice?.hint || (session.agent_id ? 'This agent no longer exists or is not available to you. Choose another one.' : MODE_HINTS[session.mode]));
  const agentGroups = $derived.by(() => {
    const groups: Array<{ label: string; choices: DeveloperAgentChoice[] }> = [];
    for (const choice of agentChoices) {
      const group = groups.find(g => g.label === choice.group);
      if (group) group.choices.push(choice);
      else groups.push({ label: choice.group, choices: [choice] });
    }
    return groups;
  });
  const effortLabel = $derived(session.reasoning_effort || 'default');
  const paletteGroups = $derived<PaletteGroup[]>(palette === 'model'
    ? modelGroups.map(group => ({ label: group.label, items: group.models.map(model => ({ label: model, current: model === modelValue, disabled: working, run: () => chooseModel(model) })) }))
    : palette === 'effort'
      ? [{ label: 'Reasoning effort', items: ['', ...efforts].map(level => ({ label: level || 'default', current: level === (session.reasoning_effort ?? ''), disabled: working, run: () => { void chooseEffort(level); } })) }]
      : agentGroups.map(group => ({ label: group.label, items: group.choices.map(choice => ({ label: choice.label, current: choice.value === agentValue, disabled: working, run: () => chooseAgent(choice.value) })) })));

  function chooseAgent(value: string) {
    if (working || value === agentValue) return;
    void changeSettings(developerAgentSettings(value));
  }

  let loadedFor = '';
  let loadedRevision = '';
  let voiceContext = $state(0);
  $effect(() => {
    const id = session.id;
    if (id === loadedFor) return;
    generation++;
    controller?.abort();
    busy = false;
    durableRun = null;
    loadedFor = id;
    voiceContext++;
    void load(id);
  });

  onMount(() => {
    const poll = createAdaptivePoll({
      active: () => !disposed && !busy && !loading && !document.hidden && navigator.onLine,
      poll: async () => {
        const id = session.id;
        const epoch = generation;
        const state = await getDeveloperActiveStream(id);
        if (disposed || epoch !== generation || busy) return false;
        const changed = `${state.session.updated_at}:${state.session.status}` !== loadedRevision;
        durableRun = state.run ?? null;
        onsession(state.session);
        if (state.stream_id) void stream('resume', {}, state.stream_id);
        else if (changed) await load(id, false);
        return changed;
      },
    });
    const wake = () => poll.wake();
    window.addEventListener('online', wake);
    document.addEventListener('visibilitychange', wake);
    return () => {
      disposed = true;
      generation++;
      changesSeq++;
      controller?.abort(); // Detach only; never cancel the server's run.
      poll.stop();
      window.removeEventListener('online', wake);
      document.removeEventListener('visibilitychange', wake);
    };
  });

  async function load(id: string, attach = true) {
    const epoch = generation;
    loading = true;
    try {
      // Read metadata first so its revision can never describe a completion
      // newer than the history we adopt. A later change is caught by polling.
      const state = await getDeveloperActiveStream(id);
      const [records, waiting] = await Promise.all([listDeveloperSessionMessages(id), getDeveloperSessionPendingTool(id)]);
      if (disposed || epoch !== generation || id !== session.id) return;
      messages = records;
      durableRun = state.run ?? null;
      pending = waiting;
      liveText = '';
      liveThinking = '';
      loadedRevision = `${state.session.updated_at}:${state.session.status}`;
      onsession(state.session);
      stickToBottom = true;
      await scrollToEnd();
      if (attach && state.stream_id && !busy) void stream('resume', {}, state.stream_id);
      else if (attach && state.session.status === 'running' && !state.stream_id) error = 'This run is not available on this server. Check saved history or request Stop. Missing replay does not mean the tools have stopped.';
    } catch (e: any) {
      if (disposed || epoch !== generation) return;
      error = e?.response?.data?.message || e?.message || 'Could not load this session';
    } finally {
      if (!disposed && epoch === generation) loading = false;
    }
  }

  async function scrollToEnd() {
    await tick();
    if (scroller && stickToBottom) scroller.scrollTop = scroller.scrollHeight;
  }

  function onScroll() {
    if (!scroller) return;
    stickToBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80;
  }

  function handle(event: DeveloperStreamEvent) {
    switch (event.type) {
      case 'status':
      case 'done':
        if (event.session.status === 'running') pending = null;
        onsession(event.session);
        break;
      case 'turn_start':
        liveText = '';
        liveThinking = '';
        break;
      case 'delta':
        if (event.content) liveText += event.content;
        if (event.reasoning) liveThinking += event.reasoning;
        void scrollToEnd();
        break;
      case 'message':
        if (event.message.role === 'user') messages = messages.filter(m => !m.id.startsWith('local-'));
        if (!messages.some(m => m.id === event.message.id)) messages = [...messages, event.message];
        if (event.message.role === 'assistant') { liveText = ''; liveThinking = ''; }
        void scrollToEnd();
        break;
      case 'tool_start':
        runningTools = { ...runningTools, [event.tool_id]: true };
        break;
      case 'tool_result': {
        const { [event.tool_id]: _, ...rest } = runningTools;
        runningTools = rest;
        if (event.changed?.length || event.changed_tree) onfileschanged(event.changed ?? [], !!event.changed_tree);
        if (event.todos) onsession({ ...session, todos: event.todos });
        break;
      }
      case 'pending':
        pending = event.pending_tool;
        void scrollToEnd();
        break;
    }
  }

  async function stream(action: 'run' | 'confirm' | 'answer' | 'resume', body: Record<string, unknown>, streamId = '') {
    if (busy || (action !== 'resume' && durableRun)) return;
    const id = session.id;
    const epoch = generation;
    busy = true;
    error = '';
    controller = new AbortController();
    try {
      const receive = (event: DeveloperStreamEvent) => { if (!disposed && epoch === generation) handle(event); };
      if (action === 'resume') await resumeDeveloperStream(id, streamId, receive, controller.signal);
      else await streamDeveloperSession(id, action, body, receive, controller.signal);
    } catch (e: any) {
      if (disposed || epoch !== generation) return;
      if (e?.name !== 'AbortError') error = e?.message || 'The agent stopped unexpectedly';
    } finally {
      if (!disposed && epoch === generation) {
        busy = false;
        runningTools = {};
        controller = null;
        await load(id, false);
        void loadChanges();
      }
    }
  }

  /**
   * While the agent works, a typed message joins the running turn at its next
   * step (OpenCode's steer) instead of waiting for the run to end.
   */
  async function steer() {
    const text = pasted.expand(prompt).trim();
    if (!text || attachments.length || pending) return;
    try {
      await steerDeveloperSession(session.id, text);
      prompt = '';
      pasted.clear();
      addToast('Added to the running turn; the agent reads it at its next step.', 'info');
    } catch (e: any) {
      if (e?.response?.status === 409) addToast('The run already finished or runs on another server; send the message again.', 'warn');
      else addToast(e?.response?.data?.message || 'Could not add the message', 'alert');
    }
  }

  async function send() {
    const text = pasted.expand(prompt).trim();
    if (working && busy && text && !attachments.length && !parseSlashInput(text)) { await steer(); return; }
    if ((!text && !attachments.length) || working || recording || transcribing || reading) return;
    if (!session.provider) { error = 'Choose a model first.'; return; }
    const command = attachments.length ? null : parseSlashInput(text);
    if (command && await runCommand(command.name, command.args)) return;
    if (session.revert_message_id && hiddenCount && !confirm(`Sending discards the ${hiddenCount} undone message${hiddenCount === 1 ? '' : 's'} for good. Continue?`)) return;
    await ensureProjectFiles();
    const files = promptMentions(text, fileSet);
    const sent = attachments;
    prompt = '';
    pasted.clear();
    attachments = [];
    attachmentError = '';
    mention = null;
    stickToBottom = true;
    // Optimistic bubble; replaced when the stored message arrives.
    messages = [...visibleMessages(messages, session.revert_message_id), { id: `local-${Date.now()}`, session_id: session.id, role: 'user', content: text, created_at: new Date().toISOString() }];
    if (session.revert_message_id) onsession({ ...session, revert_message_id: '' });
    await scrollToEnd();
    await stream('run', { prompt: text, files, attachments: sent });
  }

  async function undo() {
    if (!canUndo) { addToast('There is nothing to undo.', 'info'); return; }
    await revertAction(() => revertDeveloperSession(session.id));
  }

  async function undoTo(messageId: string) {
    if (working) return;
    if (!confirm('Undo this message and everything after it? Files are restored to how they were before it ran. Redo brings it back until you send a new message.')) return;
    await revertAction(() => revertDeveloperSession(session.id, messageId));
  }

  async function redo() {
    if (!session.revert_message_id || working) { addToast('There is nothing to redo.', 'info'); return; }
    await revertAction(() => unrevertDeveloperSession(session.id));
  }

  async function revertAction(call: () => Promise<{ session: DeveloperSession; changed?: string[] }>) {
    try {
      const result = await call();
      onsession(result.session);
      onfileschanged(result.changed ?? [], true);
      void loadChanges();
      // Put the undone prompt back in the composer, as OpenCode does.
      if (result.session.revert_message_id && !prompt.trim()) {
        const message = messages.find(m => m.id === result.session.revert_message_id);
        const entry = message && buildTranscript([message])[0];
        if (entry?.text) prompt = entry.text;
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not undo', 'alert');
    }
  }

  async function chooseEffort(level: string) {
    if (working || level === (session.reasoning_effort ?? '')) return;
    if (level && efforts.length && !efforts.includes(level)) { addToast(`${session.model || 'This model'} supports: ${efforts.join(', ')}`, 'alert'); return; }
    await changeSettings({ reasoning_effort: level });
  }

  async function decide(approved: boolean, always = false) {
    if (busy) return;
    await stream('confirm', { approved, always });
  }

  async function reply() {
    const text = answer.trim();
    if (!text || busy) return;
    answer = '';
    await stream('answer', { answer: text });
  }

  async function stop() {
    const id = session.id;
    const epoch = generation;
    controller?.abort();
    try {
      const updated = await cancelDeveloperSession(id);
      if (disposed || epoch !== generation) return;
      onsession(updated);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not stop the session', 'alert');
    }
    pending = null;
  }

  async function changeSettings(body: { mode?: DeveloperMode; agent_id?: string; provider?: string; model?: string; reasoning_effort?: string }) {
    try {
      onsession(await updateDeveloperSession(session.id, body));
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not change the session settings', 'alert');
    }
  }

  function chooseModel(value: string) {
    const slash = value.indexOf('/');
    if (slash <= 0) return;
    void changeSettings({ provider: value.slice(0, slash), model: value.slice(slash + 1) });
  }

  function keydown(event: KeyboardEvent) {
    if (textarea && (event.key === 'Backspace' || event.key === 'Delete')) {
      const value = pasted.keydown(event, textarea);
      if (value !== null) { prompt = value; return; }
    }
    const list = mention && mentionMatches.length ? mentionMatches : slashMatches.length ? slashMatches.map(c => c.name) : null;
    if (list) {
      const isMention = !!(mention && mentionMatches.length);
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        const delta = event.key === 'ArrowDown' ? 1 : -1;
        if (isMention) mentionIndex = (mentionIndex + delta + list.length) % list.length;
        else slashIndex = (slashIndex + delta + list.length) % list.length;
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey && !event.isComposing)) {
        event.preventDefault();
        if (isMention) { pickMention(list[mentionIndex]); return; }
        const command = DEVELOPER_COMMANDS.find(c => c.name === list[slashIndex])!;
        if (event.key === 'Tab' || command.args) prompt = `/${command.name} `;
        else void runCommand(command.name, '');
        return;
      }
      if (event.key === 'Escape') { event.preventDefault(); mention = null; prompt = isMention ? prompt : ''; return; }
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'm') {
      event.preventDefault();
      if (!working) palette = 'model';
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z' && !prompt) {
      event.preventDefault();
      void (event.shiftKey ? redo() : undo());
      return;
    }
    if (event.key === 'Escape' && working) {
      event.preventDefault();
      void stop();
      return;
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      void send();
    }
  }

  function grow(node: HTMLTextAreaElement, _value: unknown) {
    const fit = () => {
      node.style.height = 'auto';
      node.style.height = `${Math.min(node.scrollHeight, expandedComposer ? Math.round(window.innerHeight * 0.6) : 240)}px`;
    };
    fit();
    return { update: fit };
  }

  function pendingSummary(p: DeveloperPendingTool) {
    return p.tool_calls.map(call => ({ name: call.Name, detail: toolSummary({ name: call.Name, input: call.Arguments }) }));
  }

  function fileArg(tool: { name: string; input: Record<string, unknown> }): string {
    return ['read_file', 'write_file', 'edit_file'].includes(tool.name) && typeof tool.input.path === 'string' ? tool.input.path : '';
  }

  const TODO_MARK: Record<string, string> = { pending: '○', in_progress: '◐', completed: '●', cancelled: '×' };

  function projectFile(rel: string) {
    return session.project_path ? `${session.project_path}/${rel}` : rel;
  }
</script>

{#snippet toolRow(tool: TranscriptEntry['tools'][number])}
  <div class="flex min-w-0 items-start gap-2 text-xs">
    <div class="min-w-0 flex-1">
      <ToolActivity compact call={{ id: tool.id, type: 'function', function: { name: tool.name, arguments: JSON.stringify(tool.input) } }} result={tool.result === undefined ? undefined : { role: 'tool', content: tool.failed ? `tool error: ${tool.result}` : tool.result, tool_call_id: tool.id }} running={!!runningTools[tool.id]} />
    </div>
    {#if fileArg(tool)}<button type="button" class="shrink-0 text-dark-text-muted hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent" onclick={() => onopenfile(projectFile(fileArg(tool)))}>Open</button>{/if}
  </div>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-dark-base">
  <!-- Session bar -->
  <div class="flex min-h-10 flex-wrap items-center gap-2 border-b border-dark-border px-3 py-1.5 text-xs">
    <span class="inline-flex min-w-0 items-center gap-1 text-dark-text-muted" title="The agent works inside this folder">
      <FolderGit2 size={13} class="shrink-0" />
      <span class="truncate font-mono">/workspace{session.project_path ? `/${session.project_path}` : ''}</span>
    </span>
    <span class="text-dark-border">|</span>
    <span class={['inline-flex items-center gap-1', session.status === 'failed' ? 'text-red-400' : session.status.startsWith('waiting') ? 'text-amber-400' : 'text-dark-text-muted']}>
      {#if working}<LoaderCircle size={12} class="animate-spin motion-reduce:animate-none" />{/if}
      {STATUS_LABELS[session.status] ?? session.status}
    </span>
    <span class="ml-auto text-dark-text-faint" title="Closing this page does not stop the agent. Use Stop to cancel.">Runs on server</span>
    <button type="button" onclick={undo} disabled={!canUndo} class="p-1 text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text disabled:opacity-40" title="Undo the last prompt and its file changes (/undo, Ctrl+Z)" aria-label="Undo"><Undo2 size={14} /></button>
    <button type="button" onclick={redo} disabled={working || !session.revert_message_id} class="p-1 text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text disabled:opacity-40" title="Redo (/redo, Ctrl+Shift+Z)" aria-label="Redo"><Redo2 size={14} /></button>
  </div>

  <!-- Transcript -->
  <div bind:this={scroller} onscroll={onScroll} class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    <div class="mx-auto flex w-full max-w-4xl flex-col gap-4 px-3 py-5 sm:px-5">
      {#if transcript.length}<h2 class="px-4 text-sm font-medium text-dark-text">{session.title || 'Coding session'}</h2>{/if}
      {#if loading}
        <p class="text-sm text-dark-text-muted">Loading…</p>
      {:else if transcript.length === 0 && !liveText}
        <div class="mt-10 text-center text-sm text-dark-text-muted">
          <p class="font-medium text-dark-text-secondary">What should the agent do in {session.project_path || 'this space'}?</p>
          <p class="mt-1">{agentHint}</p>
        </div>
      {/if}
      {#each transcript as entry (entry.key)}
        {#if entry.compaction}
          <div class="border border-dark-border px-4 py-3 text-sm sm:px-[22px]">
            <p class="mb-2 text-xs text-dark-text-muted">Conversation compacted. The model continues from this summary; earlier messages stay here for reference.</p>
            <details class="text-dark-text-secondary"><summary class="cursor-pointer text-xs">Summary</summary><div class="mt-2"><Markdown source={entry.text} /></div></details>
          </div>
        {:else if entry.role === 'user'}
          <div class="group border-l-2 border-accent bg-dark-surface px-4 py-3 sm:px-[22px]">
            <div class="whitespace-pre-wrap break-words text-sm leading-relaxed text-dark-text">{entry.text}</div>
            {#if entry.mentions?.length || entry.attachments?.length}
              <div class="mt-2 flex flex-wrap gap-1.5 text-xs">
                {#each entry.mentions ?? [] as path}
                  <button type="button" onclick={() => onopenfile(session.project_path ? `${session.project_path}/${path}` : path)} class="inline-flex items-center gap-1 border border-dark-border px-1.5 py-0.5 font-mono text-dark-text-secondary hover:bg-dark-elevated"><FileText size={11} /> @{path}</button>
                {/each}
                {#each entry.attachments ?? [] as file}
                  {#if file.data && file.mediaType.startsWith('image/')}
                    <img src={`data:${file.mediaType};base64,${file.data}`} alt={file.name} class="max-h-40 max-w-full border border-dark-border object-contain" loading="lazy" />
                  {:else}
                    <span class="inline-flex items-center gap-1 border border-dark-border px-1.5 py-0.5 text-dark-text-secondary"><Paperclip size={11} /> {file.name}</span>
                  {/if}
                {/each}
              </div>
            {/if}
            <div class="mt-2 flex items-center gap-2 text-[11px] text-dark-text-faint">
              {#if entry.created_at && !entry.key.startsWith('local-')}<span>{formatMessageTime(entry.created_at)}</span>{/if}
              {#if entry.undoable && !working}
                <button type="button" onclick={() => undoTo(entry.key)} class="ml-auto inline-flex items-center gap-1 opacity-0 hover:text-dark-text group-hover:opacity-100 focus:opacity-100 [@media(pointer:coarse)]:opacity-100" title="Undo this message and everything after it, including file changes"><Undo2 size={11} /> Undo from here</button>
              {/if}
            </div>
          </div>
        {:else}
          <div class="min-w-0">
            <div class="min-w-0 px-4 py-2.5 text-sm leading-relaxed text-dark-text sm:px-[22px]">
              {#if entry.thinking}
                <details class="mb-2 text-xs text-dark-text-muted">
                  <summary class="inline-flex cursor-pointer items-center gap-1"><Brain size={12} /> Reasoning</summary>
                  <p class="mt-1 whitespace-pre-wrap">{entry.thinking}</p>
                </details>
              {/if}
              {#if entry.text}<Markdown source={entry.text} enhance />{/if}
              {#if entry.tools.length}
                <div class={['space-y-1', entry.text ? 'mt-3' : '']}>
                  {#each entry.tools as tool (tool.id)}{@render toolRow(tool)}{/each}
                </div>
              {/if}
            </div>
            {#if entry.created_at}<div class="mt-1 px-4 text-[11px] text-dark-text-faint sm:px-[22px]">{formatMessageTime(entry.created_at)}</div>{/if}
          </div>
        {/if}
      {/each}

      {#if hiddenCount > 0 && !working}
        <div role="status" class="flex flex-wrap items-center gap-2 border border-dark-border px-3 py-2 text-xs text-dark-text-secondary">
          <Undo2 size={13} class="shrink-0" />
          <span class="flex-1">{hiddenCount} message{hiddenCount === 1 ? '' : 's'} undone and the files restored. Sending a new message discards them.</span>
          <button type="button" onclick={redo} class="inline-flex items-center gap-1 border border-dark-border-subtle px-2 py-0.5 hover:bg-dark-elevated"><Redo2 size={12} /> Redo</button>
        </div>
      {/if}

      {#if busy && (liveText || liveThinking || Object.keys(runningTools).length === 0)}
        <div class="px-4 py-2.5 text-sm leading-relaxed text-dark-text sm:px-[22px]" role="status">
          {#if liveThinking && !liveText}
            <p class="flex items-center gap-1.5 text-xs text-dark-text-muted"><Brain size={12} /> Thinking…</p>
          {/if}
          {#if liveText}
            <Markdown source={liveText} />
          {:else if !liveThinking}
            <span class="inline-flex items-center gap-2 italic text-dark-text-muted"><LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" /> Working…</span>
          {/if}
        </div>
      {/if}

      {#if pending && !working}
        {#if pending.kind === 'permission'}
          <div class="border border-amber-700 bg-amber-950/40 p-3 text-sm">
            <p class="flex items-center gap-2 font-medium text-amber-200"><ShieldAlert size={16} /> The agent wants to run:</p>
            <ul class="mt-2 space-y-1">
              {#each pendingSummary(pending) as item}
                <li class="break-all font-mono text-xs text-dark-text"><span class="font-semibold">{item.name}</span> {item.detail}</li>
              {/each}
            </ul>
            <div class="mt-3 flex gap-2">
              <button type="button" onclick={() => decide(true)} class="inline-flex items-center gap-1.5 bg-accent px-3 py-1.5 text-xs font-medium text-dark-base hover:bg-accent-hover"><Check size={13} /> Allow</button>
              <button type="button" onclick={() => decide(true, true)} class="inline-flex items-center gap-1.5 border border-dark-border-subtle px-3 py-1.5 text-xs text-dark-text hover:bg-dark-elevated" title="Allow and stop asking in this session for the same command (its first word) or tool"><Check size={13} /> Always allow</button>
              <button type="button" onclick={() => decide(false)} class="inline-flex items-center gap-1.5 border border-dark-border-subtle px-3 py-1.5 text-xs text-dark-text-secondary hover:bg-dark-elevated"><X size={13} /> Reject</button>
            </div>
          </div>
        {:else}
          {@const question = pending.tool_calls.find(call => call.Name === 'ask_user')?.Arguments?.question}
          <div class="border border-blue-800 bg-blue-950/40 p-3 text-sm">
            <p class="flex items-start gap-2 text-dark-text"><MessageCircleQuestion size={16} class="mt-0.5 shrink-0 text-blue-600" /> <span class="whitespace-pre-wrap">{String(question ?? 'The agent has a question.')}</span></p>
            <form class="mt-3 flex gap-2" onsubmit={event => { event.preventDefault(); void reply(); }}>
              <input bind:value={answer} aria-label="Answer" placeholder="Your answer" class="min-w-0 flex-1 border border-dark-border bg-dark-surface px-3 py-1.5 text-sm text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20" />
              <button type="submit" disabled={!answer.trim()} class="bg-accent px-3 py-1.5 text-xs font-medium text-dark-base disabled:opacity-40">Reply</button>
            </form>
          </div>
        {/if}
      {/if}

      {#if error}
        <p role="alert" class="flex items-start gap-2 border border-red-900 bg-red-950/40 px-3 py-2 text-sm text-red-300"><SquareAlert size={15} class="mt-0.5 shrink-0" /> {error}</p>
      {:else if session.status === 'failed' && session.error && !busy}
        <p class="flex items-start gap-2 border border-red-900 bg-red-950/40 px-3 py-2 text-sm text-red-300"><SquareAlert size={15} class="mt-0.5 shrink-0" /> {session.error}</p>
      {/if}
      {#if durableRun?.interrupted}
        <p role="status" class="border border-dark-border px-3 py-2 text-sm text-dark-text-secondary">Run ownership is unresolved. Sending and approval are blocked in this session; Stop requests cancellation but does not clear the lock. Inspect saved history and files, and verify the old process and tools have stopped before using a new session.</p>
      {:else if durableRun?.cancel_requested}
        <p role="status" class="border border-dark-border px-3 py-2 text-sm text-dark-text-secondary">Stop requested. Waiting for the owning process to finish; new work stays blocked until it releases this session.</p>
      {/if}
    </div>
  </div>

  <!-- Composer -->
  <div class="bg-dark-base px-3 pb-3 pt-1">
    <div class="mx-auto max-w-4xl">
      {#if todos.length}
        <div class="mb-1.5 text-xs">
          <button type="button" onclick={() => (todosOpen = !todosOpen)} aria-expanded={todosOpen} class="inline-flex items-center gap-1.5 px-1 py-0.5 text-dark-text-secondary hover:text-dark-text">
            <ListChecks size={13} class="shrink-0 text-oc-violet" />
            <span>Tasks · {openTodos} of {todos.length} open</span>
            <ChevronDown size={13} class={todosOpen ? 'shrink-0 rotate-180' : 'shrink-0'} />
          </button>
          {#if todosOpen}
            <ul class="mt-1 max-h-48 overflow-y-auto border border-dark-border py-1">
              {#each todos as todo (todo.id)}
                <li class={['flex items-start gap-2 px-2.5 py-0.5', todo.status === 'completed' || todo.status === 'cancelled' ? 'text-dark-text-muted line-through' : todo.status === 'in_progress' ? 'text-oc-peach' : 'text-dark-text']}>
                  <span class="w-3 shrink-0 text-center" aria-label={todo.status}>{TODO_MARK[todo.status] ?? '○'}</span>
                  <span class="min-w-0 flex-1 break-words">{todo.content}</span>
                  {#if todo.priority === 'high'}<span class="shrink-0 text-[11px] text-oc-red no-underline">high</span>{/if}
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
      {#if changes.length}
        <div class="mb-1.5 text-xs">
          <button
            type="button"
            onclick={() => (changesOpen = !changesOpen)}
            aria-expanded={changesOpen}
            class="inline-flex items-center gap-1.5 px-1 py-0.5 text-dark-text-secondary hover:text-dark-text"
          >
            <FileDiff size={13} class="shrink-0 text-amber-400" />
            <span>{changes.length} file{changes.length === 1 ? '' : 's'} changed in {session.project_path || 'workspace'}</span>
            <span class="font-mono text-green-400">+{totals.add}</span>
            <span class="font-mono text-red-400">-{totals.del}</span>
            <ChevronDown size={13} class={changesOpen ? 'shrink-0 rotate-180' : 'shrink-0'} />
          </button>
          {#if changesOpen}
            <ul class="mt-1 max-h-48 overflow-y-auto border border-dark-border">
              {#each changes as change (change.path)}
                <li>
                  <button
                    type="button"
                    onclick={() => ondiff(change.path, change.untracked ? { untracked: true } : { head: true })}
                    class="flex w-full items-center gap-2 px-2.5 py-1 text-left hover:bg-dark-elevated"
                    title="Show diff"
                  >
                    <span class="min-w-0 flex-1 truncate font-mono text-dark-text">{change.path}</span>
                    {#if change.untracked}<span class="shrink-0 text-[11px] text-dark-text-muted">new</span>{/if}
                    {#if change.binary}
                      <span class="shrink-0 text-dark-text-muted">binary</span>
                    {:else}
                      <span class="shrink-0 font-mono text-green-400">+{change.additions}</span>
                      <span class="shrink-0 font-mono text-red-400">-{change.deletions}</span>
                    {/if}
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}

      <div
        role="group"
        aria-label="Message composer"
        class={['relative border-l-2 bg-dark-elevated px-1 pt-1', working ? 'border-oc-peach' : 'border-accent', dragging ? 'ring-2 ring-inset ring-accent' : '']}
        ondragover={event => { if (event.dataTransfer?.types.includes('Files')) { event.preventDefault(); dragging = true; } }}
        ondragleave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node)) dragging = false; }}
        ondrop={event => { if (!event.dataTransfer?.files.length) return; event.preventDefault(); dragging = false; void addAttachments(Array.from(event.dataTransfer.files)); }}
      >
        {#if (mention && mentionMatches.length) || slashMatches.length}
          <ul role="listbox" aria-label={mention ? 'Files' : 'Commands'} class="absolute inset-x-0 bottom-full z-20 mb-1 max-h-72 overflow-y-auto border border-dark-border bg-dark-surface py-1 text-xs shadow-lg">
            {#if mention && mentionMatches.length}
              {#each mentionMatches as path, i (path)}
                <li role="option" aria-selected={i === mentionIndex}>
                  <button type="button" onmousedown={event => { event.preventDefault(); pickMention(path); }} class={['flex w-full items-center gap-2 px-3 py-1 text-left font-mono', i === mentionIndex ? 'bg-dark-elevated text-dark-text' : 'text-dark-text-secondary hover:bg-dark-elevated']}>
                    <FileText size={12} class="shrink-0 text-dark-text-muted" /> <span class="truncate">{path}</span>
                  </button>
                </li>
              {/each}
            {:else}
              {#each slashMatches as command, i (command.name)}
                <li role="option" aria-selected={i === slashIndex}>
                  <button type="button" onmousedown={event => { event.preventDefault(); if (command.args) { prompt = `/${command.name} `; textarea?.focus(); } else void runCommand(command.name, ''); }} class={['flex w-full items-baseline gap-3 px-3 py-1 text-left', i === slashIndex ? 'bg-dark-elevated' : 'hover:bg-dark-elevated']}>
                    <span class="shrink-0 font-mono text-dark-text">/{command.name}{#if command.args}<span class="text-dark-text-muted"> {command.args}</span>{/if}</span>
                    <span class="truncate text-dark-text-muted">{command.description}</span>
                  </button>
                </li>
              {/each}
            {/if}
          </ul>
        {/if}
        {#if attachments.length || attachmentError || reading}
          <div class="flex flex-wrap items-center gap-2 px-2 pt-1.5 text-xs">
            {#each attachments as file, i}
              <span class="inline-flex max-w-56 items-center gap-1.5 border border-dark-border bg-dark-base py-0.5 pl-1 pr-0.5">
                {#if attachmentIsImage(file)}<img src={attachmentImageURL(file)} alt="" class="size-6 object-cover" />{:else}<FileText size={13} class="shrink-0 text-dark-text-muted" />{/if}
                <span class="truncate" title={file.name}>{file.name}</span>
                <span class="shrink-0 text-dark-text-muted">{attachmentSize(attachmentBytes(file))}</span>
                <button type="button" onclick={() => { attachments = attachments.filter((_, index) => index !== i); attachmentError = ''; }} class="p-0.5 text-dark-text-muted hover:text-dark-text" aria-label={`Remove ${file.name}`}><X size={12} /></button>
              </span>
            {/each}
            {#if reading}<span class="text-dark-text-muted">Reading…</span>{/if}
            {#if attachmentError}<span role="alert" class="text-red-300">{attachmentError}</span>{/if}
          </div>
        {/if}
        <textarea
          bind:this={textarea}
          bind:value={prompt}
          use:grow={[prompt, expandedComposer]}
          onkeydown={keydown}
          oninput={updateMention}
          onclick={updateMention}
          onpaste={pasteFiles}
          onblur={() => { mention = null; }}
          rows={expandedComposer ? 8 : 2}
          aria-label="Message the agent"
          placeholder={pending ? 'Answer the prompt above first' : busy ? 'Add to the running turn…  (Enter; the agent reads it at its next step)' : 'Ask the agent to change, explain or review code…  (@ for files, / for commands, Enter to send)'}
          disabled={!!pending && !busy}
          class="block w-full resize-none border-0 bg-transparent px-3 pb-1 pt-2.5 text-sm leading-[22px] focus:outline-none focus:ring-0 disabled:opacity-60 text-dark-text placeholder:text-dark-text-muted"
        ></textarea>
        <input bind:this={fileInput} type="file" multiple class="hidden" onchange={event => { const files = Array.from(event.currentTarget.files ?? []); event.currentTarget.value = ''; void addAttachments(files); }} />
        <div class="flex flex-wrap items-center gap-1 px-1.5 pb-1.5">
          <button
            type="button"
            onclick={() => (expandedComposer = !expandedComposer)}
            class="inline-flex size-7 items-center justify-center text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text"
            title={expandedComposer ? 'Shrink the message box' : 'Enlarge the message box'}
            aria-label={expandedComposer ? 'Shrink the message box' : 'Enlarge the message box'}
            aria-pressed={expandedComposer}
          >
            {#if expandedComposer}<Minimize2 size={15} />{:else}<Maximize2 size={15} />{/if}
          </button>
          <button type="button" onclick={() => fileInput?.click()} disabled={working || reading || attachments.length >= CHAT_ATTACHMENT_COUNT} class="inline-flex size-7 items-center justify-center text-dark-text-muted hover:bg-dark-elevated hover:text-dark-text disabled:opacity-40" title="Attach images or files (or paste / drop them)" aria-label="Attach files"><Paperclip size={15} /></button>
          <span class="flex-1"></span>

          <button
            type="button"
            disabled={working}
            onclick={() => (palette = 'model')}
            aria-label="Choose model"
            class={['relative inline-flex h-7 min-w-0 max-w-56 items-center gap-1.5 px-2 text-xs text-dark-text-secondary', working ? 'opacity-50' : 'cursor-pointer hover:bg-dark-elevated']}
            title={modelValue || 'Choose a model'}
          >
            <Cpu size={13} class="shrink-0 text-dark-text-muted" />
            <span class={['truncate font-medium', modelValue ? '' : 'text-amber-400']}>{modelLabel}</span>
            <ChevronDown size={12} class="shrink-0 text-dark-text-muted" />
          </button>

          {#if efforts.length || session.reasoning_effort}
            <button
              type="button"
              disabled={working}
              onclick={() => (palette = 'effort')}
              aria-label="Choose reasoning effort"
              class={['inline-flex h-7 items-center gap-1.5 px-2 text-xs', session.reasoning_effort ? 'text-dark-text' : 'text-dark-text-muted', working ? 'opacity-50' : 'cursor-pointer hover:bg-dark-elevated']}
              title="Reasoning effort (/effort)"
            >
              <Gauge size={13} class="shrink-0" />
              <span>{effortLabel}</span>
            </button>
          {/if}

          <button
            type="button"
            disabled={working}
            onclick={() => (palette = 'agent')}
            aria-label="Choose agent"
            class={['relative inline-flex h-7 min-w-0 max-w-48 items-center gap-1.5 px-2 text-xs font-medium', working ? 'opacity-50' : 'cursor-pointer hover:bg-dark-elevated',
              session.agent_id ? (agentChoice ? 'text-dark-text' : 'text-amber-400')
                : session.mode === 'build' ? 'text-green-400' : session.mode === 'plan' ? 'text-blue-400' : 'text-violet-400']}
            title={agentHint}
          >
            {#if session.agent_id}<Bot size={13} class="shrink-0" />{:else if session.mode === 'build'}<Hammer size={13} class="shrink-0" />{:else if session.mode === 'plan'}<ListChecks size={13} class="shrink-0" />{:else}<Eye size={13} class="shrink-0" />{/if}
            <span class="truncate">{agentLabel}</span>
            <ChevronDown size={12} class="shrink-0 text-dark-text-muted" />
          </button>

          <VoiceInput
            compact
            contextKey={voiceContext}
            disabled={working || (!!pending && !busy)}
            bind:recording
            bind:transcribing
            ontext={text => { prompt = (prompt ? prompt + ' ' : '') + text; textarea?.focus(); }}
          />

          {#if working}
            {#if busy && prompt.trim() && !attachments.length}
              <button type="button" onclick={steer} class="ml-1 inline-flex size-8 items-center justify-center text-dark-base bg-accent hover:bg-accent-hover" title="Add to the running turn (Enter)" aria-label="Add to the running turn"><ArrowUp size={16} /></button>
            {/if}
            <button type="button" onclick={stop} class="ml-1 inline-flex size-8 items-center justify-center bg-red-600 text-white hover:bg-red-700" title="Stop" aria-label="Stop the agent"><Square size={14} /></button>
          {:else}
            <button type="button" onclick={send} disabled={(!prompt.trim() && !attachments.length) || !!pending || !session.provider || recording || transcribing || reading} class="ml-1 inline-flex size-8 items-center justify-center text-dark-base bg-accent hover:bg-accent-hover disabled:bg-dark-elevated disabled:text-dark-text-muted" title="Send (Enter)" aria-label="Send"><ArrowUp size={16} /></button>
          {/if}
        </div>
      </div>
    </div>
  </div>
</div>

{#if palette}
  <CommandPalette title={palette === 'model' ? 'Choose model' : palette === 'effort' ? 'Reasoning effort' : 'Choose agent'} groups={paletteGroups} onclose={() => { palette = ''; textarea?.focus(); }} />
{/if}
