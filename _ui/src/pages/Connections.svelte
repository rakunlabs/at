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
    listConnectors,
    createConnector,
    updateConnector,
    deleteConnector,
    type Connector,
    type ConnectorField,
  } from '@/lib/api/connectors';
  import {
    Plug,
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
    Settings2,
    Cable,
  } from 'lucide-svelte';

  storeNavbar.title = 'Connections';

  // ─── State ───
  let connectors = $state<Connector[]>([]);
  let connections = $state<Connection[]>([]);
  let loading = $state(true);
  let saving = $state(false);

  // Connection editor modal: create (under a connector) or edit an existing row.
  type EditorMode =
    | { kind: 'create'; connector: Connector }
    | { kind: 'edit'; connection: Connection; connector?: Connector };
  let editor = $state<EditorMode | null>(null);
  let formName = $state('');
  let formDescription = $state('');
  let formFields = $state<Record<string, string>>({});
  let showSecrets = $state(false);

  // Manual OAuth flow state (per connection ID).
  type OAuthStep = 'authorize' | 'paste-code';
  let oauthStep = $state<Record<string, OAuthStep>>({});
  let oauthAuthURL = $state<Record<string, string>>({});
  let oauthRedirectURI = $state<Record<string, string>>({});
  let oauthCode = $state<Record<string, string>>({});

  // Connector (provider type) management modal.
  type ConnectorEditorMode = { kind: 'create' } | { kind: 'edit'; connector: Connector };
  let connectorEditor = $state<ConnectorEditorMode | null>(null);
  let cSlug = $state('');
  let cName = $state('');
  let cDescription = $state('');
  let cIcon = $state('');
  let cAuthKind = $state<'oauth2' | 'token' | 'custom'>('oauth2');
  let cAuthURL = $state('');
  let cTokenURL = $state('');
  let cScopes = $state('');
  let cUserinfoURL = $state('');
  let cAccountLabelPath = $state('');
  let cAccessType = $state('');
  let cPrompt = $state('');
  let cUsePKCE = $state(false);
  let cFields = $state<ConnectorField[]>([]);
  let cSaving = $state(false);

  // ─── Load ───
  async function load() {
    loading = true;
    try {
      const [cs, conns] = await Promise.all([
        listConnectors().catch(() => [] as Connector[]),
        listConnections().catch(() => [] as Connection[]),
      ]);
      connectors = cs;
      connections = conns;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load connections', 'alert');
    } finally {
      loading = false;
    }
  }
  load();

  // ─── Derived: connector lookup + grouped sections ───
  const connectorBySlug = $derived(() => {
    const m = new Map<string, Connector>();
    for (const c of connectors) m.set(c.slug, c);
    return m;
  });

  const connectionsByProvider = $derived(() => {
    const m = new Map<string, Connection[]>();
    for (const c of connections) {
      const arr = m.get(c.provider) ?? [];
      arr.push(c);
      m.set(c.provider, arr);
    }
    return m;
  });

  // Sections: one per connector (sorted by name), plus orphan providers that
  // have connections but no connector definition.
  const sections = $derived(() => {
    const out: { connector?: Connector; provider: string; items: Connection[] }[] = [];
    const seen = new Set<string>();
    for (const c of connectors) {
      seen.add(c.slug);
      out.push({ connector: c, provider: c.slug, items: connectionsByProvider().get(c.slug) ?? [] });
    }
    for (const [provider, items] of connectionsByProvider()) {
      if (!seen.has(provider)) {
        out.push({ connector: undefined, provider, items });
      }
    }
    return out;
  });

  function providerLabel(provider: string): string {
    return connectorBySlug().get(provider)?.name ?? provider;
  }

  function isOAuth(connector?: Connector): boolean {
    return connector?.auth_kind === 'oauth2';
  }

  // ─── Field helpers ───
  // Connector fields drive the credential form. When no connector definition
  // exists (orphan), fall back to the legacy fixed credential shape.
  function effectiveFields(connector: Connector | undefined, provider: string): ConnectorField[] {
    if (connector?.fields && connector.fields.length > 0) return connector.fields;
    return [
      { key: `${provider}_client_id`, label: 'Client ID', type: 'text' },
      { key: `${provider}_client_secret`, label: 'Client Secret', type: 'secret' },
      { key: `${provider}_api_key`, label: 'API Key', type: 'secret' },
    ];
  }

  function fieldIsSet(conn: Connection, key: string): boolean {
    const c = conn.credentials;
    if (key.endsWith('_client_id')) return !!c.client_id;
    if (key.endsWith('_client_secret')) return !!c.client_secret_set;
    if (key.endsWith('_refresh_token')) return !!c.refresh_token_set;
    if (key.endsWith('_api_key')) return !!c.api_key_set;
    return (c.extra_keys_set ?? []).includes(key);
  }

  function fieldPrefill(conn: Connection, key: string): string {
    // Only non-secret, server-revealed values can be prefilled (client_id).
    if (key.endsWith('_client_id')) return conn.credentials.client_id ?? '';
    return '';
  }

  function isConnected(conn: Connection, connector?: Connector): boolean {
    if (isOAuth(connector)) {
      if (conn.credentials.refresh_token_set) return true;
      return (conn.credentials.extra_keys_set ?? []).some((k) => k.endsWith('_access_token'));
    }
    return isSetupComplete(conn, connector);
  }

  function isSetupComplete(conn: Connection, connector?: Connector): boolean {
    const fields = effectiveFields(connector, conn.provider).filter((f) => !f.key.endsWith('_refresh_token'));
    let required = fields.filter((f) => f.required);
    if (required.length === 0) required = fields;
    return required.every((f) => fieldIsSet(conn, f.key));
  }

  // ─── Connection editor ───
  function initFormFields(fields: ConnectorField[], conn?: Connection) {
    const map: Record<string, string> = {};
    for (const f of fields) {
      if (f.key.endsWith('_refresh_token')) continue; // obtained via OAuth
      map[f.key] = conn ? fieldPrefill(conn, f.key) : '';
    }
    formFields = map;
  }

  function openCreate(connector: Connector) {
    editor = { kind: 'create', connector };
    formName = '';
    formDescription = '';
    showSecrets = false;
    initFormFields(effectiveFields(connector, connector.slug));
  }

  function openEdit(conn: Connection) {
    const connector = connectorBySlug().get(conn.provider);
    editor = { kind: 'edit', connection: conn, connector };
    formName = conn.name;
    formDescription = conn.description ?? '';
    showSecrets = false;
    initFormFields(effectiveFields(connector, conn.provider), conn);
  }

  function editorConnector(): Connector | undefined {
    if (!editor) return undefined;
    return editor.kind === 'create' ? editor.connector : editor.connector;
  }

  function editorProvider(): string {
    if (!editor) return '';
    return editor.kind === 'create' ? editor.connector.slug : editor.connection.provider;
  }

  function editorFields(): ConnectorField[] {
    const provider = editorProvider();
    return effectiveFields(editorConnector(), provider).filter((f) => !f.key.endsWith('_refresh_token'));
  }

  function closeEditor() {
    editor = null;
  }

  async function saveEditor() {
    if (!editor) return;
    if (!formName.trim()) {
      addToast('Name is required', 'warn');
      return;
    }
    const provider = editorProvider();
    saving = true;
    try {
      const fields: Record<string, string> = {};
      for (const [k, v] of Object.entries(formFields)) {
        if (v && v.trim()) fields[k] = v.trim();
      }
      if (editor.kind === 'create') {
        await createConnection({ provider, name: formName.trim(), description: formDescription.trim(), fields });
        addToast(`${providerLabel(provider)} account "${formName.trim()}" created`, 'info');
      } else {
        await updateConnection(editor.connection.id, {
          provider,
          name: formName.trim(),
          description: formDescription.trim(),
          fields,
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

  // ─── Delete connection ───
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

  // ─── Import from variables ───
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

  // ─── Connector (provider type) management ───
  function openConnectorCreate() {
    connectorEditor = { kind: 'create' };
    cSlug = '';
    cName = '';
    cDescription = '';
    cIcon = '';
    cAuthKind = 'oauth2';
    cAuthURL = '';
    cTokenURL = '';
    cScopes = '';
    cUserinfoURL = '';
    cAccountLabelPath = '';
    cAccessType = '';
    cPrompt = '';
    cUsePKCE = false;
    cFields = [];
  }

  function openConnectorEdit(c: Connector) {
    connectorEditor = { kind: 'edit', connector: c };
    cSlug = c.slug;
    cName = c.name;
    cDescription = c.description ?? '';
    cIcon = c.icon ?? '';
    cAuthKind = c.auth_kind;
    cAuthURL = c.oauth?.auth_url ?? '';
    cTokenURL = c.oauth?.token_url ?? '';
    cScopes = (c.oauth?.scopes ?? []).join(' ');
    cUserinfoURL = c.oauth?.userinfo_url ?? '';
    cAccountLabelPath = c.oauth?.account_label_path ?? '';
    cAccessType = c.oauth?.access_type ?? '';
    cPrompt = c.oauth?.prompt ?? '';
    cUsePKCE = c.oauth?.use_pkce ?? false;
    cFields = (c.fields ?? []).map((f) => ({ ...f }));
  }

  function closeConnectorEditor() {
    connectorEditor = null;
  }

  function addConnectorField() {
    cFields = [...cFields, { key: '', label: '', type: 'text', required: false }];
  }

  function removeConnectorField(i: number) {
    cFields = cFields.filter((_, idx) => idx !== i);
  }

  async function saveConnector() {
    if (!cSlug.trim()) {
      addToast('Slug is required', 'warn');
      return;
    }
    if (cAuthKind === 'oauth2' && (!cAuthURL.trim() || !cTokenURL.trim())) {
      addToast('OAuth2 connectors require Authorize URL and Token URL', 'warn');
      return;
    }
    cSaving = true;
    try {
      const input = {
        slug: cSlug.trim(),
        name: cName.trim() || cSlug.trim(),
        description: cDescription.trim(),
        icon: cIcon.trim(),
        auth_kind: cAuthKind,
        oauth:
          cAuthKind === 'oauth2'
            ? {
                auth_url: cAuthURL.trim(),
                token_url: cTokenURL.trim(),
                scopes: cScopes.split(/[\s,]+/).filter(Boolean),
                access_type: cAccessType.trim() || undefined,
                prompt: cPrompt.trim() || undefined,
                use_pkce: cUsePKCE,
                userinfo_url: cUserinfoURL.trim() || undefined,
                account_label_path: cAccountLabelPath.trim() || undefined,
              }
            : undefined,
        fields: cFields
          .filter((f) => f.key.trim())
          .map((f) => ({
            key: f.key.trim(),
            label: f.label?.trim() || undefined,
            type: f.type || 'text',
            required: f.required || undefined,
            placeholder: f.placeholder?.trim() || undefined,
            help: f.help?.trim() || undefined,
          })),
      };
      if (connectorEditor?.kind === 'edit') {
        await updateConnector(cSlug.trim(), input);
        addToast(`Connector "${input.name}" updated`, 'info');
      } else {
        await createConnector(input);
        addToast(`Connector "${input.name}" created`, 'info');
      }
      closeConnectorEditor();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save connector', 'alert');
    } finally {
      cSaving = false;
    }
  }

  async function removeConnector(c: Connector) {
    const msg = c.builtin
      ? `Delete connector "${c.name}"? (built-in — it cannot be removed)`
      : `Delete connector "${c.name}"? Existing accounts under it are kept but will lose their type definition.`;
    if (!confirm(msg)) return;
    try {
      await deleteConnector(c.slug);
      addToast(`Connector "${c.name}" deleted`, 'info');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete connector', 'alert');
    }
  }
</script>

<div class="p-4 max-w-4xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text">Connections</h1>
      <p class="text-sm text-gray-500 dark:text-dark-text-muted mt-0.5">
        Named external-service accounts. Providers are data-driven — add your own from "Manage providers".
      </p>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={openConnectorCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
        title="Add a new provider type (connector)"
      >
        <Cable size={14} />
        Add provider
      </button>
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
    {#each sections() as section (section.provider)}
      {@const connector = section.connector}
      <section class="mb-6">
        <div class="flex items-center justify-between mb-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">{providerLabel(section.provider)}</h2>
              {#if connector}
                <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted">
                  {connector.auth_kind}
                </span>
                {#if connector.builtin}
                  <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400">built-in</span>
                {/if}
              {:else}
                <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-50 dark:bg-amber-900/20 text-amber-600 dark:text-amber-400">no connector</span>
              {/if}
            </div>
            {#if connector?.description}
              <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{connector.description}</p>
            {/if}
          </div>
          <div class="flex items-center gap-1 shrink-0">
            {#if connector}
              <button
                onclick={() => openConnectorEdit(connector)}
                class="flex items-center gap-1.5 px-2 py-1.5 text-xs text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                title="Edit provider definition"
              >
                <Settings2 size={13} />
              </button>
            {/if}
            <button
              onclick={() => connector ? openCreate(connector) : openCreate({ slug: section.provider, name: section.provider, auth_kind: 'custom' } as Connector)}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
            >
              <Plus size={12} />
              Add account
            </button>
          </div>
        </div>

        {#if section.items.length === 0}
          <div class="text-xs text-gray-500 dark:text-dark-text-muted italic px-3 py-4 border border-dashed border-gray-200 dark:border-dark-border rounded">
            No {providerLabel(section.provider)} accounts yet. Click "Add account" above to create one.
          </div>
        {:else}
          <div class="space-y-2">
            {#each section.items as c (c.id)}
              <div class="border border-gray-200 dark:border-dark-border rounded-lg bg-white dark:bg-dark-surface">
                <div class="flex items-start justify-between p-3">
                  <div class="flex items-start gap-3 min-w-0">
                    <div class={[
                      'mt-0.5 w-8 h-8 rounded-lg flex items-center justify-center shrink-0',
                      isConnected(c, connector) ? 'bg-green-50 dark:bg-green-900/20' : 'bg-gray-50 dark:bg-dark-elevated',
                    ]}>
                      {#if isConnected(c, connector)}
                        <CheckCircle2 size={18} class="text-green-600 dark:text-green-400" />
                      {:else}
                        <XCircle size={18} class="text-gray-400 dark:text-dark-text-muted" />
                      {/if}
                    </div>
                    <div class="min-w-0">
                      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text truncate">{c.name}</h3>
                      {#if c.account_label}
                        <p class="text-xs text-gray-600 dark:text-dark-text-secondary mt-0.5 truncate">{c.account_label}</p>
                      {/if}
                      {#if c.description}
                        <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{c.description}</p>
                      {/if}
                      <div class="mt-2 flex flex-wrap items-center gap-2">
                        {#if isConnected(c, connector)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 rounded-full">
                            <CheckCircle2 size={10} /> Connected
                          </span>
                        {:else if isSetupComplete(c, connector)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-yellow-50 dark:bg-yellow-900/20 text-yellow-700 dark:text-yellow-400 rounded-full">
                            <AlertCircle size={10} /> Ready to connect
                          </span>
                        {:else}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted rounded-full">
                            <XCircle size={10} /> Not configured
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
                    {#if isOAuth(connector) && isSetupComplete(c, connector) && !isConnected(c, connector)}
                      <button
                        onclick={() => startPopupOAuth(c)}
                        class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
                      >
                        <Plug size={12} /> Connect
                      </button>
                    {/if}
                    {#if isOAuth(connector) && isConnected(c, connector)}
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
                {#if isOAuth(connector) && oauthStep[c.id]}
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
                            <ExternalLink size={12} /> Open authorization
                          </a>
                          <button
                            onclick={() => (oauthStep[c.id] = 'paste-code')}
                            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                          >
                            <ClipboardPaste size={12} /> I have the code
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
                            <Plug size={12} /> {saving ? 'Connecting…' : 'Connect'}
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
                {:else if isOAuth(connector) && isSetupComplete(c, connector)}
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

<!-- Connection editor modal -->
{#if editor}
  {@const fields = editorFields()}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40">
    <div class="bg-white dark:bg-dark-surface rounded-lg shadow-lg max-w-md w-full max-h-[90vh] overflow-y-auto">
      <div class="flex items-center justify-between p-4 border-b border-gray-200 dark:border-dark-border">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">
          {editor.kind === 'create' ? `Add ${providerLabel(editorProvider())} account` : `Edit ${providerLabel(editorProvider())} account`}
        </h2>
        <button onclick={closeEditor} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary">
          <X size={16} />
        </button>
      </div>

      <div class="p-4 space-y-3">
        <label class="block">
          <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Name <span class="text-red-500">*</span></span>
          <input
            type="text"
            bind:value={formName}
            placeholder="e.g. Main Channel"
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          />
        </label>

        <label class="block">
          <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Description</span>
          <input
            type="text"
            bind:value={formDescription}
            placeholder="Optional note for future-you"
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          />
        </label>

        {#if fields.length > 0}
          <div class="pt-2 border-t border-gray-100 dark:border-dark-border">
            <div class="flex items-center justify-between mb-2">
              <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Credentials</h3>
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
              {#each fields as f (f.key)}
                {@const stored = editor.kind === 'edit' && fieldIsSet(editor.connection, f.key)}
                <label class="block">
                  <span class="block text-xs text-gray-500 dark:text-dark-text-muted mb-1">
                    {f.label || f.key}
                    {#if f.required}<span class="text-red-500">*</span>{/if}
                    {#if stored && f.type === 'secret'}<span class="text-green-600 dark:text-green-400 font-normal ml-1">(stored)</span>{/if}
                  </span>
                  <input
                    type={f.type === 'secret' && !showSecrets ? 'password' : 'text'}
                    bind:value={formFields[f.key]}
                    placeholder={stored && f.type === 'secret' ? '(leave blank to keep stored value)' : (f.placeholder ?? '')}
                    class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
                  />
                  {#if f.help}<span class="block text-[11px] text-gray-400 dark:text-dark-text-muted mt-0.5">{f.help}</span>{/if}
                </label>
              {/each}
              {#if isOAuth(editorConnector())}
                <p class="text-[11px] text-gray-400 dark:text-dark-text-muted">
                  The refresh token is obtained automatically — save, then click "Connect".
                </p>
              {/if}
            </div>
          </div>
        {/if}
      </div>

      <div class="flex items-center justify-end gap-2 p-4 border-t border-gray-200 dark:border-dark-border">
        <button onclick={closeEditor} class="px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors">Cancel</button>
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

<!-- Connector (provider type) editor modal -->
{#if connectorEditor}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40">
    <div class="bg-white dark:bg-dark-surface rounded-lg shadow-lg max-w-lg w-full max-h-[90vh] overflow-y-auto">
      <div class="flex items-center justify-between p-4 border-b border-gray-200 dark:border-dark-border">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-dark-text">
          {connectorEditor.kind === 'create' ? 'Add provider' : `Edit provider: ${cName || cSlug}`}
        </h2>
        <div class="flex items-center gap-2">
          {#if connectorEditor.kind === 'edit'}
            <button
              onclick={() => removeConnector((connectorEditor as { connector: Connector }).connector)}
              class="text-red-500 hover:text-red-700"
              title="Delete connector"
            >
              <Trash2 size={15} />
            </button>
          {/if}
          <button onclick={closeConnectorEditor} class="text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary">
            <X size={16} />
          </button>
        </div>
      </div>

      <div class="p-4 space-y-3">
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Slug <span class="text-red-500">*</span></span>
            <input
              type="text"
              bind:value={cSlug}
              disabled={connectorEditor.kind === 'edit'}
              placeholder="e.g. spotify"
              class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono disabled:opacity-60"
            />
          </label>
          <label class="block">
            <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Name</span>
            <input
              type="text"
              bind:value={cName}
              placeholder="Spotify"
              class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
            />
          </label>
        </div>

        <label class="block">
          <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Description</span>
          <input
            type="text"
            bind:value={cDescription}
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          />
        </label>

        <label class="block">
          <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Auth kind</span>
          <select
            bind:value={cAuthKind}
            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent"
          >
            <option value="oauth2">OAuth2</option>
            <option value="token">Token / API key</option>
            <option value="custom">Custom</option>
          </select>
        </label>

        {#if cAuthKind === 'oauth2'}
          <div class="pt-2 border-t border-gray-100 dark:border-dark-border space-y-2">
            <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">OAuth2 endpoints</h3>
            <input bind:value={cAuthURL} placeholder="Authorize URL *" class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            <input bind:value={cTokenURL} placeholder="Token URL *" class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            <input bind:value={cScopes} placeholder="Scopes (space or comma separated)" class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            <div class="grid grid-cols-2 gap-2">
              <input bind:value={cAccessType} placeholder="access_type (e.g. offline)" class="px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
              <input bind:value={cPrompt} placeholder="prompt (e.g. consent)" class="px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            </div>
            <input bind:value={cUserinfoURL} placeholder="Userinfo URL (optional, for account label)" class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            <input bind:value={cAccountLabelPath} placeholder="Account label path (e.g. email)" class="w-full px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
            <label class="flex items-center gap-2 text-xs text-gray-600 dark:text-dark-text-secondary">
              <input type="checkbox" bind:checked={cUsePKCE} /> Use PKCE (for public clients / X / Twitter)
            </label>
          </div>
        {/if}

        <div class="pt-2 border-t border-gray-100 dark:border-dark-border">
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Credential fields</h3>
            <button onclick={addConnectorField} class="flex items-center gap-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary">
              <Plus size={12} /> Add field
            </button>
          </div>
          {#if cFields.length === 0}
            <p class="text-[11px] text-gray-400 dark:text-dark-text-muted italic">No fields yet. For OAuth2, add client_id and client_secret.</p>
          {/if}
          <div class="space-y-2">
            {#each cFields as f, i (i)}
              <div class="flex items-center gap-2">
                <input bind:value={f.key} placeholder="key (e.g. spotify_client_id)" class="flex-1 px-2 py-1 text-xs border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
                <input bind:value={f.label} placeholder="label" class="w-24 px-2 py-1 text-xs border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent" />
                <select bind:value={f.type} class="px-2 py-1 text-xs border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent">
                  <option value="text">text</option>
                  <option value="secret">secret</option>
                </select>
                <label class="flex items-center gap-1 text-[11px] text-gray-500 dark:text-dark-text-muted" title="Required">
                  <input type="checkbox" bind:checked={f.required} /> req
                </label>
                <button onclick={() => removeConnectorField(i)} class="text-red-500 hover:text-red-700" title="Remove field">
                  <X size={13} />
                </button>
              </div>
            {/each}
          </div>
        </div>
      </div>

      <div class="flex items-center justify-end gap-2 p-4 border-t border-gray-200 dark:border-dark-border">
        <button onclick={closeConnectorEditor} class="px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors">Cancel</button>
        <button
          onclick={saveConnector}
          disabled={cSaving || !cSlug.trim()}
          class="flex items-center gap-1.5 px-4 py-1.5 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
        >
          {cSaving ? 'Saving…' : connectorEditor.kind === 'create' ? 'Create' : 'Save'}
        </button>
      </div>
    </div>
  </div>
{/if}
