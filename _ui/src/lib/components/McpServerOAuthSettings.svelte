<script lang="ts">
  import { KeyRound, Plus, Trash2, Copy } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { deploymentUrl } from '@/lib/helper/deployment-url';
  import {
    createMCPAuthClient,
    deleteMCPAuthClient,
    listMCPAuthClients,
    type MCPAuthClient,
    type MCPServerOAuth,
  } from '@/lib/api/mcp-servers';

  interface Props {
    value: MCPServerOAuth;
    /** Saved server ID; clients can be registered only for a saved server. */
    serverID: string | null;
    serverName: string;
  }
  let { value = $bindable(), serverID, serverName }: Props = $props();

  let patterns = $state((value.redirect_patterns ?? []).join('\n'));
  let clients = $state<MCPAuthClient[]>([]);
  let loadedFor = '';
  let newName = $state('');
  let newRedirects = $state('');
  let newConfidential = $state(false);
  let creating = $state(false);
  let created = $state<MCPAuthClient | null>(null);

  $effect(() => {
    value.redirect_patterns = patterns.split('\n').map(p => p.trim()).filter(Boolean);
  });

  $effect(() => {
    if (!value.enabled || !serverID || loadedFor === serverID) return;
    loadedFor = serverID;
    listMCPAuthClients(serverID).then(list => { clients = list; }).catch(() => { clients = []; });
  });

  async function create() {
    if (!serverID) return;
    creating = true;
    try {
      const client = await createMCPAuthClient(serverID, {
        name: newName.trim() || 'MCP client',
        redirect_uris: newRedirects.split('\n').map(u => u.trim()).filter(Boolean),
        confidential: newConfidential,
      });
      created = client;
      clients = [...clients, { ...client, client_secret: undefined }];
      newName = ''; newRedirects = ''; newConfidential = false;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to register the client', 'alert');
    } finally {
      creating = false;
    }
  }

  async function remove(c: MCPAuthClient) {
    if (!serverID || !confirm(`Delete client "${c.name}"? Everyone signed in through it loses access.`)) return;
    try {
      await deleteMCPAuthClient(serverID, c.id);
      clients = clients.filter(x => x.id !== c.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete the client', 'alert');
    }
  }

  function copy(text: string) {
    navigator.clipboard?.writeText(text).then(() => addToast('Copied')).catch(() => addToast('Copy failed', 'alert'));
  }

  let endpoint = $derived(serverName ? deploymentUrl(`gateway/v1/mcp/${encodeURIComponent(serverName)}`) : '');
  const input = 'w-full border border-dark-border-subtle px-2 py-1 text-xs font-mono bg-dark-elevated text-dark-text placeholder:text-dark-text-muted';
</script>

<div class="border border-dark-border">
  <label class="flex items-start gap-2 cursor-pointer px-3 py-2 bg-dark-base border-b border-dark-border-subtle">
    <input type="checkbox" bind:checked={value.enabled} class="mt-0.5 w-3.5 h-3.5 bg-dark-elevated border-dark-border-subtle accent-accent" />
    <span class="text-xs text-dark-text-secondary leading-relaxed">
      <span class="flex items-center gap-1.5 text-sm font-medium text-dark-text"><KeyRound size={14} /> Sign in with AT (OAuth)</span>
      <span class="block text-dark-text-muted mt-0.5">
        MCP clients (Claude Code, Cursor, ChatGPT, …) connect without an API token: the person signs in to AT in the browser and approves.
        Tools then run as that person with their workspace permissions, and upstreams set to "Signed-in user" use their own connected accounts.
        API tokens keep working.
      </span>
    </span>
  </label>
  {#if value.enabled}
    <div class="p-3 space-y-3">
      {#if endpoint}
        <div class="flex items-center gap-2 text-xs">
          <span class="text-dark-text-muted shrink-0">Client URL</span>
          <code class="min-w-0 truncate px-1 py-0.5 bg-dark-elevated">{endpoint}</code>
          <button type="button" class="p-1 text-dark-text-muted hover:text-dark-text" title="Copy" onclick={() => copy(endpoint)}><Copy size={12} /></button>
        </div>
      {/if}

      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={value.dynamic_clients} class="mt-0.5 w-3.5 h-3.5 accent-accent" />
        <span class="text-xs text-dark-text-secondary">
          <span class="font-medium text-dark-text">Allow dynamic client registration</span>
          <span class="block text-dark-text-muted">Clients register themselves (RFC 7591); what most MCP clients expect. Off admits only the clients registered below.</span>
        </span>
      </label>
      {#if value.dynamic_clients}
        <div class="space-y-1">
          <label for="oauth-patterns" class="text-xs font-medium text-dark-text-secondary">Allowed redirect URIs for dynamic clients</label>
          <textarea id="oauth-patterns" rows="3" bind:value={patterns} class={input} placeholder={'http://127.0.0.1:*\nhttps://claude.ai/api/mcp/auth_callback'}></textarea>
          <p class="text-[10px] text-dark-text-muted">One per line; end with * for a prefix. Empty allows any https or loopback redirect.</p>
        </div>
      {/if}

      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={value.chain_upstreams} class="mt-0.5 w-3.5 h-3.5 accent-accent" />
        <span class="text-xs text-dark-text-secondary">
          <span class="font-medium text-dark-text">Connect upstream accounts during sign-in</span>
          <span class="block text-dark-text-muted">After approving, the person is asked to connect each OAuth upstream (GitLab, GitHub, …) they have not connected yet.</span>
        </span>
      </label>

      <div class="border border-dark-border">
        <div class="px-3 py-2 text-xs font-medium text-dark-text-secondary border-b border-dark-border-subtle">Registered clients</div>
        {#if !serverID}
          <p class="px-3 py-2 text-xs text-dark-text-muted">Save the server to register clients.</p>
        {:else}
          {#if created?.client_secret}
            <div class="m-3 border border-oc-peach p-2 text-xs space-y-1">
              <p class="text-dark-text">Copy the secret now; it is not shown again.</p>
              <div class="flex items-center gap-2"><span class="text-dark-text-muted w-16 shrink-0">Client ID</span><code class="truncate">{created.client_id}</code><button type="button" class="p-1" onclick={() => copy(created!.client_id)}><Copy size={11} /></button></div>
              <div class="flex items-center gap-2"><span class="text-dark-text-muted w-16 shrink-0">Secret</span><code class="truncate">{created.client_secret}</code><button type="button" class="p-1" onclick={() => copy(created!.client_secret!)}><Copy size={11} /></button></div>
            </div>
          {/if}
          {#if clients.length}
            <ul class="divide-y divide-dark-border">
              {#each clients as c (c.id)}
                <li class="flex items-start justify-between gap-2 px-3 py-2 text-xs">
                  <span class="min-w-0">
                    <span class="text-dark-text">{c.name}</span>
                    <span class="ml-1 text-dark-text-muted">{c.confidential ? 'confidential' : 'public'}</span>
                    <span class="flex items-center gap-1 font-mono text-dark-text-muted"><span class="truncate">{c.client_id}</span><button type="button" class="p-0.5" title="Copy client ID" onclick={() => copy(c.client_id)}><Copy size={10} /></button></span>
                    <span class="block text-dark-text-faint truncate">{c.redirect_uris.join(', ')}</span>
                  </span>
                  <button type="button" class="p-1 text-red-400 hover:bg-red-900/20" title="Delete" onclick={() => remove(c)}><Trash2 size={12} /></button>
                </li>
              {/each}
            </ul>
          {/if}
          <div class="p-3 space-y-2 border-t border-dark-border-subtle">
            <input type="text" bind:value={newName} placeholder="Client name" class={input} />
            <textarea rows="2" bind:value={newRedirects} placeholder="Redirect URIs, one per line" class={input}></textarea>
            <div class="flex items-center justify-between gap-2">
              <label class="flex items-center gap-1.5 text-xs text-dark-text-secondary"><input type="checkbox" bind:checked={newConfidential} class="accent-accent" /> Issue a client secret</label>
              <button type="button" disabled={creating || !newRedirects.trim()} onclick={create} class="flex items-center gap-1 px-2 py-1 text-xs bg-accent text-dark-base disabled:opacity-50"><Plus size={12} /> Register client</button>
            </div>
            <p class="text-[10px] text-dark-text-muted">For clients without dynamic registration, or when it is off. A client is bound to this server.</p>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>
