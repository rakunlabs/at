<script lang="ts">
  import AuthShell from './AuthShell.svelte';
  import { onMount, tick } from 'svelte';
  import { ChevronDown, ChevronRight, KeyRound } from 'lucide-svelte';
  import { beginPasskeyLogin, finishPasskeyLogin, loginWithPassword, loginErrorMessage, type LoginResult, type MFAChallenge } from '../api/auth';
  import { identityAPI, verifyMFA } from '../api/identity';
  import { externalPopup } from '../helper/auth-popup';
  import { isWebAuthnSupported, startAuthentication } from '../helper/webauthn';
  import { storeAuth, authOrigins } from '../store/auth.svelte';
  import { authSession } from '../api/transport';
  let { onlogin, sessionNotice = '' }: { onlogin: () => Promise<void>; sessionNotice?: string } = $props();
  let username = $state(''); let password = $state(''); let code = $state(''); let remember = $state(false);
  let busy = $state(false); let error = $state(''); let providerError = $state('');
  let providers = $state<{id: string; label: string}[]>([]); let mfa = $state<MFAChallenge | null>(null);
  let wrongOrigin = $derived(!!authOrigins.primary && location.origin !== authOrigins.primary && !authOrigins.allowed.includes(location.origin));
  let secondaryOrigin = $derived(!!authOrigins.primary && location.origin !== authOrigins.primary);
  let continuation = ''; const controller = new AbortController();
  // Collapsing hides the local form behind a disclosure; it never disables it,
  // so the reveal control is always present while local sign-in is enabled.
  let revealed = $state(false); let usernameInput = $state<HTMLInputElement>();
  let collapsed = $derived(storeAuth.localLogin && storeAuth.localLoginCollapsed && !mfa && !revealed);
  let showLocal = $derived(storeAuth.localLogin && !collapsed);
  async function toggleLocal() { revealed = !revealed; error = ''; if (revealed) { await tick(); usernameInput?.focus(); } }
  async function loadProviders() { try { providers = (await identityAPI.get('login-providers')).data || []; providerError = ''; } catch { providerError = 'Sign-in providers are unavailable. Retry loading them.'; } }
  onMount(() => { void loadProviders(); return () => { controller.abort(); password = code = ''; }; });
  async function complete(result: LoginResult) {
    if ('mfa_required' in result && result.mfa_required) { mfa = result; return; }
    if (!('subject' in result) || !result.subject) throw new Error('Invalid login result');
    if (continuation) window.location.assign(continuation);
    else { await onlogin(); if (!storeAuth.identity) throw new Error('Sign-in was accepted, but the browser session could not be verified. Check that cookies are allowed for this site.'); }
  }
  async function run(action: () => Promise<LoginResult>, popupFlow = false) {
    if (busy) return; busy = true; error = '';
    try { await complete(await action()); }
    catch (e) { if (!controller.signal.aborted) error = popupFlow && e instanceof Error ? e.message : mfa ? 'The code could not be verified. Try a fresh authenticator or unused backup code, or restart sign-in.' : loginErrorMessage(e); }
    finally { busy = false; password = code = ''; }
  }
  function external(id: string) {
    const popup = externalPopup(id, { purpose: 'login', remember_me: remember, ...(location.hash.startsWith('#/mobile-authorize?') ? { mobile_request_id: new URLSearchParams(location.hash.split('?')[1]).get('request_id') } : {}) }, controller.signal);
    void run(async () => { const result = await popup; continuation = result.continue || ''; if ('subject' in result.result && result.result.subject) await authSession.adoptExternalLogin(result.result.subject); return result.result as LoginResult; }, true);
  }
</script>
<AuthShell
  title={mfa ? 'Verify your sign-in' : `Sign in to ${storeAuth.title}`}
  subtitle={mfa ? 'Enter your authenticator code or an unused backup code.' : 'Use your account to access your workspaces.'}
>
  {#snippet action()}
    {#if storeAuth.localLogin && storeAuth.localLoginCollapsed && !mfa}
      <button type="button" class="settings-button px-2 py-1" aria-expanded={revealed} aria-controls="local-login" onclick={toggleLocal}>
        <KeyRound size={12} />Local sign-in{#if revealed}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
      </button>
    {/if}
  {/snippet}
  {#if sessionNotice}<p role="status" class="settings-note">{sessionNotice}</p>{/if}
  {#if wrongOrigin}
    <div role="alert" class="border border-amber-300 dark:border-amber-900/40 bg-amber-50 dark:bg-amber-950/20 p-3 text-xs leading-relaxed text-amber-900 dark:text-amber-200">
      <p class="font-medium">This address is not configured for sign-in</p>
      <p class="mt-1 break-words">You opened {location.origin}. The primary address is {authOrigins.primary}. Ask an administrator to update Authentication settings from an existing session.</p>
    </div>
  {/if}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
  {#if mfa}
    <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(() => verifyMFA(mfa!.challenge, code)); }}>
      <label>Authentication or backup code<input bind:value={code} autocomplete="one-time-code" required /></label>
      <button class="settings-primary w-full min-h-11 sm:min-h-0" disabled={busy}>{busy ? 'Verifying…' : 'Verify and sign in'}</button>
      <button type="button" class="settings-button w-full min-h-11 sm:min-h-0" disabled={busy} onclick={() => { mfa = null; code = error = continuation = ''; }}>Restart sign-in</button>
    </form>
  {:else}
    {#if showLocal}
      <form id="local-login" class="space-y-4" onsubmit={e => { e.preventDefault(); void run(() => loginWithPassword(username, password, remember, controller.signal)); }}>
        <label>Username<input bind:this={usernameInput} bind:value={username} required autocomplete="username" autocapitalize="none" maxlength="128" /></label>
        <label>Password<input type="password" bind:value={password} required autocomplete="current-password" /></label>
        <label><input type="checkbox" bind:checked={remember} disabled={busy} />Remember this sign-in</label>
        <button class="settings-primary w-full min-h-11 sm:min-h-0" disabled={busy}>{busy ? 'Signing in…' : 'Sign in with password'}</button>
        {#if storeAuth.passkeys && !secondaryOrigin}<button type="button" class="settings-button w-full min-h-11 sm:min-h-0" disabled={busy || !isWebAuthnSupported()} onclick={() => {
          if (!username.trim()) { error = 'Enter your username before using a passkey.'; return; }
          void run(async () => { const options = await beginPasskeyLogin(username, remember, controller.signal); const credential = await startAuthentication(options.publicKey, controller.signal); if (!credential) throw new Error('Cancelled'); return finishPasskeyLogin(credential, controller.signal); });
        }}>Sign in with passkey</button>{/if}
      </form>
    {:else}
      <label><input type="checkbox" bind:checked={remember} disabled={busy} />Remember this sign-in</label>
      {#if collapsed}<p class="settings-note">Signing in with a username and password is still available from the Local sign-in control above.</p>{/if}
    {/if}
    {#if secondaryOrigin}
      <div class="border-t border-gray-100 dark:border-dark-border pt-4 space-y-2">
        <p class="settings-note">Passkeys and external sign-in providers use the primary address.</p>
        <a class="settings-button w-full min-h-11 sm:min-h-0 break-all" href={`${authOrigins.primary}${new URL('.', document.baseURI).pathname}${location.hash}`}>Open primary sign-in</a>
      </div>
    {:else if providers.length}
      <div class="border-t border-gray-100 dark:border-dark-border pt-4 space-y-2">{#each providers as provider}<button class="settings-button w-full min-h-11 sm:min-h-0" disabled={busy} onclick={() => external(provider.id)}>Continue with {provider.label}</button>{/each}</div>
    {/if}
    {#if providerError}<div class="space-y-2"><p role="alert" class="settings-error">{providerError}</p><button class="settings-button" onclick={loadProviders}>Reload providers</button></div>{/if}
    <p class="settings-note border-t border-gray-100 dark:border-dark-border pt-4">Need account recovery? Use a backup code after your normal sign-in. If you have lost every sign-in method, ask an installation administrator for a recovery link.</p>
  {/if}
</AuthShell>
