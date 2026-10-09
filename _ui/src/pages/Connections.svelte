<script lang="ts">
  import LoadIssues from '@/lib/components/LoadIssues.svelte';
  import { createPageLoader } from '@/lib/helper/page-load.svelte';
  import { connectionSections } from '@/lib/helper/connection-sections';
  const pageLoad = createPageLoader();
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
    connectMCPAccount,
    listMCPOAuthAccounts,
    type MCPOAuthAccountTarget,
    type Connection,
    type ConnectionScope,
  } from '@/lib/api/connections';
  import { isNativeAdmin, storeAuth } from '@/lib/store/auth.svelte';
  import { can } from '@/lib/store/workspace.svelte';
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
    SquareCheck,
    SquareX,
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
    KeyRound,
    User,
  } from 'lucide-svelte';
  import SquareAlert from '@/lib/components/icons/SquareAlert.svelte';
  import { listMCPAuthGrants, revokeMCPAuthGrant, type MCPAuthGrant } from '@/lib/api/mcp-auth';

  storeNavbar.title = 'Connections';

  // Members manage their own personal accounts; workspace accounts, provider
  // definitions and the variable import stay with administrators.
  let admin = $derived(isNativeAdmin());
  let mayManageWorkspace = $derived(admin || (can('connections.write') && can('credentials.manage')));
  let mayPersonal = $derived(admin || can('connections.use'));
  let mayUseMCP = $derived(admin || can('mcp.use'));
  let myUserID = $derived(storeAuth.identity?.subject ?? '');

  // ─── State ───
  let connectors = $state<Connector[]>([]);
  let connections = $state<Connection[]>([]);
  let mcpTargets = $state<MCPOAuthAccountTarget[]>([]);
  let mcpGrants = $state<MCPAuthGrant[]>([]);
  let mcpConnecting = $state('');
  let showProviderCatalog = $state(false);
  let providerSearch = $state('');
  let providerDialog = $state<HTMLDialogElement>();
  let providerSearchInput = $state<HTMLInputElement>();
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
  let formScope = $state<ConnectionScope>('personal');
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
    pageLoad.reset();
    try {
      await Promise.all([
        pageLoad.load('Connector catalog', listConnectors, result => { connectors = result || []; }, 'external_connections'),
        pageLoad.load('Connections', listConnections, result => { connections = result || []; }, 'external_connections'),
        mayUseMCP
          ? pageLoad.load('MCP servers', listMCPOAuthAccounts, result => { mcpTargets = result || []; }, 'mcp_servers')
          : Promise.resolve(false),
        mayUseMCP
          ? pageLoad.load('MCP sign-ins', listMCPAuthGrants, result => { mcpGrants = result || []; }, 'mcp_servers')
          : Promise.resolve(false),
      ]);
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

  // MCP OAuth accounts are listed on their own: they are created by the
  // authorization flow, never through a connector form.
  const mcpAccounts = $derived(connections.filter((c) => !!c.mcp_oauth));
  // Accounts already shown next to an MCP server row are not repeated below.
  const listedMCPConnectionIDs = $derived(new Set(mcpTargets.map((t) => t.account?.connection_id).filter(Boolean)));
  const otherMCPAccounts = $derived(mcpAccounts.filter((c) => !listedMCPConnectionIDs.has(c.id)));

  function connectMCPTarget(t: MCPOAuthAccountTarget) {
    const key = `${t.set_id}:${t.upstream_index}`;
    mcpConnecting = key;
    connectMCPAccount(t.account
      ? { connection_id: t.account.connection_id, target: 'personal' }
      : { set_id: t.set_id, upstream_index: t.upstream_index, target: 'personal', connection_name: 'My account' })
      .then((r) => { addToast(r.message || 'Account connected'); load(); })
      .catch((e: any) => addToast(e?.message || 'Authorization failed', 'alert'))
      .finally(() => { if (mcpConnecting === key) mcpConnecting = ''; });
  }

  async function revokeGrant(g: MCPAuthGrant) {
    if (!confirm(`Revoke ${g.client_name}'s access to ${g.server_name || 'this MCP server'}? It will have to sign in again.`)) return;
    try {
      await revokeMCPAuthGrant(g.id);
      mcpGrants = mcpGrants.filter((x) => x.id !== g.id);
      addToast('Access revoked');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to revoke access', 'alert');
    }
  }

  function mcpSourcesText(t: MCPOAuthAccountTarget): string {
    const labels: Record<string, string> = { user: 'your account', agent: 'agent binding', shared: 'shared account' };
    return t.accounts.map((a) => labels[a] ?? a).join(' → ');
  }

  const sections = $derived(connectionSections(connections, connectors));
  const catalogProviders = $derived(connectors.filter(connector =>
    `${connector.name} ${connector.slug} ${connector.description ?? ''}`.toLowerCase().includes(providerSearch.trim().toLowerCase())));

  function openProviderCatalog() {
    providerSearch = '';
    showProviderCatalog = true;
  }

  $effect(() => {
    if (showProviderCatalog && providerDialog && !providerDialog.open) {
      providerDialog.showModal();
      providerSearchInput?.focus();
    } else if (!showProviderCatalog && providerDialog?.open) {
      providerDialog.close();
    }
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

  function isOAuthVerified(conn: Connection): boolean {
    return conn.metadata?.oauth_status === 'verified';
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
    showProviderCatalog = false;
    editor = { kind: 'create', connector };
    formScope = mayManageWorkspace ? 'workspace' : 'personal';
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
        await createConnection({ provider, name: formName.trim(), description: formDescription.trim(), fields, scope: formScope });
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

  // ─── MCP accounts ───
  function ownsConnection(c: Connection): boolean {
    return c.scope === 'personal' ? c.owner_user_id === myUserID : mayManageWorkspace;
  }

  function reconnectMCP(c: Connection) {
    connectMCPAccount({ connection_id: c.id, target: c.scope === 'personal' ? 'personal' : 'shared' })
      .then((r) => { addToast(r.message || 'Account reconnected'); load(); })
      .catch((e: any) => addToast(e?.message || 'Authorization failed', 'alert'));
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

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <LoadIssues issues={pageLoad.issues} retry={load} {loading} />
  <!-- Header -->
  <div class="flex flex-wrap items-start justify-between gap-3 mb-6">
    <div>
      <h1 class="text-lg font-semibold text-dark-text">Connections</h1>
      <p class="text-sm text-dark-text-muted mt-0.5">
        Your accounts on MCP servers and external services.
      </p>
      <span class="text-xs text-dark-text-muted">
        {connections.length} account{connections.length === 1 ? '' : 's'}
      </span>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <button
        onclick={() => load()}
        class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
        title="Refresh"
        aria-label="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      {#if admin}
      <button
        onclick={runImport}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated"
        title="Import from existing global variables (youtube_client_id, etc.)"
      >
        <Download size={14} />
        Import from variables
      </button>
      <button
        onclick={openConnectorCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated"
        title="Define a custom connection template"
      >
        <Cable size={14} />
        Add custom provider
      </button>
      {/if}
      {#if mayPersonal}
        <button onclick={openProviderCatalog} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover">
          <Plus size={14} /> Add connection
        </button>
      {/if}
    </div>
  </div>

  {#if mcpTargets.length > 0}
    <section class="border border-dark-border mb-4">
      <div class="px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text flex items-center gap-1.5"><KeyRound size={14} /> MCP servers</h2>
        <p class="text-xs text-dark-text-muted mt-0.5">
          MCP servers in your sets that sign in with OAuth. Connect your own account once; your chats and agents then use it.
          Tokens refresh automatically and are never shown.
        </p>
      </div>
      <div class="divide-y divide-dark-border">
        {#each mcpTargets as t (`${t.set_id}:${t.upstream_index}`)}
          {@const key = `${t.set_id}:${t.upstream_index}`}
          {@const usesMine = t.accounts.includes('user')}
          <div class="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-sm text-dark-text">{t.set_name}</span>
                <span class="px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted">{t.set_scope === 'personal' ? 'my set' : 'workspace set'}</span>
                {#if !usesMine}
                  <span class="px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted">managed account</span>
                {:else if t.account?.needs_reauth}
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-oc-red text-oc-red"><SquareAlert size={10} /> Needs reconnecting</span>
                {:else if t.account}
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-oc-green"><SquareCheck size={10} /> Connected</span>
                {:else}
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted"><SquareX size={10} /> Not connected</span>
                {/if}
              </div>
              <p class="text-xs text-dark-text-muted mt-0.5 font-mono truncate">{t.server}{t.account?.label ? ` · ${t.account.label}` : ''}</p>
              <p class="text-xs text-dark-text-muted mt-0.5">
                {#if usesMine}
                  Uses: {mcpSourcesText(t)}
                {:else}
                  This server uses {mcpSourcesText(t)}; there is nothing for you to connect.
                {/if}
              </p>
            </div>
            {#if usesMine && mayPersonal}
              <div class="flex items-center gap-1 shrink-0">
                {#if t.account}
                  <button onclick={() => connectMCPTarget(t)} disabled={mcpConnecting === key}
                    class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50" title="Re-authorize this account">
                    <RefreshCw size={12} /> Reconnect
                  </button>
                  {@const conn = connections.find((c) => c.id === t.account?.connection_id)}
                  {#if conn}
                    <button onclick={() => remove(conn)} class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-900/20" title="Disconnect">
                      <Trash2 size={12} /> Disconnect
                    </button>
                  {/if}
                {:else}
                  <button onclick={() => connectMCPTarget(t)} disabled={mcpConnecting === key}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-dark-base hover:bg-accent-hover disabled:opacity-50">
                    <Plug size={12} /> Connect my account
                  </button>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}

  {#if mcpGrants.length > 0}
    <section class="border border-dark-border mb-4">
      <div class="px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text flex items-center gap-1.5"><KeyRound size={14} /> Apps with MCP access</h2>
        <p class="text-xs text-dark-text-muted mt-0.5">MCP clients you signed in to an MCP server of this workspace. They act as your account; revoke one to sign it out.</p>
      </div>
      <div class="divide-y divide-dark-border">
        {#each mcpGrants as g (g.id)}
          <div class="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-sm text-dark-text">{g.client_name}</span>
                <span class="text-xs text-dark-text-muted">→ {g.server_name || g.mcp_server_id}</span>
              </div>
              <p class="text-xs text-dark-text-muted mt-0.5">
                Approved {new Date(g.created_at).toLocaleString()}{g.last_used_at ? ` · last used ${new Date(g.last_used_at).toLocaleString()}` : ''}
              </p>
            </div>
            <button onclick={() => revokeGrant(g)} class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-900/20">
              <Trash2 size={12} /> Revoke
            </button>
          </div>
        {/each}
      </div>
    </section>
  {/if}

  {#if otherMCPAccounts.length > 0}
    <section class="border border-dark-border mb-4">
      <div class="px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text flex items-center gap-1.5"><KeyRound size={14} /> {mcpTargets.length > 0 ? 'Other MCP accounts' : 'MCP accounts'}</h2>
        <p class="text-xs text-dark-text-muted mt-0.5">
          Accounts authorized through an MCP server's OAuth that no set you can use currently points at, plus shared workspace accounts.
        </p>
      </div>
      <div class="divide-y divide-dark-border">
        {#each otherMCPAccounts as c (c.id)}
          <div class="flex items-start justify-between gap-3 px-4 py-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-sm text-dark-text">{c.name}</span>
                <span class="px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-dark-text-muted">{c.scope === 'personal' ? 'personal' : 'workspace'}</span>
                <span class="text-xs font-mono text-dark-text-muted">{c.provider}</span>
                {#if c.mcp_oauth?.needs_reauth}
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-oc-red text-oc-red"><SquareAlert size={10} /> Needs re-authorization</span>
                {:else}
                  <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] border border-dark-border-subtle text-oc-green"><SquareCheck size={10} /> Connected</span>
                {/if}
                {#if c.used_by_agents?.length}
                  <span class="inline-flex items-center gap-1 text-[11px] text-dark-text-muted" title={c.used_by_agents.map((a) => a.name).join(', ')}><Users size={10} /> {c.used_by_agents.length}</span>
                {/if}
              </div>
              <p class="text-xs text-dark-text-muted mt-0.5 truncate font-mono" title={c.mcp_oauth?.mcp_url}>{c.mcp_oauth?.mcp_url}</p>
              {#if c.mcp_oauth?.scopes?.length}
                <p class="text-xs text-dark-text-muted mt-0.5">Scopes: <span class="font-mono">{c.mcp_oauth.scopes.join(' ')}</span></p>
              {/if}
            </div>
            {#if ownsConnection(c)}
              <div class="flex items-center gap-1 shrink-0">
                <button onclick={() => reconnectMCP(c)} class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-dark-text-secondary hover:bg-dark-elevated" title="Re-authorize this account">
                  <RefreshCw size={12} /> Reconnect
                </button>
                <button onclick={() => remove(c)} class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-900/20" title="Disconnect">
                  <Trash2 size={12} /> Disconnect
                </button>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}

  {#if sections.length > 0}
    <h2 class="text-sm font-medium text-dark-text flex items-center gap-1.5 mb-2"><Cable size={14} /> External service connections</h2>
  {/if}

  {#if pageLoad.loading('Connections')}
    <div class="border border-dark-border px-4 py-10 text-center text-sm text-dark-text-muted">
      Loading connections…
    </div>
  {:else if pageLoad.error('Connections') && !connections.length}
    <p class="text-sm text-dark-text-secondary">Connections could not be loaded. Retry above.</p>
  {:else if sections.length === 0}
    <div class="border border-dark-border px-4 py-8 text-center">
      <div class="text-xs text-dark-text-muted">
        No external service connections yet.
        {#if mayPersonal}<button onclick={openProviderCatalog} class="text-accent hover:underline">Add connection</button> to connect an account.{/if}
      </div>
    </div>
  {:else}
    <div class="space-y-4">
    {#each sections as section (section.provider)}
      {@const connector = section.connector}
      <section class="border border-dark-border overflow-hidden">
        <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-dark-border">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-sm font-medium text-dark-text">{providerLabel(section.provider)}</h2>
              {#if connector}
                <span class="px-1.5 py-0.5 text-[11px] font-medium border border-dark-border-subtle bg-dark-elevated text-dark-text-muted">
                  {connector.auth_kind}
                </span>
                {#if connector.builtin}
                  <span class="px-1.5 py-0.5 text-[11px] font-medium border border-blue-800 bg-blue-900/20 text-blue-400">built-in</span>
                {/if}
              {:else}
                <span class="px-1.5 py-0.5 text-[11px] font-medium border border-amber-800 bg-amber-900/20 text-amber-400">no connector</span>
              {/if}
              <span class="text-xs text-dark-text-muted">({section.items.length})</span>
            </div>
            {#if connector?.description}
              <p class="text-xs text-dark-text-muted mt-0.5">{connector.description}</p>
            {/if}
          </div>
          <div class="flex items-center gap-2 shrink-0">
            {#if connector && admin}
              <button
                onclick={() => openConnectorEdit(connector)}
                class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
                title="Edit provider definition"
                aria-label="Edit provider definition"
              >
                <Settings2 size={13} />
              </button>
            {/if}
            {#if mayPersonal}
            <button
              onclick={() => connector ? openCreate(connector) : openCreate({ slug: section.provider, name: section.provider, auth_kind: 'custom' } as Connector)}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover"
            >
              <Plus size={12} />
              Add account
            </button>
            {/if}
          </div>
        </div>

          <div class="divide-y divide-dark-border">
            {#each section.items as c (c.id)}
              {@const connectionVerified = isConnected(c, connector) && (!isOAuth(connector) || isOAuthVerified(c))}
              <div>
                <div class="flex items-start justify-between gap-3 p-4">
                  <div class="flex items-start gap-3 min-w-0">
                    <div class={[
                      'mt-0.5 w-8 h-8 border flex items-center justify-center shrink-0',
                      connectionVerified
                        ? 'bg-green-900/20 border-green-900/40'
                        : isOAuth(connector) && isConnected(c, connector)
                          ? 'bg-amber-900/20 border-amber-800'
                          : 'bg-dark-elevated border-dark-border',
                    ]}>
                      {#if connectionVerified}
                        <SquareCheck size={18} class="text-green-400" />
                      {:else if isOAuth(connector) && isConnected(c, connector)}
                        <SquareAlert size={18} class="text-amber-400" />
                      {:else}
                        <SquareX size={18} class="text-dark-text-muted" />
                      {/if}
                    </div>
                    <div class="min-w-0">
                      <h3 class="text-sm font-medium text-dark-text truncate flex items-center gap-1.5">
                        {c.name}
                        {#if c.scope === 'personal'}
                          <span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] font-normal border border-dark-border-subtle text-dark-text-muted" title={c.owner_user_id === myUserID ? 'Only you can use this account' : 'Another account\'s personal connection'}><User size={10} /> personal</span>
                        {/if}
                      </h3>
                      {#if c.account_label}
                        <p class="text-xs text-dark-text-secondary mt-0.5 truncate">{c.account_label}</p>
                      {/if}
                      {#if c.description}
                        <p class="text-xs text-dark-text-muted mt-0.5">{c.description}</p>
                      {/if}
                      <div class="mt-2 flex flex-wrap items-center gap-2">
                        {#if isConnected(c, connector) && (!isOAuth(connector) || isOAuthVerified(c))}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-green-900/20 text-green-400 border">
                            <SquareCheck size={10} /> {isOAuth(connector) ? 'Verified' : 'Connected'}
                          </span>
                        {:else if isOAuth(connector) && isConnected(c, connector)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-amber-900/20 text-amber-400 border border-amber-800" title="Stored credentials have not been verified by refreshing an access token">
                            <SquareAlert size={10} /> Stored — re-authorize to verify
                          </span>
                        {:else if isSetupComplete(c, connector)}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-yellow-900/20 text-yellow-400 border">
                            <SquareAlert size={10} /> Ready to connect
                          </span>
                        {:else}
                          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-dark-elevated text-dark-text-muted border">
                            <SquareX size={10} /> Not configured
                          </span>
                        {/if}
                        {#if c.used_by_agents && c.used_by_agents.length > 0}
                          <span
                            class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-blue-900/20 text-blue-400 border"
                            title={c.used_by_agents.map((a) => a.name).join(', ')}
                          >
                            <Users size={10} />
                            {c.used_by_agents.length} agent{c.used_by_agents.length === 1 ? '' : 's'}
                          </span>
                        {/if}
                      </div>
                    </div>
                  </div>
                  {#if ownsConnection(c)}
                  <div class="flex items-center gap-1 shrink-0">
                    {#if isOAuth(connector) && isSetupComplete(c, connector) && !isConnected(c, connector)}
                      <button
                        onclick={() => startPopupOAuth(c)}
                        class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover"
                      >
                        <Plug size={12} /> Connect
                      </button>
                    {/if}
                    {#if isOAuth(connector) && isConnected(c, connector)}
                      <button
                        onclick={() => startPopupOAuth(c)}
                        class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-dark-text-secondary hover:bg-dark-elevated"
                        title="Re-authorize"
                      >
                        <RefreshCw size={12} />
                      </button>
                    {/if}
                    <button
                      onclick={() => openEdit(c)}
                      class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-dark-text-secondary hover:bg-dark-elevated"
                      title="Edit"
                    >
                      <Pencil size={12} />
                    </button>
                    <button
                      onclick={() => remove(c)}
                      class="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-900/20"
                      title="Delete"
                    >
                      <Trash2 size={12} />
                    </button>
                  </div>
                  {/if}
                </div>

                <!-- Manual OAuth flow panel -->
                {#if isOAuth(connector) && oauthStep[c.id]}
                  <div class="border-t border-dark-border p-3 bg-dark-base/50">
                    {#if oauthStep[c.id] === 'authorize'}
                      <div class="space-y-2">
                        <p class="text-xs font-medium text-dark-text-secondary">
                          Open the link below, sign in, and authorize. Then paste the code here.
                        </p>
                        <div class="flex items-center gap-2">
                          <a
                            href={oauthAuthURL[c.id]}
                            target="_blank"
                            rel="noopener"
                            class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover"
                          >
                            <ExternalLink size={12} /> Open authorization
                          </a>
                          <button
                            onclick={() => (oauthStep[c.id] = 'paste-code')}
                            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-dark-text-secondary hover:bg-dark-elevated"
                          >
                            <ClipboardPaste size={12} /> I have the code
                          </button>
                          <button
                            onclick={() => cancelManualOAuth(c)}
                            class="text-xs text-dark-text-muted hover:text-dark-text-secondary"
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    {:else if oauthStep[c.id] === 'paste-code'}
                      <div class="space-y-2">
                        <p class="text-xs font-medium text-dark-text-secondary">Paste the authorization code:</p>
                        <div class="flex items-center gap-2">
                          <input
                            type="text"
                            value={oauthCode[c.id] ?? ''}
                            oninput={(e) => (oauthCode[c.id] = (e.target as HTMLInputElement).value)}
                            placeholder="Paste code here"
                            class="flex-1 px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle font-mono"
                          />
                          <button
                            onclick={() => submitOAuthCode(c)}
                            disabled={saving}
                            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover disabled:opacity-50"
                          >
                            <Plug size={12} /> {saving ? 'Connecting…' : 'Connect'}
                          </button>
                          <button
                            onclick={() => cancelManualOAuth(c)}
                            class="text-xs text-dark-text-muted hover:text-dark-text-secondary"
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    {/if}
                  </div>
                {:else if isOAuth(connector) && isSetupComplete(c, connector)}
                  <div class="border-t border-dark-border px-3 py-2">
                    <button
                      onclick={() => startManualOAuth(c)}
                      class="text-xs text-dark-text-muted hover:text-blue-400"
                    >
                      Popup blocked? Use manual connection
                    </button>
                  </div>
                {/if}
              </div>
            {/each}
          </div>
      </section>
    {/each}
    </div>
  {/if}
</div>

<!-- Templates are a creation choice, never entries in the saved-account list. -->
<dialog bind:this={providerDialog} onclose={() => (showProviderCatalog = false)} aria-labelledby="provider-catalog-title"
  class="m-auto p-0 w-[calc(100%-2rem)] max-w-xl max-h-[85dvh] border border-dark-border bg-dark-surface text-dark-text backdrop:bg-black/60">
  <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-dark-border">
    <div>
      <h2 id="provider-catalog-title" class="text-sm font-medium">Provider catalog</h2>
      <p class="text-xs text-dark-text-secondary mt-1">Choose a template to add a connection. These are not connected accounts.</p>
    </div>
    <button onclick={() => (showProviderCatalog = false)} aria-label="Close provider catalog" class="p-1.5 hover:bg-dark-elevated"><X size={16} /></button>
  </div>
  <div class="p-4">
    <label class="block text-xs text-dark-text-secondary mb-1" for="provider-search">Search providers</label>
    <input id="provider-search" bind:this={providerSearchInput} bind:value={providerSearch} type="search"
      class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-base text-dark-text focus:outline-2 focus:outline-accent" />
  </div>
  <div class="max-h-[50dvh] overflow-y-auto border-t border-dark-border">
    {#if pageLoad.loading('Connector catalog')}
      <p class="p-4 text-xs text-dark-text-secondary">Loading provider catalog…</p>
    {:else if pageLoad.error('Connector catalog')}
      <div class="p-4 text-xs text-dark-text-secondary">Provider catalog could not be loaded. <button onclick={load} class="text-accent hover:underline">Retry</button></div>
    {:else if connectors.length === 0}
      <p class="p-4 text-xs text-dark-text-secondary">No connection templates available.{admin ? ' Add a custom provider to define one.' : ''}</p>
    {:else if catalogProviders.length === 0}
      <p class="p-4 text-xs text-dark-text-secondary">No providers match your search.</p>
    {:else}
      <div class="divide-y divide-dark-border">
        {#each catalogProviders as connector (connector.slug)}
          <button onclick={() => openCreate(connector)} class="block w-full px-4 py-3 text-left hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent focus-visible:-outline-offset-2">
            <span class="text-sm">{connector.name || connector.slug}</span>
            {#if connector.description}<span class="block text-xs text-dark-text-secondary mt-1">{connector.description}</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>
  {#if admin}
    <div class="px-4 py-3 border-t border-dark-border">
      <button onclick={() => { showProviderCatalog = false; openConnectorCreate(); }} class="inline-flex items-center gap-1.5 text-xs text-dark-text-secondary hover:text-dark-text"><Plus size={12} /> Add custom provider</button>
    </div>
  {/if}
</dialog>

<!-- Connection editor modal -->
{#if editor}
  {@const fields = editorFields()}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60">
    <div class="bg-dark-surface shadow-xl border border-dark-border max-w-md w-full max-h-[90vh] overflow-y-auto">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text">
          {editor.kind === 'create' ? `Add ${providerLabel(editorProvider())} account` : `Edit ${providerLabel(editorProvider())} account`}
        </h2>
        <button onclick={closeEditor} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" aria-label="Close">
          <X size={16} />
        </button>
      </div>

      <div class="p-4 space-y-3">
        {#if editor.kind === 'create'}
          <div>
            <span class="block text-xs font-medium text-dark-text-secondary mb-1">Owner</span>
            <div class="flex text-xs border border-dark-border-subtle w-fit">
              <button type="button" onclick={() => (formScope = 'personal')} disabled={!mayPersonal}
                class="px-2 py-1 {formScope === 'personal' ? 'bg-accent text-dark-base' : 'bg-dark-elevated text-dark-text-secondary hover:bg-dark-border'} disabled:opacity-50">Personal</button>
              <button type="button" onclick={() => (formScope = 'workspace')} disabled={!mayManageWorkspace}
                class="px-2 py-1 {formScope === 'workspace' ? 'bg-accent text-dark-base' : 'bg-dark-elevated text-dark-text-secondary hover:bg-dark-border'} disabled:opacity-50">Workspace</button>
            </div>
            <p class="text-xs text-dark-text-muted mt-1">
              {formScope === 'personal' ? 'Only you can see and use this account, in this workspace.' : 'Shared with the workspace; agents and MCP sets can bind it.'}
            </p>
          </div>
        {:else}
          <p class="text-xs text-dark-text-muted">{editor.connection.scope === 'personal' ? 'Personal account — only its owner can use it.' : 'Workspace account.'}</p>
        {/if}
        <label class="block">
          <span class="block text-xs font-medium text-dark-text-secondary mb-1">Name <span class="text-red-500">*</span></span>
          <input
            type="text"
            bind:value={formName}
            placeholder="e.g. Main Channel"
            class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle"
          />
        </label>

        <label class="block">
          <span class="block text-xs font-medium text-dark-text-secondary mb-1">Description</span>
          <input
            type="text"
            bind:value={formDescription}
            placeholder="Optional note for future-you"
            class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle"
          />
        </label>

        {#if fields.length > 0}
          <div class="pt-2 border-t border-dark-border">
            <div class="flex items-center justify-between mb-2">
              <h3 class="text-xs font-medium text-dark-text-secondary">Credentials</h3>
              <button
                type="button"
                onclick={() => (showSecrets = !showSecrets)}
                class="text-dark-text-muted hover:text-dark-text-secondary"
                title={showSecrets ? 'Hide' : 'Show'}
              >
                {#if showSecrets}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
              </button>
            </div>

            <div class="space-y-2">
              {#each fields as f (f.key)}
                {@const stored = editor.kind === 'edit' && fieldIsSet(editor.connection, f.key)}
                <label class="block">
                  <span class="block text-xs text-dark-text-muted mb-1">
                    {f.label || f.key}
                    {#if f.required}<span class="text-red-500">*</span>{/if}
                    {#if stored && f.type === 'secret'}<span class="text-green-400 font-normal ml-1">(stored)</span>{/if}
                  </span>
                  <input
                    type={f.type === 'secret' && !showSecrets ? 'password' : 'text'}
                    bind:value={formFields[f.key]}
                    placeholder={stored && f.type === 'secret' ? '(leave blank to keep stored value)' : (f.placeholder ?? '')}
                    class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle font-mono"
                  />
                  {#if f.help}<span class="block text-[11px] text-dark-text-muted mt-0.5">{f.help}</span>{/if}
                </label>
              {/each}
              {#if isOAuth(editorConnector())}
                <p class="text-[11px] text-dark-text-muted">
                  The refresh token is obtained automatically — save, then click "Connect".
                </p>
              {/if}
            </div>
          </div>
        {/if}
      </div>

      <div class="flex items-center justify-end gap-2 px-4 py-3 border-t border-dark-border">
        <button onclick={closeEditor} class="px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
        <button
          onclick={saveEditor}
          disabled={saving || !formName.trim()}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover disabled:opacity-50"
        >
          {saving ? 'Saving…' : editor.kind === 'create' ? 'Create' : 'Save'}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Connector (provider type) editor modal -->
{#if connectorEditor}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60">
    <div class="bg-dark-surface shadow-xl border border-dark-border max-w-lg w-full max-h-[90vh] overflow-y-auto">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
        <h2 class="text-sm font-medium text-dark-text">
          {connectorEditor.kind === 'create' ? 'Add provider' : `Edit provider: ${cName || cSlug}`}
        </h2>
        <div class="flex items-center gap-2">
          {#if connectorEditor.kind === 'edit'}
            <button
              onclick={() => removeConnector((connectorEditor as { connector: Connector }).connector)}
              class="text-red-500 hover:text-red-300"
              title="Delete connector"
            >
              <Trash2 size={15} />
            </button>
          {/if}
          <button onclick={closeConnectorEditor} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary" aria-label="Close">
            <X size={16} />
          </button>
        </div>
      </div>

      <div class="p-4 space-y-3">
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="block text-xs font-medium text-dark-text-secondary mb-1">Slug <span class="text-red-500">*</span></span>
            <input
              type="text"
              bind:value={cSlug}
              disabled={connectorEditor.kind === 'edit'}
              placeholder="e.g. spotify"
              class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle font-mono disabled:opacity-60"
            />
          </label>
          <label class="block">
            <span class="block text-xs font-medium text-dark-text-secondary mb-1">Name</span>
            <input
              type="text"
              bind:value={cName}
              placeholder="Spotify"
              class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text placeholder-dark-text-muted placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle"
            />
          </label>
        </div>

        <label class="block">
          <span class="block text-xs font-medium text-dark-text-secondary mb-1">Description</span>
          <input
            type="text"
            bind:value={cDescription}
            class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle"
          />
        </label>

        <label class="block">
          <span class="block text-xs font-medium text-dark-text-secondary mb-1">Auth kind</span>
          <select
            bind:value={cAuthKind}
            class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle"
          >
            <option value="oauth2">OAuth2</option>
            <option value="token">Token / API key</option>
            <option value="custom">Custom</option>
          </select>
        </label>

        {#if cAuthKind === 'oauth2'}
          <div class="pt-2 border-t border-dark-border space-y-2">
            <h3 class="text-xs font-medium text-dark-text-secondary">OAuth2 endpoints</h3>
            <input bind:value={cAuthURL} placeholder="Authorize URL *" class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            <input bind:value={cTokenURL} placeholder="Token URL *" class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            <input bind:value={cScopes} placeholder="Scopes (space or comma separated)" class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            <div class="grid grid-cols-2 gap-2">
              <input bind:value={cAccessType} placeholder="access_type (e.g. offline)" class="px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
              <input bind:value={cPrompt} placeholder="prompt (e.g. consent)" class="px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            </div>
            <input bind:value={cUserinfoURL} placeholder="Userinfo URL (optional, for account label)" class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            <input bind:value={cAccountLabelPath} placeholder="Account label path (e.g. email)" class="w-full px-3 py-1.5 text-sm border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
            <label class="flex items-center gap-2 text-xs text-dark-text-secondary">
              <input type="checkbox" bind:checked={cUsePKCE} /> Use PKCE (for public clients / X / Twitter)
            </label>
          </div>
        {/if}

        <div class="pt-2 border-t border-dark-border">
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs font-medium text-dark-text-secondary">Credential fields</h3>
            <button onclick={addConnectorField} class="flex items-center gap-1 text-xs text-dark-text-muted hover:text-dark-text-secondary">
              <Plus size={12} /> Add field
            </button>
          </div>
          {#if cFields.length === 0}
            <p class="text-[11px] text-dark-text-muted italic">No fields yet. For OAuth2, add client_id and client_secret.</p>
          {/if}
          <div class="space-y-2">
            {#each cFields as f, i (i)}
              <div class="flex items-center gap-2">
                <input bind:value={f.key} placeholder="key (e.g. spotify_client_id)" class="flex-1 px-2 py-1 text-xs border border-dark-border-subtle bg-dark-elevated text-dark-text font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
                <input bind:value={f.label} placeholder="label" class="w-24 px-2 py-1 text-xs border border-dark-border-subtle bg-dark-elevated text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle" />
                <select bind:value={f.type} class="px-2 py-1 text-xs border border-dark-border-subtle bg-dark-elevated text-dark-text focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle">
                  <option value="text">text</option>
                  <option value="secret">secret</option>
                </select>
                <label class="flex items-center gap-1 text-[11px] text-dark-text-muted" title="Required">
                  <input type="checkbox" bind:checked={f.required} /> req
                </label>
                <button onclick={() => removeConnectorField(i)} class="text-red-500 hover:text-red-300" title="Remove field">
                  <X size={13} />
                </button>
              </div>
            {/each}
          </div>
        </div>
      </div>

      <div class="flex items-center justify-end gap-2 px-4 py-3 border-t border-dark-border">
        <button onclick={closeConnectorEditor} class="px-3 py-1.5 text-xs font-medium border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
        <button
          onclick={saveConnector}
          disabled={cSaving || !cSlug.trim()}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-accent text-gray-950 hover:bg-accent-hover disabled:opacity-50"
        >
          {cSaving ? 'Saving…' : connectorEditor.kind === 'create' ? 'Create' : 'Save'}
        </button>
      </div>
    </div>
  </div>
{/if}
