<script lang="ts">
  import { ArrowDown, ArrowUp, KeyRound, Link2, RefreshCw } from 'lucide-svelte';
  import type { MCPUpstream, MCPUpstreamAuth } from '@/lib/api/mcp-servers';
  import { connectMCPAccount, type Connection } from '@/lib/api/connections';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    MCP_ACCOUNT_LABELS,
    MCP_ACCOUNT_SOURCES,
    mcpAuthForm,
    mcpAuthProblems,
    mcpProviderForURL,
    mcpUpstreamAuth,
    moveAccountSource,
    toggleAccountSource,
    type MCPAuthForm,
  } from '@/lib/helper/mcp-oauth';

  interface Props {
    upstream: MCPUpstream;
    /** Saved set and index; Connect is offered only for a saved upstream. */
    setID?: string | null;
    index: number;
    /** True when the upstream differs from what is saved. */
    dirty?: boolean;
    connections: Connection[];
    /** The signed-in account may create/renew a shared (workspace) account. */
    mayShare?: boolean;
    onchange: (auth: MCPUpstreamAuth | undefined) => void;
    onconnected?: () => void;
  }

  let { upstream, setID = null, index, dirty = false, connections, mayShare = false, onchange, onconnected }: Props = $props();

  let form = $state<MCPAuthForm>(mcpAuthForm(upstream.auth));
  let showAdvanced = $state(!!(upstream.auth?.client_id || upstream.auth?.authorization_server));
  let connecting = $state(false);

  function update(patch: Partial<MCPAuthForm>) {
    form = { ...form, ...patch };
    onchange(mcpUpstreamAuth(form));
  }

  let provider = $derived(form.provider.trim() || mcpProviderForURL(upstream.url ?? ''));
  let problems = $derived(mcpAuthProblems(upstream, form));
  let sameServer = (c: Connection) => !!c.mcp_oauth && c.provider === provider;
  let personal = $derived(connections.filter(c => c.scope === 'personal' && sameServer(c)));
  let shared = $derived(connections.filter(c => c.scope === 'workspace' && c.provider === provider));
  let sharedSelected = $derived(shared.find(c => c.id === form.sharedConnectionID));

  async function connect(target: 'personal' | 'shared', connectionID?: string) {
    if (!setID) return;
    connecting = true;
    try {
      const result = await connectMCPAccount(connectionID
        ? { connection_id: connectionID, target }
        : { set_id: setID, upstream_index: index, target, connection_name: target === 'shared' ? `${provider} (shared)` : 'My account' });
      addToast(result.message || 'Account connected');
      onconnected?.();
    } catch (e: any) {
      addToast(e?.message || 'Authorization failed', 'alert');
    } finally {
      connecting = false;
    }
  }

  function statusText(c: Connection): string {
    if (c.mcp_oauth?.needs_reauth) return 'needs reconnecting';
    if (c.mcp_oauth?.expires_at) return `connected · token refreshes automatically`;
    return 'connected';
  }
</script>

<div class="space-y-2 pt-2 border-t border-dark-border">
  <div class="flex items-center gap-2">
    <span class="text-xs font-medium text-dark-text-secondary flex items-center gap-1"><KeyRound size={11} /> Authentication</span>
    <div class="flex text-xs border border-dark-border-subtle">
      <button type="button" onclick={() => update({ enabled: false })}
        class="px-1.5 py-0.5 {!form.enabled ? 'bg-accent text-dark-base' : 'bg-dark-elevated text-dark-text-secondary hover:bg-dark-border'}">None</button>
      <button type="button" onclick={() => update({ enabled: true })}
        class="px-1.5 py-0.5 {form.enabled ? 'bg-accent text-dark-base' : 'bg-dark-elevated text-dark-text-secondary hover:bg-dark-border'}">OAuth</button>
    </div>
  </div>

  {#if form.enabled}
    <p class="text-xs text-dark-text-muted">
      Each person (or agent) uses their own account on this server. Discovery, client registration and token refresh follow the MCP authorization spec.
    </p>

    <div class="grid grid-cols-4 gap-2 items-center">
      <label for="mcp-auth-provider-{index}" class="text-xs font-medium text-dark-text-secondary">Provider key</label>
      <input id="mcp-auth-provider-{index}" type="text" value={form.provider} placeholder={mcpProviderForURL(upstream.url ?? '') || 'mcp-github'}
        oninput={(e) => update({ provider: (e.target as HTMLInputElement).value })}
        class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text placeholder:text-dark-text-muted" />
      <span class="col-start-2 col-span-3 text-[10px] text-dark-text-muted">Accounts are stored as connections of this provider; agents bind them under the same key.</span>
    </div>

    <div class="grid grid-cols-4 gap-2 items-start">
      <span class="text-xs font-medium text-dark-text-secondary pt-1">Accounts</span>
      <div class="col-span-3 space-y-1">
        {#each form.accounts as source, i (source)}
          <div class="flex items-center gap-1 text-xs">
            <span class="w-4 text-dark-text-muted">{i + 1}.</span>
            <span class="flex-1 text-dark-text">{MCP_ACCOUNT_LABELS[source]}</span>
            <button type="button" title="Try earlier" disabled={i === 0} onclick={() => update({ accounts: moveAccountSource(form.accounts, i, -1) })}
              class="p-0.5 text-dark-text-muted hover:text-dark-text disabled:opacity-30"><ArrowUp size={11} /></button>
            <button type="button" title="Try later" disabled={i === form.accounts.length - 1} onclick={() => update({ accounts: moveAccountSource(form.accounts, i, 1) })}
              class="p-0.5 text-dark-text-muted hover:text-dark-text disabled:opacity-30"><ArrowDown size={11} /></button>
            <button type="button" onclick={() => update({ accounts: toggleAccountSource(form.accounts, source) })}
              class="px-1.5 py-0.5 text-xs text-dark-text-muted hover:text-red-400">Remove</button>
          </div>
        {/each}
        <div class="flex flex-wrap items-center gap-1">
          {#each MCP_ACCOUNT_SOURCES.filter(s => !form.accounts.includes(s)) as source (source)}
            <button type="button" onclick={() => update({ accounts: toggleAccountSource(form.accounts, source) })}
              class="px-1.5 py-0.5 text-xs border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">+ {MCP_ACCOUNT_LABELS[source]}</button>
          {/each}
        </div>
        <p class="text-[10px] text-dark-text-muted">
          Tried top to bottom; the first with a connected account wins. A source that is not listed is never used.
          <em>Signed-in user</em> is the account a run executes as (a personal API token's owner on the gateway);
          <em>Agent binding</em> is the connection the agent selects for this provider.
        </p>
      </div>
    </div>

    {#if form.accounts.includes('shared')}
      <div class="grid grid-cols-4 gap-2 items-center">
        <label for="mcp-auth-shared-{index}" class="text-xs font-medium text-dark-text-secondary">Shared account</label>
        <select id="mcp-auth-shared-{index}" value={form.sharedConnectionID} onchange={(e) => update({ sharedConnectionID: (e.target as HTMLSelectElement).value })}
          class="col-span-3 text-xs border border-dark-border-subtle bg-dark-elevated px-2 py-1 text-dark-text">
          <option value="">Choose a workspace connection…</option>
          {#each shared as c (c.id)}
            <option value={c.id}>{c.name}{c.account_label ? ` — ${c.account_label}` : ''}{c.mcp_oauth?.needs_reauth ? ' (needs reconnecting)' : ''}</option>
          {/each}
        </select>
      </div>
    {/if}

    <div class="grid grid-cols-4 gap-2 items-center">
      <label for="mcp-auth-scopes-{index}" class="text-xs font-medium text-dark-text-secondary">Scopes</label>
      <input id="mcp-auth-scopes-{index}" type="text" value={form.scopes} placeholder="Server default"
        oninput={(e) => update({ scopes: (e.target as HTMLInputElement).value })}
        class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text placeholder:text-dark-text-muted" />
    </div>

    <button type="button" onclick={() => (showAdvanced = !showAdvanced)} class="text-xs text-dark-text-muted hover:text-dark-text">
      {showAdvanced ? 'Hide' : 'Show'} advanced
    </button>
    {#if showAdvanced}
      <div class="grid grid-cols-4 gap-2 items-center">
        <label for="mcp-auth-client-{index}" class="text-xs font-medium text-dark-text-secondary">Client ID</label>
        <input id="mcp-auth-client-{index}" type="text" value={form.clientID} placeholder="Dynamic registration"
          oninput={(e) => update({ clientID: (e.target as HTMLInputElement).value })}
          class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text placeholder:text-dark-text-muted" />
        <span class="col-start-2 col-span-3 text-[10px] text-dark-text-muted">Only for servers without dynamic client registration. Never put a client secret here.</span>
        <label for="mcp-auth-issuer-{index}" class="text-xs font-medium text-dark-text-secondary">Issuer</label>
        <input id="mcp-auth-issuer-{index}" type="text" value={form.authorizationServer} placeholder="Discovered from the server"
          oninput={(e) => update({ authorizationServer: (e.target as HTMLInputElement).value })}
          class="col-span-3 border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text placeholder:text-dark-text-muted" />
      </div>
    {/if}

    {#each problems as problem}
      <p class="text-xs text-oc-red">{problem}</p>
    {/each}

    <div class="flex flex-wrap items-center gap-2 pt-1 text-xs">
      {#if !setID || dirty || !upstream.auth}
        <span class="text-dark-text-muted">Save the set to connect accounts.</span>
      {:else}
        {#each personal as c (c.id)}
          <span class="flex items-center gap-1 {c.mcp_oauth?.needs_reauth ? 'text-oc-red' : 'text-oc-green'}"><Link2 size={11} /> Your account: {statusText(c)}</span>
          <button type="button" disabled={connecting} onclick={() => connect('personal', c.id)}
            class="flex items-center gap-1 px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50"><RefreshCw size={10} /> Reconnect</button>
        {:else}
          <button type="button" disabled={connecting || problems.length > 0} onclick={() => connect('personal')}
            class="flex items-center gap-1 px-2 py-1 bg-accent text-dark-base disabled:opacity-50"><Link2 size={11} /> Connect my account</button>
        {/each}
        {#if mayShare && form.accounts.includes('shared')}
          {#if sharedSelected?.mcp_oauth}
            <button type="button" disabled={connecting} onclick={() => connect('shared', sharedSelected.id)}
              class="flex items-center gap-1 px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50"><RefreshCw size={10} /> Reconnect shared account</button>
          {:else}
            <button type="button" disabled={connecting || problems.some(p => !p.startsWith('Choose the shared'))} onclick={() => connect('shared')}
              class="flex items-center gap-1 px-1.5 py-0.5 border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50"><Link2 size={10} /> Connect shared account</button>
          {/if}
        {/if}
      {/if}
    </div>
  {/if}
</div>
