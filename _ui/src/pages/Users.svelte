<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, RefreshCw } from 'lucide-svelte';
  import { authErrorMessage, createAuthUser, isAuthUnauthorized, listAuthUsers, passwordPolicyError, resetAuthUserPassword, revokeAuthUserSessions, setAuthUserEnabled, type AuthUser } from '@/lib/api/auth';
  import { isNativeAdmin, returnToLogin, storeAuth } from '@/lib/store/auth.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import ChangePassword from '@/lib/components/ChangePassword.svelte';
  import Passkeys from '@/lib/components/Passkeys.svelte';

  storeNavbar.title = 'Users';
  let users = $state<AuthUser[]>([]);
  let cursors = $state(['']);
  let page = $state(0);
  let nextCursor = $state('');
  let loading = $state(true);
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state('');
  let formError = $state('');
  let creating = $state(false);
  let username = $state('');
  let password = $state('');
  let admin = $state(false);
  let resetTarget = $state<AuthUser | null>(null);
  let resetPassword = $state('');
  const inputClass = 'w-full rounded-md border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-surface px-3 py-2.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent';
  const buttonClass = 'inline-flex items-center justify-center gap-2 rounded-md border border-gray-300 dark:border-dark-border px-3 py-2.5 text-sm font-medium hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed';
  const primaryClass = 'inline-flex items-center justify-center gap-2 rounded-md bg-gray-900 dark:bg-accent text-white dark:text-gray-950 px-4 py-2.5 text-sm font-medium hover:bg-gray-800 dark:hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50';

  async function load(target = page, after = cursors[target]) {
    if (!isNativeAdmin()) return;
    loading = true;
    error = '';
    try {
      const result = await listAuthUsers(after);
      users = result.data;
      nextCursor = result.next_cursor;
      cursors = [...cursors.slice(0, target), after];
      page = target;
      loaded = true;
      resetTarget = null;
      resetPassword = '';
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      error = authErrorMessage(e, 'Could not load users. Check your connection and retry.');
    } finally { loading = false; }
  }

  onMount(() => { if (isNativeAdmin()) void load(); });

  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    formError = passwordPolicyError(password);
    if (formError) return;
    if (admin && !window.confirm(`Give ${username} administrator access? Administrators control this entire AT installation, including secrets, files and users.`)) return;
    busy = true;
    try {
      const identity = await createAuthUser({ username, password, admin });
      creating = false;
      username = '';
      admin = false;
      addToast(`Created ${identity.name}. Share the password through a secure channel.`);
      await load(0, '');
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      formError = authErrorMessage(e, 'Could not create user. Check your connection and retry.');
    } finally { password = ''; busy = false; }
  }

  async function mutate(user: AuthUser, action: 'enable' | 'disable' | 'revoke' | 'password') {
    if (busy || loading) return;
    const self = user.id === storeAuth.identity?.subject;
    if (action === 'password') {
      formError = passwordPolicyError(resetPassword);
      if (formError) return;
    }
    const description = action === 'disable' ? 'Disable this account and end all its sessions? It cannot sign in until re-enabled.'
      : action === 'enable' ? 'Enable this account? Any existing sessions will also end.'
      : action === 'password' ? 'Replace this account password and end all its sessions? Its role and enabled status will not change.'
      : 'End all sessions for this account? It can sign in again with its existing password.';
    if (!window.confirm(`${user.username}\n\n${description}${self ? '\n\nThis is your account. You will need to sign in again.' : ''}`)) return;
    busy = true;
    error = '';
    formError = '';
    try {
      if (action === 'password') await resetAuthUserPassword(user.id, resetPassword);
      else if (action === 'revoke') await revokeAuthUserSessions(user.id);
      else await setAuthUserEnabled(user.id, action === 'enable');
      if (self) { returnToLogin(); return; }
      resetTarget = null;
      addToast(action === 'password' ? `Password reset for ${user.username}.` : action === 'revoke' ? `Sessions revoked for ${user.username}.` : `${user.username} ${action}d.`);
      await load();
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      const message = authErrorMessage(e, 'Could not update user. Check your connection and retry.');
      if (action === 'password') formError = message;
      else error = message;
    } finally { resetPassword = ''; busy = false; }
  }
</script>

<svelte:head><title>AT | Users</title></svelte:head>

{#if isNativeAdmin()}
  <main class="mx-auto max-w-6xl p-4 sm:p-6 space-y-6 text-gray-900 dark:text-dark-text">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div class="max-w-2xl">
        <h1 class="text-2xl font-semibold">Users</h1>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">Manage local accounts and access to this server. Administrators control the entire installation; other users can only manage their own password and passkeys.</p>
      </div>
      <button class={primaryClass} disabled={busy || loading} aria-expanded={creating} aria-controls="create-user" onclick={() => { creating = !creating; resetTarget = null; password = resetPassword = formError = ''; }}><Plus size={16} />{creating ? 'Close form' : 'Create user'}</button>
    </header>

    {#if creating}
      <section id="create-user" aria-labelledby="create-title" class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-4 sm:p-5">
        <h2 id="create-title" class="text-base font-semibold">Create local user</h2>
        <form onsubmit={create} aria-busy={busy} class="mt-4 space-y-4 max-w-xl">
          <fieldset disabled={busy} class="space-y-4">
            <div><label for="user-username" class="block text-sm font-medium mb-1.5">Username</label>
              <input id="user-username" bind:value={username} autocomplete="off" autocapitalize="none" spellcheck={false} required minlength={3} maxlength={128} pattern={'[a-zA-Z0-9._@+\\-]{3,128}'} aria-describedby="username-help" class={inputClass} />
              <p id="username-help" class="mt-1.5 text-sm text-gray-600 dark:text-dark-text-secondary">3-128 letters, digits, or . _ @ + -. Stored in lowercase.</p></div>
            <div><label for="user-password" class="block text-sm font-medium mb-1.5">Initial password</label>
              <input id="user-password" type="password" bind:value={password} autocomplete="new-password" required aria-describedby="create-password-help" class={inputClass} />
              <p id="create-password-help" class="mt-1.5 text-sm text-gray-600 dark:text-dark-text-secondary">At least 15 characters, at most 1024 UTF-8 bytes. Share securely; passwords cannot be retrieved later.</p></div>
            <label class="flex items-start gap-3 text-sm leading-6"><input type="checkbox" bind:checked={admin} class="mt-1 size-4 accent-accent" /><span>Administrator access<br /><span class="text-gray-600 dark:text-dark-text-secondary">Full access to secrets, files, tools, organizations and users. Roles cannot be edited after creation.</span></span></label>
          </fieldset>
          {#if formError}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{formError}</p>{/if}
          <div class="flex flex-wrap gap-2"><button disabled={busy} class={primaryClass}>{busy ? 'Creating user...' : 'Create user'}</button><button type="button" disabled={busy} class={buttonClass} onclick={() => { creating = false; password = formError = ''; }}>Cancel</button></div>
        </form>
      </section>
    {/if}

    <section aria-label="User accounts" aria-busy={loading || busy} class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-border px-4 py-3">
        <p role="status" class="text-sm text-gray-600 dark:text-dark-text-secondary">{loading ? 'Loading users...' : busy ? 'Updating user...' : loaded ? `${users.length} users on page ${page + 1}` : 'User list unavailable'}</p>
        <button class={buttonClass} disabled={busy || loading} onclick={() => load()}><RefreshCw size={14} />Refresh</button>
      </div>
      {#if error}<div class="p-4 flex flex-wrap items-center gap-3"><p role="alert" class="text-sm text-red-700 dark:text-red-300">{error}</p><button class={buttonClass} disabled={busy || loading} onclick={() => load()}>Reload users</button></div>{/if}
      {#if loaded && !users.length && !loading}
        <p class="p-6 text-sm text-gray-600 dark:text-dark-text-secondary">No users on this page. {page ? 'Return to the previous page or refresh the list.' : 'Create a local account to grant access.'}</p>
      {/if}
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">
        {#each users as user (user.id)}
          <li class="p-4">
            <div class="flex flex-col lg:flex-row lg:items-center justify-between gap-4">
              <div class="min-w-0 space-y-1.5">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="text-sm font-semibold break-all">{user.username}</h3>
                  {#if user.id === storeAuth.identity?.subject}<span class="text-xs text-gray-600 dark:text-dark-text-secondary">You</span>{/if}
                  <span class="border border-gray-200 dark:border-dark-border px-2 py-0.5 text-xs">{user.admin ? 'Administrator' : 'User'}</span>
                  <span class={['px-2 py-0.5 text-xs font-medium', user.disabled ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300' : 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400']}>{user.disabled ? 'Disabled' : 'Enabled'}</span>
                </div>
                <p class="font-mono text-xs text-gray-600 dark:text-dark-text-secondary break-all">{user.id}</p>
              </div>
              <div class="flex flex-wrap gap-2 shrink-0">
                <button class={buttonClass} disabled={busy || loading} aria-expanded={resetTarget?.id === user.id} aria-controls={resetTarget?.id === user.id ? 'reset-user-password' : undefined} onclick={() => { resetTarget = resetTarget?.id === user.id ? null : user; creating = false; password = resetPassword = formError = ''; }}>Reset password</button>
                <button class={buttonClass} disabled={busy || loading} onclick={() => mutate(user, 'revoke')}>Revoke sessions</button>
                <button class={buttonClass} disabled={busy || loading || (!user.disabled && user.id === storeAuth.identity?.subject)} title={user.id === storeAuth.identity?.subject ? 'You cannot disable your own account' : undefined} onclick={() => mutate(user, user.disabled ? 'enable' : 'disable')}>{user.disabled ? 'Enable' : 'Disable'}</button>
              </div>
            </div>
            {#if resetTarget?.id === user.id}
              <form id="reset-user-password" onsubmit={(event) => { event.preventDefault(); void mutate(user, 'password'); }} aria-busy={busy} class="mt-5 border-t border-gray-200 dark:border-dark-border pt-4 max-w-md space-y-3">
                <label for="reset-password" class="block text-sm font-medium">New password for {user.username}</label>
                <input id="reset-password" type="password" required autocomplete="new-password" disabled={busy} bind:value={resetPassword} aria-describedby="reset-help" class={inputClass} />
                <p id="reset-help" class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">At least 15 characters, at most 1024 UTF-8 bytes. All sessions will end.{user.id === storeAuth.identity?.subject ? ' You will be signed out.' : ' Share the new password securely.'}</p>
                {#if formError}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{formError}</p>{/if}
                <div class="flex flex-wrap gap-2"><button disabled={busy || loading} class={primaryClass}>{busy ? 'Resetting password...' : 'Reset password'}</button><button type="button" disabled={busy} class={buttonClass} onclick={() => { resetTarget = null; resetPassword = formError = ''; }}>Cancel</button></div>
              </form>
            {/if}
          </li>
        {/each}
      </ul>
      <nav aria-label="User list pagination" class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 dark:border-dark-border px-4 py-3">
        <span class="text-sm text-gray-600 dark:text-dark-text-secondary">50 per page</span>
        <div class="flex gap-2"><button class={buttonClass} disabled={busy || loading || page === 0} onclick={() => load(page - 1)}>Previous</button><button class={buttonClass} disabled={busy || loading || !nextCursor} onclick={() => load(page + 1, nextCursor)}>Next</button></div>
      </nav>
    </section>

    <div class="border-t border-gray-200 dark:border-dark-border pt-6"><ChangePassword /></div>
    <div class="border-t border-gray-200 dark:border-dark-border pt-6"><Passkeys /></div>
  </main>
{/if}
