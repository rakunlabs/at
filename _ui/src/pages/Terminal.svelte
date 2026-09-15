<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, RefreshCw, TerminalSquare, X, Pencil, ArrowLeft, ArrowRight, Power, Moon, Sun, Monitor, Type, Keyboard, Maximize2, Minimize2 } from 'lucide-svelte';
  import HostTerminal from '@/lib/components/HostTerminal.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listTerminals, terminalUsers, createTerminal, updateTerminal, deleteTerminal, startTerminal, saveTerminalPreferences, type TerminalSession, type TerminalTarget, type LinuxUser, type TerminalPreferences, type TerminalAppearance, type TerminalKeyBar } from '@/lib/api/terminals';

  // Operate surface: inherit AT's neutral theme, compact controls and full-height
  // work area. Saved tabs lead directly to a host shell; tmux is not exposed.
  storeNavbar.title = 'Terminal';
  let sessions = $state<TerminalSession[]>([]);
  let targets = $state<TerminalTarget[]>([]);
  let preferences = $state<TerminalPreferences>({ active_id: '', default_users: {}, appearance: 'dark', font_family: '', font_size: 14 });
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
  let showDisplay = $state(false);
  let customFont = $state(false);
  let maximized = $state(false);
  let controls = $state(false);
  let controlsTimer: ReturnType<typeof setTimeout>;
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

  // Pointer capability decides two things: whether the key row appears under
  // "auto", and whether the full screen exit control may fade out.
  const touchOnly = typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;

  // Display settings are stored per owner and apply to every saved terminal.
  // The terminal keeps its own palette, so a light page theme does not force a
  // light shell; "system" opts back into following the page.
  const appearances: TerminalAppearance[] = ['dark', 'light', 'system'];
  let appearance = $derived<TerminalAppearance>(preferences.appearance || 'dark');
  let fontFamily = $derived(preferences.font_family || '');
  let fontSize = $derived(preferences.font_size || 14);
  // One account may be used from both a phone and a desktop, so the stored value
  // is a policy and "auto" resolves per device instead of forcing a key row onto
  // a machine that has a real keyboard.
  let keyBarMode = $derived<TerminalKeyBar>(preferences.key_bar || 'auto');
  let keyBar = $derived(keyBarMode === 'auto' ? touchOnly : keyBarMode === 'on');
  // BUNDLED ships with the app, so phones and locked-down machines get Nerd Font
  // glyphs without installing anything. Every other name must already be present
  // on the device running this browser, not on the host; an absent family
  // silently falls back to the monospace stack.
  const BUNDLED = 'JetBrainsMono Nerd Font Mono';
  const fontChoices = [BUNDLED, 'JetBrainsMono Nerd Font', 'FiraCode Nerd Font', 'Hack Nerd Font', 'MesloLGS NF', 'CaskaydiaCove Nerd Font', 'SauceCodePro Nerd Font', 'UbuntuMono Nerd Font', 'JetBrains Mono', 'Fira Code', 'Cascadia Mono', 'Menlo', 'Consolas'];
  let fontReady = $derived(fontFamily === BUNDLED || fontInstalled(fontFamily));

  function fontInstalled(name: string): boolean {
    const wanted = name.trim().replace(/["']/g, '');
    if (!wanted) return true;
    try {
      const context = document.createElement('canvas').getContext('2d');
      if (!context) return true;
      const sample = 'MWmwi1lO0@#';
      // A missing family renders through the fallback, so a width that differs
      // from at least one fallback baseline proves the font resolved here.
      return ['monospace', 'serif'].some(fallback => {
        context.font = `48px "at-missing-font", ${fallback}`;
        const baseline = context.measureText(sample).width;
        context.font = `48px "${wanted}", ${fallback}`;
        return Math.abs(context.measureText(sample).width - baseline) > 0.5;
      });
    } catch { return true; }
  }

  // Full screen shows the terminal alone. The exit control floats over it and
  // fades out so it never covers shell output while you work.
  //
  // Touch devices keep it on screen permanently: there is no hover to bring it
  // back, and the keyboard chord below needs keys a phone keyboard does not
  // have, so fading it out would strand the reader inside full screen.
  function revealControls() {
    if (!maximized) return;
    controls = true;
    clearTimeout(controlsTimer);
    if (touchOnly) return;
    controlsTimer = setTimeout(() => { controls = false; }, 2500);
  }

  function setMaximized(value: boolean) {
    maximized = value;
    clearTimeout(controlsTimer);
    controls = false;
    if (!value) return;
    // Settings rows belong to the framed view; reopen them after leaving.
    showDisplay = false;
    editTitle = false;
    revealControls();
  }

  // Escape stays with the shell, where editors and pagers rely on it, so the
  // keyboard exit is a chord the pty is very unlikely to want. Capture phase
  // takes it before xterm reads the key.
  function fullScreenKey(event: KeyboardEvent) {
    if (!(event.ctrlKey || event.metaKey) || !event.shiftKey || event.altKey) return;
    if (event.code !== 'KeyF' && event.key.toLowerCase() !== 'f') return;
    if (!maximized && !active) return;
    event.preventDefault();
    event.stopPropagation();
    setMaximized(!maximized);
  }

  function cycleAppearance() {
    preferences.appearance = appearances[(appearances.indexOf(appearance) + 1) % appearances.length];
    persistPreferences();
  }

  // The quick toggle writes an explicit choice for the current device state;
  // "auto" stays reachable from the display settings.
  function toggleKeyBar() {
    preferences.key_bar = keyBar ? 'off' : 'on';
    persistPreferences();
  }

  function pickFont(value: string) {
    if (value === 'custom') { customFont = true; return; }
    customFont = false;
    preferences.font_family = value;
    persistPreferences();
  }

  function setFontSize(value: number) {
    preferences.font_size = Math.min(28, Math.max(10, Math.round(value) || 14));
    persistPreferences();
  }

  async function load() {
    loading = true; error = '';
    try {
      const data = await listTerminals();
      if (!mounted) return;
      sessions = data.sessions;
      targets = data.targets;
      preferences = {
        active_id: data.preferences.active_id || '',
        default_users: data.preferences.default_users || {},
        appearance: data.preferences.appearance || 'dark',
        font_family: data.preferences.font_family || '',
        font_size: data.preferences.font_size || 14,
        key_bar: data.preferences.key_bar || 'auto',
      };
      customFont = !!preferences.font_family && !fontChoices.includes(preferences.font_family);
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
        if (!sessions.length) { showNew = true; maximized = false; await loadUsers(); }
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

  onMount(() => {
    void load();
    window.addEventListener('keydown', fullScreenKey, true);
    return () => {
      mounted = false;
      userRequest++;
      clearTimeout(controlsTimer);
      window.removeEventListener('keydown', fullScreenKey, true);
    };
  });
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

  <!-- Full screen lifts the terminal over the app shell and drops the tabs and
  toolbar, leaving the shell alone on screen. The wrapper is display:contents
  otherwise, so the normal page layout is unchanged. -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class={maximized ? 'terminal-stage fixed inset-0 z-50 flex flex-col bg-white dark:bg-dark-surface' : 'contents'} onpointermove={revealControls} onpointerdown={revealControls}>
  {#if sessions.length && !maximized}
    <div role="tablist" aria-label="Saved terminals" class="flex shrink-0 overflow-x-auto border-b border-gray-200 dark:border-dark-border">
      {#each sessions as session, index (session.id)}
        <button role="tab" id={`terminal-tab-${session.id}`} aria-controls="terminal-panel" aria-selected={activeID === session.id} tabindex={activeID === session.id ? 0 : -1} onclick={() => select(session.id)} onkeydown={(e) => tabKeys(e, index)} title={`${session.username}@${session.target_name}`} class={['max-w-64 shrink-0 border-b-2 px-4 py-3 text-sm focus-visible:outline-2 focus-visible:outline-accent', activeID === session.id ? 'border-accent bg-gray-50 font-medium dark:bg-dark-elevated' : 'border-transparent text-gray-600 hover:bg-gray-50 dark:text-dark-text-secondary dark:hover:bg-dark-elevated']}><span class="block truncate">{session.title}</span></button>
      {/each}
    </div>
  {/if}

  {#if active}
    {#if !maximized}
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-3 py-2 dark:border-dark-border">
      <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs"><span class="font-mono">{active.username}@{active.target_name}</span><span role="status" class="text-gray-600 dark:text-dark-text-secondary">{!target ? 'Host offline' : status === 'connected' ? 'Connected' : status === 'connecting' ? 'Connecting…' : 'Disconnected'}</span></div>
      <div class="flex flex-wrap items-center gap-1">
        <button class="terminal-button" title="Rename terminal" aria-label="Rename terminal" disabled={busy} onclick={() => { renamed = active!.title; editTitle = !editTitle; }}><Pencil size={14} /></button>
        <button class="terminal-button" title="Move tab left" aria-label="Move tab left" disabled={busy || sessions[0]?.id === activeID} onclick={() => void move(-1)}><ArrowLeft size={14} /></button>
        <button class="terminal-button" title="Move tab right" aria-label="Move tab right" disabled={busy || sessions[sessions.length - 1]?.id === activeID} onclick={() => void move(1)}><ArrowRight size={14} /></button>
        <button class="terminal-button" title={`Terminal colours: ${appearance === 'system' ? 'match the page theme' : appearance}. Click to change.`} aria-label={`Terminal colours: ${appearance === 'system' ? 'match the page theme' : appearance}. Change`} onclick={cycleAppearance}>
          {#if appearance === 'dark'}<Moon size={14} />{:else if appearance === 'light'}<Sun size={14} />{:else}<Monitor size={14} />{/if}
        </button>
        <button class="terminal-button" title={keyBar ? 'Hide the touch key row' : 'Show a key row for Ctrl, Esc, Tab and arrows'} aria-label={keyBar ? 'Hide the touch key row' : 'Show the touch key row'} aria-pressed={keyBar} onclick={toggleKeyBar}><Keyboard size={14} /></button>
        <button class="terminal-button" title="Font and size" aria-label="Font and size" aria-expanded={showDisplay} onclick={() => showDisplay = !showDisplay}><Type size={14} /></button>
        <button class="terminal-button" title={touchOnly ? 'Full screen — terminal only' : 'Full screen — terminal only (Ctrl/Cmd + Shift + F)'} aria-label="Full screen, terminal only" onclick={() => setMaximized(true)}><Maximize2 size={14} /></button>
        <button class="terminal-button" disabled={!target || busy} onclick={() => { generation++; status = 'connecting'; statusMessage = ''; }}><RefreshCw size={14} />Reconnect</button>
        <button class="terminal-button" disabled={!target || busy} onclick={() => askAction('terminate')}><Power size={14} />End terminal</button>
      </div>
    </div>
    {#if editTitle}<form class="flex gap-2 border-b border-gray-200 p-3 dark:border-dark-border" onsubmit={e => { e.preventDefault(); void rename(); }}><label class="sr-only" for="terminal-name">Terminal name</label><input id="terminal-name" class="terminal-input min-w-0 flex-1" bind:value={renamed} maxlength="80" /><button class="terminal-button" disabled={busy || !renamed.trim()}>Save name</button><button class="terminal-button" type="button" onclick={() => editTitle = false}>Cancel</button></form>{/if}
    {#if showDisplay}
      <div class="flex flex-wrap items-end gap-3 border-b border-gray-200 p-3 text-xs dark:border-dark-border">
        <label class="flex flex-col gap-1">Colours
          <select class="terminal-input" bind:value={preferences.appearance} onchange={persistPreferences}>
            <option value="dark">Dark</option>
            <option value="light">Light</option>
            <option value="system">Match page theme</option>
          </select>
        </label>
        <label class="flex min-w-52 flex-col gap-1">Font
          <select class="terminal-input" value={customFont ? 'custom' : fontFamily} onchange={(e) => pickFont(e.currentTarget.value)}>
            <option value="">System monospace</option>
            {#each fontChoices as choice}<option value={choice}>{choice === BUNDLED ? `${choice} — included` : choice}</option>{/each}
            <option value="custom">Other font…</option>
          </select>
        </label>
        {#if customFont}
          <label class="flex min-w-52 flex-col gap-1">Font name
            <input class="terminal-input" bind:value={preferences.font_family} maxlength="120" placeholder="e.g. Iosevka Nerd Font" onchange={persistPreferences} />
          </label>
        {/if}
        <label class="flex flex-col gap-1">Size
          <input class="terminal-input w-20" type="number" min="10" max="28" value={fontSize} onchange={(e) => setFontSize(e.currentTarget.valueAsNumber)} />
        </label>
        <label class="flex flex-col gap-1">Key row
          <select class="terminal-input" bind:value={preferences.key_bar} onchange={persistPreferences}>
            <option value="auto">Touch devices only</option>
            <option value="on">Always</option>
            <option value="off">Never</option>
          </select>
        </label>
        <button class="terminal-button" onclick={() => showDisplay = false}>Done</button>
        <p class="basis-full text-gray-600 dark:text-dark-text-secondary">
          {#if fontFamily === BUNDLED}Included with AT, so nothing has to be installed — the one choice that works on a phone. About 1 MB per weight, downloaded once and then cached.{:else if !fontReady}Not installed on this device, so the system monospace font is used. Fonts are installed on this device, not on the host; on a phone, pick the included font instead.{:else}Any other font has to be installed on this device. Glyphs still depend on what the program prints.{/if}
        </p>
      </div>
    {/if}
    {/if}
    <!-- A dead terminal explains itself even in full screen; hiding this would
    leave a frozen screen with no reason and no way back. -->
    {#if !target || status === 'error' || status === 'disconnected'}
      <div role="status" class="flex flex-wrap items-center justify-between gap-2 bg-gray-50 px-3 py-2 text-xs dark:bg-dark-elevated">
        <p>{!target ? 'This host is offline. Refresh hosts when it is available again.' : statusMessage}</p>
        <div class="flex items-center gap-1">
          {#if maximized && target}<button class="terminal-button" disabled={busy} onclick={() => { generation++; status = 'connecting'; statusMessage = ''; }}><RefreshCw size={14} />Reconnect</button>{/if}
          {#if target}<button class="terminal-button" disabled={busy} onclick={() => askAction('restart')}>Start shell if ended</button>{/if}
        </div>
      </div>
    {/if}
    <div id="terminal-panel" role="tabpanel" tabindex="0" aria-labelledby={`terminal-tab-${active.id}`} class="relative min-h-40 flex-1 overflow-hidden">
      {#if target}
        {#key `${active.id}:${generation}`}<HostTerminal id={active.id} {appearance} {fontFamily} {fontSize} {keyBar} onstatus={(value, text) => { status = value; statusMessage = text; }} />{/key}
      {:else}<div class="p-6 text-sm text-gray-500 dark:text-dark-text-secondary">Saved terminal: {active.title}. It will reconnect to {active.target_name}, not another host.</div>{/if}
      {#if maximized}
        <button
          class={['absolute top-3 right-4 z-10 inline-flex items-center gap-1.5 rounded-md border border-gray-400/60 bg-white/90 px-2.5 py-1.5 text-xs text-gray-900 shadow-sm backdrop-blur transition-opacity hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-offset-2 dark:border-dark-border dark:bg-dark-elevated/90 dark:text-dark-text', controls ? 'opacity-100' : 'pointer-events-none opacity-0']}
          onfocus={() => { clearTimeout(controlsTimer); controls = true; }}
          onblur={revealControls}
          onclick={() => setMaximized(false)}
        ><Minimize2 size={14} />Exit full screen {#if !touchOnly}<span class="text-gray-500 dark:text-dark-text-muted">Ctrl/Cmd + Shift + F</span>{/if}</button>
      {/if}
    </div>
  {:else if !loading && !error}
    <div class="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center"><TerminalSquare size={30} class="text-gray-400" /><h2 class="text-base font-medium">Your host terminals, saved here</h2><p class="max-w-md text-sm text-gray-600 dark:text-dark-text-secondary">Choose a host and Linux user to open your first terminal. You can leave this page and return to the same running shell.</p></div>
  {/if}
  </div>
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
  /* The overlay is fixed to the viewport, so the body's safe-area padding does
     not apply and a notch or home indicator would sit over the shell. */
  .terminal-stage { padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left); }
  :global(.dark) .terminal-button { border-color: var(--color-dark-border); }
  :global(.dark) .terminal-button:hover { background: var(--color-dark-elevated); }
  :global(.dark) .terminal-input { border-color: var(--color-dark-border); background: var(--color-dark-surface); }
</style>
