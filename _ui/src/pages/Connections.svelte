<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listConnections, disconnectProvider, getOAuthStartURL, saveVariable, getManualAuthURL, exchangeCode, type Connection, type ConnectionVar } from '@/lib/api/connections';
  import { Plug, Unplug, RefreshCw, Settings, CheckCircle2, XCircle, AlertCircle, Eye, EyeOff, ChevronDown, ChevronUp, ExternalLink, ClipboardPaste } from 'lucide-svelte';

  storeNavbar.title = 'Connections';

  // --- State ---
  let connections = $state<Connection[]>([]);
  let loading = $state(true);
  let saving = $state(false);

  // Inline form state: provider -> { varKey -> value }
  let formValues = $state<Record<string, Record<string, string>>>({});
  let showPassword = $state<Record<string, boolean>>({});
  let expandedSetup = $state<Record<string, boolean>>({});

  // Manual OAuth flow state
  let oauthStep = $state<Record<string, 'credentials' | 'authorize' | 'paste-code'>>({});
  let oauthAuthURL = $state<Record<string, string>>({});
  let oauthRedirectURI = $state<Record<string, string>>({});
  let oauthCode = $state<Record<string, string>>({});
  let showManualFlow = $state<Record<string, boolean>>({});

  // --- Load ---
  async function load() {
    loading = true;
    try {
      connections = await listConnections();
      // Auto-expand setup for unconfigured OAuth connections.
      for (const conn of connections) {
        if (conn.type === 'oauth' && !conn.setup_complete && !conn.connected) {
          expandedSetup[conn.provider] = true;
        }
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load connections', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // --- Get/set form value ---
  function getFormValue(provider: string, key: string): string {
    return formValues[provider]?.[key] ?? '';
  }

  function setFormValue(provider: string, key: string, value: string) {
    if (!formValues[provider]) formValues[provider] = {};
    formValues[provider][key] = value;
  }

  // --- Save credentials and start OAuth ---
  async function saveCredentialsAndStartAuth(conn: Connection) {
    if (!conn.required_variables) return;

    // Validate all fields have values (skip already-set ones).
    for (const v of conn.required_variables) {
      if (!v.set && !getFormValue(conn.provider, v.key)) {
        addToast(`Please fill in ${formatVarLabel(v.key)}`, 'warn');
        return;
      }
    }

    saving = true;
    try {
      // Save each variable that has a new value.
      for (const v of conn.required_variables) {
        const newValue = getFormValue(conn.provider, v.key);
        if (newValue) {
          await saveVariable({
            key: v.key,
            value: newValue,
            description: v.description,
            secret: v.secret,
          });
        }
      }

      // Try popup OAuth first.
      startPopupOAuth(conn.provider);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save credentials', 'alert');
    } finally {
      saving = false;
    }
  }

  // --- Popup OAuth flow (primary) ---
  function startPopupOAuth(provider: string) {
    const url = getOAuthStartURL(provider);
    const w = 500;
    const h = 650;
    const left = window.screenX + (window.outerWidth - w) / 2;
    const top = window.screenY + (window.outerHeight - h) / 2;
    const popup = window.open(url, 'oauth-connect', `width=${w},height=${h},left=${left},top=${top},toolbar=yes,menubar=yes,scrollbars=yes,resizable=yes`);

    // Listen for the OAuth result from the popup.
    function handleMessage(event: MessageEvent) {
      if (event.data?.type === 'oauth-result') {
        window.removeEventListener('message', handleMessage);
        if (event.data.status === 'success') {
          addToast('Connected successfully!', 'info');
          expandedSetup[provider] = false;
          load();
        } else {
          addToast('Connection failed. Try the manual method below.', 'alert');
          showManualFlow[provider] = true;
        }
      }
    }
    window.addEventListener('message', handleMessage);

    // Poll for popup close.
    const pollInterval = setInterval(() => {
      if (popup && popup.closed) {
        clearInterval(pollInterval);
        window.removeEventListener('message', handleMessage);
        setTimeout(() => load(), 1000);
      }
    }, 500);

    // Show manual fallback link after a delay in case the popup doesn't work.
    setTimeout(() => {
      showManualFlow[provider] = true;
    }, 3000);
  }

  // --- Manual OAuth flow (fallback) ---
  async function startManualOAuth(provider: string) {
    expandedSetup[provider] = true;
    try {
      const result = await getManualAuthURL(provider);
      oauthAuthURL[provider] = result.url;
      oauthRedirectURI[provider] = result.redirect_uri;
      oauthCode[provider] = '';
      oauthStep[provider] = 'authorize';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to get auth URL', 'alert');
    }
  }

  async function submitOAuthCode(provider: string) {
    const code = oauthCode[provider]?.trim();
    if (!code) {
      addToast('Please paste the authorization code', 'warn');
      return;
    }

    saving = true;
    try {
      const result = await exchangeCode(provider, code, oauthRedirectURI[provider]);
      addToast(result.message || 'Connected successfully!', 'info');
      oauthStep[provider] = 'credentials';
      oauthCode[provider] = '';
      formValues[provider] = {};
      expandedSetup[provider] = false;
      showManualFlow[provider] = false;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to exchange code', 'alert');
    } finally {
      saving = false;
    }
  }

  // --- Disconnect ---
  async function disconnect(provider: string, name: string) {
    if (!confirm(`Disconnect ${name}? This will remove the stored credentials.`)) return;
    try {
      await disconnectProvider(provider);
      addToast(`${name} disconnected`, 'info');
      load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to disconnect', 'alert');
    }
  }

  // --- Helpers ---
  const oauthConnections = $derived(connections.filter(c => c.type === 'oauth'));
  const tokenConnections = $derived(connections.filter(c => c.type === 'token'));

  function formatVarLabel(key: string): string {
    // youtube_client_id -> Client ID, google_client_secret -> Client Secret
    const parts = key.split('_');
    // Remove the provider prefix (first 1-2 segments).
    const meaningful = parts.slice(parts.indexOf('client') >= 0 ? parts.indexOf('client') : 1);
    return meaningful.map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
  }

  function toggleSetup(provider: string) {
    expandedSetup[provider] = !expandedSetup[provider];
  }
</script>

<div class="p-4 max-w-4xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div>
      <h1 class="text-lg font-semibold text-gray-900 dark:text-dark-text-primary">Connections</h1>
      <p class="text-sm text-gray-500 dark:text-dark-text-muted mt-0.5">Manage external service connections and credentials</p>
    </div>
    <button
      onclick={() => load()}
      class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
    >
      <RefreshCw size={14} />
      Refresh
    </button>
  </div>

  {#if loading}
    <div class="text-sm text-gray-500 dark:text-dark-text-muted p-8 text-center">Loading connections...</div>
  {:else}
    <!-- OAuth Connections -->
    {#if oauthConnections.length > 0}
      <div class="mb-6">
        <h2 class="text-xs font-medium text-gray-400 dark:text-dark-text-muted tracking-wider uppercase mb-3">OAuth Connections</h2>
        <div class="space-y-3">
          {#each oauthConnections as conn}
            <div class="border border-gray-200 dark:border-dark-border rounded-lg bg-white dark:bg-dark-surface transition-colors">
              <!-- Header row -->
              <div class="flex items-start justify-between p-4">
                <div class="flex items-start gap-3">
                  <div class={[
                    "mt-0.5 w-8 h-8 rounded-lg flex items-center justify-center",
                    conn.connected
                      ? "bg-green-50 dark:bg-green-900/20"
                      : "bg-gray-50 dark:bg-dark-elevated"
                  ]}>
                    {#if conn.connected}
                      <CheckCircle2 size={18} class="text-green-600 dark:text-green-400" />
                    {:else}
                      <XCircle size={18} class="text-gray-400 dark:text-dark-text-muted" />
                    {/if}
                  </div>
                  <div>
                    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text-primary">{conn.name}</h3>
                    <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{conn.description}</p>
                    <div class="mt-2">
                      {#if conn.connected}
                        <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 rounded-full">
                          <CheckCircle2 size={10} />
                          Connected
                        </span>
                      {:else if conn.setup_complete}
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
                    </div>
                  </div>
                </div>
                <div class="flex items-center gap-2">
                  {#if conn.connected}
                    <button
                      onclick={() => disconnect(conn.provider, conn.name)}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 rounded transition-colors"
                    >
                      <Unplug size={12} />
                      Disconnect
                    </button>
                  {:else if conn.setup_complete}
                    <button
                      onclick={() => startPopupOAuth(conn.provider)}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
                    >
                      <Plug size={12} />
                      Connect {conn.name}
                    </button>
                  {:else}
                    <button
                      onclick={() => toggleSetup(conn.provider)}
                      class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                    >
                      <Settings size={12} />
                      Set up
                      {#if expandedSetup[conn.provider]}
                        <ChevronUp size={12} />
                      {:else}
                        <ChevronDown size={12} />
                      {/if}
                    </button>
                  {/if}
                </div>
              </div>

              <!-- Inline setup form (multi-step) -->
              {#if !conn.connected && expandedSetup[conn.provider] && conn.required_variables}
                <div class="border-t border-gray-100 dark:border-dark-border p-4 bg-gray-50/50 dark:bg-dark-base/50 rounded-b-lg">

                  {#if oauthStep[conn.provider] === 'authorize'}
                    <!-- Step 2: Open Google auth link -->
                    <div class="space-y-3">
                      <div class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary">
                        <span class="flex items-center justify-center w-5 h-5 rounded-full bg-gray-900 dark:bg-accent text-white text-[10px] font-bold">2</span>
                        Open the link below, sign in with Google, and authorize access
                      </div>
                      <a
                        href={oauthAuthURL[conn.provider]}
                        target="_blank"
                        rel="noopener"
                        class="flex items-center gap-2 px-4 py-2.5 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors w-fit"
                      >
                        <ExternalLink size={14} />
                        Open {conn.name} Authorization
                      </a>
                      <p class="text-xs text-gray-500 dark:text-dark-text-muted">
                        After authorizing, you'll see a page with a code. Copy that code and come back here.
                      </p>
                      <button
                        onclick={() => oauthStep[conn.provider] = 'paste-code'}
                        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                      >
                        <ClipboardPaste size={12} />
                        I have the code, next step
                      </button>
                    </div>

                  {:else if oauthStep[conn.provider] === 'paste-code'}
                    <!-- Step 3: Paste authorization code -->
                    <div class="space-y-3">
                      <div class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary">
                        <span class="flex items-center justify-center w-5 h-5 rounded-full bg-gray-900 dark:bg-accent text-white text-[10px] font-bold">3</span>
                        Paste the authorization code
                      </div>
                      <input
                        type="text"
                        value={oauthCode[conn.provider] ?? ''}
                        oninput={(e) => oauthCode[conn.provider] = (e.target as HTMLInputElement).value}
                        placeholder="Paste the authorization code here"
                        class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text-primary placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
                      />
                      <div class="flex items-center gap-3">
                        <button
                          onclick={() => submitOAuthCode(conn.provider)}
                          disabled={saving}
                          class="flex items-center gap-1.5 px-4 py-2 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
                        >
                          <Plug size={14} />
                          {saving ? 'Connecting...' : `Connect ${conn.name}`}
                        </button>
                        <button
                          onclick={() => oauthStep[conn.provider] = 'authorize'}
                          class="px-3 py-2 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                        >
                          Back
                        </button>
                      </div>
                    </div>

                  {:else}
                    <!-- Step 1: Enter credentials -->
                    <div class="space-y-3">
                      <div class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary">
                        <span class="flex items-center justify-center w-5 h-5 rounded-full bg-gray-900 dark:bg-accent text-white text-[10px] font-bold">1</span>
                        Enter your OAuth2 credentials from
                        <a href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener" class="text-blue-600 dark:text-blue-400 underline">Google Cloud Console</a>
                      </div>
                      {#each conn.required_variables as v}
                        <div>
                          <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">
                            {formatVarLabel(v.key)}
                            {#if v.set}
                              <span class="text-green-600 dark:text-green-400 font-normal ml-1">(already set)</span>
                            {/if}
                          </label>
                          <div class="relative">
                            <input
                              type={v.secret && !showPassword[v.key] ? 'password' : 'text'}
                              value={getFormValue(conn.provider, v.key)}
                              oninput={(e) => setFormValue(conn.provider, v.key, (e.target as HTMLInputElement).value)}
                              placeholder={v.set ? '(stored - leave blank to keep)' : v.description}
                              class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text-primary placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent pr-9"
                            />
                            {#if v.secret}
                              <button
                                type="button"
                                onclick={() => showPassword[v.key] = !showPassword[v.key]}
                                class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"
                              >
                                {#if showPassword[v.key]}
                                  <EyeOff size={14} />
                                {:else}
                                  <Eye size={14} />
                                {/if}
                              </button>
                            {/if}
                          </div>
                        </div>
                      {/each}
                      <div class="flex items-center gap-3 pt-1">
                        <button
                          onclick={() => saveCredentialsAndStartAuth(conn)}
                          disabled={saving}
                          class="flex items-center gap-1.5 px-4 py-2 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
                        >
                          {saving ? 'Saving...' : 'Save & Continue'}
                        </button>
                        <button
                          onclick={() => expandedSetup[conn.provider] = false}
                          class="px-3 py-2 text-sm text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                        >
                          Cancel
                        </button>
                      </div>
                    </div>
                  {/if}

                </div>
              {/if}

              <!-- Manual flow fallback - shows after popup attempt or on demand -->
              {#if !conn.connected && (showManualFlow[conn.provider] || oauthStep[conn.provider]) && conn.setup_complete && !expandedSetup[conn.provider]}
                <div class="border-t border-gray-100 dark:border-dark-border px-4 py-3 bg-gray-50/50 dark:bg-dark-base/50 rounded-b-lg">
                  {#if oauthStep[conn.provider] === 'authorize'}
                    <div class="space-y-3">
                      <p class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Manual connection: open the link, authorize, then paste the code</p>
                      <a
                        href={oauthAuthURL[conn.provider]}
                        target="_blank"
                        rel="noopener"
                        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors"
                      >
                        <ExternalLink size={12} />
                        Open {conn.name} Authorization
                      </a>
                      <button
                        onclick={() => oauthStep[conn.provider] = 'paste-code'}
                        class="ml-2 text-xs text-blue-600 dark:text-blue-400 hover:underline"
                      >
                        I have the code
                      </button>
                    </div>
                  {:else if oauthStep[conn.provider] === 'paste-code'}
                    <div class="space-y-3">
                      <p class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Paste the authorization code:</p>
                      <div class="flex items-center gap-2">
                        <input
                          type="text"
                          value={oauthCode[conn.provider] ?? ''}
                          oninput={(e) => oauthCode[conn.provider] = (e.target as HTMLInputElement).value}
                          placeholder="Paste code here"
                          class="flex-1 px-3 py-1.5 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text-primary placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent font-mono"
                        />
                        <button
                          onclick={() => submitOAuthCode(conn.provider)}
                          disabled={saving}
                          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
                        >
                          <Plug size={12} />
                          {saving ? 'Connecting...' : 'Connect'}
                        </button>
                      </div>
                      <button
                        onclick={() => oauthStep[conn.provider] = 'authorize'}
                        class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700"
                      >
                        Back
                      </button>
                    </div>
                  {:else}
                    <button
                      onclick={() => startManualOAuth(conn.provider)}
                      class="text-xs text-gray-500 dark:text-dark-text-muted hover:text-blue-600 dark:hover:text-blue-400 transition-colors"
                    >
                      Popup not working? Try manual connection method
                    </button>
                  {/if}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Token-based Connections -->
    {#if tokenConnections.length > 0}
      <div class="mb-6">
        <h2 class="text-xs font-medium text-gray-400 dark:text-dark-text-muted tracking-wider uppercase mb-3">API Key Connections</h2>
        <div class="space-y-3">
          {#each tokenConnections as conn}
            <div class="border border-gray-200 dark:border-dark-border rounded-lg bg-white dark:bg-dark-surface transition-colors">
              <div class="flex items-start justify-between p-4">
                <div class="flex items-start gap-3">
                  <div class={[
                    "mt-0.5 w-8 h-8 rounded-lg flex items-center justify-center",
                    conn.connected
                      ? "bg-green-50 dark:bg-green-900/20"
                      : "bg-gray-50 dark:bg-dark-elevated"
                  ]}>
                    {#if conn.connected}
                      <CheckCircle2 size={18} class="text-green-600 dark:text-green-400" />
                    {:else}
                      <XCircle size={18} class="text-gray-400 dark:text-dark-text-muted" />
                    {/if}
                  </div>
                  <div>
                    <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text-primary">{conn.name}</h3>
                    <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{conn.description}</p>
                    <div class="mt-2">
                      {#if conn.connected}
                        <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 rounded-full">
                          <CheckCircle2 size={10} />
                          Configured
                        </span>
                      {:else}
                        <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted rounded-full">
                          <XCircle size={10} />
                          Not configured
                        </span>
                      {/if}
                    </div>
                  </div>
                </div>
                <div>
                  <button
                    onclick={() => toggleSetup(conn.provider)}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated rounded transition-colors"
                  >
                    <Settings size={12} />
                    {conn.connected ? 'Update' : 'Configure'}
                    {#if expandedSetup[conn.provider]}
                      <ChevronUp size={12} />
                    {:else}
                      <ChevronDown size={12} />
                    {/if}
                  </button>
                </div>
              </div>

              <!-- Inline config form for token connections -->
              {#if expandedSetup[conn.provider] && conn.required_variables}
                <div class="border-t border-gray-100 dark:border-dark-border p-4 bg-gray-50/50 dark:bg-dark-base/50 rounded-b-lg">
                  <div class="space-y-3">
                    {#each conn.required_variables as v}
                      <div>
                        <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">
                          {formatVarLabel(v.key)}
                          {#if v.set}
                            <span class="text-green-600 dark:text-green-400 font-normal ml-1">(already set)</span>
                          {/if}
                        </label>
                        <div class="relative">
                          <input
                            type={v.secret && !showPassword[v.key] ? 'password' : 'text'}
                            value={getFormValue(conn.provider, v.key)}
                            oninput={(e) => setFormValue(conn.provider, v.key, (e.target as HTMLInputElement).value)}
                            placeholder={v.set ? '(stored - leave blank to keep)' : v.description}
                            class="w-full px-3 py-2 text-sm border border-gray-200 dark:border-dark-border rounded bg-white dark:bg-dark-surface text-gray-900 dark:text-dark-text-primary placeholder-gray-400 dark:placeholder-dark-text-muted focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent pr-9"
                          />
                          {#if v.secret}
                            <button
                              type="button"
                              onclick={() => showPassword[v.key] = !showPassword[v.key]}
                              class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary"
                            >
                              {#if showPassword[v.key]}
                                <EyeOff size={14} />
                              {:else}
                                <Eye size={14} />
                              {/if}
                            </button>
                          {/if}
                        </div>
                      </div>
                    {/each}
                  </div>
                  <div class="mt-4 flex items-center gap-3">
                    <button
                      onclick={async () => {
                        saving = true;
                        try {
                          for (const v of conn.required_variables || []) {
                            const val = getFormValue(conn.provider, v.key);
                            if (val) {
                              await saveVariable({ key: v.key, value: val, description: v.description, secret: v.secret });
                            }
                          }
                          addToast('Credentials saved', 'info');
                          formValues[conn.provider] = {};
                          await load();
                        } catch (e: any) {
                          addToast(e?.response?.data?.message || 'Failed to save', 'alert');
                        } finally {
                          saving = false;
                        }
                      }}
                      disabled={saving}
                      class="flex items-center gap-1.5 px-4 py-2 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent/90 rounded transition-colors disabled:opacity-50"
                    >
                      {saving ? 'Saving...' : 'Save'}
                    </button>
                    <button
                      onclick={() => expandedSetup[conn.provider] = false}
                      class="px-3 py-2 text-sm text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    {#if connections.length === 0}
      <div class="text-center py-12">
        <Plug size={32} class="mx-auto text-gray-300 dark:text-dark-text-muted mb-3" />
        <p class="text-sm text-gray-500 dark:text-dark-text-muted">No connections available</p>
        <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Install skill templates to see available connections</p>
      </div>
    {/if}
  {/if}
</div>
