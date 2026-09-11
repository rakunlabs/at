<script lang="ts">
  import { onMount } from 'svelte';
  import { identityAPI, validateSettings, type AuthSettings, type IdentityProvider } from '../lib/api/identity';
  import { authErrorMessage } from '../lib/api/auth';
  import { storeNavbar } from '../lib/store/store.svelte';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  storeNavbar.title = 'Authentication';
  let settings = $state<AuthSettings | null>(null); let providers = $state<IdentityProvider[]>([]);
  let error = $state(''); let notice = $state(''); let busy = $state(false); let editing = $state<IdentityProvider | null>(null);
  let secret = $state(''); let clearSecret = $state(false); let scopes = $state('openid profile email');
  async function load() { busy = true; error = ''; try { const [s, p] = await Promise.all([identityAPI.get('settings'), identityAPI.get('identity-providers')]); settings = s.data; providers = p.data || []; } catch { error = 'Could not load authentication settings. Retry when the server is available.'; } finally { busy = false; } }
  onMount(() => { if (isNativeAdmin()) void load(); return () => { secret = ''; }; });
  async function run(action: () => Promise<void>) { busy = true; error = notice = ''; try { await action(); notice = 'Changes saved.'; } catch (e) { error = authErrorMessage(e, 'Could not save. Reload the latest configuration and retry.'); } finally { busy = false; secret = ''; } }
  function edit(p?: IdentityProvider) { editing = p ? structuredClone($state.snapshot(p)) : { id: '', label: '', mode: 'oidc', enabled: true, version: 0, issuer: '', client_id: '', auth_url: '', token_url: '', userinfo_url: '', jwks_url: '', scopes: ['openid','profile','email'], subject_claim: '', auth_header_style: 'client_secret_basic' }; scopes = editing.scopes.join(' '); secret = ''; clearSecret = false; }
  async function saveProvider(e: SubmitEvent) { e.preventDefault(); if (!editing || busy) return; const p = editing;
    await run(async () => { const { has_client_secret, ...config } = p; const body = { ...config, scopes: scopes.split(/\s+/).filter(Boolean), ...(secret ? { client_secret: secret } : {}), ...(clearSecret ? { clear_client_secret: true } : {}) }; if (p.id) await identityAPI.put(`identity-providers/${encodeURIComponent(p.id)}`, body); else await identityAPI.post('identity-providers', body); editing = null; await load(); });
  }
</script>
<div class="settings-page settings-form"><header><h1 class="text-2xl font-semibold">Authentication</h1><p class="settings-note mt-2">Installation-wide sign-in and admission settings.</p></header>
{#if !isNativeAdmin()}<p class="settings-note">Only installation administrators can configure authentication.</p>{:else}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
  <button class="settings-button" disabled={busy} onclick={load}>{busy ? 'Working…' : 'Reload settings'}</button>
  {#if settings}<form class="settings-section space-y-5" onsubmit={e => { e.preventDefault(); error = validateSettings(settings!); if (!error) void run(async () => { settings = (await identityAPI.put('settings', settings)).data; }); }}>
    <h2 class="text-lg font-semibold">Sign-in policy</h2><label>Display title<input bind:value={settings.display_title} required maxlength="120" /></label>
    <label>Canonical origin<input value={settings.origin} readonly /><span class="settings-note">Pinned at setup. Origin changes require an operator migration.</span></label>
    <label><input type="checkbox" bind:checked={settings.local_login_enabled} />Allow local password and passkey sign-in</label><p class="settings-note">Disabling local sign-in requires an enabled provider linked to an active installation administrator.</p>
    <div class="grid sm:grid-cols-2 gap-5"><label>Session lifetime (seconds)<input type="number" min="600" max="86400" step="1" bind:value={settings.session_ttl_seconds} required /></label><label>Remembered lifetime (seconds)<input type="number" min={settings.session_ttl_seconds} max="2592000" step="1" bind:value={settings.remember_ttl_seconds} required /></label></div>
    <label>New external-account admission<select bind:value={settings.signup_admission}><option value="invite_only">Invitation only</option><option value="approval_required">Administrator approval required</option></select></label>
    <p class="settings-note">Enrolled authenticators are always required. Maximum 20 sessions per account. Lifetime changes apply to new sessions. Configuration version {settings.version}.</p>
    <button class="settings-primary" disabled={busy}>Save sign-in policy</button>
  </form>{/if}
  <section class="settings-section"><div class="flex flex-wrap justify-between items-center gap-3"><h2 class="text-lg font-semibold">Identity providers</h2><button class="settings-button" disabled={busy} onclick={() => edit()}>Add provider</button></div>
    <p class="settings-note">Use OIDC discovery or explicitly configured OAuth2 endpoints. Provider claims grant permissions only through workspace mappings.</p>
    {#if providers.length === 0}<p class="settings-note">No identity providers configured.</p>{/if}
    <ul class="divide-y divide-gray-200 dark:divide-dark-border">{#each providers as p}<li class="py-4 space-y-2"><div class="flex flex-wrap justify-between gap-3"><div><strong>{p.label}</strong><p class="settings-note">{p.mode.toUpperCase()} · {p.enabled ? 'Enabled' : 'Disabled'} · version {p.version}</p></div><div class="flex gap-2"><button class="settings-button" disabled={busy} onclick={() => edit(p)}>Edit</button><button class="settings-button" disabled={busy || !p.enabled} onclick={() => run(async () => { await identityAPI.delete(`identity-providers/${encodeURIComponent(p.id)}`, { data: { version: p.version } }); await load(); })}>Disable</button></div></div><p class="settings-note break-all">Callback: {new URL(`auth/external/${encodeURIComponent(p.id)}/callback`, document.baseURI).href}</p></li>{/each}</ul>
    {#if editing}<form class="settings-section space-y-5" onsubmit={saveProvider}>
      <h3 class="font-semibold">{editing.id ? `Edit ${editing.label}` : 'New identity provider'}</h3>
      <label>Display label<input bind:value={editing.label} required /></label><label>Protocol<select bind:value={editing.mode}><option value="oidc">OpenID Connect</option><option value="oauth2">OAuth2</option></select></label><label><input type="checkbox" bind:checked={editing.enabled} />Enabled</label>
      <label>Issuer URL<input type="url" bind:value={editing.issuer} required={editing.mode === 'oidc'} placeholder="https://login.example.com" /></label><label>Client ID<input bind:value={editing.client_id} required /></label>
      <label>Client secret<input type="password" bind:value={secret} disabled={clearSecret} autocomplete="new-password" /><span class="settings-note">{editing.has_client_secret ? 'A secret is stored. Leave blank to preserve it.' : 'No secret is stored.'}</span></label><label><input type="checkbox" bind:checked={clearSecret} onchange={() => { if (clearSecret) secret = ''; }} />Clear stored client secret</label>
      <label>Scopes (space separated)<input bind:value={scopes} required /></label>
      <label>Client authentication<select bind:value={editing.auth_header_style}><option value="client_secret_basic">HTTP Basic</option><option value="client_secret_post">Request body</option></select></label>
      {#if editing.mode === 'oauth2'}<label>Stable subject claim<input bind:value={editing.subject_claim} required placeholder="node_id" /></label>{/if}
      <details open={editing.mode === 'oauth2'}><summary class="cursor-pointer font-medium">Endpoints {editing.mode === 'oidc' ? '(optional with discovery)' : '(required)'}</summary><div class="space-y-4 mt-4"><label>Authorization URL<input type="url" bind:value={editing.auth_url} required={editing.mode === 'oauth2'} /></label><label>Token URL<input type="url" bind:value={editing.token_url} required={editing.mode === 'oauth2'} /></label><label>User info URL<input type="url" bind:value={editing.userinfo_url} required={editing.mode === 'oauth2'} /></label>{#if editing.mode === 'oidc'}<label>JWKS URL<input type="url" bind:value={editing.jwks_url} /></label>{/if}</div></details>
      <p class="settings-note">Edits revoke linked users’ sessions. Changing a linked provider’s identity namespace requires a new provider.</p><div class="flex gap-3"><button class="settings-primary" disabled={busy}>Save provider</button><button type="button" class="settings-button" disabled={busy} onclick={() => { editing = null; secret = ''; }}>Cancel</button></div>
    </form>{/if}
  </section>
{/if}</div>
