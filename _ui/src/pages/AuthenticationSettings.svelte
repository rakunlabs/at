<script lang="ts">
  import { onMount } from 'svelte';
  import { identityAPI, validateSettings, parseAuthDuration, formatAuthDuration, type AuthSettings, type IdentityProvider } from '../lib/api/identity';
  import { authErrorMessage } from '../lib/api/auth';
  import { storeNavbar } from '../lib/store/store.svelte';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  storeNavbar.title = 'Authentication';
  let settings = $state<AuthSettings | null>(null); let providers = $state<IdentityProvider[]>([]);
  let error = $state(''); let notice = $state(''); let busy = $state(false); let editing = $state<IdentityProvider | null>(null);
  let secret = $state(''); let clearSecret = $state(false); let scopes = $state('openid profile email'); let rolesClaims = $state('');
  let additionalOrigins = $state(''); let sessionLifetime = $state(''); let rememberedLifetime = $state(''); let originalOrigin = $state('');
  function adopt(value: AuthSettings) { settings = value; originalOrigin = value.origin; additionalOrigins = (value.allowed_origins || []).join('\n'); sessionLifetime = formatAuthDuration(value.session_ttl_seconds); rememberedLifetime = formatAuthDuration(value.remember_ttl_seconds); }
  async function load() { busy = true; error = ''; try { const [s, p] = await Promise.all([identityAPI.get('settings'), identityAPI.get('identity-providers')]); adopt(s.data); providers = p.data || []; } catch { error = 'Could not load authentication settings. Retry when the server is available.'; } finally { busy = false; } }
  onMount(() => { if (isNativeAdmin()) void load(); return () => { secret = ''; }; });
  async function run(action: () => Promise<void>) { busy = true; error = notice = ''; try { await action(); notice = 'Changes saved.'; } catch (e) { error = authErrorMessage(e, 'Could not save. Reload the latest configuration and retry.'); } finally { busy = false; secret = ''; } }
  function edit(p?: IdentityProvider) { editing = p ? structuredClone($state.snapshot(p)) : { id: '', label: '', enabled: true, version: 0, client_id: '', auth_url: '', token_url: '', userinfo_url: '', jwks_url: '', scopes: ['openid','profile','email'], subject_claim: 'sub', auth_header_style: 'client_secret_basic' }; scopes = editing.scopes.join(' '); rolesClaims = (editing.roles_claims || []).join('\n'); secret = ''; clearSecret = false; }
  // Mirrors the server rule: an identity has to come from a source the browser
  // could not have written, so userinfo or JWKS (or both) must be configured.
  let claimSourceMissing = $derived(!!editing && !editing.userinfo_url && !editing.jwks_url);
  async function saveProvider(e: SubmitEvent) { e.preventDefault(); if (!editing || busy || claimSourceMissing) return; const p = editing;
    await run(async () => { const { has_client_secret, ...config } = p; const body = { ...config, scopes: scopes.split(/\s+/).filter(Boolean), roles_claims: rolesClaims.split('\n').map(v => v.trim()).filter(Boolean), ...(secret ? { client_secret: secret } : {}), ...(clearSecret ? { clear_client_secret: true } : {}) }; if (p.id) await identityAPI.put(`identity-providers/${encodeURIComponent(p.id)}`, body); else await identityAPI.post('identity-providers', body); editing = null; await load(); });
  }
  async function savePolicy(e: SubmitEvent) {
    e.preventDefault(); if (!settings || busy) return;
    const session = parseAuthDuration(sessionLifetime), remembered = parseAuthDuration(rememberedLifetime);
    if (!Number.isFinite(session) || !Number.isFinite(remembered)) { error = 'Enter durations such as 8h, 30d or 4w1d2h. Units: w, d, h, m, s; use whole seconds or longer.'; return; }
    const next = { ...settings, origin: settings.origin.trim(), allowed_origins: additionalOrigins.split('\n').map(s => s.trim()).filter(Boolean), session_ttl_seconds: session, remember_ttl_seconds: remembered };
    error = validateSettings(next); if (error) return;
    await run(async () => {
      const { session_ttl_seconds, remember_ttl_seconds, ...body } = next;
      adopt((await identityAPI.put('settings', { ...body, session_ttl: sessionLifetime.trim(), remember_ttl: rememberedLifetime.trim() })).data);
    });
  }
</script>
<svelte:head><title>AT | Authentication</title></svelte:head>
<div class="settings-page settings-form"><header><h1 class="settings-title">Authentication</h1><p class="settings-subtitle">Installation-wide sign-in and admission settings.</p></header>
{#if !isNativeAdmin()}<p class="settings-note">Only installation administrators can configure authentication.</p>{:else}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
  {#if notice && originalOrigin !== location.origin}<a class="settings-button inline-block break-all" href={`${originalOrigin}${new URL('.', document.baseURI).pathname}#/settings/authentication`}>Continue at the primary address</a>{/if}
  <button class="settings-button" disabled={busy} onclick={load}>{busy ? 'Working…' : 'Reload settings'}</button>
  {#if settings}<form class="settings-section" onsubmit={savePolicy}>
    <h2 class="settings-section-title">Sign-in policy</h2><label>Display title<input bind:value={settings.display_title} required maxlength="120" /></label>
    <label>Primary sign-in address<input type="url" bind:value={settings.origin} required placeholder="https://at.example.com" spellcheck="false" /><span class="settings-note">The main address for OAuth callbacks, passkeys and mobile sign-in. Include the scheme, without a path or trailing slash.</span></label>
    {#if settings.origin !== originalOrigin}<p class="settings-note">After changing the primary address, sign in there. Update OAuth callback URLs at your providers; passkeys registered for a different hostname must be enrolled again.</p>{/if}
    <label>Additional sign-in addresses<textarea bind:value={additionalOrigins} rows={3} spellcheck="false" placeholder="https://at.internal.example.com&#10;http://localhost:8080"></textarea><span class="settings-note">One address per line, up to 16. HTTPS addresses can be combined with HTTP loopback addresses such as http://localhost:8080. Each host has its own browser session. Passkeys and external providers use the primary address.</span></label>
    <label><input type="checkbox" bind:checked={settings.local_login_enabled} />Allow local password and passkey sign-in</label><p class="settings-note">Disabling local sign-in requires an enabled provider linked to an active installation administrator.</p>
    <label><input type="checkbox" bind:checked={settings.local_login_collapsed} disabled={!settings.local_login_enabled} />Hide the local sign-in form until it is requested</label><p class="settings-note">Presentation only: the sign-in screen leads with your identity providers and reveals the username and password form from a control in its header. Every local account can still sign in.</p>
    <label><input type="checkbox" checked={!settings.passkey_login_disabled} disabled={!settings.local_login_enabled} onchange={e => settings && (settings.passkey_login_disabled = !e.currentTarget.checked)} />Allow passkey sign-in</label><p class="settings-note">Turning this off removes the passkey button from the sign-in screen and refuses the sign-in ceremony. Accounts keep their passkeys and can still add, remove and verify with them under Account security.</p>
    <div class="grid sm:grid-cols-2 gap-3"><label>Session lifetime<input bind:value={sessionLifetime} required spellcheck="false" placeholder="8h" aria-describedby="session-lifetime-hint" /><span id="session-lifetime-hint" class="settings-note">Between 10m and 1d. Default: 8h.</span></label><label>Remembered lifetime<input bind:value={rememberedLifetime} required spellcheck="false" placeholder="4w2d" aria-describedby="remembered-lifetime-hint" /><span id="remembered-lifetime-hint" class="settings-note">At least the session lifetime, up to 30d (4w2d).</span></label></div>
    <p class="settings-note">Combine weeks (w), days (d), hours (h), minutes (m) and seconds (s): <code>4w1d2h</code> = 29 days and 2 hours.</p>
    <label>New external-account admission<select bind:value={settings.signup_admission}><option value="invite_only">Invitation only</option><option value="approval_required">Administrator approval required</option></select></label>
    <p class="settings-note">Enrolled authenticators are always required. Maximum 20 sessions per account. Lifetime changes apply to new sessions. Configuration version {settings.version}.</p>
    <button class="settings-primary" disabled={busy}>Save sign-in policy</button>
  </form>{/if}
  <section class="settings-section"><div class="flex flex-wrap justify-between items-center gap-3"><h2 class="settings-section-title">Identity providers</h2><button class="settings-button" disabled={busy} onclick={() => edit()}>Add provider</button></div>
    <p class="settings-note">Every provider is an OAuth2 authorization-code client with explicitly configured endpoints. There is no issuer URL and no discovery, so a sign-in never depends on a remote document. Provider claims grant permissions only through workspace mappings.</p>
    {#if providers.length === 0}<p class="settings-note">No identity providers configured.</p>{/if}
    <ul class="settings-list">{#each providers as p}<li class="space-y-2"><div class="flex flex-wrap justify-between gap-3"><div><strong>{p.label}</strong><p class="settings-note">{p.enabled ? 'Enabled' : 'Disabled'} · version {p.version}</p></div><div class="flex gap-2"><button class="settings-button" disabled={busy} onclick={() => edit(p)}>Edit</button><button class="settings-button" disabled={busy || !p.enabled} onclick={() => run(async () => { await identityAPI.delete(`identity-providers/${encodeURIComponent(p.id)}`, { data: { version: p.version } }); await load(); })}>Disable</button></div></div><p class="settings-note break-all">Callback: {new URL(`auth/external/${encodeURIComponent(p.id)}/callback`, document.baseURI).href}</p></li>{/each}</ul>
    {#if editing}<form class="settings-section" onsubmit={saveProvider}>
      <h3 class="settings-subsection-title">{editing.id ? `Edit ${editing.label}` : 'New identity provider'}</h3>
      <label>Display label<input bind:value={editing.label} required /></label><label><input type="checkbox" bind:checked={editing.enabled} />Enabled</label>
      <label>Client ID<input bind:value={editing.client_id} required /></label>
      <label>Client secret<input type="password" bind:value={secret} disabled={clearSecret} autocomplete="new-password" /><span class="settings-note">{editing.has_client_secret ? 'A secret is stored. Leave blank to preserve it.' : 'No secret is stored.'}</span></label><label><input type="checkbox" bind:checked={clearSecret} onchange={() => { if (clearSecret) secret = ''; }} />Clear stored client secret</label>
      <label>Scopes (space separated)<input bind:value={scopes} required /></label>
      <label>Role claim paths (one per line)<textarea bind:value={rolesClaims} rows={3} spellcheck="false" placeholder="realm_access.roles&#10;resource_access.*.roles"></textarea><span class="settings-note">Dot paths into the token's claims, with <code>*</code> matching every key of an object or element of an array. Their values join the roles list that workspace permission mappings match with claim kind <code>roles</code>. Leave empty to record only top-level roles, groups, permissions and scope claims. Values are whitespace-split, so a role containing a space is not addressable. Up to 16 paths, 8 segments each.</span></label>
      <label>Client authentication<select bind:value={editing.auth_header_style}><option value="client_secret_basic">HTTP Basic</option><option value="client_secret_post">Request body</option></select></label>
      <label>Stable subject claim<input bind:value={editing.subject_claim} required placeholder="sub" /><span class="settings-note">The claim read as this account's permanent identifier. It must never be reassigned between people, so prefer an opaque ID (<code>sub</code>, GitHub's <code>node_id</code>) over an email address or username.</span></label>
      <h4 class="settings-subsection-title">Endpoints</h4>
      <p class="settings-note">Enter each endpoint as published by the provider. HTTPS is required (loopback HTTP is allowed in development).</p>
      <label>Authorization URL<input type="url" bind:value={editing.auth_url} required placeholder="https://login.example.com/oauth2/authorize" /></label>
      <label>Token URL<input type="url" bind:value={editing.token_url} required placeholder="https://login.example.com/oauth2/token" /></label>
      <label>User info URL<input type="url" bind:value={editing.userinfo_url} placeholder="https://login.example.com/oauth2/userinfo" /></label>
      <label>JWKS URL<input type="url" bind:value={editing.jwks_url} placeholder="https://login.example.com/oauth2/keys" /></label>
      <p class="settings-note">Configure the user info endpoint, the JWKS endpoint, or both — at least one is required, because the identity has to come from a source the browser could not have written. User info is read with the access token. JWKS instead verifies the returned <code>id_token</code>'s signature, audience, expiry and nonce; the <code>iss</code> claim is not checked, since no issuer is configured. With both, the user info subject must also match the <code>id_token</code> subject.</p>
      {#if claimSourceMissing}<p role="alert" class="settings-error">Add a user info URL or a JWKS URL before saving.</p>{/if}
      <p class="settings-note">Edits revoke linked users’ sessions. Changing the client ID or subject claim of a provider that already has linked accounts requires a new provider.</p><div class="flex gap-3"><button class="settings-primary" disabled={busy || claimSourceMissing}>Save provider</button><button type="button" class="settings-button" disabled={busy} onclick={() => { editing = null; secret = ''; }}>Cancel</button></div>
    </form>{/if}
  </section>
{/if}</div>
