<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    cloneDeveloperRepository, createDeveloperRepository, createDeveloperSession,
    createDeveloperSpace, createDeveloperWorktree, deleteDeveloperRepository, deleteDeveloperSession, deleteDeveloperWorktree,
    deleteDeveloperSpace, getDeveloperWorktreeDiff, getDeveloperWorktreeStatus,
    stageDeveloperWorktree, commitDeveloperWorktree, pushDeveloperWorktree,
    listDeveloperRepositories, listDeveloperSessions,
    listDeveloperSpaces, listDeveloperWorktrees, provisionDeveloperWorktree,
    runDeveloperSession, confirmDeveloperSessionTool, answerDeveloperSessionQuestion, cancelDeveloperSession,
    getDeveloperSessionPendingTool, getDeveloperSessionSnapshot, listDeveloperSessionMessages,
    listDeveloperSessionSnapshots, startDeveloperSpace, stopDeveloperSpace,
    developerWorktreeTerminalURL,
    type DeveloperPendingTool, type DeveloperRepository, type DeveloperSession,
    type DeveloperSessionMessage, type DeveloperSessionSnapshot, type DeveloperSpace, type DeveloperWorktree,
  } from '@/lib/api/developer-spaces';
  import { Box, GitBranch, Loader2, Play, Plus, RefreshCw, Square, Trash2 } from 'lucide-svelte';
  import HostTerminal from '@/lib/components/HostTerminal.svelte';

  storeNavbar.title = 'Developer Spaces';

  let spaces = $state<DeveloperSpace[]>([]);
  let selected = $state<DeveloperSpace | null>(null);
  let repositories = $state<DeveloperRepository[]>([]);
  let worktrees = $state<Record<string, DeveloperWorktree[]>>({});
  let sessions = $state<DeveloperSession[]>([]);
  let loading = $state(true);
  let busy = $state('');
  let showSpaceForm = $state(false);
  let showRepositoryForm = $state(false);
  let spaceName = $state('');
  let spaceImage = $state('at-agent-runtime:latest');
  let repositoryName = $state('');
  let repositoryURL = $state('');
  let repositoryBranch = $state('main');
  let worktreeName = $state('');
  let worktreeBranch = $state('');
  let sessionTitle = $state('');
  let sessionMode = $state<'plan' | 'build' | 'review'>('build');
  let sessionProvider = $state('');
  let sessionModel = $state('');
  let sessionPrompt = $state<Record<string, string>>({});
  let sessionOutput = $state<Record<string, string>>({});
  let sessionMessages = $state<Record<string, DeveloperSessionMessage[]>>({});
  let pendingTools = $state<Record<string, DeveloperPendingTool | null>>({});
  let sessionSnapshots = $state<Record<string, DeveloperSessionSnapshot[]>>({});
  let sessionSnapshotDiff = $state<Record<string, string>>({});
  let activeSessionID = $state('');
  let questionAnswer = $state<Record<string, string>>({});
  let terminalWorktree = $state<DeveloperWorktree | null>(null);
  let worktreeReview = $state<Record<string, { status: string; diff: string; staged: boolean }>>({});
  let commitMessages = $state<Record<string, string>>({});

  const buttonClass = 'inline-flex min-h-9 items-center justify-center gap-1.5 border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-elevated disabled:opacity-50';
  const inputClass = 'h-9 w-full border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-base px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-accent';

  function failure(error: any, fallback: string) {
    return error?.response?.data?.message || (typeof error?.response?.data === 'string' ? error.response.data : fallback);
  }

  async function loadSpaces() {
    loading = true;
    try {
      spaces = await listDeveloperSpaces();
      if (selected) selected = spaces.find((space) => space.id === selected?.id) || null;
    } catch (error) {
      addToast(failure(error, 'Could not load developer spaces'), 'alert');
    } finally {
      loading = false;
    }
  }

  async function selectSpace(space: DeveloperSpace) {
    selected = space;
    busy = 'detail';
    try {
      [repositories, sessions] = await Promise.all([listDeveloperRepositories(space.id), listDeveloperSessions(space.id)]);
      const pairs = await Promise.all(repositories.map(async (repository) => [repository.id, await listDeveloperWorktrees(repository.id)] as const));
      worktrees = Object.fromEntries(pairs);
      const details = await Promise.all(sessions.map(async (session) => {
        const [messages, pending, snapshots] = await Promise.all([listDeveloperSessionMessages(session.id), getDeveloperSessionPendingTool(session.id), listDeveloperSessionSnapshots(session.id)]);
        return [session.id, messages, pending, snapshots] as const;
      }));
      sessionMessages = Object.fromEntries(details.map(([id, messages]) => [id, messages]));
      pendingTools = Object.fromEntries(details.map(([id, , pending]) => [id, pending]));
      sessionSnapshots = Object.fromEntries(details.map(([id, , , snapshots]) => [id, snapshots]));
    } catch (error) {
      addToast(failure(error, 'Could not load the space'), 'alert');
    } finally {
      busy = '';
    }
  }

  async function submitSpace() {
    if (!spaceName.trim()) return;
    busy = 'space';
    try {
      const created = await createDeveloperSpace({ name: spaceName, image: spaceImage });
      spaceName = '';
      showSpaceForm = false;
      await loadSpaces();
      await selectSpace(created);
    } catch (error) {
      addToast(failure(error, 'Could not create developer space'), 'alert');
    } finally { busy = ''; }
  }

  async function runtime(action: 'start' | 'stop') {
    if (!selected) return;
    busy = action;
    try {
      selected = action === 'start' ? await startDeveloperSpace(selected.id) : await stopDeveloperSpace(selected.id);
      await loadSpaces();
    } catch (error) {
      addToast(failure(error, `Could not ${action} developer space`), 'alert');
    } finally { busy = ''; }
  }

  async function removeSpace(space: DeveloperSpace) {
    if (!confirm(`Delete developer space "${space.name}" and its repositories?`)) return;
    busy = space.id;
    try {
      await deleteDeveloperSpace(space.id);
      if (selected?.id === space.id) selected = null;
      await loadSpaces();
    } catch (error) { addToast(failure(error, 'Could not delete developer space'), 'alert'); }
    finally { busy = ''; }
  }

  async function submitRepository() {
    if (!selected || !repositoryName.trim() || !repositoryURL.trim()) return;
    busy = 'repository';
    try {
      await createDeveloperRepository(selected.id, { name: repositoryName, remote_url: repositoryURL, default_branch: repositoryBranch });
      repositoryName = ''; repositoryURL = ''; showRepositoryForm = false;
      await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not add repository'), 'alert'); }
    finally { busy = ''; }
  }

  async function clone(repository: DeveloperRepository) {
    busy = repository.id;
    try {
      await cloneDeveloperRepository(repository.id);
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not clone repository'), 'alert'); }
    finally { busy = ''; }
  }

  async function removeRepository(repository: DeveloperRepository) {
    if (!confirm(`Remove repository "${repository.name}"? Sessions are retained.`)) return;
    busy = repository.id;
    try { await deleteDeveloperRepository(repository.id); if (selected) await selectSpace(selected); }
    catch (error) { addToast(failure(error, 'Could not remove repository'), 'alert'); }
    finally { busy = ''; }
  }

  async function addWorktree(repository: DeveloperRepository) {
    if (!worktreeName.trim() || !worktreeBranch.trim()) return;
    busy = `worktree-${repository.id}`;
    try {
      const created = await createDeveloperWorktree(repository.id, { name: worktreeName, branch: worktreeBranch, base_ref: repository.default_branch || 'HEAD' });
      await provisionDeveloperWorktree(created.id);
      worktreeName = ''; worktreeBranch = '';
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not create worktree'), 'alert'); }
    finally { busy = ''; }
  }

  async function addSession(worktree?: DeveloperWorktree) {
    if (!selected) return;
    if (!sessionProvider.trim()) { addToast('Provider is required for a coding session', 'alert'); return; }
    busy = 'session';
    try {
      await createDeveloperSession(selected.id, { title: sessionTitle, mode: sessionMode, worktree_id: worktree?.id, repository_id: worktree?.repository_id, provider: sessionProvider, model: sessionModel });
      sessionTitle = '';
      await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not create coding session'), 'alert'); }
    finally { busy = ''; }
  }

  async function removeWorktree(worktree: DeveloperWorktree) {
    if (!confirm(`Prune worktree "${worktree.name}"? Coding-session history will be retained.`)) return;
    busy = `worktree-${worktree.id}`;
    try {
      await deleteDeveloperWorktree(worktree.id);
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not prune worktree'), 'alert'); }
    finally { busy = ''; }
  }

  async function removeSession(session: DeveloperSession) {
    if (!confirm(`Delete coding session "${session.title || 'Untitled session'}" and its retained snapshots?`)) return;
    busy = `run-${session.id}`;
    try {
      await deleteDeveloperSession(session.id);
      delete sessionMessages[session.id];
      delete pendingTools[session.id];
      delete sessionSnapshots[session.id];
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not delete coding session'), 'alert'); }
    finally { busy = ''; }
  }

  async function reviewWorktree(worktree: DeveloperWorktree, staged = false) {
    busy = `review-${worktree.id}`;
    try {
      const [status, diff] = await Promise.all([getDeveloperWorktreeStatus(worktree.id), getDeveloperWorktreeDiff(worktree.id, staged)]);
      worktreeReview[worktree.id] = { status: status.porcelain_v2, diff: diff.diff, staged };
    } catch (error) { addToast(failure(error, 'Could not load worktree changes'), 'alert'); }
    finally { busy = ''; }
  }

  async function stageAll(worktree: DeveloperWorktree) {
    busy = `review-${worktree.id}`;
    try { await stageDeveloperWorktree(worktree.id, ['.']); await reviewWorktree(worktree, true); }
    catch (error) { addToast(failure(error, 'Could not stage worktree changes'), 'alert'); }
    finally { busy = ''; }
  }

  async function commitWorktree(worktree: DeveloperWorktree) {
    const message = commitMessages[worktree.id]?.trim();
    if (!message) return;
    busy = `review-${worktree.id}`;
    try {
      await commitDeveloperWorktree(worktree.id, message);
      commitMessages[worktree.id] = '';
      await reviewWorktree(worktree, false);
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not commit worktree changes'), 'alert'); }
    finally { busy = ''; }
  }

  async function pushWorktree(worktree: DeveloperWorktree) {
    if (!confirm(`Push HEAD to origin/${worktree.branch}?`)) return;
    busy = `review-${worktree.id}`;
    try { await pushDeveloperWorktree(worktree.id, worktree.branch); addToast(`Pushed ${worktree.branch}`, 'info'); }
    catch (error) { addToast(failure(error, 'Could not push worktree branch'), 'alert'); }
    finally { busy = ''; }
  }

  async function runSession(session: DeveloperSession) {
    const prompt = sessionPrompt[session.id]?.trim();
    if (!prompt) return;
    busy = `run-${session.id}`;
    session.status = 'running';
    session.error = '';
    try {
      const result = await runDeveloperSession(session.id, prompt);
      sessionPrompt[session.id] = '';
      pendingTools[session.id] = result.pending_tool || null;
      if (result.content) sessionOutput[session.id] = result.content;
      if (selected) await selectSpace(selected);
    } catch (error) {
      addToast(failure(error, 'Coding session failed'), 'alert');
      if (selected) await selectSpace(selected);
    }
    finally { busy = ''; }
  }

  async function decideTool(session: DeveloperSession, approved: boolean) {
    busy = `run-${session.id}`;
    try {
      const result = await confirmDeveloperSessionTool(session.id, approved);
      pendingTools[session.id] = result.pending_tool || null;
      if (result.content) sessionOutput[session.id] = result.content;
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not continue coding session'), 'alert'); }
    finally { busy = ''; }
  }

  async function answerQuestion(session: DeveloperSession) {
    const answer = questionAnswer[session.id]?.trim();
    if (!answer) return;
    busy = `run-${session.id}`;
    try {
      const result = await answerDeveloperSessionQuestion(session.id, answer);
      questionAnswer[session.id] = '';
      pendingTools[session.id] = result.pending_tool || null;
      if (result.content) sessionOutput[session.id] = result.content;
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not submit the answer'), 'alert'); }
    finally { busy = ''; }
  }

  async function cancelSession(session: DeveloperSession) {
    busy = `run-${session.id}`;
    try {
      await cancelDeveloperSession(session.id);
      pendingTools[session.id] = null;
      if (selected) await selectSpace(selected);
    } catch (error) { addToast(failure(error, 'Could not cancel coding session'), 'alert'); }
    finally { busy = ''; }
  }

  async function showLatestSnapshot(session: DeveloperSession) {
    const snapshots = sessionSnapshots[session.id] || [];
    const snapshot = [...snapshots].reverse().find((item) => item.phase === 'after') || snapshots.at(-1);
    if (!snapshot) return;
    busy = `snapshot-${session.id}`;
    try {
      const payload = await getDeveloperSessionSnapshot(session.id, snapshot.id);
      sessionSnapshotDiff[session.id] = `${payload.truncated ? '[Snapshot truncated]\n' : ''}${payload.diff || 'No changes in this snapshot.'}`;
    } catch (error) { addToast(failure(error, 'Could not load session snapshot'), 'alert'); }
    finally { busy = ''; }
  }

  function contentText(content: unknown): string {
    if (typeof content === 'string') return content;
    if (!Array.isArray(content)) return '';
    return content.map((block: any) => {
      if (block?.type === 'text') return block.text || '';
      if (block?.type === 'tool_use') return `Tool: ${block.name}`;
      if (block?.type === 'tool_result') return typeof block.content === 'string' ? block.content : JSON.stringify(block.content);
      return '';
    }).filter(Boolean).join('\n');
  }

  function pendingQuestion(sessionID: string): string {
    const call = pendingTools[sessionID]?.tool_calls.find((item) => item.Name === 'ask_user');
    return typeof call?.Arguments?.question === 'string' ? call.Arguments.question : 'The coding agent needs more information.';
  }

  void loadSpaces();
</script>

<svelte:head><title>AT | Developer Spaces</title></svelte:head>

<div class="h-full min-h-0 overflow-auto bg-gray-50 dark:bg-dark-base p-4 text-gray-900 dark:text-dark-text">
  <div class="mx-auto max-w-7xl space-y-4">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div><h1 class="text-lg font-semibold">Developer Spaces</h1><p class="text-xs text-gray-500 dark:text-dark-text-muted">Isolated repositories, worktrees and native AT coding sessions.</p></div>
      <div class="flex gap-2"><button class={buttonClass} onclick={loadSpaces}><RefreshCw size={13}/>Refresh</button><button class={buttonClass} onclick={() => showSpaceForm = !showSpaceForm}><Plus size={13}/>New space</button></div>
    </header>

    {#if showSpaceForm}
      <form class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 grid gap-3 md:grid-cols-3" onsubmit={(event) => { event.preventDefault(); void submitSpace(); }}>
        <label class="text-xs font-medium">Name<input class={inputClass} bind:value={spaceName} placeholder="My project" /></label>
        <label class="text-xs font-medium">Runtime image<input class={inputClass} bind:value={spaceImage} /></label>
        <div class="flex items-end"><button class={buttonClass} disabled={busy === 'space'}>{#if busy === 'space'}<Loader2 size={13} class="animate-spin motion-reduce:animate-none"/>{:else}<Plus size={13}/>{/if}Create</button></div>
      </form>
    {/if}

    <div class="grid gap-4 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface min-h-64">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-xs font-semibold">Spaces</div>
        {#if loading}<div class="p-4 text-xs text-gray-500">Loading…</div>
        {:else if spaces.length === 0}<div class="p-4 text-xs text-gray-500">No developer spaces yet.</div>
        {:else}<div class="divide-y divide-gray-100 dark:divide-dark-border">{#each spaces as space}
          <div class={['p-3 flex items-start gap-2', selected?.id === space.id ? 'bg-accent/5' : '']}>
            <button class="min-w-0 flex-1 text-left focus-visible:outline-2 focus-visible:outline-accent" onclick={() => void selectSpace(space)}><span class="flex items-center gap-2 text-sm font-medium"><Box size={14}/>{space.name}</span><span class="mt-1 block text-[11px] text-gray-500">{space.status}{space.image ? ` · ${space.image}` : ''}</span></button>
            <button class="p-1.5 text-gray-400 hover:text-red-600" aria-label="Delete space" onclick={() => void removeSpace(space)}><Trash2 size={13}/></button>
          </div>
        {/each}</div>{/if}
      </section>

      <section class="min-w-0 space-y-4">
        {#if !selected}<div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-8 text-center text-sm text-gray-500">Select a space to manage its repositories and sessions.</div>
        {:else}
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base flex flex-wrap items-center justify-between gap-2"><div><h2 class="text-sm font-semibold">{selected.name}</h2><p class="text-[11px] text-gray-500">{selected.status}{selected.error ? ` · ${selected.error}` : ''}</p></div><div class="flex gap-2">{#if selected.status === 'ready'}<button class={buttonClass} onclick={() => void runtime('stop')}><Square size={12}/>Stop</button>{:else}<button class={buttonClass} onclick={() => void runtime('start')}><Play size={12}/>Start</button>{/if}<button class={buttonClass} onclick={() => showRepositoryForm = !showRepositoryForm}><Plus size={12}/>Repository</button></div></div>
            {#if showRepositoryForm}<form class="p-4 grid gap-3 md:grid-cols-3" onsubmit={(event) => { event.preventDefault(); void submitRepository(); }}><input class={inputClass} bind:value={repositoryName} placeholder="Repository name"/><input class={inputClass} bind:value={repositoryURL} placeholder="https://host/org/repo.git"/><div class="flex gap-2"><input class={inputClass} bind:value={repositoryBranch} placeholder="main"/><button class={buttonClass}>Add</button></div></form>{/if}
          </div>

          {#each repositories as repository}
            <article class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              <header class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base flex flex-wrap items-center justify-between gap-2"><div class="min-w-0"><h3 class="flex items-center gap-2 text-sm font-semibold"><GitBranch size={14}/>{repository.name}</h3><p class="truncate text-[11px] text-gray-500" title={repository.remote_url}>{repository.remote_url} · {repository.status}</p></div><div class="flex gap-2">{#if repository.status !== 'ready'}<button class={buttonClass} onclick={() => void clone(repository)}>Clone</button>{/if}<button class={buttonClass} onclick={() => void removeRepository(repository)}><Trash2 size={12}/></button></div></header>
              <div class="p-4 space-y-3">
                <div class="grid gap-2 md:grid-cols-[1fr_1fr_auto]"><input class={inputClass} bind:value={worktreeName} placeholder="Worktree name"/><input class={inputClass} bind:value={worktreeBranch} placeholder="feature/my-change"/><button class={buttonClass} disabled={repository.status !== 'ready'} onclick={() => void addWorktree(repository)}>Create worktree</button></div>
                {#if (worktrees[repository.id] || []).length === 0}
                  <p class="text-xs text-gray-500">No worktrees.</p>
                {:else}
                  <div class="divide-y divide-gray-100 dark:divide-dark-border">
                    {#each worktrees[repository.id] || [] as worktree}
                      <div class="py-2 space-y-2">
                        <div class="flex flex-wrap items-center justify-between gap-2">
                          <div><span class="text-xs font-medium">{worktree.name}</span><span class="ml-2 text-[11px] text-gray-500">{worktree.branch} · {worktree.state}</span></div>
                          <div class="flex flex-wrap gap-2">
                            <button class={buttonClass} disabled={worktree.state !== 'ready'} onclick={() => terminalWorktree = worktree}>Terminal</button>
                            <button class={buttonClass} disabled={worktree.state !== 'ready'} onclick={() => void reviewWorktree(worktree)}>Changes</button>
                            <button class={buttonClass} disabled={worktree.state !== 'ready'} onclick={() => void addSession(worktree)}>New session</button>
                            <button class={buttonClass} onclick={() => void removeWorktree(worktree)}>Prune</button>
                          </div>
                        </div>
                        {#if worktreeReview[worktree.id]}
                          <div class="border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base p-3 space-y-2">
                            <div class="flex flex-wrap gap-2">
                              <button class={buttonClass} onclick={() => void reviewWorktree(worktree, false)}>Unstaged diff</button>
                              <button class={buttonClass} onclick={() => void reviewWorktree(worktree, true)}>Staged diff</button>
                              <button class={buttonClass} onclick={() => void stageAll(worktree)}>Stage all</button>
                              <button class={buttonClass} onclick={() => void pushWorktree(worktree)}>Push {worktree.branch}</button>
                            </div>
                            <pre class="max-h-40 overflow-auto whitespace-pre-wrap text-[11px]">{worktreeReview[worktree.id].status || 'Working tree clean'}</pre>
                            <pre class="max-h-96 overflow-auto whitespace-pre text-[11px]">{worktreeReview[worktree.id].diff || 'No diff for this view.'}</pre>
                            <div class="flex gap-2"><input class={inputClass} bind:value={commitMessages[worktree.id]} placeholder="Commit message"/><button class={buttonClass} onclick={() => void commitWorktree(worktree)}>Commit staged</button></div>
                          </div>
                        {/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
            </article>
          {/each}

          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base text-xs font-semibold">Coding sessions</div>
            <div class="p-4 space-y-3">
              <div class="grid gap-2 md:grid-cols-[1fr_8rem_9rem_9rem]">
                <input class={inputClass} bind:value={sessionTitle} placeholder="Session title"/>
                <select class={inputClass} bind:value={sessionMode}><option value="plan">Plan</option><option value="build">Build</option><option value="review">Review</option></select>
                <input class={inputClass} bind:value={sessionProvider} placeholder="Provider"/>
                <input class={inputClass} bind:value={sessionModel} placeholder="Model"/>
              </div>
              <p class="text-[11px] text-gray-500">Set the defaults above, then choose <strong>New session</strong> on a ready worktree.</p>
              {#if sessions.length === 0}
                <p class="text-xs text-gray-500">No coding sessions.</p>
              {:else}
                <div class="divide-y divide-gray-100 dark:divide-dark-border">
                  {#each sessions as session}
                    <div class="py-3 space-y-2">
                      <div class="flex items-center justify-between gap-3">
                        <button class="min-w-0 text-left" onclick={() => activeSessionID = activeSessionID === session.id ? '' : session.id}>
                          <span class="text-xs font-medium">{session.title || 'Untitled session'}</span>
                          <span class="ml-2 text-[11px] text-gray-500">{session.mode} · {session.status} · {session.provider || 'no provider'}{session.model ? `/${session.model}` : ''}</span>
                          {#if session.error}<span class="mt-1 block text-[11px] text-red-600 dark:text-red-300">{session.error}</span>{/if}
                        </button>
                        <div class="flex items-center gap-2">
                          {#if !session.worktree_id}<span class="text-[11px] text-amber-700 dark:text-amber-300">worktree unavailable</span>{/if}
                          {#if (sessionSnapshots[session.id] || []).length > 0}<button class={buttonClass} onclick={() => void showLatestSnapshot(session)}>Snapshot {(sessionSnapshots[session.id] || []).length}</button>{/if}
                          {#if session.status === 'running' || session.status === 'waiting_permission' || session.status === 'waiting_question'}<button class={buttonClass} onclick={() => void cancelSession(session)}>Cancel</button>{/if}
                          <button class={buttonClass} disabled={session.status === 'running'} onclick={() => void removeSession(session)}>Delete</button>
                        </div>
                      </div>
                      {#if activeSessionID === session.id}
                        <div class="max-h-80 space-y-2 overflow-auto border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base p-3">
                          {#if (sessionMessages[session.id] || []).length === 0}<p class="text-xs text-gray-500">No messages yet.</p>{/if}
                          {#each sessionMessages[session.id] || [] as message}
                            <div class="border-l-2 border-gray-300 dark:border-dark-border pl-2">
                              <div class="text-[10px] font-semibold uppercase text-gray-500">{message.role}</div>
                              <pre class="whitespace-pre-wrap break-words font-sans text-xs">{contentText(message.content)}</pre>
                            </div>
                          {/each}
                          {#if sessionOutput[session.id]}<pre class="whitespace-pre-wrap break-words text-xs">{sessionOutput[session.id]}</pre>{/if}
                        </div>
                      {/if}
                      {#if sessionSnapshotDiff[session.id]}<pre class="max-h-96 overflow-auto whitespace-pre border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base p-3 text-[11px]">{sessionSnapshotDiff[session.id]}</pre>{/if}
                      {#if session.worktree_id && session.status !== 'waiting_permission' && session.status !== 'waiting_question'}
                        <div class="flex gap-2"><input class={inputClass} bind:value={sessionPrompt[session.id]} placeholder="Ask the coding agent…"/><button class={buttonClass} disabled={busy === `run-${session.id}` || session.status === 'running'} onclick={() => void runSession(session)}>Run</button></div>
                      {/if}
                      {#if (session.status === 'waiting_permission' || pendingTools[session.id]?.kind === 'permission') && pendingTools[session.id]?.state !== 'executing'}
                        <div class="border border-amber-300 bg-amber-50 dark:border-amber-800 dark:bg-amber-900/20 p-2 space-y-2 text-xs">
                          <p>A tool call is waiting for approval: <strong>{pendingTools[session.id]?.tool_calls.map((call) => call.Name).join(', ') || 'command'}</strong></p>
                          {#each pendingTools[session.id]?.tool_calls || [] as call}
                            <div class="border border-amber-200 dark:border-amber-800 p-2">
                              <div class="font-semibold">{call.Name}</div>
                              <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all text-[11px]">{JSON.stringify(call.Arguments, null, 2)}</pre>
                            </div>
                          {/each}
                          <div class="flex gap-2"><button class={buttonClass} onclick={() => void decideTool(session, false)}>Reject</button><button class={buttonClass} onclick={() => void decideTool(session, true)}>Approve</button></div>
                        </div>
                      {/if}
                      {#if (session.status === 'waiting_question' || pendingTools[session.id]?.kind === 'question') && pendingTools[session.id]?.state !== 'executing'}
                        <div class="border border-blue-300 bg-blue-50 dark:border-blue-800 dark:bg-blue-900/20 p-2 space-y-2 text-xs">
                          <p>{pendingQuestion(session.id)}</p>
                          <div class="flex gap-2"><input class={inputClass} bind:value={questionAnswer[session.id]} placeholder="Your answer"/><button class={buttonClass} onclick={() => void answerQuestion(session)}>Answer</button></div>
                        </div>
                      {/if}
                      {#if pendingTools[session.id]?.state === 'executing'}
                        <div class="border border-gray-300 dark:border-dark-border bg-gray-50 dark:bg-dark-base p-2 text-xs">Tool resolution is in progress. If it was interrupted, cancel this session before starting another turn.</div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}
      </section>
    </div>
  </div>
</div>

{#if terminalWorktree}
  <div class="fixed inset-0 z-50 bg-black/70 p-3 md:p-8" role="presentation" onclick={(event) => { if (event.currentTarget === event.target) terminalWorktree = null; }}>
    <div class="mx-auto flex h-full max-w-6xl flex-col border border-gray-700 bg-[#0b0f14]" role="dialog" aria-modal="true" aria-label={`Terminal for ${terminalWorktree.name}`}>
      <header class="flex items-center justify-between border-b border-gray-700 px-3 py-2 text-xs text-gray-200"><span>{terminalWorktree.name} · {terminalWorktree.branch}</span><button class="px-3 py-1 hover:bg-gray-800" onclick={() => terminalWorktree = null}>Close</button></header>
      <div class="min-h-0 flex-1"><HostTerminal id={terminalWorktree.id} socketUrl={developerWorktreeTerminalURL(terminalWorktree.id)} persistentShell={false} onstatus={() => {}} /></div>
    </div>
  </div>
{/if}
