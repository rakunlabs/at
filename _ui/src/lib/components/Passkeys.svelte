<script lang="ts">
  import { onMount } from 'svelte';
  import { authErrorMessage, beginPasskeyEnrollment, deleteAuthPasskey, finishPasskeyEnrollment, isAuthUnauthorized, listAuthPasskeys, type AuthPasskey } from '@/lib/api/auth';
  import { isPasskeyCancellation, isWebAuthnSupported, startRegistration } from '@/lib/helper/webauthn';
  import { returnToLogin, storeAuth } from '@/lib/store/auth.svelte';

  let keys = $state<AuthPasskey[]>([]);
  let loaded = $state(false);
  let busy = $state(false);
  let supported = $state(false);
  let name = $state('');
  let password = $state('');
  let deleting = $state<AuthPasskey | null>(null);
  let error = $state('');
  let notice = $state('');
  let cancellable = $state(false);
  let controller: AbortController | undefined;
  const inputClass = 'w-full rounded-md border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-2.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent';
  const buttonClass = 'rounded-md border border-gray-300 dark:border-dark-border px-3 py-2.5 text-sm font-medium hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed';
  const primaryClass = 'rounded-md bg-gray-900 dark:bg-accent text-white dark:text-gray-950 px-4 py-2.5 text-sm font-medium hover:bg-gray-800 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed';

  async function load() {
    if (busy || !storeAuth.passkeys) return;
    busy = true;
    error = '';
    const pending = controller = new AbortController();
    try {
      const result = await listAuthPasskeys(pending.signal);
      if (!pending.signal.aborted) { keys = result.items; loaded = true; }
    } catch (e) {
      if (!pending.signal.aborted) {
        if (isAuthUnauthorized(e)) returnToLogin();
        error = authErrorMessage(e, 'Could not load passkeys. Check your connection and reload the list.');
      }
    } finally { busy = false; controller = undefined; }
  }

  onMount(() => {
    supported = isWebAuthnSupported();
    void load();
    return () => { controller?.abort(); password = ''; };
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !storeAuth.passkeys || (!deleting && (!supported || !loaded || keys.length >= 20))) return;
    error = notice = '';
    if (!deleting && (!name.trim() || new TextEncoder().encode(name.trim()).length > 80 || /\p{Cc}/u.test(name))) {
      error = 'Use a name of 1-80 UTF-8 bytes without control characters.';
      password = '';
      return;
    }
    if (deleting && !window.confirm(`Delete passkey "${deleting.name}"? All your sessions will end, including this one. Sign in again with your password or another passkey.`)) { password = ''; return; }
    busy = true;
    const pending = controller = new AbortController();
    try {
      if (deleting) {
        await deleteAuthPasskey(deleting.id, password, pending.signal);
        password = '';
        if (!pending.signal.aborted) returnToLogin();
        return;
      }
      cancellable = true;
      const options = await beginPasskeyEnrollment(name.trim(), password, pending.signal);
      password = '';
      notice = 'Follow your browser prompt to save a passkey or use a security key.';
      const credential = await startRegistration(options.publicKey, pending.signal);
      if (!credential || pending.signal.aborted) { notice = 'No passkey created. You can try again.'; return; }
      cancellable = false;
      notice = 'Saving passkey...';
      await finishPasskeyEnrollment(credential, pending.signal);
      name = '';
      notice = 'Passkey added. Your password remains available as a fallback.';
      const result = await listAuthPasskeys(pending.signal);
      if (!pending.signal.aborted) { keys = result.items; loaded = true; }
    } catch (e) {
      password = '';
      notice = '';
      if (pending.signal.aborted || isPasskeyCancellation(e)) notice = 'Passkey setup cancelled or timed out. You can try again.';
      else {
        error = authErrorMessage(e, 'Could not complete the request. Check your connection and try again.');
        if (e instanceof Error && e.name === 'InvalidStateError') error = 'This passkey is already registered. Try a different authenticator or reload the list.';
        // Reconcile ambiguous commits. Unlike reauthentication, a list 401 means the session ended.
        try {
          const result = await listAuthPasskeys(pending.signal);
          if (!pending.signal.aborted) { keys = result.items; loaded = true; }
        } catch (listError) {
          if (!pending.signal.aborted) {
            if (isAuthUnauthorized(listError)) returnToLogin();
            loaded = false;
            error += ' Reload passkeys before retrying; the change may have been saved.';
          }
        }
      }
    } finally { password = ''; busy = cancellable = false; controller = undefined; }
  }
</script>

<section aria-labelledby="passkeys-title" aria-busy={busy} class="space-y-4">
  <div>
    <h2 id="passkeys-title" class="text-base font-semibold">Your passkeys</h2>
    <p class="mt-1 max-w-2xl text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Sign in with a passkey or security key instead of a password. Passkeys are bound to this server's domain; keep your password available if the domain changes or a key is unavailable. You can save up to 20 passkeys.</p>
  </div>
  {#if !storeAuth.passkeys}
    <p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Passkeys are not enabled for this server. Continue using your password.</p>
  {:else}
    <div class="flex flex-wrap items-center justify-between gap-3 max-w-2xl">
      <p role="status" class="text-sm text-gray-600 dark:text-dark-text-secondary">{!loaded ? (busy ? 'Loading passkeys...' : 'Passkey list unavailable') : `${keys.length} of 20 passkeys`}</p>
      <button class={buttonClass} disabled={busy} onclick={load}>Reload passkeys</button>
    </div>
    {#if loaded && keys.length === 0}<p class="text-sm text-gray-600 dark:text-dark-text-secondary">No passkeys yet. Add one below to use it at your next sign-in.</p>{/if}
    <ul class="max-w-2xl divide-y divide-gray-200 dark:divide-dark-border">
      {#each keys as key (key.id)}
        <li class="flex flex-wrap items-start justify-between gap-3 py-3">
          <div class="min-w-0 flex-1">
            <h3 class="text-sm font-semibold break-all">{key.name}</h3>
            <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Added <time datetime={key.created_at}>{new Date(key.created_at).toLocaleString()}</time><br />Last used: {#if key.last_used_at}<time datetime={key.last_used_at}>{new Date(key.last_used_at).toLocaleString()}</time>{:else}Never{/if}</p>
          </div>
          <button class={buttonClass} disabled={busy} aria-label={`Delete passkey ${key.name}`} aria-expanded={deleting?.id === key.id} aria-controls="passkey-form" onclick={() => { deleting = key; password = error = notice = ''; }}>Delete</button>
        </li>
      {/each}
    </ul>
    {#if !supported}<p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Adding a passkey needs a supported browser and a secure connection. You can still manage existing keys and sign in with your password.</p>{/if}
    {#if keys.length >= 20}<p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">You have reached the 20-passkey limit. Delete an unused key before adding another. Deletion signs you out everywhere.</p>{/if}
    {#if deleting || (supported && loaded && keys.length < 20)}
      <form id="passkey-form" onsubmit={submit} class="max-w-md space-y-4 border-t border-gray-200 dark:border-dark-border pt-4">
        <h3 class="text-sm font-semibold break-all">{deleting ? `Delete ${deleting.name}` : 'Add a passkey'}</h3>
        <fieldset disabled={busy} class="space-y-4">
          {#if !deleting}<div><label for="passkey-name" class="block text-sm font-medium mb-1.5">Passkey name</label><input id="passkey-name" bind:value={name} required maxlength={80} autocomplete="off" placeholder="e.g. Personal laptop" aria-describedby="passkey-name-help" class={inputClass} /><p id="passkey-name-help" class="mt-1.5 text-sm text-gray-600 dark:text-dark-text-secondary">A name you will recognize, up to 80 UTF-8 bytes.</p></div>{/if}
          <div><label for="passkey-password" class="block text-sm font-medium mb-1.5">Current password</label><input id="passkey-password" type="password" bind:value={password} required autocomplete="current-password" class={inputClass} /></div>
        </fieldset>
        {#if deleting}<p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Deleting this key ends all your sessions. You will need to sign in again.</p>{/if}
        <div class="flex flex-wrap gap-2">
          <button disabled={busy} class={primaryClass}>{busy ? 'Working...' : deleting ? 'Delete passkey and sign out' : 'Add passkey'}</button>
          {#if deleting}<button type="button" disabled={busy} class={buttonClass} onclick={() => { deleting = null; password = error = ''; }}>Cancel deletion</button>{/if}
          {#if cancellable}<button type="button" class={buttonClass} onclick={() => controller?.abort()}>Cancel passkey setup</button>{/if}
        </div>
      </form>
    {/if}
    {#if error}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{error}</p>{/if}
    {#if notice}<p role="status" class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">{notice}</p>{/if}
  {/if}
</section>
