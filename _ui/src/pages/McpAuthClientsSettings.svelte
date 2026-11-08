<script lang="ts">
  import { onMount } from 'svelte';
  import { Trash2, ChevronRight, ChevronDown, Search } from 'lucide-svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAllMCPAuthClients, adminDeleteMCPAuthClient, type MCPAuthClientUsage } from '@/lib/api/mcp-auth';

  storeNavbar.title = 'MCP OAuth clients';

  let clients = $state<MCPAuthClientUsage[]>([]);
  let loading = $state(true);
  let error = $state('');
  let query = $state('');
  let kind = $state<'all' | 'dynamic' | 'metadata' | 'registered' | 'unused'>('all');
  let open = $state<Record<string, boolean>>({});

  function kindOf(c: MCPAuthClientUsage) {
    if (c.metadata_document) return 'metadata';
    return c.dynamic ? 'dynamic' : 'registered';
  }
  const kindLabel: Record<string, string> = { metadata: 'Metadata document', dynamic: 'Dynamic', registered: 'Pre-registered' };

  let filtered = $derived(clients.filter(c => {
    if (kind === 'unused' ? c.grants > 0 : kind !== 'all' && kindOf(c) !== kind) return false;
    const q = query.trim().toLowerCase();
    if (!q) return true;
    return [c.name, c.client_id, c.server_name || '', ...c.redirect_uris, ...c.users.map(u => u.username || u.user_id)]
      .some(v => v.toLowerCase().includes(q));
  }));

  function when(ts?: string) { return ts ? new Date(ts).toLocaleString() : '—'; }

  async function load() {
    loading = true;
    error = '';
    try {
      clients = await listAllMCPAuthClients();
    } catch (e: any) {
      error = e?.response?.data?.message || 'Could not load OAuth clients.';
    } finally {
      loading = false;
    }
  }

  async function remove(c: MCPAuthClientUsage) {
    const who = c.active_users ? ` ${c.active_users} account(s) signed in through it will be signed out.` : '';
    if (!confirm(`Delete the client "${c.name}"?${who} A dynamic client can register again.`)) return;
    try {
      await adminDeleteMCPAuthClient(c.id);
      clients = clients.filter(x => x.id !== c.id);
      addToast('Client deleted');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Could not delete the client', 'alert');
    }
  }

  onMount(() => { void load(); });
</script>

<svelte:head><title>AT | MCP OAuth clients</title></svelte:head>
<div class="settings-page">
  <header>
    <h1 class="settings-title">MCP OAuth clients</h1>
    <p class="settings-subtitle">Applications that sign in to this installation's MCP servers: dynamically registered, identified by a metadata document, or pre-registered on a server.</p>
  </header>

  {#if error}
    <div role="alert" class="settings-error"><p>{error}</p><button type="button" class="settings-button mt-2" onclick={load}>Retry</button></div>
  {/if}

  <section class="border border-dark-border">
    <div class="px-4 py-3 border-b border-dark-border flex flex-wrap items-center gap-2">
      <label class="relative flex-1 min-w-48">
        <Search size={14} class="absolute left-2 top-1/2 -translate-y-1/2 text-dark-text-muted" />
        <input class="settings-input pl-7" placeholder="Search name, client ID, redirect, user" bind:value={query} />
      </label>
      <select class="settings-input w-48" bind:value={kind}>
        <option value="all">All clients</option>
        <option value="dynamic">Dynamic</option>
        <option value="metadata">Metadata document</option>
        <option value="registered">Pre-registered</option>
        <option value="unused">Never used</option>
      </select>
    </div>
    {#if loading}
      <p class="p-4 settings-note" role="status">Loading clients…</p>
    {:else if filtered.length === 0}
      <p class="p-4 settings-note">{clients.length ? 'No client matches.' : 'No MCP client has registered yet.'}</p>
    {:else}
      <ul class="divide-y divide-dark-border">
        {#each filtered as c (c.id)}
          <li class="px-4 py-3 text-sm">
            <div class="flex flex-wrap items-start gap-3">
              <button type="button" class="mt-0.5 text-dark-text-muted" title={open[c.id] ? 'Hide accounts' : 'Show accounts'} onclick={() => open[c.id] = !open[c.id]} disabled={!c.users.length}>
                {#if open[c.id]}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}
              </button>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{c.name}</span>
                  <span class="px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted">{kindLabel[kindOf(c)]}</span>
                  {#if c.confidential}<span class="px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted">confidential</span>{/if}
                  {#if c.server_name}<span class="text-xs text-dark-text-muted">→ {c.server_name}</span>{/if}
                </div>
                <p class="settings-note font-mono break-all">{c.client_id}</p>
                <p class="settings-note break-all">{c.redirect_uris.join(', ')}</p>
                <p class="settings-note">{c.active_users} account(s) · {c.grants} sign-in(s) · registered {when(c.created_at)} · last used {when(c.last_used_at)}</p>
              </div>
              <button type="button" class="settings-button min-h-11 sm:min-h-0" title="Delete client" onclick={() => remove(c)}><Trash2 size={14} /></button>
            </div>
            {#if open[c.id] && c.users.length}
              <table class="mt-2 ml-6 text-xs">
                <thead><tr class="text-dark-text-muted text-left"><th class="pr-4 font-normal">Account</th><th class="pr-4 font-normal">MCP server</th><th class="pr-4 font-normal">Approved</th><th class="font-normal">Last used</th></tr></thead>
                <tbody>
                  {#each c.users as u}
                    <tr><td class="pr-4 break-all">{u.username || u.user_id}</td><td class="pr-4">{u.server_name || '—'}</td><td class="pr-4">{when(u.created_at)}</td><td>{when(u.last_used_at)}</td></tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
  <p class="settings-note">Dynamic and metadata-document clients that never obtain a sign-in are removed after a day. Deleting a client ends every sign-in made through it; each account can also revoke its own under Connections.</p>
</div>
