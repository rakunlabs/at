<script lang="ts">
  import { onMount } from 'svelte';
  import { beginPasskeyLogin, finishPasskeyLogin, loginWithPassword, type LoginResult, type MFAChallenge } from '../api/auth';
  import { identityAPI, verifyMFA } from '../api/identity';
  import { externalPopup } from '../helper/auth-popup';
  import { isWebAuthnSupported, startAuthentication } from '../helper/webauthn';
  import { storeAuth } from '../store/auth.svelte';
  import { authSession } from '../api/transport';
  let { onlogin, sessionNotice = '' }: { onlogin: () => Promise<void>; sessionNotice?: string } = $props();
  let username = $state(''); let password = $state(''); let code = $state(''); let remember = $state(false);
  let busy = $state(false); let error = $state(''); let providerError = $state('');
  let providers = $state<{id: string; label: string}[]>([]); let mfa = $state<MFAChallenge | null>(null);
  let continuation = ''; const controller = new AbortController();
  async function loadProviders() { try { providers = (await identityAPI.get('login-providers')).data || []; providerError = ''; } catch { providerError = 'Sign-in providers are unavailable. Retry loading them.'; } }
  onMount(() => { void loadProviders(); return () => { controller.abort(); password = code = ''; }; });
  async function complete(result: LoginResult) {
    if ('mfa_required' in result && result.mfa_required) { mfa = result; return; }
    if (!('subject' in result) || !result.subject) throw new Error('Invalid login result');
    if (continuation) window.location.assign(continuation);
    else await onlogin();
  }
  async function run(action: () => Promise<LoginResult>, popupFlow = false) {
    if (busy) return; busy = true; error = '';
    try { await complete(await action()); }
    catch (e) { if (!controller.signal.aborted) error = popupFlow && e instanceof Error ? e.message : mfa ? 'The code could not be verified. Try a fresh authenticator or unused backup code, or restart sign-in.' : 'Sign-in could not be completed. Check your credentials and try again.'; }
    finally { busy = false; password = code = ''; }
  }
  function external(id: string) {
    const popup = externalPopup(id, { purpose: 'login', remember_me: remember, ...(location.hash.startsWith('#/mobile-authorize?') ? { mobile_request_id: new URLSearchParams(location.hash.split('?')[1]).get('request_id') } : {}) }, controller.signal);
    void run(async () => { const result = await popup; continuation = result.continue || ''; if ('subject' in result.result && result.result.subject) await authSession.adoptExternalLogin(result.result.subject); return result.result as LoginResult; }, true);
  }
</script>
<main class="min-h-full flex items-center justify-center px-6 py-12"><div class="w-full max-w-sm settings-form">
  <h1 class="text-2xl font-semibold">{mfa ? 'Verify your sign-in' : `Sign in to ${storeAuth.title}`}</h1>
  <p class="settings-note mt-2">{mfa ? 'Enter your authenticator code or an unused backup code.' : 'Use your account to access your workspaces.'}</p>
  {#if sessionNotice}<p role="status" class="settings-note mt-4">{sessionNotice}</p>{/if}
  {#if mfa}
    <form class="mt-8 space-y-5" onsubmit={e => { e.preventDefault(); void run(() => verifyMFA(mfa!.challenge, code)); }}>
      <label>Authentication or backup code<input bind:value={code} autocomplete="one-time-code" required /></label>
      <button class="settings-primary w-full" disabled={busy}>{busy ? 'Verifying…' : 'Verify and sign in'}</button>
      <button type="button" class="settings-button w-full" disabled={busy} onclick={() => { mfa = null; code = error = continuation = ''; }}>Restart sign-in</button>
    </form>
  {:else}
    {#if storeAuth.localLogin}
      <form class="mt-8 space-y-5" onsubmit={e => { e.preventDefault(); void run(() => loginWithPassword(username, password, remember, controller.signal)); }}>
        <label>Username<input bind:value={username} required autocomplete="username" autocapitalize="none" maxlength="128" /></label>
        <label>Password<input type="password" bind:value={password} required autocomplete="current-password" /></label>
        <label><input type="checkbox" bind:checked={remember} disabled={busy} />Remember this sign-in</label>
        <button class="settings-primary w-full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in with password'}</button>
        {#if storeAuth.passkeys}<button type="button" class="settings-button w-full" disabled={busy || !isWebAuthnSupported()} onclick={() => {
          if (!username.trim()) { error = 'Enter your username before using a passkey.'; return; }
          void run(async () => { const options = await beginPasskeyLogin(username, remember, controller.signal); const credential = await startAuthentication(options.publicKey, controller.signal); if (!credential) throw new Error('Cancelled'); return finishPasskeyLogin(credential, controller.signal); });
        }}>Sign in with passkey</button>{/if}
      </form>
    {:else}<label class="mt-6"><input type="checkbox" bind:checked={remember} disabled={busy} />Remember this sign-in</label>{/if}
    <div class="space-y-3 mt-6">{#each providers as provider}<button class="settings-button w-full" disabled={busy} onclick={() => external(provider.id)}>Continue with {provider.label}</button>{/each}</div>
    {#if providerError}<p role="alert" class="settings-error mt-4">{providerError}</p><button class="settings-button mt-2" onclick={loadProviders}>Reload providers</button>{/if}
    <p class="settings-note mt-8">Need account recovery? Use a backup code after your normal sign-in. If you have lost every sign-in method, ask an installation administrator for a recovery link.</p>
  {/if}
  {#if error}<p role="alert" class="settings-error mt-4">{error}</p>{/if}
</div></main>
