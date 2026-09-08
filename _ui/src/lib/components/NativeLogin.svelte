<script lang="ts">
  import { onMount } from 'svelte';
  import { authErrorMessage, beginPasskeyLogin, finishPasskeyLogin, isAuthUnauthorized, loginWithPassword } from '@/lib/api/auth';
  import { isPasskeyCancellation, isWebAuthnSupported, startAuthentication } from '@/lib/helper/webauthn';
  import { storeAuth } from '@/lib/store/auth.svelte';
  interface Props { onlogin: () => Promise<void>; sessionNotice?: string }
  let { onlogin, sessionNotice = '' }: Props = $props();
  let username = $state('');
  let password = $state('');
  let busy = $state(false);
  let error = $state('');
  let remember = $state(false);
  let supported = $state(false);
  let notice = $state('');
  let cancellable = $state(false);
  let controller: AbortController | undefined;

  onMount(() => {
    supported = isWebAuthnSupported();
    return () => { controller?.abort(); password = ''; };
  });

  async function login(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    notice = '';
    const pending = controller = new AbortController();
    try {
      await loginWithPassword(username, password, remember, pending.signal);
      password = '';
      if (!pending.signal.aborted) await onlogin();
    } catch (e) {
      if (!pending.signal.aborted) error = isAuthUnauthorized(e) ? 'Username or password is incorrect.' : authErrorMessage(e, 'Cannot sign in. Check your connection and try again.');
    } finally {
      password = '';
      controller = undefined;
      busy = false;
    }
  }

  async function passkeyLogin() {
    if (busy || !supported || !storeAuth.passkeys) return;
    password = '';
    error = notice = '';
    if (!username.trim()) { error = 'Enter your username first, then choose Sign in with passkey.'; return; }
    busy = cancellable = true;
    const pending = controller = new AbortController();
    try {
      const options = await beginPasskeyLogin(username, remember, pending.signal);
      notice = 'Follow your browser prompt to use a passkey or security key.';
      const credential = await startAuthentication(options.publicKey, pending.signal);
      if (!credential || pending.signal.aborted) { notice = 'No passkey selected. Try again or use your password.'; return; }
      cancellable = false;
      notice = 'Signing in...';
      await finishPasskeyLogin(credential, pending.signal);
      if (!pending.signal.aborted) await onlogin();
    } catch (e) {
      notice = '';
      if (pending.signal.aborted || isPasskeyCancellation(e)) notice = 'Passkey sign-in cancelled or timed out. Try again or use your password.';
      else error = authErrorMessage(e, 'Could not use this passkey. Start again or sign in with your password.');
    } finally {
      password = '';
      busy = cancellable = false;
      controller = undefined;
    }
  }
</script>

<main class="min-h-full flex items-center justify-center bg-gray-50 dark:bg-dark-base px-6 py-12">
  <div class="w-full max-w-sm">
    <h1 class="text-2xl font-semibold text-gray-900 dark:text-dark-text">Sign in to AT</h1>
    <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">
      Administrator access to this server. Your account is managed by its operator.
    </p>
    {#if sessionNotice}<p role="status" class="mt-4 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">{sessionNotice}</p>{/if}
    <form class="mt-8 space-y-5" onsubmit={login} aria-busy={busy}>
      <div>
        <label for="auth-username" class="block text-sm font-medium mb-2">Username</label>
        <input id="auth-username" name="username" bind:value={username} disabled={busy} autocomplete="username" autocapitalize="none" spellcheck={false} required maxlength={128}
          class="w-full rounded-md border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-2.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" />
      </div>
      <div>
        <label for="auth-password" class="block text-sm font-medium mb-2">Password</label>
        <input id="auth-password" name="password" type="password" bind:value={password} disabled={busy} autocomplete="current-password" required maxlength={1024}
          class="w-full rounded-md border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-2.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" />
      </div>
      <label class="flex items-center gap-3 text-sm leading-6"><input type="checkbox" bind:checked={remember} disabled={busy} class="size-4 accent-accent" />Remember me for 30 days</label>
      {#if error}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{error}</p>{/if}
      {#if notice}<p role="status" class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">{notice}</p>{/if}
      <button type="submit" disabled={busy} class="w-full rounded-md bg-accent px-4 py-3 text-sm font-semibold text-gray-950 hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-60 disabled:cursor-wait">
        {busy ? 'Signing in...' : 'Sign in with password'}
      </button>
      {#if storeAuth.passkeys}
        <button type="button" onclick={passkeyLogin} disabled={busy || !supported} class="w-full rounded-md border border-gray-300 dark:border-dark-border px-4 py-3 text-sm font-medium hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed">Sign in with passkey</button>
        <p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">{supported ? 'Enter your username to use a saved passkey or security key. No password is needed.' : 'Passkeys need a supported browser and a secure connection. You can still sign in with your password.'}</p>
      {/if}
      {#if cancellable}<button type="button" onclick={() => controller?.abort()} class="w-full rounded-md border border-gray-300 dark:border-dark-border px-4 py-3 text-sm font-medium hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent">Cancel passkey sign-in</button>{/if}
    </form>
    <p class="mt-8 border-t border-gray-200 dark:border-dark-border pt-5 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">
      First administrator? Ask the server operator to complete the token-protected bootstrap. Public registration is not available.
    </p>
  </div>
</main>
