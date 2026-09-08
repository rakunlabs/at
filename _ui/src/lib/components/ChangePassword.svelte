<script lang="ts">
  import { authErrorMessage, changeAuthPassword, getAuthIdentity, isAuthUnauthorized, passwordPolicyError } from '@/lib/api/auth';
  import { returnToLogin, storeAuth } from '@/lib/store/auth.svelte';

  let current = $state('');
  let password = $state('');
  let confirmation = $state('');
  let busy = $state(false);
  let error = $state('');
  const inputClass = 'w-full rounded-md border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-2.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent';

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    error = passwordPolicyError(password) || (password !== confirmation ? 'New passwords do not match.' : '');
    if (error || !window.confirm('Change your password? All your sessions will end, including this one. Sign in again with your new password.')) return;
    busy = true;
    try {
      await changeAuthPassword(current, password);
      returnToLogin();
    } catch (e) {
      error = authErrorMessage(e, 'Could not change your password. Check your connection and retry.');
      // Incorrect current password is also 401. Only discard a session proven invalid.
      if (isAuthUnauthorized(e)) {
        try { storeAuth.identity = await getAuthIdentity(); }
        catch (sessionError) { if (isAuthUnauthorized(sessionError)) returnToLogin(); }
      }
    } finally {
      current = password = confirmation = '';
      busy = false;
    }
  }
</script>

<section aria-labelledby="change-password-title" class="space-y-4">
  <div>
    <h2 id="change-password-title" class="text-base font-semibold">Change your password</h2>
    <p id="password-policy" class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Use at least 15 characters (maximum 1024 UTF-8 bytes). Changing your password signs you out on every device.</p>
  </div>
  <form onsubmit={submit} aria-busy={busy} class="space-y-4 max-w-md">
    <fieldset disabled={busy} class="space-y-4">
      <div><label for="current-password" class="block text-sm font-medium mb-1.5">Current password</label>
        <input id="current-password" type="password" autocomplete="current-password" required bind:value={current} class={inputClass} /></div>
      <div><label for="new-password" class="block text-sm font-medium mb-1.5">New password</label>
        <input id="new-password" type="password" autocomplete="new-password" required bind:value={password} aria-describedby="password-policy" class={inputClass} /></div>
      <div><label for="confirm-password" class="block text-sm font-medium mb-1.5">Confirm new password</label>
        <input id="confirm-password" type="password" autocomplete="new-password" required bind:value={confirmation} class={inputClass} /></div>
    </fieldset>
    {#if error}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{error}</p>{/if}
    <button disabled={busy} class="rounded-md bg-gray-900 dark:bg-accent text-white dark:text-gray-950 px-4 py-2.5 text-sm font-medium hover:bg-gray-800 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50">{busy ? 'Changing password...' : 'Change password'}</button>
  </form>
</section>
