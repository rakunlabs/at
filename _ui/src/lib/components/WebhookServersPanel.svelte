<script lang="ts">
  import {
    listWebhookServers,
    createWebhookServer,
    updateWebhookServer,
    deleteWebhookServer,
    reloadWebhookServers,
    webhookServerBaseUrl,
    type WebhookServer,
  } from '@/lib/api/webhook-servers';
  import { listWorkspaces, type Workspace } from '@/lib/api/workspaces';
  import { isNativeAdmin } from '@/lib/store/auth.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { Server, Plus, Pencil, Trash2, X, Save, RefreshCw, Copy, Lock } from 'lucide-svelte';

  interface Props { onchange?: () => void }
  let { onchange }: Props = $props();

  const admin = $derived(isNativeAdmin());

  let servers = $state<WebhookServer[]>([]);
  let workspaces = $state<Workspace[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let saving = $state(false);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  let fName = $state('');
  let fDescription = $state('');
  let fHost = $state('');
  let fPort = $state<number | null>(5050);
  let fBasePath = $state('');
  let fEnabled = $state(true);
  let fAllWorkspaces = $state(false);
  let fWorkspaceIds = $state<string[]>([]);
  let fPublicUrl = $state('');
  let fCidrs = $state('');
  let fMaxBodyMB = $state<number | null>(null);
  let fRate = $state<number | null>(null);
  let fTlsCert = $state('');
  let fTlsKey = $state('');
  let fTlsStored = $state(false);

  async function load() {
    loading = true;
    loadError = '';
    try {
      const [list, ws] = await Promise.all([listWebhookServers(), admin ? listWorkspaces() : Promise.resolve([] as Workspace[])]);
      servers = list;
      workspaces = ws.filter(w => !w.archived);
    } catch (e: any) {
      loadError = e?.response?.data?.message || 'Webhook servers could not be loaded.';
    } finally {
      loading = false;
    }
  }

  load();

  function resetForm() {
    fName = fDescription = fHost = fBasePath = fPublicUrl = fCidrs = fTlsCert = fTlsKey = '';
    fPort = 5050;
    fEnabled = true;
    fAllWorkspaces = false;
    fWorkspaceIds = [];
    fMaxBodyMB = null;
    fRate = null;
    fTlsStored = false;
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(s: WebhookServer) {
    resetForm();
    editingId = s.id;
    fName = s.name;
    fDescription = s.description;
    fHost = s.bind_host;
    fPort = s.port;
    fBasePath = s.base_path;
    fEnabled = s.enabled;
    fAllWorkspaces = s.all_workspaces;
    fWorkspaceIds = [...(s.workspace_ids || [])];
    fPublicUrl = s.public_url;
    fCidrs = (s.allowed_cidrs || []).join('\n');
    fMaxBodyMB = s.max_body_bytes ? Math.round(s.max_body_bytes / (1 << 20)) : null;
    fRate = s.rate_limit_per_minute || null;
    fTlsCert = s.tls_cert;
    fTlsKey = s.tls_key;
    fTlsStored = !!s.tls_key;
    showForm = true;
  }

  function toggleWorkspace(id: string) {
    fWorkspaceIds = fWorkspaceIds.includes(id) ? fWorkspaceIds.filter(w => w !== id) : [...fWorkspaceIds, id];
  }

  async function handleSubmit() {
    if (!fName.trim() || !fPort) {
      addToast('Name and port are required', 'warn');
      return;
    }
    saving = true;
    try {
      const payload: Partial<WebhookServer> = {
        name: fName.trim(),
        description: fDescription.trim(),
        bind_host: fHost.trim(),
        port: Number(fPort),
        base_path: fBasePath.trim(),
        enabled: fEnabled,
        all_workspaces: fAllWorkspaces,
        workspace_ids: fAllWorkspaces ? [] : fWorkspaceIds,
        public_url: fPublicUrl.trim(),
        allowed_cidrs: fCidrs.split(/[\s,]+/).map(v => v.trim()).filter(Boolean),
        max_body_bytes: fMaxBodyMB ? Math.round(Number(fMaxBodyMB) * (1 << 20)) : 0,
        rate_limit_per_minute: fRate ? Number(fRate) : 0,
        tls_cert: fTlsCert.trim(),
        tls_key: fTlsKey.trim(),
      };
      const saved = editingId ? await updateWebhookServer(editingId, payload) : await createWebhookServer(payload);
      if (saved.status?.state === 'error') {
        addToast(`Saved, but the port could not be opened: ${saved.status.error}`, 'warn');
      } else {
        addToast(editingId ? 'Webhook server updated' : 'Webhook server created');
      }
      resetForm();
      await load();
      onchange?.();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save webhook server', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteWebhookServer(id);
      addToast('Webhook server deleted. Its webhooks remain on their other routes.');
      deleteConfirm = null;
      await load();
      onchange?.();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete webhook server', 'alert');
    }
  }

  async function retry() {
    try {
      servers = await reloadWebhookServers();
      addToast('Listeners reloaded');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to reload listeners', 'alert');
    }
  }

  async function copy(url: string) {
    try {
      await navigator.clipboard.writeText(url);
      addToast('URL copied');
    } catch {
      addToast('Failed to copy URL', 'alert');
    }
  }

  function workspaceName(id: string) {
    return workspaces.find(w => w.id === id)?.name || id.slice(0, 10);
  }

  function stateClass(state?: string) {
    switch (state) {
      case 'running': return 'text-green-400';
      case 'error': return 'text-red-400';
      default: return 'text-dark-text-muted';
    }
  }

  const inputClass = 'w-full border border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted';
  const labelClass = 'text-sm font-medium text-dark-text-secondary';
  const hintClass = 'text-xs text-dark-text-muted mt-1';
</script>

<div class="flex items-center justify-between mb-4">
  <div class="flex items-center gap-2">
    <Server size={16} class="text-dark-text-muted" />
    <h2 class="text-sm font-medium text-dark-text">Webhook servers</h2>
    <span class="text-xs text-dark-text-muted">({servers.length})</span>
  </div>
  <div class="flex items-center gap-2">
    <button onclick={load} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" title="Refresh">
      <RefreshCw size={14} />
    </button>
    {#if admin}
      <button onclick={retry} class="px-3 py-1.5 text-xs border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary" title="Retry listeners whose port was busy">
        Reload listeners
      </button>
      <button onclick={openCreate} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-dark-base bg-accent hover:bg-accent-hover">
        <Plus size={12} />
        New Server
      </button>
    {/if}
  </div>
</div>

<p class="text-xs text-dark-text-muted mb-4 max-w-prose">
  A webhook server opens its own port that serves only the webhooks bound to it — no UI, API or gateway. Bind webhooks from the Webhooks tab.
  {#if !admin}Servers are managed by installation administrators; you see the ones open to this workspace.{/if}
</p>

{#if showForm && admin}
  <div class="border border-dark-border mb-6 bg-dark-surface overflow-hidden">
    <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
      <span class="text-sm font-medium text-dark-text">{editingId ? 'Edit Webhook Server' : 'New Webhook Server'}</span>
      <button onclick={resetForm} class="p-1 hover:bg-dark-elevated text-dark-text-muted"><X size={14} /></button>
    </div>
    <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
      <div class="grid grid-cols-4 gap-3 items-center">
        <label for="ws-name" class={labelClass}>Name</label>
        <input id="ws-name" bind:value={fName} placeholder="e.g. public-hooks" class={['col-span-3', inputClass]} />
      </div>
      <div class="grid grid-cols-4 gap-3 items-center">
        <label for="ws-desc" class={labelClass}>Description</label>
        <input id="ws-desc" bind:value={fDescription} placeholder="Optional" class={['col-span-3', inputClass]} />
      </div>
      <div class="grid grid-cols-4 gap-3 items-start">
        <label for="ws-port" class={[labelClass, 'pt-1.5']}>Listen</label>
        <div class="col-span-3">
          <div class="grid grid-cols-3 gap-2">
            <input bind:value={fHost} placeholder="All interfaces" aria-label="Bind host" class={['col-span-2 font-mono', inputClass]} />
            <input id="ws-port" type="number" min="1024" max="65535" bind:value={fPort} aria-label="Port" class={['font-mono', inputClass]} />
          </div>
          <div class={hintClass}>Leave the host empty to listen on every interface. Ports below 1024 and the main server's port are refused.</div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start">
        <label for="ws-base" class={[labelClass, 'pt-1.5']}>Base path</label>
        <div class="col-span-3">
          <input id="ws-base" bind:value={fBasePath} placeholder="Optional, e.g. /hooks" class={['font-mono', inputClass]} />
          <div class={hintClass}>Prefix for every webhook on this port. GET {fBasePath ? '/' + fBasePath.replace(/^\/+|\/+$/g, '') : ''}/healthz answers a health check.</div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start">
        <label for="ws-public" class={[labelClass, 'pt-1.5']}>Public URL</label>
        <div class="col-span-3">
          <input id="ws-public" bind:value={fPublicUrl} placeholder="Optional, e.g. https://hooks.example.com" class={['font-mono', inputClass]} />
          <div class={hintClass}>How senders reach this port (load balancer, tunnel). Only used to show copyable URLs; defaults to this host and port.</div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start border-t border-dark-border pt-4">
        <span class={[labelClass, 'pt-1']}>Workspaces</span>
        <div class="col-span-3 space-y-2">
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={fAllWorkspaces} />
            <span class="text-dark-text-secondary">All workspaces, including future ones</span>
          </label>
          {#if !fAllWorkspaces}
            <div class="flex flex-wrap gap-1.5">
              {#each workspaces as w (w.id)}
                <button type="button" onclick={() => toggleWorkspace(w.id)} aria-pressed={fWorkspaceIds.includes(w.id)}
                  class={['px-2 py-1 text-xs border', fWorkspaceIds.includes(w.id) ? 'text-dark-base bg-accent border-accent' : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated']}>
                  {w.name}
                </button>
              {/each}
            </div>
            <div class={hintClass}>Members of the selected workspaces can publish their webhooks here. Removing a workspace unpublishes its webhooks from this server.</div>
          {/if}
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start border-t border-dark-border pt-4">
        <label for="ws-cidrs" class={[labelClass, 'pt-1.5']}>Allowed addresses</label>
        <div class="col-span-3">
          <textarea id="ws-cidrs" bind:value={fCidrs} rows="2" placeholder="Optional, e.g. 140.82.112.0/20" class={['font-mono text-xs', inputClass]}></textarea>
          <div class={hintClass}>CIDR ranges or addresses, one per line. Empty allows everyone. Forwarded headers are honoured only from server.trusted_proxies.</div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start">
        <span class={[labelClass, 'pt-1.5']}>Limits</span>
        <div class="col-span-3 grid grid-cols-2 gap-2">
          <div>
            <input type="number" min="1" max="256" bind:value={fMaxBodyMB} placeholder="10" aria-label="Max body (MiB)" class={['font-mono', inputClass]} />
            <div class={hintClass}>Max body, MiB (default 10)</div>
          </div>
          <div>
            <input type="number" min="1" bind:value={fRate} placeholder="Off" aria-label="Requests per minute per client" class={['font-mono', inputClass]} />
            <div class={hintClass}>Requests/minute per client</div>
          </div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-start border-t border-dark-border pt-4">
        <span class={[labelClass, 'pt-1.5']}>TLS</span>
        <div class="col-span-3 space-y-2">
          <textarea bind:value={fTlsCert} rows="3" placeholder="-----BEGIN CERTIFICATE----- (optional)" aria-label="TLS certificate (PEM)" class={['font-mono text-xs', inputClass]}></textarea>
          <textarea
            bind:value={fTlsKey}
            rows="3"
            placeholder="-----BEGIN PRIVATE KEY-----"
            aria-label="TLS private key (PEM)"
            onfocus={() => { if (fTlsKey === '***') fTlsKey = ''; }}
            onblur={() => { if (!fTlsKey && fTlsStored && fTlsCert) fTlsKey = '***'; }}
            class={['font-mono text-xs', inputClass]}
          ></textarea>
          <div class={hintClass}>Serve HTTPS directly. Leave both empty for HTTP (for example behind a TLS-terminating proxy). The key is stored encrypted and never shown again.</div>
        </div>
      </div>
      <div class="grid grid-cols-4 gap-3 items-center">
        <span class={labelClass}>Enabled</span>
        <label class="col-span-3 flex items-center gap-2 text-sm">
          <input type="checkbox" bind:checked={fEnabled} />
          <span class="text-dark-text-muted text-xs">Disabled servers close their port; bindings are kept.</span>
        </label>
      </div>
      <div class="flex justify-end gap-2 pt-3 border-t border-dark-border">
        <button type="button" onclick={resetForm} class="px-3 py-1.5 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary">Cancel</button>
        <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-dark-base bg-accent hover:bg-accent-hover disabled:opacity-50">
          <Save size={14} />
          {saving ? 'Saving...' : editingId ? 'Update' : 'Create'}
        </button>
      </div>
    </form>
  </div>
{/if}

{#if loading}
  <div class="text-center py-12 text-dark-text-muted text-sm">Loading webhook servers...</div>
{:else if loadError}
  <div class="border border-dark-border p-4 text-sm text-dark-text-secondary">
    {loadError} <button onclick={load} class="underline underline-offset-2">Retry</button>
  </div>
{:else if servers.length === 0}
  <div class="text-center py-12 border border-dark-border">
    <Server size={24} class="mx-auto mb-2 text-dark-text-muted" />
    <p class="text-sm text-dark-text-muted">No webhook servers</p>
    <p class="text-xs text-dark-text-muted mt-1">{admin ? 'Create one to open a dedicated webhook port, for example :5050' : 'None is open to this workspace yet'}</p>
  </div>
{:else}
  <div class="border border-dark-border overflow-hidden">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-dark-border">
          <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Server</th>
          <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Status</th>
          {#if admin}<th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Workspaces</th>{/if}
          <th class="text-right px-4 py-2.5 w-28"></th>
        </tr>
      </thead>
      <tbody>
        {#each servers as s (s.id)}
          <tr class="border-b border-dark-border last:border-b-0 align-top">
            <td class="px-4 py-2.5">
              <div class="flex items-center gap-1.5">
                <span class="font-medium text-dark-text">{s.name}</span>
                {#if s.tls_cert}<span title="HTTPS"><Lock size={12} class="text-green-400" /></span>{/if}
              </div>
              {#if s.description}<div class="text-xs text-dark-text-muted">{s.description}</div>{/if}
              <div class="flex items-center gap-1 text-[10px] font-mono text-dark-text-muted">
                <span class="truncate">{webhookServerBaseUrl(s)}/</span>
                <button onclick={() => copy(webhookServerBaseUrl(s) + '/')} class="p-0.5 hover:bg-dark-elevated" title="Copy base URL"><Copy size={10} /></button>
              </div>
              {#if admin && (s.allowed_cidrs?.length || s.rate_limit_per_minute)}
                <div class="text-[10px] text-dark-text-muted">
                  {#if s.allowed_cidrs?.length}{s.allowed_cidrs.length} allowed range(s){/if}
                  {#if s.rate_limit_per_minute} · {s.rate_limit_per_minute}/min per client{/if}
                </div>
              {/if}
            </td>
            <td class="px-4 py-2.5">
              <span class={['text-xs', stateClass(s.status?.state)]}>{s.status?.state || 'unknown'}</span>
              {#if s.status?.address}<div class="text-[10px] font-mono text-dark-text-muted">{s.status.address}</div>{/if}
              {#if s.status?.error}<div class="text-[10px] text-red-400 max-w-56 break-words">{s.status.error}</div>{/if}
            </td>
            {#if admin}
              <td class="px-4 py-2.5 text-xs text-dark-text-secondary">
                {#if s.all_workspaces}All{:else if s.workspace_ids.length === 0}<span class="text-dark-text-muted">None</span>{:else}{s.workspace_ids.map(workspaceName).join(', ')}{/if}
              </td>
            {/if}
            <td class="px-4 py-2.5 text-right">
              {#if admin}
                <div class="flex justify-end gap-1">
                  <button onclick={() => openEdit(s)} class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text" title="Edit"><Pencil size={14} /></button>
                  {#if deleteConfirm === s.id}
                    <button onclick={() => handleDelete(s.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-dark-border-subtle hover:bg-dark-elevated">Cancel</button>
                  {:else}
                    <button onclick={() => (deleteConfirm = s.id)} class="p-1.5 hover:bg-red-900/20 text-dark-text-muted hover:text-red-400" title="Delete"><Trash2 size={14} /></button>
                  {/if}
                </div>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
