<script lang="ts">
  import { tick } from 'svelte';
  import {
    ArrowUp, Brain, Check, ChevronDown, ChevronRight, CircleAlert, Cpu, Eye, FileDiff, FolderGit2, Hammer, ListChecks, LoaderCircle,
    Maximize2, MessageCircleQuestion, Minimize2, ShieldAlert, Square, Wrench, X,
  } from 'lucide-svelte';
  import Markdown from '@/lib/components/Markdown.svelte';
  import VoiceInput from '@/lib/components/VoiceInput.svelte';
  import {
    getDeveloperGitStatus, getDeveloperSessionPendingTool, listDeveloperSessionMessages, streamDeveloperSession, cancelDeveloperSession, updateDeveloperSession,
    type DeveloperMode, type DeveloperPendingTool, type DeveloperSession, type DeveloperSessionMessage, type DeveloperStreamEvent,
  } from '@/lib/api/developer-spaces';
  import { buildTranscript, toolSummary, MODE_HINTS, MODE_LABELS, STATUS_LABELS, type TranscriptEntry } from '@/lib/helper/developer-space';
  import { formatMessageTime } from '@/lib/helper/format';
  import { addToast } from '@/lib/store/toast.svelte';

  interface ModelGroup { label: string; models: string[] }
  interface Props {
    session: DeveloperSession;
    modelGroups: ModelGroup[];
    /** Bumped by the page whenever files may have changed (saves, git actions). */
    revision?: number;
    onsession: (session: DeveloperSession) => void;
    /** Files the agent wrote, so open editors can reload them. */
    onfileschanged: (paths: string[], tree: boolean) => void;
    onopenfile: (path: string) => void;
    ondiff: (file: string, opts: { untracked?: boolean; head?: boolean }) => void;
  }
  let { session, modelGroups, revision = 0, onsession, onfileschanged, onopenfile, ondiff }: Props = $props();

  interface ChangedFile { path: string; additions: number; deletions: number; binary: boolean; untracked: boolean }

  let messages = $state<DeveloperSessionMessage[]>([]);
  let pending = $state<DeveloperPendingTool | null>(null);
  let loading = $state(true);
  let prompt = $state('');
  let answer = $state('');
  let busy = $state(false);
  let error = $state('');
  // Live text of the turn being generated; replaced by the saved message.
  let liveText = $state('');
  let liveThinking = $state('');
  let runningTools = $state<Record<string, boolean>>({});
  let controller: AbortController | null = null;
  let scroller: HTMLDivElement;
  let stickToBottom = true;

  const transcript = $derived(buildTranscript(messages));
  const working = $derived(busy || session.status === 'running');
  const modelValue = $derived(session.provider ? `${session.provider}/${session.model ?? ''}` : '');
  const modelLabel = $derived(session.model || 'Choose a model');

  // ─── Changed files strip ───
  let changes = $state<ChangedFile[]>([]);
  let changesOpen = $state(false);
  let expandedComposer = $state(false);
  let changesSeq = 0;
  let recording = $state(false);
  let transcribing = $state(false);
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

  function toggleMode() {
    if (working) return;
    const order: DeveloperMode[] = ['build', 'plan', 'review'];
    void changeSettings({ mode: order[(order.indexOf(session.mode) + 1) % order.length] });
  }

  let loadedFor = '';
  let voiceContext = $state(0);
  $effect(() => {
    const id = session.id;
    if (id === loadedFor) return;
    loadedFor = id;
    voiceContext++;
    void load(id);
  });

  async function load(id: string) {
    loading = true;
    error = '';
    liveText = '';
    liveThinking = '';
    try {
      const [records, waiting] = await Promise.all([listDeveloperSessionMessages(id), getDeveloperSessionPendingTool(id)]);
      if (id !== session.id) return;
      messages = records;
      pending = waiting;
      stickToBottom = true;
      await scrollToEnd();
    } catch (e: any) {
      error = e?.response?.data?.message || e?.message || 'Could not load this session';
    } finally {
      loading = false;
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

  async function stream(action: 'run' | 'confirm' | 'answer', body: Record<string, unknown>) {
    busy = true;
    error = '';
    controller = new AbortController();
    try {
      await streamDeveloperSession(session.id, action, body, handle, controller.signal);
    } catch (e: any) {
      if (e?.name !== 'AbortError') error = e?.message || 'The agent stopped unexpectedly';
      // Whatever was saved before the failure is authoritative.
      await load(session.id);
    } finally {
      busy = false;
      liveText = '';
      liveThinking = '';
      runningTools = {};
      controller = null;
      void loadChanges();
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
    messages = messages.filter(m => !m.id.startsWith('local-'));
    await load(session.id);
  }

  async function decide(approved: boolean) {
    pending = null;
    await stream('confirm', { approved });
  }

  async function reply() {
    const text = answer.trim();
    if (!text) return;
    answer = '';
    pending = null;
    await stream('answer', { answer: text });
  }

  async function stop() {
    controller?.abort();
    try {
      onsession(await cancelDeveloperSession(session.id));
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not stop the session', 'alert');
    }
    pending = null;
  }

  async function changeSettings(body: { mode?: DeveloperMode; provider?: string; model?: string }) {
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
  {@const running = runningTools[tool.id]}
  <details class="min-w-0 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base group/tool">
    <summary class="flex cursor-pointer list-none items-center gap-2 px-2.5 py-1.5 text-xs hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent [&::-webkit-details-marker]:hidden">
      <ChevronRight size={13} class="shrink-0 text-gray-400 group-open/tool:rotate-90" />
      <Wrench size={12} class="shrink-0 text-gray-500 dark:text-dark-text-muted" />
      <span class="shrink-0 font-mono font-medium text-gray-800 dark:text-dark-text">{tool.name}</span>
      <span class="min-w-0 flex-1 truncate font-mono text-gray-500 dark:text-dark-text-muted">{toolSummary(tool)}</span>
      {#if fileArg(tool)}
        <button type="button" class="shrink-0 text-gray-500 hover:text-gray-900 dark:hover:text-dark-text underline-offset-2 hover:underline" onclick={event => { event.preventDefault(); onopenfile(projectFile(fileArg(tool))); }}>Open</button>
      {/if}
      {#if running}
        <LoaderCircle size={13} class="shrink-0 animate-spin text-gray-500 motion-reduce:animate-none" />
      {:else if tool.failed}
        <CircleAlert size={13} class="shrink-0 text-red-600 dark:text-red-400" />
      {:else if tool.result !== undefined}
        <Check size={13} class="shrink-0 text-green-600 dark:text-green-400" />
      {/if}
    </summary>
    <div class="space-y-2 border-t border-gray-200 dark:border-dark-border p-2.5">
      <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-words border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-2 text-[11px] text-gray-800 dark:text-dark-text">{JSON.stringify(tool.input, null, 2)}</pre>
      {#if tool.result !== undefined}
        <pre class={['max-h-72 overflow-auto whitespace-pre-wrap break-words border bg-white dark:bg-dark-surface p-2 text-[11px]', tool.failed ? 'border-red-200 dark:border-red-900 text-red-800 dark:text-red-300' : 'border-gray-200 dark:border-dark-border text-gray-800 dark:text-dark-text']}>{tool.result || '(empty)'}</pre>
      {:else}
        <p class="text-[11px] text-gray-500 dark:text-dark-text-muted">{running ? 'Running…' : 'No result recorded.'}</p>
      {/if}
    </div>
  </details>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-gray-50 dark:bg-dark-base">
  <!-- Session bar -->
  <div class="flex min-h-10 flex-wrap items-center gap-2 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-1.5 text-xs">
    <span class="inline-flex min-w-0 items-center gap-1 text-gray-500 dark:text-dark-text-muted" title="The agent works inside this folder">
      <FolderGit2 size={13} class="shrink-0" />
      <span class="truncate font-mono">/workspace{session.project_path ? `/${session.project_path}` : ''}</span>
    </span>
    <span class="text-gray-300 dark:text-dark-border">|</span>
    <span class={['inline-flex items-center gap-1', session.status === 'failed' ? 'text-red-700 dark:text-red-400' : session.status.startsWith('waiting') ? 'text-amber-700 dark:text-amber-400' : 'text-gray-500 dark:text-dark-text-muted']}>
      {#if working}<LoaderCircle size={12} class="animate-spin motion-reduce:animate-none" />{/if}
      {STATUS_LABELS[session.status] ?? session.status}
    </span>
  </div>

  <!-- Transcript -->
  <div bind:this={scroller} onscroll={onScroll} class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    <div class="mx-auto flex max-w-3xl flex-col gap-4 px-4 py-5">
      {#if loading}
        <p class="text-sm text-gray-500 dark:text-dark-text-muted">Loading…</p>
      {:else if transcript.length === 0 && !liveText}
        <div class="mt-10 text-center text-sm text-gray-500 dark:text-dark-text-muted">
          <p class="font-medium text-gray-700 dark:text-dark-text-secondary">What should the agent do in {session.project_path || 'this space'}?</p>
          <p class="mt-1">{MODE_HINTS[session.mode]}</p>
        </div>
      {/if}
      {#each transcript as entry (entry.key)}
        {#if entry.role === 'user'}
          <div class="flex justify-end">
            <div class="max-w-[85%]">
              <div class="whitespace-pre-wrap break-words bg-gray-900 dark:bg-accent px-4 py-2.5 text-sm leading-relaxed text-white">{entry.text}</div>
              {#if entry.created_at && !entry.key.startsWith('local-')}<div class="mt-1 text-right text-[11px] text-gray-400 dark:text-dark-text-muted">{formatMessageTime(entry.created_at)}</div>{/if}
            </div>
          </div>
        {:else}
          <div class="min-w-0">
            <div class="min-w-0 border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-4 py-2.5 text-sm leading-relaxed text-gray-800 dark:text-dark-text shadow-sm">
              {#if entry.thinking}
                <details class="mb-2 text-xs text-gray-500 dark:text-dark-text-muted">
                  <summary class="inline-flex cursor-pointer items-center gap-1"><Brain size={12} /> Reasoning</summary>
                  <p class="mt-1 whitespace-pre-wrap">{entry.thinking}</p>
                </details>
              {/if}
              {#if entry.text}<Markdown source={entry.text} enhance />{/if}
              {#if entry.tools.length}
                <div class={['space-y-1', entry.text ? 'mt-2 border-t border-gray-200 dark:border-dark-border pt-2' : '']}>
                  {#each entry.tools as tool (tool.id)}{@render toolRow(tool)}{/each}
                </div>
              {/if}
            </div>
            {#if entry.created_at}<div class="mt-1 text-[11px] text-gray-400 dark:text-dark-text-muted">{formatMessageTime(entry.created_at)}</div>{/if}
          </div>
        {/if}
      {/each}

      {#if busy && (liveText || liveThinking || Object.keys(runningTools).length === 0)}
        <div class="border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-4 py-2.5 text-sm leading-relaxed text-gray-800 dark:text-dark-text shadow-sm">
          {#if liveThinking && !liveText}
            <p class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted"><Brain size={12} /> Thinking…</p>
          {/if}
          {#if liveText}
            <Markdown source={liveText} />
          {:else if !liveThinking}
            <span class="inline-flex items-center gap-2 italic text-gray-400 dark:text-dark-text-muted"><LoaderCircle size={13} class="animate-spin motion-reduce:animate-none" /> Working…</span>
          {/if}
        </div>
      {/if}

      {#if pending && !busy}
        {#if pending.kind === 'permission'}
          <div class="border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 p-3 text-sm">
            <p class="flex items-center gap-2 font-medium text-amber-900 dark:text-amber-200"><ShieldAlert size={16} /> The agent wants to run:</p>
            <ul class="mt-2 space-y-1">
              {#each pendingSummary(pending) as item}
                <li class="break-all font-mono text-xs text-gray-800 dark:text-dark-text"><span class="font-semibold">{item.name}</span> {item.detail}</li>
              {/each}
            </ul>
            <div class="mt-3 flex gap-2">
              <button type="button" onclick={() => decide(true)} class="inline-flex items-center gap-1.5 bg-gray-900 dark:bg-accent px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800 dark:hover:bg-accent-hover"><Check size={13} /> Allow</button>
              <button type="button" onclick={() => decide(false)} class="inline-flex items-center gap-1.5 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-xs text-gray-700 dark:text-dark-text-secondary hover:bg-white dark:hover:bg-dark-elevated"><X size={13} /> Reject</button>
            </div>
          </div>
        {:else}
          {@const question = pending.tool_calls.find(call => call.Name === 'ask_user')?.Arguments?.question}
          <div class="border border-blue-300 dark:border-blue-800 bg-blue-50 dark:bg-blue-950/40 p-3 text-sm">
            <p class="flex items-start gap-2 text-gray-900 dark:text-dark-text"><MessageCircleQuestion size={16} class="mt-0.5 shrink-0 text-blue-600" /> <span class="whitespace-pre-wrap">{String(question ?? 'The agent has a question.')}</span></p>
            <form class="mt-3 flex gap-2" onsubmit={event => { event.preventDefault(); void reply(); }}>
              <input bind:value={answer} aria-label="Answer" placeholder="Your answer" class="min-w-0 flex-1 border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-1.5 text-sm dark:text-dark-text focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20" />
              <button type="submit" disabled={!answer.trim()} class="bg-gray-900 dark:bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-40">Reply</button>
            </form>
          </div>
        {/if}
      {/if}

      {#if error}
        <p role="alert" class="flex items-start gap-2 border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-800 dark:text-red-300"><CircleAlert size={15} class="mt-0.5 shrink-0" /> {error}</p>
      {:else if session.status === 'failed' && session.error && !busy}
        <p class="flex items-start gap-2 border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-800 dark:text-red-300"><CircleAlert size={15} class="mt-0.5 shrink-0" /> {session.error}</p>
      {/if}
    </div>
  </div>

  <!-- Composer -->
  <div class="bg-gray-50 dark:bg-dark-base px-3 pb-3 pt-1">
    <div class="mx-auto max-w-3xl">
      {#if changes.length}
        <div class="mb-1.5 text-xs">
          <button
            type="button"
            onclick={() => (changesOpen = !changesOpen)}
            aria-expanded={changesOpen}
            class="inline-flex items-center gap-1.5 px-1 py-0.5 text-gray-600 hover:text-gray-900 dark:text-dark-text-secondary dark:hover:text-dark-text"
          >
            <FileDiff size={13} class="shrink-0 text-amber-600 dark:text-amber-400" />
            <span>{changes.length} file{changes.length === 1 ? '' : 's'} changed in {session.project_path || 'workspace'}</span>
            <span class="font-mono text-green-700 dark:text-green-400">+{totals.add}</span>
            <span class="font-mono text-red-700 dark:text-red-400">-{totals.del}</span>
            <ChevronDown size={13} class={changesOpen ? 'shrink-0 rotate-180' : 'shrink-0'} />
          </button>
          {#if changesOpen}
            <ul class="mt-1 max-h-48 overflow-y-auto border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              {#each changes as change (change.path)}
                <li>
                  <button
                    type="button"
                    onclick={() => ondiff(change.path, change.untracked ? { untracked: true } : { head: true })}
                    class="flex w-full items-center gap-2 px-2.5 py-1 text-left hover:bg-gray-100 dark:hover:bg-dark-elevated"
                    title="Show diff"
                  >
                    <span class="min-w-0 flex-1 truncate font-mono text-gray-800 dark:text-dark-text">{change.path}</span>
                    {#if change.untracked}<span class="shrink-0 text-[10px] uppercase text-gray-400">new</span>{/if}
                    {#if change.binary}
                      <span class="shrink-0 text-gray-400">binary</span>
                    {:else}
                      <span class="shrink-0 font-mono text-green-700 dark:text-green-400">+{change.additions}</span>
                      <span class="shrink-0 font-mono text-red-700 dark:text-red-400">-{change.deletions}</span>
                    {/if}
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}

      <div class="border border-gray-300 bg-white focus-within:border-gray-400 dark:border-dark-border dark:bg-dark-surface dark:focus-within:border-dark-text-muted">
        <textarea
          bind:this={textarea}
          bind:value={prompt}
          use:grow={[prompt, expandedComposer]}
          onkeydown={keydown}
          rows={expandedComposer ? 8 : 2}
          aria-label="Message the agent"
          placeholder={pending ? 'Answer the prompt above first' : 'Ask the agent to change, explain or review code…  (Enter to send, Shift+Enter for a new line)'}
          disabled={!!pending && !busy}
          class="block w-full resize-none border-0 bg-transparent px-3 pb-1 pt-2.5 text-sm leading-[22px] text-gray-900 placeholder:text-gray-400 focus:outline-none focus:ring-0 disabled:opacity-60 dark:text-dark-text dark:placeholder:text-dark-text-muted"
        ></textarea>
        <div class="flex items-center gap-1 px-1.5 pb-1.5">
          <button
            type="button"
            onclick={() => (expandedComposer = !expandedComposer)}
            class="inline-flex size-7 items-center justify-center text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-text-muted dark:hover:bg-dark-elevated dark:hover:text-dark-text"
            title={expandedComposer ? 'Shrink the message box' : 'Enlarge the message box'}
            aria-label={expandedComposer ? 'Shrink the message box' : 'Enlarge the message box'}
            aria-pressed={expandedComposer}
          >
            {#if expandedComposer}<Minimize2 size={15} />{:else}<Maximize2 size={15} />{/if}
          </button>
          <span class="flex-1"></span>

          <label
            class={['relative inline-flex h-7 min-w-0 max-w-56 items-center gap-1.5 px-2 text-xs text-gray-700 dark:text-dark-text-secondary', working ? 'opacity-50' : 'cursor-pointer hover:bg-gray-100 dark:hover:bg-dark-elevated']}
            title={modelValue || 'Choose a model'}
          >
            <Cpu size={13} class="shrink-0 text-gray-500 dark:text-dark-text-muted" />
            <span class={['truncate font-medium', modelValue ? '' : 'text-amber-700 dark:text-amber-400']}>{modelLabel}</span>
            <ChevronDown size={12} class="shrink-0 text-gray-400" />
            <select
              value={modelValue}
              disabled={working}
              onchange={event => chooseModel(event.currentTarget.value)}
              aria-label="Model"
              class="absolute inset-0 cursor-pointer opacity-0 disabled:cursor-default"
            >
              {#if !modelValue}<option value="">Choose a model…</option>{/if}
              {#if modelValue && !modelGroups.some(g => g.models.includes(modelValue))}<option value={modelValue}>{modelValue}</option>{/if}
              {#each modelGroups as group}
                <optgroup label={group.label}>
                  {#each group.models as model}<option value={model}>{model.slice(model.indexOf('/') + 1)}</option>{/each}
                </optgroup>
              {/each}
            </select>
          </label>

          <button
            type="button"
            onclick={toggleMode}
            disabled={working}
            title={`${MODE_HINTS[session.mode]} — click to switch mode`}
            aria-label={`Mode: ${MODE_LABELS[session.mode]}. Click to switch.`}
            class={['inline-flex h-7 items-center gap-1.5 px-2 text-xs font-medium hover:bg-gray-100 disabled:opacity-50 dark:hover:bg-dark-elevated',
              session.mode === 'build' ? 'text-green-700 dark:text-green-400' : session.mode === 'plan' ? 'text-blue-700 dark:text-blue-400' : 'text-violet-700 dark:text-violet-400']}
          >
            {#if session.mode === 'build'}<Hammer size={13} />{:else if session.mode === 'plan'}<ListChecks size={13} />{:else}<Eye size={13} />{/if}
            {MODE_LABELS[session.mode]}
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
            <button type="button" onclick={send} disabled={!prompt.trim() || !!pending || !session.provider || recording || transcribing} class="ml-1 inline-flex size-8 items-center justify-center bg-gray-900 text-white hover:bg-gray-800 disabled:bg-gray-200 disabled:text-gray-400 dark:bg-accent dark:hover:bg-accent-hover dark:disabled:bg-dark-elevated dark:disabled:text-dark-text-muted" title="Send (Enter)" aria-label="Send"><ArrowUp size={16} /></button>
          {/if}
        </div>
      </div>
    </div>
  </div>
</div>
