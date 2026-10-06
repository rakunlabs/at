<script lang="ts">
  import LoadIssues from '@/lib/components/LoadIssues.svelte';
  import ExecutionBinding from '@/lib/components/ExecutionBinding.svelte';
  import WebhookServersPanel from '@/lib/components/WebhookServersPanel.svelte';
  import { createPageLoader } from '@/lib/helper/page-load.svelte';
  const pageLoad = createPageLoader();
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listAllTriggers,
    createTrigger,
    updateTrigger,
    deleteTrigger,
    listWebhookDeliveries,
    type Trigger,
    type WebhookRoute,
    type WebhookSignatureScheme,
    type WebhookDelivery,
  } from '@/lib/api/triggers';
  import { listWebhookServers, webhookServerUrl, type WebhookServer } from '@/lib/api/webhook-servers';
  import { listWorkflows, getWorkflow, type Workflow, type WorkflowNode } from '@/lib/api/workflows';
  import { deploymentUrl } from '@/lib/helper/deployment-url';
  import { isFeatureEnabled } from '@/lib/store/features.svelte';
  import { FEATURE_WEBHOOK_SERVERS } from '@/lib/api/features';
  import {
    Globe,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Copy,
    ShieldCheck,
    ShieldOff,
    Power,
    PowerOff,
    Server,
    EyeOff,
    KeyRound,
    History,
  } from 'lucide-svelte';
  import { formatDate } from '@/lib/helper/format';

  storeNavbar.title = 'Webhooks';

  const METHODS = ['POST', 'PUT', 'PATCH', 'GET', 'DELETE'];

  // ─── State ───

  let tab = $state<'webhooks' | 'servers'>('webhooks');
  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);

  // Reference data
  let workflows = $state<Workflow[]>([]);
  let servers = $state<WebhookServer[]>([]);
  let serversAvailable = $derived(isFeatureEnabled(FEATURE_WEBHOOK_SERVERS));

  // Form
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let saving = $state(false);
  let deleteConfirm = $state<string | null>(null);

  // Form fields
  let formTargetType = $state('workflow');
  let formTargetId = $state('');
  let formEntryNodeId = $state('');
  let formAlias = $state('');
  let formPublic = $state(false);
  let formEnabled = $state(true);
  let formMethods = $state<string[]>(['POST']);
  let formRoutes = $state<WebhookRoute[]>([]);
  let formHideFromMain = $state(false);
  let formSigScheme = $state<WebhookSignatureScheme>('');
  let formSigSecret = $state('');
  let formSigHeader = $state('X-Signature');
  let formSigPrefix = $state('');
  let formSigEncoding = $state<'hex' | 'base64'>('hex');
  let formSigStored = $state(false);
  let formConfig = $state<Record<string, any>>({});

  // Entry node selection
  let inputNodes = $state<WorkflowNode[]>([]);
  let loadingInputNodes = $state(false);

  // Delivery history
  let historyFor = $state<Trigger | null>(null);
  let deliveries = $state<WebhookDelivery[]>([]);
  let loadingDeliveries = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    pageLoad.reset();
    try {
      const jobs = [
        pageLoad.load('Webhooks', () => listAllTriggers({ type: 'http' }), result => { triggers = result || []; }, 'workflow_builder'),
        pageLoad.load('Workflows', () => listWorkflows({ _limit: 1000 }), result => { workflows = result.data || []; }, 'workflow_builder'),
      ];
      if (serversAvailable) {
        jobs.push(pageLoad.load('Webhook servers', () => listWebhookServers(), result => { servers = result || []; }, FEATURE_WEBHOOK_SERVERS));
      }
      await Promise.all(jobs);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load webhooks', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Helpers ───

  function getTargetName(t: Trigger): string {
    if (t.target_type === 'workflow') {
      return workflows.find(w => w.id === t.target_id)?.name || t.target_id;
    }
    return t.target_id;
  }

  function serverById(id: string): WebhookServer | undefined {
    return servers.find(s => s.id === id);
  }

  function mainUrl(t: Pick<Trigger, 'alias' | 'id'>): string {
    // The route is registered under the deployment base path, so the copied
    // URL must carry it too.
    return deploymentUrl(`webhooks/${t.alias || t.id}`);
  }

  function routeUrl(t: Pick<Trigger, 'alias' | 'id'>, r: WebhookRoute): string {
    const srv = serverById(r.server_id);
    if (!srv) return '';
    return webhookServerUrl(srv, r.path || t.alias || t.id);
  }

  /** Every URL the webhook answers on, main route first. */
  function webhookUrls(t: Trigger): { label: string; url: string }[] {
    const urls: { label: string; url: string }[] = [];
    if (!t.hide_from_main) urls.push({ label: 'Main', url: mainUrl(t) });
    for (const r of t.webhook_routes || []) {
      const url = routeUrl(t, r);
      urls.push({ label: serverById(r.server_id)?.name || 'Server', url: url || `(server ${r.server_id.slice(0, 8)}… not visible)` });
    }
    return urls;
  }

  async function copy(url: string) {
    try {
      await navigator.clipboard.writeText(url);
      addToast('Webhook URL copied');
    } catch {
      addToast('Failed to copy URL', 'alert');
    }
  }

  function methodsOf(t: Trigger): string[] {
    const m = Array.isArray(t.config?.methods) ? t.config.methods.map((v: any) => String(v).toUpperCase()) : [];
    return m.length ? m : ['POST'];
  }

  // ─── Form ───

  function resetForm() {
    formTargetType = 'workflow';
    formTargetId = '';
    formEntryNodeId = '';
    formAlias = '';
    formPublic = false;
    formEnabled = true;
    formMethods = ['POST'];
    formRoutes = [];
    formHideFromMain = false;
    formSigScheme = '';
    formSigSecret = '';
    formSigHeader = 'X-Signature';
    formSigPrefix = '';
    formSigEncoding = 'hex';
    formSigStored = false;
    formConfig = {};
    editingId = null;
    showForm = false;
    inputNodes = [];
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  async function openEdit(t: Trigger) {
    resetForm();
    editingId = t.id;
    formTargetType = t.target_type || 'workflow';
    formTargetId = t.target_id;
    formEntryNodeId = t.entry_node_id || '';
    formAlias = t.alias || '';
    formPublic = t.public;
    formEnabled = t.enabled;
    formConfig = { ...(t.config || {}) };
    formMethods = methodsOf(t);
    formRoutes = (t.webhook_routes || []).map(r => ({ ...r }));
    formHideFromMain = !!t.hide_from_main;
    if (t.signature?.scheme) {
      formSigScheme = t.signature.scheme;
      formSigSecret = t.signature.secret || '***';
      formSigHeader = t.signature.header || 'X-Signature';
      formSigPrefix = t.signature.prefix || '';
      formSigEncoding = t.signature.encoding || 'hex';
      formSigStored = true;
    }
    showForm = true;
    if (formTargetType === 'workflow' && formTargetId) {
      await loadInputNodes(formTargetId);
    }
  }

  async function loadInputNodes(workflowId: string) {
    if (!workflowId) {
      inputNodes = [];
      return;
    }
    loadingInputNodes = true;
    try {
      const wf = await getWorkflow(workflowId);
      inputNodes = (wf.graph?.nodes || []).filter((n: WorkflowNode) => n.type === 'input');
    } catch {
      inputNodes = [];
    } finally {
      loadingInputNodes = false;
    }
  }

  async function handleTargetIdChange(newId: string) {
    formTargetId = newId;
    formEntryNodeId = '';
    if (formTargetType === 'workflow') {
      await loadInputNodes(newId);
    }
  }

  function toggleMethod(m: string) {
    formMethods = formMethods.includes(m) ? formMethods.filter(x => x !== m) : [...formMethods, m];
  }

  function toggleServer(id: string) {
    if (formRoutes.some(r => r.server_id === id)) {
      formRoutes = formRoutes.filter(r => r.server_id !== id);
      if (formRoutes.length === 0) formHideFromMain = false;
    } else {
      formRoutes = [...formRoutes, { server_id: id, path: '' }];
    }
  }

  function setRoutePath(id: string, path: string) {
    formRoutes = formRoutes.map(r => (r.server_id === id ? { ...r, path } : r));
  }

  function previewTrigger(): Pick<Trigger, 'alias' | 'id'> {
    return { alias: formAlias.trim(), id: editingId || '<id>' };
  }

  async function handleSubmit() {
    if (!formTargetId.trim()) {
      addToast('Target is required', 'warn');
      return;
    }
    if (formMethods.length === 0) {
      addToast('Select at least one HTTP method', 'warn');
      return;
    }
    if (formSigScheme && !formSigSecret.trim()) {
      addToast('Signature secret is required', 'warn');
      return;
    }

    saving = true;
    try {
      const config: Record<string, any> = { ...formConfig };
      if (formMethods.length === 1 && formMethods[0] === 'POST') delete config.methods;
      else config.methods = formMethods;
      const payload: Partial<Trigger> = {
        type: 'http',
        target_type: formTargetType,
        target_id: formTargetId,
        entry_node_id: formEntryNodeId || undefined,
        alias: formAlias.trim() || undefined,
        public: formPublic,
        enabled: formEnabled,
        config,
        webhook_routes: formRoutes.map(r => ({ server_id: r.server_id, path: r.path.trim() })),
        hide_from_main: formRoutes.length > 0 && formHideFromMain,
        signature: formSigScheme
          ? {
              scheme: formSigScheme,
              secret: formSigSecret,
              header: formSigScheme === 'hmac_sha256' ? formSigHeader.trim() : undefined,
              prefix: formSigScheme === 'hmac_sha256' ? formSigPrefix.trim() : undefined,
              encoding: formSigScheme === 'hmac_sha256' ? formSigEncoding : undefined,
            }
          : { scheme: '' },
      };

      if (editingId) {
        await updateTrigger(editingId, payload);
        addToast('Webhook updated');
        resetForm();
      } else {
        const created = await createTrigger(payload);
        addToast('Webhook created. Bind an execution identity so it can run.');
        resetForm();
        await openEdit(created);
      }
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save webhook', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteTrigger(id);
      addToast('Webhook deleted');
      deleteConfirm = null;
      if (historyFor?.id === id) historyFor = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete webhook', 'alert');
    }
  }

  async function toggleEnabled(t: Trigger) {
    try {
      // Routes and signature are omitted, which preserves them.
      await updateTrigger(t.id, { enabled: !t.enabled, type: t.type, target_type: t.target_type, target_id: t.target_id, entry_node_id: t.entry_node_id, alias: t.alias, public: t.public, config: t.config });
      addToast(t.enabled ? 'Webhook disabled' : 'Webhook enabled');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update webhook', 'alert');
    }
  }

  async function openHistory(t: Trigger) {
    historyFor = t;
    await loadHistory();
  }

  async function loadHistory() {
    if (!historyFor) return;
    loadingDeliveries = true;
    try {
      deliveries = await listWebhookDeliveries(historyFor.id);
    } catch (e: any) {
      deliveries = [];
      addToast(e?.response?.data?.message || 'Failed to load deliveries', 'alert');
    } finally {
      loadingDeliveries = false;
    }
  }

  function statusClass(code: number): string {
    if (code >= 500) return 'text-red-400';
    if (code >= 400) return 'text-amber-400';
    return 'text-green-400';
  }

  const inputClass = 'w-full border border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted';
  const toggleTrack = "w-9 h-5 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-accent/20 rounded-full peer bg-dark-elevated peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-dark-border after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-dark-surface after:border after:rounded-full after:h-4 after:w-4 after:border-dark-border-subtle peer-checked:bg-accent";
</script>

<svelte:head>
  <title>AT | Webhooks</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto">
  <LoadIssues issues={pageLoad.issues} retry={load} {loading} />

  {#if serversAvailable}
    <div class="flex gap-1 mb-4 border-b border-dark-border" role="tablist">
      <button role="tab" aria-selected={tab === 'webhooks'} onclick={() => (tab = 'webhooks')} class={['flex items-center gap-1.5 px-3 py-1.5 text-sm -mb-px border-b-2', tab === 'webhooks' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}>
        <Globe size={14} /> Webhooks
      </button>
      <button role="tab" aria-selected={tab === 'servers'} onclick={() => (tab = 'servers')} class={['flex items-center gap-1.5 px-3 py-1.5 text-sm -mb-px border-b-2', tab === 'servers' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}>
        <Server size={14} /> Servers
      </button>
    </div>
  {/if}

  {#if tab === 'servers' && serversAvailable}
    <WebhookServersPanel onchange={load} />
  {:else}
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Globe size={16} class="text-dark-text-muted" />
      <h2 class="text-sm font-medium text-dark-text">Webhooks</h2>
      <span class="text-xs text-dark-text-muted">({triggers.length})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={load}
        class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-dark-base bg-accent hover:bg-accent-hover"
      >
        <Plus size={12} />
        New Webhook
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-dark-border mb-6 bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border bg-dark-base">
        <span class="text-sm font-medium text-dark-text">
          {editingId ? 'Edit Webhook' : 'New Webhook'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Target Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target-type" class="text-sm font-medium text-dark-text-secondary">Target Type</label>
          <select
            id="form-target-type"
            bind:value={formTargetType}
            onchange={() => { formTargetId = ''; formEntryNodeId = ''; inputNodes = []; }}
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
          >
            <option value="workflow">Workflow</option>
          </select>
        </div>

        <!-- Target -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target" class="text-sm font-medium text-dark-text-secondary">Target</label>
          <select
            id="form-target"
            value={formTargetId}
            onchange={(e) => handleTargetIdChange((e.target as HTMLSelectElement).value)}
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
          >
            <option value="">Select target...</option>
            {#each workflows as w}
              <option value={w.id}>{w.name}</option>
            {/each}
          </select>
        </div>

        <!-- Entry Node (only for workflow) -->
        {#if formTargetType === 'workflow' && inputNodes.length > 1}
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-entry-node" class="text-sm font-medium text-dark-text-secondary">Entry Node</label>
            <div class="col-span-3">
              <select
                id="form-entry-node"
                bind:value={formEntryNodeId}
                disabled={loadingInputNodes}
                class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
              >
                <option value="">All input nodes (default)</option>
                {#each inputNodes as node}
                  <option value={node.id}>{node.data?.label || 'Input'} ({node.id.slice(0, 8)}...)</option>
                {/each}
              </select>
              <div class="text-xs text-dark-text-muted mt-1">
                Select a specific input node or leave empty to run all input nodes.
              </div>
            </div>
          </div>
        {/if}

        <!-- Alias -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-alias" class="text-sm font-medium text-dark-text-secondary">Alias</label>
          <div class="col-span-3">
            <input id="form-alias" type="text" bind:value={formAlias} placeholder="e.g., order-created (optional)" class={[inputClass, 'font-mono']} />
            <div class="text-xs text-dark-text-muted mt-1">
              Human-friendly URL slug, unique across the installation.
            </div>
          </div>
        </div>

        <!-- Methods -->
        <div class="grid grid-cols-4 gap-3 items-start">
          <span class="text-sm font-medium text-dark-text-secondary pt-1">Methods</span>
          <div class="col-span-3">
            <div class="flex flex-wrap gap-1.5">
              {#each METHODS as m}
                <button
                  type="button"
                  onclick={() => toggleMethod(m)}
                  aria-pressed={formMethods.includes(m)}
                  class={['px-2 py-1 text-xs font-mono border', formMethods.includes(m) ? 'text-dark-base bg-accent border-accent' : 'border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated']}
                >{m}</button>
              {/each}
            </div>
            <div class="text-xs text-dark-text-muted mt-1">
              Accepted on webhook servers. The main route always accepts POST.
            </div>
          </div>
        </div>

        <!-- Public -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-dark-text-secondary">Public</span>
          <div class="col-span-3 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input type="checkbox" bind:checked={formPublic} class="sr-only peer" />
              <div class={toggleTrack}></div>
            </label>
            <span class="text-xs text-dark-text-muted">
              {formPublic ? (formSigScheme ? 'No token; the signature proves the sender' : 'No authentication required') : 'Requires Bearer token'}
            </span>
          </div>
        </div>

        <!-- Enabled -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-dark-text-secondary">Enabled</span>
          <div class="col-span-3 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input type="checkbox" bind:checked={formEnabled} class="sr-only peer" />
              <div class={toggleTrack}></div>
            </label>
          </div>
        </div>

        <!-- Signature -->
        <div class="grid grid-cols-4 gap-3 items-start border-t border-dark-border pt-4">
          <label for="form-sig" class="text-sm font-medium text-dark-text-secondary pt-1.5">Signature</label>
          <div class="col-span-3 space-y-2">
            <select id="form-sig" bind:value={formSigScheme} class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text">
              <option value="">No signature check</option>
              <option value="github">GitHub (X-Hub-Signature-256)</option>
              <option value="stripe">Stripe (Stripe-Signature, 5 min tolerance)</option>
              <option value="hmac_sha256">Custom HMAC-SHA256</option>
            </select>
            {#if formSigScheme}
              <input
                type="password"
                autocomplete="new-password"
                bind:value={formSigSecret}
                onfocus={() => { if (formSigSecret === '***') formSigSecret = ''; }}
                onblur={() => { if (!formSigSecret && formSigStored) formSigSecret = '***'; }}
                placeholder="Signing secret"
                aria-label="Signing secret"
                class={[inputClass, 'font-mono']}
              />
              {#if formSigScheme === 'hmac_sha256'}
                <div class="grid grid-cols-3 gap-2">
                  <input bind:value={formSigHeader} placeholder="Header" aria-label="Signature header" class={[inputClass, 'font-mono']} />
                  <input bind:value={formSigPrefix} placeholder="Prefix (e.g. sha256=)" aria-label="Signature prefix" class={[inputClass, 'font-mono']} />
                  <select bind:value={formSigEncoding} aria-label="Signature encoding" class="border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text">
                    <option value="hex">hex</option>
                    <option value="base64">base64</option>
                  </select>
                </div>
              {/if}
              <div class="text-xs text-dark-text-muted">
                HMAC-SHA256 over the raw request body. Requests without a valid signature are refused before the workflow runs.
                {#if formSigStored}Leave *** to keep the stored secret.{/if}
              </div>
            {/if}
          </div>
        </div>

        <!-- Webhook servers -->
        {#if serversAvailable}
          <div class="grid grid-cols-4 gap-3 items-start border-t border-dark-border pt-4">
            <span class="text-sm font-medium text-dark-text-secondary pt-1">Servers</span>
            <div class="col-span-3 space-y-2">
              {#if servers.length === 0}
                <p class="text-xs text-dark-text-muted">
                  No webhook server is open to this workspace. An installation administrator can add one under the Servers tab.
                </p>
              {:else}
                {#each servers as srv (srv.id)}
                  {@const route = formRoutes.find(r => r.server_id === srv.id)}
                  <div class="border border-dark-border">
                    <label class="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer">
                      <input type="checkbox" checked={!!route} onchange={() => toggleServer(srv.id)} />
                      <span class="font-medium text-dark-text">{srv.name}</span>
                      <span class="text-xs font-mono text-dark-text-muted">:{srv.port}{srv.base_path}</span>
                      {#if !srv.enabled}<span class="text-xs text-amber-400">disabled</span>{/if}
                    </label>
                    {#if route}
                      <div class="px-3 pb-2 space-y-1">
                        <input
                          value={route.path}
                          oninput={(e) => setRoutePath(srv.id, (e.target as HTMLInputElement).value)}
                          placeholder="Custom path (optional), e.g. github/push"
                          aria-label={`Custom path on ${srv.name}`}
                          class={[inputClass, 'font-mono text-xs']}
                        />
                        <div class="text-[11px] font-mono text-dark-text-muted break-all">
                          {webhookServerUrl(srv, route.path || formAlias.trim() || editingId || '<id>')}
                        </div>
                      </div>
                    {/if}
                  </div>
                {/each}
              {/if}
              <label class={['flex items-center gap-2 text-sm', formRoutes.length === 0 ? 'opacity-50' : '']}>
                <input type="checkbox" bind:checked={formHideFromMain} disabled={formRoutes.length === 0} />
                <span class="text-dark-text-secondary">Hide from the main server</span>
              </label>
              <div class="text-xs text-dark-text-muted break-all">
                {#if formRoutes.length > 0 && formHideFromMain}
                  Only reachable on the selected servers. {mainUrl(previewTrigger())} answers 404.
                {:else}
                  Also reachable at {mainUrl(previewTrigger())}
                {/if}
              </div>
            </div>
          </div>
        {/if}

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-dark-border">
          <button type="button" onclick={resetForm} class="px-3 py-1.5 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary">
            Cancel
          </button>
          <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-dark-base bg-accent hover:bg-accent-hover disabled:opacity-50">
            <Save size={14} />
            {saving ? 'Saving...' : editingId ? 'Update' : 'Create'}
          </button>
        </div>
      </form>

      {#if editingId}
        <div class="px-4 pb-4">
          <ExecutionBinding kind="trigger" subjectId={editingId} />
        </div>
      {/if}
    </div>
  {/if}

  <!-- Delivery history -->
  {#if historyFor}
    <div class="border border-dark-border mb-6 bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border bg-dark-base">
        <span class="text-sm font-medium text-dark-text">Recent deliveries · {historyFor.alias || historyFor.id.slice(0, 12)}</span>
        <div class="flex items-center gap-1">
          <button onclick={loadHistory} title="Refresh" class="p-1 hover:bg-dark-elevated text-dark-text-muted"><RefreshCw size={14} /></button>
          <button onclick={() => (historyFor = null)} title="Close" class="p-1 hover:bg-dark-elevated text-dark-text-muted"><X size={14} /></button>
        </div>
      </div>
      {#if loadingDeliveries}
        <p class="p-4 text-sm text-dark-text-muted">Loading deliveries…</p>
      {:else if deliveries.length === 0}
        <p class="p-4 text-sm text-dark-text-muted">No requests received yet. The latest 100 are kept.</p>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead>
              <tr class="border-b border-dark-border text-dark-text-muted uppercase tracking-wider">
                <th class="text-left px-4 py-2 font-medium">Time</th>
                <th class="text-left px-4 py-2 font-medium">Status</th>
                <th class="text-left px-4 py-2 font-medium">Request</th>
                <th class="text-left px-4 py-2 font-medium">Server</th>
                <th class="text-left px-4 py-2 font-medium">Client</th>
                <th class="text-left px-4 py-2 font-medium">Detail</th>
              </tr>
            </thead>
            <tbody>
              {#each deliveries as d (d.id)}
                <tr class="border-b border-dark-border last:border-b-0 align-top">
                  <td class="px-4 py-1.5 whitespace-nowrap text-dark-text-secondary">{formatDate(d.created_at)}</td>
                  <td class={['px-4 py-1.5 font-mono', statusClass(d.status)]}>{d.status}</td>
                  <td class="px-4 py-1.5 font-mono text-dark-text-secondary">{d.method} {d.path} <span class="text-dark-text-muted">· {d.body_bytes} B · {d.duration_ms} ms</span></td>
                  <td class="px-4 py-1.5 text-dark-text-secondary">{d.server_id ? serverById(d.server_id)?.name || d.server_id.slice(0, 8) : 'Main'}</td>
                  <td class="px-4 py-1.5 font-mono text-dark-text-muted">{d.client_ip || '—'}</td>
                  <td class="px-4 py-1.5 text-dark-text-secondary break-all">
                    {#if d.error}{d.error}{:else if d.run_id}<span class="font-mono">{d.run_id}</span>{/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  {/if}

  <!-- List -->
  {#if pageLoad.loading('Webhooks')}
    <div class="text-center py-12 text-dark-text-muted text-sm">Loading webhooks...</div>
  {:else if pageLoad.error('Webhooks') && !triggers.length}
    <p class="text-sm text-dark-text-secondary">Webhooks could not be loaded. Retry above.</p>
  {:else if triggers.length === 0}
    <div class="text-center py-12 border border-dark-border bg-dark-surface">
      <Globe size={24} class="mx-auto mb-2 text-dark-text-muted" />
      <p class="text-sm text-dark-text-muted">No webhooks configured</p>
      <p class="text-xs text-dark-text-muted mt-1">Create a webhook to trigger workflows via HTTP</p>
    </div>
  {:else}
    <div class="border border-dark-border bg-dark-surface overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-dark-border bg-dark-base">
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Webhook</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Target</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Auth</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
            <th class="text-right px-4 py-2.5 font-medium text-dark-text-muted text-xs uppercase tracking-wider w-36"></th>
          </tr>
        </thead>
        <tbody>
          {#each triggers as t (t.id)}
            <tr class="border-b border-dark-border last:border-b-0 hover:bg-dark-elevated/50 align-top">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2">
                  {#if t.alias}
                    <span class="font-medium text-dark-text">{t.alias}</span>
                  {:else}
                    <span class="font-mono text-xs text-dark-text-muted">{t.id.slice(0, 12)}...</span>
                  {/if}
                  {#if t.hide_from_main}
                    <span class="inline-flex items-center gap-0.5 text-[10px] text-dark-text-muted" title="Not reachable on the main server"><EyeOff size={10} /> servers only</span>
                  {/if}
                </div>
                {#each webhookUrls(t) as u}
                  <div class="flex items-center gap-1 text-[10px] font-mono text-dark-text-muted max-w-md">
                    <span class="shrink-0 px-1 bg-dark-elevated text-dark-text-secondary">{u.label}</span>
                    <span class="truncate" title={u.url}>{methodsOf(t).join('/')} {u.url}</span>
                    <button onclick={() => copy(u.url)} class="p-0.5 shrink-0 hover:bg-dark-elevated hover:text-dark-text-secondary" title="Copy URL">
                      <Copy size={10} />
                    </button>
                  </div>
                {/each}
              </td>
              <td class="px-4 py-2.5">
                <span class="text-xs px-1.5 py-0.5 bg-dark-elevated text-dark-text-secondary">
                  Workflow
                </span>
                <span class="ml-1.5 text-xs text-dark-text-secondary">{getTargetName(t)}</span>
              </td>
              <td class="px-4 py-2.5 space-y-0.5">
                {#if t.public}
                  <span class="flex items-center gap-1 text-xs text-amber-400">
                    <ShieldOff size={12} />
                    Public
                  </span>
                {:else}
                  <span class="flex items-center gap-1 text-xs text-green-400">
                    <ShieldCheck size={12} />
                    Token
                  </span>
                {/if}
                {#if t.signature?.scheme}
                  <span class="flex items-center gap-1 text-xs text-green-400" title="Requests must carry a valid HMAC signature">
                    <KeyRound size={12} />
                    {t.signature.scheme === 'hmac_sha256' ? 'HMAC' : t.signature.scheme === 'github' ? 'GitHub' : 'Stripe'}
                  </span>
                {/if}
              </td>
              <td class="px-4 py-2.5">
                <button
                  onclick={() => toggleEnabled(t)}
                  class={['inline-flex items-center gap-1 text-xs', t.enabled ? 'text-green-400' : 'text-dark-text-muted']}
                  title={t.enabled ? 'Click to disable' : 'Click to enable'}
                >
                  {#if t.enabled}
                    <Power size={12} />
                    Active
                  {:else}
                    <PowerOff size={12} />
                    Disabled
                  {/if}
                </button>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => openHistory(t)}
                    class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text"
                    title="Recent deliveries"
                  >
                    <History size={14} />
                  </button>
                  <button
                    onclick={() => openEdit(t)}
                    class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === t.id}
                    <button onclick={() => handleDelete(t.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-dark-border-subtle hover:bg-dark-elevated">Cancel</button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = t.id)}
                      class="p-1.5 hover:bg-red-900/20 text-dark-text-muted hover:text-red-400"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  {/if}
</div>
