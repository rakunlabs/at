<script lang="ts">
  import AuthShell from '@/lib/components/AuthShell.svelte';
  import { onMount } from 'svelte';
  import axios from 'axios';
  import { isAuthUnauthorized } from '@/lib/api/auth';
  import { ReauthenticationRequired } from '@/lib/api/session-transport';
  import { storeAuth } from '@/lib/store/auth.svelte';
  import { workspaceState } from '@/lib/store/workspace.svelte';
  import { connectMCPAccount } from '@/lib/api/connections';
  import {
    decideMCPAuthRequest,
    getMCPAuthRequest,
    mcpAuthParams,
    safeMCPAuthRedirect,
    type MCPAuthPendingAccount,
    type MCPAuthRequestInfo,
  } from '@/lib/api/mcp-auth';

  interface Props { query: string; onlogin: () => void }
  let { query, onlogin }: Props = $props();

  const params = $derived(mcpAuthParams(query));
  const targetWorkspace = $derived(new URLSearchParams(query).get('at_workspace') || '');

  let info = $state<MCPAuthRequestInfo | null>(null);
  let phase = $state<'loading' | 'ready' | 'sending' | 'chaining' | 'finished' | 'error'>('loading');
  let message = $state('');
  let redirect = $state('');
  let pending = $state<MCPAuthPendingAccount[]>([]);
  let connected = $state<string[]>([]);
  let connecting = $state('');
  let workspaceName = $derived(workspaceState.items.find(w => w.id === info?.workspace_id)?.name || '');

  function fail(error: unknown) {
    if (isAuthUnauthorized(error) || error instanceof ReauthenticationRequired) { onlogin(); return; }
    phase = 'error';
    const response = axios.isAxiosError(error) ? error.response : undefined;
    message = response?.status === 403
      ? 'Your account is not a member of the workspace that owns this MCP server, or lacks access to it. Ask a workspace administrator, then start the sign-in again from your MCP client.'
      : (response?.data as { message?: string })?.message || 'The request could not be completed. Start the sign-in again from your MCP client.';
  }

  onMount(() => {
    // Asked in the workspace that owns the server, without changing the
    // tab's own selection.
    getMCPAuthRequest(params, targetWorkspace).then(data => {
      info = data;
      if (!data.valid) { phase = 'error'; message = data.message || 'This sign-in request is not valid.'; return; }
      phase = 'ready';
    }).catch(fail);
  });

  function leave(target: string) {
    const safe = safeMCPAuthRedirect(target);
    if (!safe) { phase = 'error'; message = 'The client returned an unusable address.'; return; }
    phase = 'finished';
    window.location.assign(safe);
  }

  async function decide(approve: boolean) {
    if (phase !== 'ready') return;
    phase = 'sending';
    try {
      const decision = await decideMCPAuthRequest(params, approve, targetWorkspace);
      redirect = decision.redirect;
      pending = approve ? decision.pending_accounts ?? [] : [];
      if (pending.length) { phase = 'chaining'; return; }
      leave(redirect);
    } catch (error) { fail(error); }
  }

  const key = (p: MCPAuthPendingAccount) => `${p.set_id}:${p.upstream_index}`;

  // Called from a click so the popup keeps its user activation.
  function connect(p: MCPAuthPendingAccount) {
    connecting = key(p);
    connectMCPAccount({ set_id: p.set_id, upstream_index: p.upstream_index, target: 'personal', connection_name: 'My account' })
      .then(() => { connected = [...connected, key(p)]; })
      .catch((e: Error) => { message = e.message || 'Authorization failed'; })
      .finally(() => { connecting = ''; });
  }
  let remaining = $derived(pending.filter(p => !connected.includes(key(p))));
</script>

<AuthShell
  title={phase === 'chaining' ? 'Connect your accounts' : phase === 'finished' ? 'Returning to your app' : 'Allow MCP access?'}
  subtitle={phase === 'ready' ? 'Review the request before approving it.' : ''}
  width="lg"
>
  {#if phase === 'loading'}
    <p role="status" class="settings-note">Loading the sign-in request…</p>
  {:else if phase === 'error'}
    <p role="alert" class="settings-error">{message}</p>
  {:else if phase === 'finished'}
    <p role="status" class="settings-note">Your MCP client should continue now. You can close this page.</p>
  {:else if phase === 'chaining'}
    <p class="text-sm text-dark-text-secondary leading-relaxed">
      Access was approved. <strong>{info?.server_name}</strong> also reaches services that sign in with your own account. Connect them now, or later under Connections; until then their tools answer with a request to connect.
    </p>
    <ul class="settings-list">
      {#each pending as p (key(p))}
        <li class="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
          <span class="min-w-0">
            <span class="block text-sm font-medium">{p.set_name}</span>
            <span class="block settings-note font-mono truncate">{p.server}</span>
          </span>
          {#if connected.includes(key(p))}
            <span class="text-xs text-oc-green">Connected</span>
          {:else}
            <button type="button" class="settings-button min-h-11 sm:min-h-0" disabled={connecting !== ''} onclick={() => connect(p)}>
              {connecting === key(p) ? 'Connecting…' : 'Connect'}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if message}<p role="alert" class="settings-error">{message}</p>{/if}
    <button type="button" class="settings-primary w-full min-h-11 sm:min-h-0" disabled={connecting !== ''} onclick={() => leave(redirect)}>
      {remaining.length ? 'Continue without the rest' : 'Continue'}
    </button>
  {:else if info}
    <p class="text-sm text-dark-text-secondary leading-relaxed">
      <strong><bdi>{info.client_name}</bdi></strong> wants to use the MCP server <strong>{info.server_name}</strong> as your account. Its tools will run with your permissions.
    </p>
    <dl class="settings-list">
      <div class="px-4 py-3"><dt class="settings-note">Signed in as</dt><dd class="mt-0.5 text-sm font-medium break-all"><bdi>{storeAuth.identity?.name || storeAuth.identity?.subject}</bdi></dd></div>
      <div class="px-4 py-3"><dt class="settings-note">MCP server</dt><dd class="mt-0.5 text-sm break-all">{info.server_name}{workspaceName ? ` · ${workspaceName}` : ''}{#if info.description}<span class="block settings-note">{info.description}</span>{/if}</dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Application{info.client_dynamic ? ' (name chosen by the app, unverified)' : ''}</dt><dd class="mt-0.5 text-sm break-all"><bdi>{info.client_name}</bdi></dd></div>
      <div class="px-4 py-3"><dt class="settings-note">Returns to</dt><dd class="mt-0.5 text-sm font-mono break-all">{info.redirect_host}</dd></div>
    </dl>
    {#if info.workspace_mismatch}
      <p role="alert" class="settings-error">You are not a member of the workspace that owns this MCP server, so you cannot approve this request.</p>
    {:else if info.allowed === false}
      <p role="alert" class="settings-error">Your account does not have access to this MCP server. Ask a workspace administrator.</p>
    {/if}
    <p class="settings-note leading-relaxed">Approve only if you just started this sign-in in that app. You can revoke access at any time under Connections.</p>
    <div class="flex flex-col gap-2 sm:flex-row" aria-busy={phase === 'sending'}>
      <button type="button" onclick={() => decide(false)} disabled={phase !== 'ready'} class="settings-button flex-1 min-h-11 sm:min-h-0">Deny</button>
      <button type="button" onclick={() => decide(true)} disabled={phase !== 'ready' || info.workspace_mismatch || info.allowed === false} class="settings-primary flex-1 min-h-11 sm:min-h-0">Allow access</button>
    </div>
  {/if}
</AuthShell>
