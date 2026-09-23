<script lang="ts">
  import { Check, Copy, KeyRound, Plus, RefreshCw, RotateCw, ShieldCheck, Trash2, X } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { createGitCredential, deleteGitCredential, listGitCredentials, rotateGitCredential, scanGitHost, testGitCredential, type GitCredential, type GitHostScan } from '@/lib/api/git-credentials';

  let items = $state<GitCredential[]>([]);
  let loading = $state(true);
  let showCreate = $state(false);
  let name = $state('');
  let host = $state('');
  let port = $state(22);
  let scan = $state<GitHostScan | null>(null);
  let confirmed = $state(false);
  let busy = $state(false);
  let rotateConfirm = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let testID = $state<string | null>(null);
  let testRepositoryURL = $state('');

  storeNavbar.title = 'Git credentials';

  function message(e: any, fallback: string) { return e?.response?.data?.message || fallback; }

  async function load() {
    loading = true;
    try { items = await listGitCredentials(); }
    catch (e: any) { addToast(message(e, 'Failed to load Git credentials'), 'alert'); }
    finally { loading = false; }
  }

  async function scanHost() {
    if (!host.trim()) { addToast('Git host is required', 'warn'); return; }
    busy = true; scan = null; confirmed = false;
    try { scan = await scanGitHost(host.trim(), port || 22); }
    catch (e: any) { addToast(message(e, 'Failed to scan Git host'), 'alert'); }
    finally { busy = false; }
  }

  async function create() {
    if (!scan || !confirmed || !name.trim()) { addToast('Name and confirmed host fingerprints are required', 'warn'); return; }
    busy = true;
    try {
      const created = await createGitCredential({ name: name.trim(), host: scan.host, port: scan.port, fingerprints: scan.fingerprints });
      items = [...items, created];
      name = ''; host = ''; port = 22; scan = null; confirmed = false; showCreate = false;
      addToast('Deploy key generated. Add its public key to the repository.');
    } catch (e: any) { addToast(message(e, 'Failed to generate deploy key'), 'alert'); }
    finally { busy = false; }
  }

  async function rotate(item: GitCredential) {
    if (rotateConfirm !== item.id) { rotateConfirm = item.id; deleteConfirm = null; return; }
    busy = true;
    try {
      const updated = await rotateGitCredential(item.id);
      items = items.map(v => v.id === item.id ? updated : v);
      rotateConfirm = null;
      addToast('New deploy key generated. Replace the old public key in the repository.');
    } catch (e: any) { addToast(message(e, 'Failed to rotate deploy key'), 'alert'); }
    finally { busy = false; }
  }

  async function remove(item: GitCredential) {
    if (deleteConfirm !== item.id) { deleteConfirm = item.id; rotateConfirm = null; return; }
    busy = true;
    try { await deleteGitCredential(item.id); items = items.filter(v => v.id !== item.id); deleteConfirm = null; addToast('Git credential deleted'); }
    catch (e: any) { addToast(message(e, 'Failed to delete Git credential'), 'alert'); }
    finally { busy = false; }
  }

  async function testAccess(item: GitCredential) {
    if (testID !== item.id) {
      testID = item.id;
      testRepositoryURL = '';
      rotateConfirm = null;
      deleteConfirm = null;
      return;
    }
    if (!testRepositoryURL.trim()) { addToast('Repository clone URL is required', 'warn'); return; }
    busy = true;
    try { await testGitCredential(item.id, testRepositoryURL.trim()); addToast('Repository access succeeded'); testID = null; testRepositoryURL = ''; }
    catch (e: any) { addToast(message(e, 'Repository access failed'), 'alert'); }
    finally { busy = false; }
  }

  async function copy(value: string) {
    try { await navigator.clipboard.writeText(value); addToast('Public key copied'); }
    catch { addToast('Could not copy public key', 'alert'); }
  }

  load();
</script>

<svelte:head><title>AT | Git credentials</title></svelte:head>

<div class="settings-page">
  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
    <div><h1 class="settings-title">Git credentials</h1><p class="settings-subtitle">Generate workspace deploy keys for private skill repositories.</p></div>
    <div class="flex gap-2">
      <button class="settings-button" onclick={load} disabled={loading}><RefreshCw size={13} /> Refresh</button>
      <button class="settings-primary" onclick={() => { showCreate = !showCreate; scan = null; confirmed = false; }}><Plus size={13} /> Generate deploy key</button>
    </div>
  </div>

  <div class="flex items-start gap-2 border border-emerald-200 bg-emerald-50 p-3 text-xs text-emerald-950 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-100"><ShieldCheck size={15} class="shrink-0" /><span>Private keys are encrypted at rest and never returned by the API. AT accepts host keys only after you review their fingerprints.</span></div>

  {#if showCreate}
    <section class="settings-section">
      <div class="flex items-start justify-between gap-3"><div><h2 class="settings-section-title">New deploy key</h2><p class="settings-note">Scan the Git host first, verify its fingerprints, then generate an Ed25519 key.</p></div><button aria-label="Close" onclick={() => showCreate = false}><X size={14} /></button></div>
      <div class="settings-form space-y-3">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_7rem]">
          <label>Name<input bind:value={name} placeholder="Production skills" /></label>
          <label>Git host<input bind:value={host} placeholder="gitlab.com" oninput={() => { scan = null; confirmed = false; }} /></label>
          <label>SSH port<input type="number" min="1" max="65535" bind:value={port} oninput={() => { scan = null; confirmed = false; }} /></label>
        </div>
        <div><button class="settings-button" onclick={scanHost} disabled={busy || !host.trim()}><ShieldCheck size={13} /> {busy ? 'Scanning…' : 'Scan host keys'}</button></div>
        {#if scan}
          <div class="border border-amber-300 bg-amber-50 p-3 text-xs text-amber-950 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100">
            <p class="font-semibold">Verify these fingerprints through a trusted channel</p>
            <div class="mt-2 space-y-1 font-mono break-all">{#each scan.fingerprints as fingerprint}<div>{fingerprint}</div>{/each}</div>
            <label class="mt-3 flex items-start gap-2 font-sans"><input class="mt-0.5" type="checkbox" bind:checked={confirmed} /><span>I verified these fingerprints with the Git provider or server administrator.</span></label>
          </div>
        {/if}
        <div class="flex justify-end"><button class="settings-primary" onclick={create} disabled={busy || !scan || !confirmed || !name.trim()}><KeyRound size={13} /> Generate key</button></div>
      </div>
    </section>
  {/if}

  <section class="settings-section !p-0 !space-y-0 overflow-hidden">
    <div class="px-4 py-3 bg-gray-50 dark:bg-dark-base border-b border-gray-200 dark:border-dark-border"><h2 class="settings-section-title">Deploy keys</h2><p class="settings-note">Add each public key to its Git repository with read-only access.</p></div>
    {#if loading}
      <div class="p-6 text-center settings-note">Loading Git credentials…</div>
    {:else if items.length === 0}
      <div class="p-6 text-center settings-note">No deploy keys yet. Generate one to access a private skill repository.</div>
    {:else}
      <div class="divide-y divide-gray-200 dark:divide-dark-border">
        {#each items as item (item.id)}
          <div class="p-4 space-y-3">
            <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
              <div class="min-w-0"><div class="text-sm font-semibold text-gray-900 dark:text-dark-text">{item.name}</div><div class="text-xs text-gray-500 dark:text-dark-text-muted">{item.host}:{item.port}</div></div>
              <div class="flex flex-wrap gap-2">
                <button class="settings-button" onclick={() => testAccess(item)} disabled={busy}><Check size={12} /> Test</button>
                <button class="settings-button" onclick={() => rotate(item)} disabled={busy}><RotateCw size={12} /> {rotateConfirm === item.id ? 'Confirm rotation' : 'Rotate'}</button>
                <button class="settings-danger" onclick={() => remove(item)} disabled={busy}><Trash2 size={12} /> {deleteConfirm === item.id ? 'Confirm delete' : 'Delete'}</button>
              </div>
            </div>
            {#if testID === item.id}
              <div class="settings-form flex flex-col gap-2 sm:flex-row sm:items-end">
                <label class="min-w-0 flex-1">SSH repository clone URL<input bind:value={testRepositoryURL} placeholder="git@gitlab.com:team/private-skills.git" /></label>
                <button class="settings-primary" onclick={() => testAccess(item)} disabled={busy || !testRepositoryURL.trim()}><Check size={12} /> {busy ? 'Testing…' : 'Run test'}</button>
              </div>
            {/if}
            {#if rotateConfirm === item.id}<p class="text-xs text-amber-700 dark:text-amber-300">Rotation immediately replaces AT’s private key. Update the repository with the new public key after confirming.</p>{/if}
            <div class="flex items-start gap-2 border border-gray-200 bg-gray-50 p-2 dark:border-dark-border dark:bg-dark-base"><code class="min-w-0 flex-1 break-all text-[11px] text-gray-700 dark:text-dark-text-secondary">{item.public_key}</code><button class="settings-button shrink-0" onclick={() => copy(item.public_key)}><Copy size={12} /> Copy</button></div>
            <div class="text-[11px] text-gray-500 dark:text-dark-text-muted">Host fingerprints: {item.fingerprints.join(' · ')}</div>
          </div>
        {/each}
      </div>
    {/if}
  </section>
</div>
