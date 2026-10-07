<script lang="ts">
  import { ArrowDown, ArrowUp, GitBranch, Minus, Plus, RefreshCw, RotateCcw, Upload, Download } from 'lucide-svelte';
  import {
    checkoutDeveloperGit, commitDeveloperGit, discardDeveloperGit, getDeveloperGitStatus, listDeveloperGitBranches,
    pullDeveloperGit, pushDeveloperGit, stageDeveloperGit, unstageDeveloperGit,
    type DeveloperGitBranch, type DeveloperGitStatus,
  } from '@/lib/api/developer-spaces';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    project: string;
    /** Bumped by the page when files change, so status refreshes. */
    revision: number;
    ondiff: (file: string, opts: { staged?: boolean; untracked?: boolean }) => void;
    onchanged: () => void;
  }
  let { project, revision, ondiff, onchanged }: Props = $props();

  let status = $state<DeveloperGitStatus | null>(null);
  let branches = $state<DeveloperGitBranch[]>([]);
  let loading = $state(false);
  let working = $state('');
  let message = $state('');
  let newBranch = $state('');
  let showBranches = $state(false);

  $effect(() => {
    void revision;
    if (project) void refresh();
  });

  async function refresh() {
    loading = true;
    try {
      status = await getDeveloperGitStatus(project);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not read Git status', 'alert');
    } finally {
      loading = false;
    }
  }

  async function run(label: string, action: () => Promise<unknown>, success = '') {
    working = label;
    try {
      await action();
      if (success) addToast(success);
      await refresh();
      onchanged();
    } catch (e: any) {
      addToast(e?.response?.data?.message || e?.message || `${label} failed`, 'alert');
    } finally {
      working = '';
    }
  }

  const stageAll = () => run('Stage', () => stageDeveloperGit(project, ['.']));
  const unstageAll = () => run('Unstage', () => unstageDeveloperGit(project, status!.staged.map(f => f.path)));

  async function commit() {
    if (!message.trim()) return;
    await run('Commit', async () => {
      const result = await commitDeveloperGit(project, message.trim());
      message = '';
      return result;
    }, 'Committed');
  }

  async function push() {
    const branch = status?.branch;
    if (!branch || branch === '(detached)') return;
    if (!confirm(`Push branch "${branch}" to origin?`)) return;
    await run('Push', () => pushDeveloperGit(project, branch), `Pushed ${branch}`);
  }

  async function discard(path: string) {
    if (!confirm(`Discard your changes to ${path}? This cannot be undone.`)) return;
    await run('Discard', () => discardDeveloperGit(project, [path]));
  }

  async function openBranches() {
    showBranches = !showBranches;
    if (!showBranches) return;
    try {
      branches = await listDeveloperGitBranches(project);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not list branches', 'alert');
    }
  }

  async function checkout(name: string, create = false) {
    const local = name.replace(/^(remotes\/)?origin\//, '');
    showBranches = false;
    await run('Switch branch', () => checkoutDeveloperGit(project, local, create), `On ${local}`);
    newBranch = '';
  }

  const stateLabel: Record<string, string> = { M: 'M', A: 'A', D: 'D', R: 'R', C: 'C', T: 'T', U: 'U' };
</script>

<div class="flex h-full min-h-0 flex-col text-xs">
  {#if !project}
    <p class="p-3 text-dark-text-muted">Open a project folder to see its changes.</p>
  {:else if status && !status.repository}
    <p class="p-3 text-dark-text-muted"><span class="font-mono">{project}</span> is not a Git repository. Run <code>git init</code> in the terminal, or clone a repository as a new project.</p>
  {:else}
    <div class="flex items-center gap-1 border-b border-dark-border px-2 py-1.5">
      <button type="button" onclick={openBranches} class="inline-flex min-w-0 items-center gap-1 px-1.5 py-0.5 hover:bg-dark-elevated" title="Switch branch">
        <GitBranch size={13} class="shrink-0" />
        <span class="truncate font-mono">{status?.branch ?? '…'}</span>
      </button>
      {#if status?.ahead}<span class="inline-flex items-center text-dark-text-muted" title="Commits to push"><ArrowUp size={11} />{status.ahead}</span>{/if}
      {#if status?.behind}<span class="inline-flex items-center text-dark-text-muted" title="Commits to pull"><ArrowDown size={11} />{status.behind}</span>{/if}
      <span class="flex-1"></span>
      <button type="button" onclick={() => run('Pull', () => pullDeveloperGit(project), 'Pulled')} disabled={!!working} class="p-1 text-dark-text-muted hover:text-dark-text disabled:opacity-40 hover:bg-dark-elevated" title="Pull (fast-forward only)" aria-label="Pull"><Download size={13} /></button>
      <button type="button" onclick={push} disabled={!!working || !status?.branch} class="p-1 text-dark-text-muted hover:text-dark-text disabled:opacity-40 hover:bg-dark-elevated" title="Push current branch" aria-label="Push"><Upload size={13} /></button>
      <button type="button" onclick={refresh} class="p-1 text-dark-text-muted hover:text-dark-text hover:bg-dark-elevated" title="Refresh" aria-label="Refresh Git status"><RefreshCw size={13} class={loading ? 'animate-spin motion-reduce:animate-none' : ''} /></button>
    </div>

    {#if showBranches}
      <div class="max-h-56 overflow-y-auto border-b border-dark-border bg-dark-base">
        <form class="flex gap-1 p-2" onsubmit={event => { event.preventDefault(); if (newBranch.trim()) void checkout(newBranch.trim(), true); }}>
          <input bind:value={newBranch} placeholder="New branch name" aria-label="New branch name" class="min-w-0 flex-1 border border-dark-border px-2 py-1 font-mono text-dark-text" />
          <button type="submit" disabled={!newBranch.trim()} class="px-2 py-1 text-dark-base disabled:opacity-40 bg-accent">Create</button>
        </form>
        {#each branches as branch (branch.name)}
          <button type="button" disabled={branch.current} onclick={() => checkout(branch.name)} class="block w-full truncate px-3 py-1 text-left font-mono disabled:font-semibold disabled:hover:bg-transparent hover:bg-dark-elevated">{branch.name}{branch.current ? ' (current)' : ''}</button>
        {/each}
      </div>
    {/if}

    <div class="border-b border-dark-border p-2">
      <textarea bind:value={message} rows={2} placeholder="Commit message" aria-label="Commit message" onkeydown={event => { if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') void commit(); }} class="block w-full resize-y border border-dark-border bg-dark-surface px-2 py-1.5 text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20"></textarea>
      <button type="button" onclick={commit} disabled={!message.trim() || !status?.staged.length || !!working} class="mt-1.5 w-full py-1.5 font-medium text-dark-base disabled:opacity-40 bg-accent hover:bg-accent-hover" title={status?.staged.length ? 'Commit staged changes (Ctrl+Enter)' : 'Stage changes first'}>
        {working === 'Commit' ? 'Committing…' : `Commit${status?.staged.length ? ` ${status.staged.length} file${status.staged.length === 1 ? '' : 's'}` : ''}`}
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto">
      {#if status?.conflicted.length}
        <h4 class="px-2 pt-2 pb-1 font-semibold text-[11px] text-red-600">Conflicts</h4>
        {#each status.conflicted as file}
          <button type="button" onclick={() => ondiff(file, {})} class="block w-full truncate px-3 py-1 text-left font-mono text-red-400 hover:bg-dark-elevated">{file}</button>
        {/each}
      {/if}

      <div class="flex items-center px-2 pt-2 pb-1">
        <h4 class="flex-1 font-semibold text-[11px] text-dark-text-muted">Staged ({status?.staged.length ?? 0})</h4>
        {#if status?.staged.length}<button type="button" onclick={unstageAll} class="p-0.5 text-dark-text-muted hover:text-dark-text" title="Unstage all" aria-label="Unstage all"><Minus size={13} /></button>{/if}
      </div>
      {#each status?.staged ?? [] as file (file.path)}
        <div class="group flex items-center gap-1 px-2 hover:bg-dark-elevated">
          <button type="button" onclick={() => ondiff(file.path, { staged: true })} class="min-w-0 flex-1 truncate py-1 text-left font-mono" title={file.orig_path ? `${file.orig_path} → ${file.path}` : file.path}>{file.path}</button>
          <button type="button" onclick={() => run('Unstage', () => unstageDeveloperGit(project, [file.path]))} class="p-0.5 text-dark-text-muted opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-dark-text [@media(pointer:coarse)]:opacity-100" title="Unstage" aria-label={`Unstage ${file.path}`}><Minus size={12} /></button>
          <span class="w-4 text-center font-mono text-green-400">{stateLabel[file.index] ?? file.index}</span>
        </div>
      {/each}

      <div class="flex items-center px-2 pt-3 pb-1">
        <h4 class="flex-1 font-semibold text-[11px] text-dark-text-muted">Changes ({(status?.unstaged.length ?? 0) + (status?.untracked.length ?? 0)})</h4>
        {#if status?.unstaged.length || status?.untracked.length}<button type="button" onclick={stageAll} class="p-0.5 text-dark-text-muted hover:text-dark-text" title="Stage all" aria-label="Stage all"><Plus size={13} /></button>{/if}
      </div>
      {#each status?.unstaged ?? [] as file (file.path)}
        <div class="group flex items-center gap-1 px-2 hover:bg-dark-elevated">
          <button type="button" onclick={() => ondiff(file.path, {})} class="min-w-0 flex-1 truncate py-1 text-left font-mono" title={file.path}>{file.path}</button>
          <button type="button" onclick={() => discard(file.path)} class="p-0.5 text-dark-text-muted opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-red-300 [@media(pointer:coarse)]:opacity-100" title="Discard changes" aria-label={`Discard changes to ${file.path}`}><RotateCcw size={12} /></button>
          <button type="button" onclick={() => run('Stage', () => stageDeveloperGit(project, [file.path]))} class="p-0.5 text-dark-text-muted opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-dark-text [@media(pointer:coarse)]:opacity-100" title="Stage" aria-label={`Stage ${file.path}`}><Plus size={12} /></button>
          <span class="w-4 text-center font-mono text-amber-400">{stateLabel[file.worktree] ?? file.worktree}</span>
        </div>
      {/each}
      {#each status?.untracked ?? [] as file (file)}
        <div class="group flex items-center gap-1 px-2 hover:bg-dark-elevated">
          <button type="button" onclick={() => ondiff(file, { untracked: true })} class="min-w-0 flex-1 truncate py-1 text-left font-mono" title={file}>{file}</button>
          <button type="button" onclick={() => run('Stage', () => stageDeveloperGit(project, [file]))} class="p-0.5 text-dark-text-muted opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-dark-text [@media(pointer:coarse)]:opacity-100" title="Stage" aria-label={`Stage ${file}`}><Plus size={12} /></button>
          <span class="w-4 text-center font-mono text-green-400">U</span>
        </div>
      {/each}
      {#if status && !status.staged.length && !status.unstaged.length && !status.untracked.length && !status.conflicted.length}
        <p class="px-3 py-2 text-dark-text-muted">No changes.</p>
      {/if}
    </div>
  {/if}
</div>
