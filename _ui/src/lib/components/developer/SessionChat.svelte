<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    ArrowUp, Bot, Brain, Check, ChevronDown, Cpu, Eye, FileDiff, FolderGit2, Hammer, ListChecks, LoaderCircle,
    Maximize2, MessageCircleQuestion, Minimize2, ShieldAlert, Square, X,
  } from 'lucide-svelte';
  import SquareAlert from '@/lib/components/icons/SquareAlert.svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import ToolActivity from '@/lib/components/ToolActivity.svelte';
  import CommandPalette, { type PaletteGroup } from '@/lib/components/playground/CommandPalette.svelte';
  import { createAdaptivePoll } from '@/lib/helper/adaptive-poll';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import {
    getDeveloperGitStatus, getDeveloperSessionPendingTool, getDeveloperActiveStream, resumeDeveloperStream, listDeveloperSessionMessages, streamDeveloperSession, cancelDeveloperSession, updateDeveloperSession,
    type DeveloperMode, type DeveloperPendingTool, type DeveloperRun, type DeveloperSession, type DeveloperSessionMessage, type DeveloperStreamEvent,
  } from '@/lib/api/developer-spaces';
  import {
    buildTranscript, developerAgentSettings, developerAgentValue, toolSummary, MODE_HINTS, STATUS_LABELS,
    type DeveloperAgentChoice, type TranscriptEntry,
  } from '@/lib/helper/developer-space';
  import { formatMessageTime } from '@/lib/helper/format';
  import { addToast } from '@/lib/store/toast.svelte';

  interface ModelGroup { label: string; models: string[] }
  interface Props {
    session: DeveloperSession;
    modelGroups: ModelGroup[];
    /** Built-in profiles plus the workspace's agents. */
    agentChoices: DeveloperAgentChoice[];
    /** Bumped by the page whenever files may have changed (saves, git actions). */
    revision?: number;
    onsession: (session: DeveloperSession) => void;
    /** Files the agent wrote, so open editors can reload them. */
    onfileschanged: (paths: string[], tree: boolean) => void;
    onopenfile: (path: string) => void;
    ondiff: (file: string, opts: { untracked?: boolean; head?: boolean }) => void;
  }
  let { session, modelGroups, agentChoices, revision = 0, onsession, onfileschanged, onopenfile, ondiff }: Props = $props();

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

  const transcript = $derived(buildTranscript(messages));
  const working = $derived(busy || session.status === 'running' || !!durableRun);
  const modelValue = $derived(session.provider ? `${session.provider}/${session.model ?? ''}` : '');
  const modelLabel = $derived(session.model || 'Choose a model');

  // ─── Changed files strip ───
  let changes = $state<ChangedFile[]>([]);
  let changesOpen = $state(false);
  let expandedComposer = $state(false);
  let changesSeq = 0;
  let recording = $state(false);
  let transcribing = $state(false);
  let palette = $state<'model' | 'agent' | ''>('');
  let textarea = $state<HTMLTextAreaElement>();
  const totals = $derived(changes.reduce((sum, c) => ({ add: sum.add + c.additions, del: sum.del + c.deletions }), { add: 0, del: 0 }));

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
  const paletteGroups = $derived<PaletteGroup[]>(palette === 'model'
    ? modelGroups.map(group => ({ label: group.label, items: group.models.map(model => ({ label: model, current: model === modelValue, disabled: working, run: () => chooseModel(model) })) }))
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

  async function send() {
    const text = prompt.trim();
    if (!text || working || recording || transcribing) return;
    if (!session.provider) { error = 'Choose a model first.'; return; }
    prompt = '';
    stickToBottom = true;
    // Optimistic bubble; replaced when the stored message arrives.
    messages = [...messages, { id: `local-${Date.now()}`, session_id: session.id, role: 'user', content: text, created_at: new Date().toISOString() }];
    await scrollToEnd();
    await stream('run', { prompt: text });
  }

  async function decide(approved: boolean) {
    if (busy) return;
    await stream('confirm', { approved });
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

  async function changeSettings(body: { mode?: DeveloperMode; agent_id?: string; provider?: string; model?: string }) {
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
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'm') {
      event.preventDefault();
      if (!working) palette = 'model';
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
        {#if entry.role === 'user'}
          <div class="border-l-2 border-accent bg-dark-surface px-4 py-3 sm:px-[22px]">
            <div class="whitespace-pre-wrap break-words text-sm leading-relaxed text-dark-text">{entry.text}</div>
            {#if entry.created_at && !entry.key.startsWith('local-')}<div class="mt-2 text-[11px] text-dark-text-faint">{formatMessageTime(entry.created_at)}</div>{/if}
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

      <div class={['border-l-2 bg-dark-elevated px-1 pt-1', working ? 'border-oc-peach' : 'border-accent']}>
        <textarea
          bind:this={textarea}
          bind:value={prompt}
          use:grow={[prompt, expandedComposer]}
          onkeydown={keydown}
          rows={expandedComposer ? 8 : 2}
          aria-label="Message the agent"
          placeholder={pending ? 'Answer the prompt above first' : 'Ask the agent to change, explain or review code…  (Enter to send, Shift+Enter for a new line)'}
          disabled={!!pending && !busy}
          class="block w-full resize-none border-0 bg-transparent px-3 pb-1 pt-2.5 text-sm leading-[22px] focus:outline-none focus:ring-0 disabled:opacity-60 text-dark-text placeholder:text-dark-text-muted"
        ></textarea>
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
            <button type="button" onclick={stop} class="ml-1 inline-flex size-8 items-center justify-center bg-red-600 text-white hover:bg-red-700" title="Stop" aria-label="Stop the agent"><Square size={14} /></button>
          {:else}
            <button type="button" onclick={send} disabled={!prompt.trim() || !!pending || !session.provider || recording || transcribing} class="ml-1 inline-flex size-8 items-center justify-center text-dark-base bg-accent hover:bg-accent-hover disabled:bg-dark-elevated disabled:text-dark-text-muted" title="Send (Enter)" aria-label="Send"><ArrowUp size={16} /></button>
          {/if}
        </div>
      </div>
    </div>
  </div>
</div>

{#if palette}
  <CommandPalette title={palette === 'model' ? 'Choose model' : 'Choose agent'} groups={paletteGroups} onclose={() => { palette = ''; textarea?.focus(); }} />
{/if}
