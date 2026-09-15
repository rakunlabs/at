<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, RefreshCw, TerminalSquare, X, Pencil, ArrowLeft, ArrowRight, Power } from 'lucide-svelte';
  import HostTerminal from '@/lib/components/HostTerminal.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listTerminals, terminalUsers, createTerminal, updateTerminal, deleteTerminal, startTerminal, saveTerminalPreferences, type TerminalSession, type TerminalTarget, type LinuxUser, type TerminalPreferences } from '@/lib/api/terminals';

  // Operate surface: inherit AT's neutral theme, compact controls and full-height
  // work area. Saved tabs lead directly to a host shell; tmux is not exposed.
  storeNavbar.title = 'Terminal';
  let sessions = $state<TerminalSession[]>([]);
  let targets = $state<TerminalTarget[]>([]);
  let preferences = $state<TerminalPreferences>({ active_id: '', default_users: {} });
  let activeID = $state('');
  let active = $derived(sessions.find(s => s.id === activeID));
  let target = $derived(targets.find(t => t.id === active?.target_id));
  let loading = $state(true);
  let busy = $state(false);
  let error = $state('');
  let showNew = $state(false);
  let selectedTarget = $state('');
  let selectedUser = $state('');
  let users = $state<LinuxUser[]>([]);
  let usersLoading = $state(false);
  let userError = $state('');
  let title = $state('');
  let rememberUser = $state(true);
  let status = $state('connecting');
  let statusMessage = $state('');
  let generation = $state(0);
  let editTitle = $state(false);
  let renamed = $state('');
  let actionDialog: HTMLDialogElement;
  let action = $state<'terminate' | 'restart'>('terminate');
  let mounted = true;
  let userRequest = 0;
  let preferenceQueue: Promise<void> = Promise.resolve();
  const message = (e: any) => e?.response?.data?.message || e?.message || 'Terminal operation failed';

  function persistPreferences() {
    preferences.active_id = activeID;
    const snapshot = structuredClone($state.snapshot(preferences));
    preferenceQueue = preferenceQueue.catch(() => {}).then(() => saveTerminalPreferences(snapshot)).catch(e => addToast(message(e), 'alert'));
  }

  async function load() {
    loading = true; error = '';
    try {
      const data = await listTerminals();
      if (!mounted) return;
      sessions = data.sessions;
      targets = data.targets;
      preferences = { active_id: data.preferences.active_id || '', default_users: data.preferences.default_users || {} };
      activeID = sessions.some(s => s.id === activeID) ? activeID : sessions.some(s => s.id === preferences.active_id) ? preferences.active_id : sessions[0]?.id || '';
      showNew = !sessions.length;
      selectedTarget = targets.find(t => t.available)?.id || targets[0]?.id || '';
      if (showNew) await loadUsers();
    } catch (e) { error = message(e); }
    finally { if (mounted) loading = false; }
  }

  async function loadUsers() {
    const request = ++userRequest;
    users = []; selectedUser = ''; userError = ''; usersLoading = true;
    const id = selectedTarget;
    try {
      if (!id) return;
      const result = await terminalUsers(id);
      if (!mounted || request !== userRequest) return;
      users = result;
      const preferred = preferences.default_users[id];
      selectedUser = result.some(u => u.name === preferred) ? preferred : result.find(u => u.name === 'root')?.name || result[0]?.name || '';
    } catch (e) { if (request === userRequest) userError = message(e); }
    finally { if (request === userRequest) usersLoading = false; }
  }

  function select(id: string) {
    if (activeID === id) return;
    activeID = id; editTitle = false;
    status = 'connecting'; statusMessage = '';
    persistPreferences();
  }

  async function create() {
    if (busy || !selectedTarget || !selectedUser) return;
    busy = true;
    try {
      const item = await createTerminal(selectedTarget, selectedUser, title);
      sessions = [...sessions, item]; activeID = item.id;
      if (rememberUser) preferences.default_users[selectedTarget] = selectedUser;
      persistPreferences();
      showNew = false; title = ''; status = 'connecting'; statusMessage = '';
    } catch (e) { addToast(message(e), 'alert'); await load(); }
    finally { busy = false; }
  }

  async function rename() {
    if (!active || busy || !renamed.trim()) return;
    busy = true;
    try {
      const item = { ...active, title: renamed.trim() };
      await updateTerminal(item);
      sessions = sessions.map(s => s.id === item.id ? item : s);
      editTitle = false;
    } catch (e) { addToast(message(e), 'alert'); }
    finally { busy = false; }
  }

  async function move(direction: number) {
    const index = sessions.findIndex(s => s.id === activeID);
    const other = index + direction;
    if (busy || index < 0 || other < 0 || other >= sessions.length) return;
    busy = true;
    try {
      const next = [...sessions];
      [next[index], next[other]] = [next[other], next[index]];
      // Persist the visible order; a later retry can safely repeat these writes.
      for (let position = 0; position < next.length; position++) {
        next[position] = { ...next[position], position };
        await updateTerminal(next[position]);
      }
      sessions = next;
    } catch (e) { addToast(message(e), 'alert'); await load(); }
    finally { busy = false; }
  }

  function askAction(value: 'terminate' | 'restart') { action = value; actionDialog.showModal(); }
  async function performAction() {
    if (!active || busy) return;
    busy = true;
    try {
      if (action === 'terminate') {
        await deleteTerminal(active.id);
        sessions = sessions.filter(s => s.id !== activeID);
        activeID = sessions[0]?.id || '';
        persistPreferences();
        if (!sessions.length) { showNew = true; await loadUsers(); }
      } else {
        await startTerminal(active.id);
        generation++; status = 'connecting'; statusMessage = '';
      }
      actionDialog.close();
    } catch (e) { addToast(message(e), 'alert'); }
    finally { busy = false; }
  }

  function tabKeys(event: KeyboardEvent, index: number) {
    let next = index;
    if (event.key === 'ArrowRight') next = (index + 1) % sessions.length;
    else if (event.key === 'ArrowLeft') next = (index - 1 + sessions.length) % sessions.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = sessions.length - 1;
    else return;
    event.preventDefault();
    select(sessions[next].id);
    document.getElementById(`terminal-tab-${sessions[next].id}`)?.focus();
  }

  onMount(() => { void load(); return () => { mounted = false; userRequest++; }; });
</script>

<svelte:head><title>AT | Terminal</title></svelte:head>

<div class="flex h-full min-h-0 flex-col bg-white text-gray-900 dark:bg-dark-surface dark:text-dark-text">
  <header class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-3 py-2 dark:border-dark-border">
    <div class="flex items-center gap-2"><TerminalSquare size={18} /><h1 class="text-sm font-semibold">Terminal</h1><span class="text-xs text-gray-500 dark:text-dark-text-muted">Host access</span></div>
    <div class="flex items-center gap-2">
      <button class="terminal-button" disabled={loading || busy} onclick={() => void load()} aria-label="Refresh hosts and saved terminals"><RefreshCw size={15} /></button>
      <button class="terminal-button" disabled={loading || busy} onclick={() => { showNew = !showNew; if (showNew) void loadUsers(); }}><Plus size={15} />New terminal</button>
    </div>
  </header>

  {#if error}<div role="alert" class="m-3 border border-red-300 p-3 text-sm text-red-700 dark:border-red-800 dark:text-red-300">{error}<button class="terminal-button ml-3" onclick={() => void load()}>Retry</button></div>{/if}
  {#if loading && !sessions.length}<p role="status" class="p-6 text-sm text-gray-500 dark:text-dark-text-secondary">Loading saved terminals…</p>{/if}

  {#if showNew && !loading}
    <form onsubmit={(e) => { e.preventDefault(); void create(); }} class="border-b border-gray-200 bg-gray-50 p-3 dark:border-dark-border dark:bg-dark-elevated">
      <div class="flex flex-wrap items-end gap-3">
        <label class="flex min-w-40 flex-1 flex-col gap-1 text-xs">Host
          <select aria-label="Host" class="terminal-input" bind:value={selectedTarget} onchange={() => void loadUsers()} disabled={busy}>
            {#each targets as host}<option value={host.id} disabled={!host.available}>{host.name || 'Local host'}{host.available ? '' : ' — unavailable'}</option>{/each}
          </select>
        </label>
        <label class="flex min-w-36 flex-1 flex-col gap-1 text-xs">Linux user
          <select aria-label="Linux user" class="terminal-input" bind:value={selectedUser} disabled={busy || usersLoading || !users.length}>
            {#if usersLoading}<option value="">Loading users…</option>{:else if !users.length}<option value="">No users available</option>{/if}
            {#each users as user}<option value={user.name}>{user.name} (UID {user.uid})</option>{/each}
          </select>
        </label>
        <label class="flex min-w-40 flex-1 flex-col gap-1 text-xs">Name <span class="sr-only">(optional)</span><input class="terminal-input" bind:value={title} maxlength="80" placeholder="Optional terminal name" disabled={busy} /></label>
        <button class="terminal-button" type="submit" disabled={busy || usersLoading || !selectedUser || !targets.find(t => t.id === selectedTarget)?.available}>{busy ? 'Opening…' : 'Open terminal'}</button>
        {#if sessions.length}<button class="terminal-button" type="button" onclick={() => showNew = false} aria-label="Close new terminal form"><X size={15} /></button>{/if}
      </div>
      <label class="mt-3 flex items-center gap-2 text-xs"><input type="checkbox" bind:checked={rememberUser} />Remember this Linux user for this host</label>
      {#if userError}<p role="alert" class="mt-2 text-xs text-red-700 dark:text-red-300">{userError}<button type="button" class="ml-2 underline" onclick={() => void loadUsers()}>Retry</button></p>{/if}
      {#each targets.filter(t => !t.available) as host}<p class="mt-2 text-xs text-gray-600 dark:text-dark-text-secondary">{host.name || 'Local host'}: {host.reason}</p>{/each}
    </form>
  {/if}

  {#if sessions.length}
    <div role="tablist" aria-label="Saved terminals" class="flex shrink-0 overflow-x-auto border-b border-gray-200 dark:border-dark-border">
      {#each sessions as session, index (session.id)}
        <button role="tab" id={`terminal-tab-${session.id}`} aria-controls="terminal-panel" aria-selected={activeID === session.id} tabindex={activeID === session.id ? 0 : -1} onclick={() => select(session.id)} onkeydown={(e) => tabKeys(e, index)} title={`${session.username}@${session.target_name}`} class={['max-w-64 shrink-0 border-b-2 px-4 py-3 text-sm focus-visible:outline-2 focus-visible:outline-accent', activeID === session.id ? 'border-accent bg-gray-50 font-medium dark:bg-dark-elevated' : 'border-transparent text-gray-600 hover:bg-gray-50 dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}><span class="block truncate">{session.title}</span></button>
      {/each}
    </div>
  {/if}

  {#if active}
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-3 py-2 dark:border-dark-border">
      <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs"><span class="font-mono">{active.username}@{active.target_name}</span><span role="status" class="text-gray-600 dark:text-dark-text-secondary">{!target ? 'Host offline' : status === 'connected' ? 'Connected' : status === 'connecting' ? 'Connecting…' : 'Disconnected'}</span></div>
      <div class="flex flex-wrap items-center gap-1">
        <button class="terminal-button" title="Rename terminal" aria-label="Rename terminal" disabled={busy} onclick={() => { renamed = active!.title; editTitle = !editTitle; }}><Pencil size={14} /></button>
        <button class="terminal-button" title="Move tab left" aria-label="Move tab left" disabled={busy || sessions[0]?.id === activeID} onclick={() => void move(-1)}><ArrowLeft size={14} /></button>
        <button class="terminal-button" title="Move tab right" aria-label="Move tab right" disabled={busy || sessions[sessions.length - 1]?.id === activeID} onclick={() => void move(1)}><ArrowRight size={14} /></button>
        <button class="terminal-button" disabled={!target || busy} onclick={() => { generation++; status = 'connecting'; statusMessage = ''; }}><RefreshCw size={14} />Reconnect</button>
        <button class="terminal-button" disabled={!target || busy} onclick={() => askAction('terminate')}><Power size={14} />End terminal</button>
      </div>
    </div>
    {#if editTitle}<form class="flex gap-2 border-b border-gray-200 p-3 dark:border-dark-border" onsubmit={e => { e.preventDefault(); void rename(); }}><label class="sr-only" for="terminal-name">Terminal name</label><input id="terminal-name" class="terminal-input min-w-0 flex-1" bind:value={renamed} maxlength="80" /><button class="terminal-button" disabled={busy || !renamed.trim()}>Save name</button><button class="terminal-button" type="button" onclick={() => editTitle = false}>Cancel</button></form>{/if}
    {#if !target || status === 'error' || status === 'disconnected'}
      <div role="status" class="flex flex-wrap items-center justify-between gap-2 bg-gray-50 px-3 py-2 text-xs dark:bg-dark-elevated">
        <p>{!target ? 'This host is offline. Refresh hosts when it is available again.' : statusMessage}</p>
        {#if target}<button class="terminal-button" disabled={busy} onclick={() => askAction('restart')}>Start shell if ended</button>{/if}
      </div>
    {/if}
    <div id="terminal-panel" role="tabpanel" tabindex="0" aria-labelledby={`terminal-tab-${active.id}`} class="relative min-h-40 flex-1 overflow-hidden">
      {#if target}
        {#key `${active.id}:${generation}`}<HostTerminal id={active.id} onstatus={(value, text) => { status = value; statusMessage = text; }} />{/key}
      {:else}<div class="p-6 text-sm text-gray-500 dark:text-dark-text-secondary">Saved terminal: {active.title}. It will reconnect to {active.target_name}, not another host.</div>{/if}
    </div>
  {:else if !loading && !error}
    <div class="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center"><TerminalSquare size={30} class="text-gray-400" /><h2 class="text-base font-medium">Your host terminals, saved here</h2><p class="max-w-md text-sm text-gray-600 dark:text-dark-text-secondary">Choose a host and Linux user to open your first terminal. You can leave this page and return to the same running shell.</p></div>
  {/if}
  <footer class="shrink-0 border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-border dark:text-dark-text-muted">Closing this page keeps shells running. Connecting takes control from another browser. Host restarts end running processes.</footer>
</div>

<dialog bind:this={actionDialog} class="m-auto w-[min(28rem,calc(100%-2rem))] rounded-lg border border-gray-200 bg-white p-5 text-gray-900 backdrop:bg-black/40 dark:border-dark-border dark:bg-dark-surface dark:text-dark-text" oncancel={() => {}}>
  <h2 class="text-base font-semibold">{action === 'terminate' ? 'End this terminal?' : 'Start a shell in this saved terminal?'}</h2>
  <p class="mt-2 text-sm text-gray-600 dark:text-dark-text-secondary">{action === 'terminate' ? `This stops the shell and its processes in “${active?.title || ''}” and removes the saved tab.` : 'If the previous shell has ended, a new login shell will start under the same Linux user. A running shell is kept. Previous commands are not replayed.'}</p>
  <div class="mt-5 flex justify-end gap-2"><button class="terminal-button" disabled={busy} onclick={() => actionDialog.close()}>Cancel</button><button class="terminal-button" disabled={busy} onclick={() => void performAction()}>{busy ? 'Working…' : action === 'terminate' ? 'End terminal' : 'Start shell'}</button></div>
</dialog>

<style>
  @reference "tailwindcss";
  .terminal-button { @apply inline-flex min-h-9 items-center justify-center gap-1.5 rounded-md border border-gray-300 px-2.5 py-1.5 text-xs hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-50; }
  .terminal-input { @apply h-9 rounded-md border border-gray-300 bg-white px-2 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-50; }
  :global(.dark) .terminal-button { border-color: var(--color-dark-border); }
  :global(.dark) .terminal-button:hover { background: var(--color-dark-elevated); }
  :global(.dark) .terminal-input { border-color: var(--color-dark-border); background: var(--color-dark-surface); }
</style>
