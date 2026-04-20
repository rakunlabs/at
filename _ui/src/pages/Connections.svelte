<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listConnections,
    createConnection,
    updateConnection,
    deleteConnection,
    importConnectionsFromVariables,
    getOAuthStartURLForConnection,
    getManualAuthURL,
    exchangeCode,
    type Connection,
  } from '@/lib/api/connections';
  import {
    Plug,
    Unplug,
    RefreshCw,
    CheckCircle2,
    XCircle,
    AlertCircle,
    Plus,
    Pencil,
    Trash2,
    Download,
    ExternalLink,
    ClipboardPaste,
    Eye,
    EyeOff,
    Users,
    X,
  } from 'lucide-svelte';

  storeNavbar.title = 'Connections';

  // ─── Provider catalog ───
  // Providers known to support OAuth. Other providers shown in the UI are
  // inferred from the live list of connections.
  const OAUTH_PROVIDERS: Record<string, { name: string; description: string }> = {
    youtube: { name: 'YouTube', description: 'Upload and publish videos to YouTube' },
    google: { name: 'Google', description: 'Access Gmail and Google Calendar' },
  };

  // ─── State ───
  let connections = $state<Connection[]>([]);
  let loading = $state(true);
  let saving = $state(false);

  // Modal state: either creating a new connection or editing an existing one.
  type EditorMode =
    | { kind: 'create'; provider: string }
    | { kind: 'edit'; connection: Connection };
  let editor = $state<EditorMode | null>(null);

  // Form fields for the editor modal.
  let formName = $state('');
  let formDescription = $state('');
  let formClientID = $state('');
  let formClientSecret = $state('');
  let formRefreshToken = $state('');
  let formAPIKey = $state('');
  let showSecrets = $state(false);

  // Manual OAuth flow state (per connection ID).
  type OAuthStep = 'authorize' | 'paste-code';
  let oauthStep = $state<Record<string, OAuthStep>>({});
  let oauthAuthURL = $state<Record<string, string>>({});
  let oauthRedirectURI = $state<Record<string, string>>({});
  let oauthCode = $state<Record<string, string>>({});

  // ─── Load ───
  async function load() {
    loading = true;
    try {
      connections = await listConnections();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load connections', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Derived: group by provider ───
  const byProvider = $derived(() => {
    const map = new Map<string, Connection[]>();
    for (const c of connections) {
      const arr = map.get(c.provider) ?? [];
      arr.push(c);
      map.set(c.provider, arr);
    }
    // Ensure all OAuth providers are represented so empty providers still
    // render an "Add account" card.
    for (const p of Object.keys(OAUTH_PROVIDERS)) {
      if (!map.has(p)) map.set(p, []);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  });

  function providerLabel(provider: string): string {
    return OAUTH_PROVIDERS[provider]?.name ?? provider;
  }

  function providerDescription(provider: string): string {
    return OAUTH_PROVIDERS[provider]?.description ?? '';
  }

  function isOAuthProvider(provider: string): boolean {
    return provider in OAUTH_PROVIDERS;
  }

  // ─── Editor modal ───
  function openCreate(provider: string) {
    editor = { kind: 'create', provider };
    formName = '';
    formDescription = '';
    formClientID = '';
    formClientSecret = '';
    formRefreshToken = '';
    formAPIKey = '';
    showSecrets = false;
  }

  function openEdit(c: Connection) {
    editor = { kind: 'edit', connection: c };
    formName = c.name;
    formDescription = c.description ?? '';
    formClientID = c.credentials.client_id ?? '';
    formClientSecret = '';
    formRefreshToken = '';
    formAPIKey = '';
    showSecrets = false;
  }

  function closeEditor() {
    editor = null;
  }

  async function saveEditor() {
    if (!editor) return;
    const provider = editor.kind === 'create' ? editor.provider : editor.connection.provider;
    if (!formName.trim()) {
      addToast('Name is required', 'warn');
      return;
    }
    saving = true;
    try {
      const credentials: Record<string, string> = {};
      if (formClientID.trim()) credentials.client_id = formClientID.trim();
      if (formClientSecret.trim()) credentials.client_secret = formClientSecret.trim();
      if (formRefreshToken.trim()) credentials.refresh_token = formRefreshToken.trim();
      if (formAPIKey.trim()) credentials.api_key = formAPIKey.trim();

      if (editor.kind === 'create') {
        await createConnection({
          provider,
          name: formName.trim(),
          description: formDescription.trim(),
          credentials,
        });
        addToast(`${providerLabel(provider)} account "${formName.trim()}" created`, 'info');
      } else {
        await updateConnection(editor.connection.id, {
          provider,
          name: formName.trim(),
          description: formDescription.trim(),
          credentials,
        });
        addToast(`${providerLabel(provider)} account "${formName.trim()}" updated`, 'info');
      }
      closeEditor();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save connection', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Delete ───
  async function remove(c: Connection) {
    if (!confirm(`Delete connection "${c.name}"?`)) return;
    try {
      const result = await deleteConnection(c.id);
      if (result?.error && result.used_by_agents?.length) {
        const names = result.used_by_agents.map((a) => a.name).join(', ');
        if (!confirm(`This connection is used by ${result.used_by_agents.length} agent(s): ${names}\n\nForce-delete and detach from all agents?`)) {
          return;
        }
        const forceResult = await deleteConnection(c.id, true);
        if (forceResult?.status === 'deleted') {
          addToast(`Deleted. Detached from ${forceResult.detached_from_agents ?? 0} agent(s).`, 'info');
          await load();
        } else {
          addToast(forceResult?.error || 'Force-delete failed', 'alert');
        }
        return;
      }
      addToast(`Connection "${c.name}" deleted`, 'info');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete connection', 'alert');
    }
  }

  // ─── OAuth: popup flow ───
  function startPopupOAuth(c: Connection) {
    const url = getOAuthStartURLForConnection(c.id, c.provider);
    const w = 500;
    const h = 650;
    const left = window.screenX + (window.outerWidth - w) / 2;
    const top = window.screenY + (window.outerHeight - h) / 2;
    const popup = window.open(
      url,
      'oauth-connect',
      `width=${w},height=${h},left=${left},top=${top},toolbar=yes,menubar=yes,scrollbars=yes,resizable=yes`,
    );

    function handleMessage(event: MessageEvent) {
      if (event.data?.type === 'oauth-result') {
        window.removeEventListener('message', handleMessage);
        if (event.data.status === 'success') {
          addToast(`${c.name} connected successfully!`, 'info');
          load();
        } else {
          addToast('Connection failed. Try the manual method.', 'alert');
        }
      }
    }
    window.addEventListener('message', handleMessage);

    const pollInterval = setInterval(() => {
      if (popup && popup.closed) {
        clearInterval(pollInterval);
        window.removeEventListener('message', handleMessage);
        setTimeout(() => load(), 1000);
      }
    }, 500);
  }

  // ─── OAuth: manual paste-code flow ───
  async function startManualOAuth(c: Connection) {
    try {
      const result = await getManualAuthURL(c.provider, c.id);
      oauthAuthURL[c.id] = result.url;
      oauthRedirectURI[c.id] = result.redirect_uri;
      oauthCode[c.id] = '';
      oauthStep[c.id] = 'authorize';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to get auth URL', 'alert');
    }
  }

  async function submitOAuthCode(c: Connection) {
    const code = oauthCode[c.id]?.trim();
    if (!code) {
      addToast('Please paste the authorization code', 'warn');
      return;
    }
    saving = true;
    try {
      const result = await exchangeCode(c.provider, code, oauthRedirectURI[c.id], c.id);
      addToast(result.message || `${c.name} connected!`, 'info');
      oauthCode[c.id] = '';
      delete oauthStep[c.id];
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to exchange code', 'alert');
    } finally {
      saving = false;
    }
  }

  function cancelManualOAuth(c: Connection) {
    delete oauthStep[c.id];
    delete oauthAuthURL[c.id];
    delete oauthRedirectURI[c.id];
    delete oauthCode[c.id];
  }

  // ─── Import ───
  async function runImport() {
    try {
      const result = await importConnectionsFromVariables();
      const created = result.created?.length ?? 0;
      const skipped = result.skipped?.length ?? 0;
      if (created > 0) {
        addToast(`Imported ${created} connection${created === 1 ? '' : 's'} from existing variables`, 'info');
      } else if (skipped > 0) {
        addToast('Nothing new to import', 'warn');
      } else {
        addToast('No existing OAuth variables found', 'warn');
      }
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Import failed', 'alert');
    }
  }

  // ─── Status helpers ───
  function isConnected(c: Connection): boolean {
    return !!c.credentials.refresh_token_set;
  }

  function isSetupComplete(c: Connection): boolean {
    return !!c.credentials.client_id && !!c.credentials.client_secret_set;
  }
</script>

<div class="p-4 max-w-4xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Connections</h1>
      <p class="text-sm text-gray-500 dark:text-dark-text-muted mt-0.5">
        Named external-service accounts. Multiple accounts per provider are supported — agents reference them by ID.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={runImport}
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
        title="Import from existing global variables (youtube_client_id, etc.)"
      >
        <Download size={14} />
        Import from variables
      </button>
      <button
        onclick={() => load()}
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
      >
        <RefreshCw size={14} />
        Refresh
      </button>
    </div>
  </div>

  {#if loading}
    <div class="text-sm text-gray-500 dark:text-dark-text-muted p-8 text-center">Loading connections…</div>
  {:else}
    {#each byProvider() as [provider, items] (provider)}
      <section class="mb-6">
        <div class="flex items-center justify-between mb-3">
          <div>
            <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {providerLabel(provider)}
            </h2>
            {#if providerDescription(provider)}
              <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">
                {providerDescription(provider)}
              </p>
            {/if}
          </div>
          <button
            onclick={() => openCreate(provider)}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
          >
            <Plus size={12} />
            Add {providerLabel(provider)} account
          </button>
        </div>

        {#if items.length === 0}
          <div class="text-xs text-gray-500 dark:text-dark-text-muted italic px-3 py-4 border border-dashed border-gray-200 dark:border-dark-border rounded">
            No {providerLabel(provider)} accounts yet. Click "Add" above to create one.
          </div>
        {:else}
          <div class="space-y-2">
            {#each items as c (c.id)}
              <div class="border border-gray-200 dark:border-dark-border rounded-lg bg-white dark:bg-dark-surface">
                <div class="flex items-start justify-between p-3">
                  <div class="flex items-start gap-3 min-w-0">
                    <div class={[
                      'mt-0.5 w-8 h-8 rounded-lg flex items-center justify-center shrink-0',
                      isConnected(c)
                        ? 'bg-green-50 dark:bg-green-900/20'
                        : 'bg-gray-50 dark:bg-dark-elevated',
                    ]}>
                      {#if isConnected(c)}
                        <CheckCircle2 size={18} class="text-green-600 dark:text-green-400" />
                      {:else}
                        <XCircle size={18} class="text-gray-400 dark:text-dark-text-muted" />
                      {/if}
                    </div>
                    <div class="min-w-0">
                      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">{c.name}</h3>
                      {#if c.account_label}
                        <p class="text-xs text-gray-600 dark:text-dark-text-secondary mt-0.5 truncate">
                          {c.account_label}
                        </p>
                      {/if}
                      {#if c.description}
                        <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{c.description}</p>
                      {/if}
                      <div class="mt-2 flex flex-wrap items-center gap-2">
                        {#if isConnected(c)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 rounded-full">
                            <CheckCircle2 size={10} />
                            Connected
                          </span>
                        {:else if isSetupComplete(c)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-yellow-50 dark:bg-yellow-900/20 text-yellow-700 dark:text-yellow-400 rounded-full">
                            <AlertCircle size={10} />
                            Ready to connect
                          </span>
                        {:else}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted rounded-full">
                            <XCircle size={10} />
                            Not configured
                          </span>
                        {/if}
                        {#if c.used_by_agents && c.used_by_agents.length > 0}
                          <span
                            class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-400 rounded-full"
                            title={c.used_by_agents.map((a) => a.name).join(', ')}
                          >
                            <Users size={10} />
                            {c.used_by_agents.length} agent{c.used_by_agents.length === 1 ? '' : 's'}
                          </span>
                        {/if}
                      </div>
                    </div>
                  </div>
                  <div class="flex items-center gap-1 shrink-0">
                    {#if isOAuthProvider(provider) && isSetupComplete(c) && !isConnected(c)}
                      <button
                        onclick={() => startPopupOAuth(c)}
                        class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
                      >
                        <Plug size={12} />
                        Connect
                      </button>
                    {/if}
                    {#if isOAuthProvider(provider) && isConnected(c)}
                      <button
                        onclick={() => startPopupOAuth(c)}
                        class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                        title="Re-authorize"
                      >
                        <RefreshCw size={12} />
                      </button>
                    {/if}
                    <button
                      onclick={() => openEdit(c)}
                      class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                      title="Edit"
                    >
                      <Pencil size={12} />
                    </button>
                    <button
                      onclick={() => remove(c)}
                      class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 rounded transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={12} />
                    </button>
                  </div>
                </div>

                <!-- Manual OAuth flow panel -->
                {#if isOAuthProvider(provider) && oauthStep[c.id]}
                  <div class="border-t border-gray-100 dark:border-dark-border p-3 bg-gray-50/50 dark:bg-dark-base/50 rounded-b-lg">
                    {#if oauthStep[c.id] === 'authorize'}
                      <div class="space-y-2">
                        <p class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">
                          Open the link below, sign in, and authorize. Then paste the code here.
                        </p>
                        <div class="flex items-center gap-2">
                          <a
                            href={oauthAuthURL[c.id]}
                            target="_blank"
                            rel="noopener"
                            class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
                          >
                            <ExternalLink size={12} />
                            Open authorization
                          </a>
                          <button
                            onclick={() => (oauthStep[c.id] = 'paste-code')}
                            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                          >
                            <ClipboardPaste size={12} />
                            I have the code
                          </button>
                          <button
                            onclick={() => cancelManualOAuth(c)}
                            class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary"
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    {:else if oauthStep[c.id] === 'paste-code'}
                      <div class="space-y-2">
                        <p class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Paste the authorization code:</p>
                        <div class="flex items-center gap-2">
                          <input
                            type="text"
                            value={oauthCode[c.id] ?? ''}
                            oninput={(e) => (oauthCode[c.id] = (e.target as HTMLInputElement).value)}
                            placeholder="Paste code here"
                            class="flex-1 px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
                          />
                          <button
                            onclick={() => submitOAuthCode(c)}
                            disabled={saving}
                            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
                          >
                            <Plug size={12} />
                            {saving ? 'Connecting…' : 'Connect'}
                          </button>
                          <button
                            onclick={() => cancelManualOAuth(c)}
                            class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary"
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    {/if}
                  </div>
                {:else if isOAuthProvider(provider) && isSetupComplete(c)}
                  <div class="border-t border-gray-100 dark:border-dark-border px-3 py-2">
                    <button
                      onclick={() => startManualOAuth(c)}
                      class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-blue-600 dark:hover:text-blue-400 transition-colors"
                    >
                      Popup blocked? Use manual connection
                    </button>
                  </div>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      </section>
    {/each}
  {/if}
</div>

<!-- Editor modal -->
{#if editor}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40">
    <div class="bg-white dark:bg-dark-surface rounded-lg shadow-lg max-w-md w-full">
      <div class="flex items-center justify-between p-4 border-b border-gray-200 dark:border-dark-border">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">
          {editor.kind === 'create'
            ? `Add ${providerLabel(editor.provider)} account`
            : `Edit ${providerLabel(editor.connection.provider)} account`}
        </h2>
        <button
          onclick={closeEditor}
          class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"
        >
          <X size={16} />
        </button>
      </div>

      <div class="p-4 space-y-3">
        <div>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">
            Name <span class="text-red-500">*</span>
          </label>
          <input
            type="text"
            bind:value={formName}
            placeholder="e.g. Main Channel"
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          />
        </div>

        <div>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">
            Description
          </label>
          <input
            type="text"
            bind:value={formDescription}
            placeholder="Optional note for future-you"
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          />
        </div>

        <div class="pt-2 border-t border-gray-100 dark:border-dark-border">
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">OAuth2 credentials</h3>
            <button
              type="button"
              onclick={() => (showSecrets = !showSecrets)}
              class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"
              title={showSecrets ? 'Hide' : 'Show'}
            >
              {#if showSecrets}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
            </button>
          </div>

          <div class="space-y-2">
            <div>
              <label class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">Client ID</label>
              <input
                type="text"
                bind:value={formClientID}
                placeholder={editor.kind === 'edit' && editor.connection.credentials.client_id
                  ? editor.connection.credentials.client_id
                  : 'Paste from Google Cloud Console'}
                class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
              />
            </div>
            <div>
              <label class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">
                Client Secret
                {#if editor.kind === 'edit' && editor.connection.credentials.client_secret_set}
                  <span class="text-green-600 dark:text-green-400 font-normal ml-1">(stored)</span>
                {/if}
              </label>
              <input
                type={showSecrets ? 'text' : 'password'}
                bind:value={formClientSecret}
                placeholder={editor.kind === 'edit' && editor.connection.credentials.client_secret_set
                  ? '(leave blank to keep stored value)'
                  : 'GOCSPX-...'}
                class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
              />
            </div>
            <div>
              <label class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">
                Refresh Token
                {#if editor.kind === 'edit' && editor.connection.credentials.refresh_token_set}
                  <span class="text-green-600 dark:text-green-400 font-normal ml-1">(stored)</span>
                {/if}
                <span class="text-gray-400 dark:text-dark-text-muted font-normal ml-1">— leave blank to obtain via OAuth</span>
              </label>
              <input
                type={showSecrets ? 'text' : 'password'}
                bind:value={formRefreshToken}
                placeholder={editor.kind === 'edit' && editor.connection.credentials.refresh_token_set
                  ? '(leave blank to keep stored value)'
                  : 'Use "Connect" after saving to get this automatically'}
                class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
              />
            </div>
            <div>
              <label class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">
                API Key
                <span class="text-gray-400 dark:text-dark-text-muted font-normal ml-1">— only for token-based providers</span>
              </label>
              <input
                type={showSecrets ? 'text' : 'password'}
                bind:value={formAPIKey}
                placeholder="(optional)"
                class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
              />
            </div>
          </div>
        </div>
      </div>

      <div class="flex items-center justify-end gap-2 p-4 border-t border-gray-200 dark:border-dark-border">
        <button
          onclick={closeEditor}
          class="px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
        >
          Cancel
        </button>
        <button
          onclick={saveEditor}
          disabled={saving || !formName.trim()}
          class="flex items-center gap-1.5 px-4 py-1.5 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
        >
          {saving ? 'Saving…' : editor.kind === 'create' ? 'Create' : 'Save'}
        </button>
      </div>
    </div>
  </div>
{/if}
