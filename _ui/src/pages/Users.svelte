<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, RefreshCw, Search, Trash2, ChevronDown, ChevronRight, X, KeyRound, LogOut, Unlock, Power, Users as UsersIcon } from 'lucide-svelte';
  import { authErrorMessage, authUserLabel, authUserMeta, createAuthUser, deleteAuthUser, getAuthUser, isAuthUnauthorized, listAuthUsers, passwordPolicyError, resetAuthUserPassword, revokeAuthUserSessions, setAuthUserEnabled, unlockAuthUserLogin, type AuthUser, type AuthUserDetail } from '@/lib/api/auth';
  import { identityAPI } from '@/lib/api/identity';
  import { isNativeAdmin, returnToLogin, storeAuth } from '@/lib/store/auth.svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import AdminRecovery from '@/lib/components/AdminRecovery.svelte';
  import UserLoginHistory from '@/lib/components/UserLoginHistory.svelte';

  storeNavbar.title = 'Users';
  let users = $state<AuthUser[]>([]);
  let cursors = $state(['']);
  let page = $state(0);
  let nextCursor = $state('');
  let search = $state('');
  let searchTimer = 0;
  let loading = $state(true);
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state('');
  let formError = $state('');
  let creating = $state(false);
  let username = $state('');
  let password = $state('');
  let admin = $state(false);
  let resetTarget = $state('');
  let resetPassword = $state('');
  // One row is expanded at a time: the detail is a per-account query, and two
  // open panels would compete for the same vertical space on a phone.
  let expanded = $state('');
  let detail = $state<AuthUserDetail | null>(null);
  let detailError = $state('');
  // A link's provider is stored as an immutable ULID, which is not a name. The
  // public sign-in list supplies the configured label; it reports enabled
  // providers only, so a link left by a disabled one falls back to its raw ID
  // rather than being labelled with a provider it does not belong to.
  let providerLabels = $state<Record<string, string>>({});
  const providerLabel = (id: string) => providerLabels[id] || id;

  async function load(target = 0, after = '') {
    if (!isNativeAdmin()) return;
    loading = true;
    error = '';
    try {
      const result = await listAuthUsers(after, search.trim());
      users = result.data;
      nextCursor = result.next_cursor;
      cursors = [...cursors.slice(0, target), after];
      page = target;
      loaded = true;
      resetTarget = '';
      resetPassword = '';
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      error = authErrorMessage(e, 'Could not load users. Check your connection and retry.');
    } finally { loading = false; }
  }

  // Search is a server predicate over every account, not a filter over the page
  // in front of you: an installation with SSO has accounts named external-<ulid>
  // that no page can be paged to by eye.
  function queueSearch() {
    clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => { cursors = ['']; void load(0, ''); }, 300);
  }

  onMount(() => {
    if (isNativeAdmin()) {
      void load();
      // Labels are presentation only; a failure leaves the raw provider IDs.
      void identityAPI.get<{ id: string; label: string }[]>('login-providers')
        .then(res => { providerLabels = Object.fromEntries((res.data || []).map(p => [p.id, p.label])); })
        .catch(() => {});
    }
    return () => clearTimeout(searchTimer);
  });

  async function toggleDetail(user: AuthUser) {
    if (expanded === user.id) { expanded = ''; return; }
    expanded = user.id;
    detail = null;
    detailError = '';
    try {
      detail = await getAuthUser(user.id);
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      detailError = authErrorMessage(e, 'Could not load this account.');
    }
  }

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
      cursors = [''];
      await load(0, '');
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      formError = authErrorMessage(e, 'Could not create user. Check your connection and retry.');
    } finally { password = ''; busy = false; }
  }

  async function mutate(user: AuthUser, action: 'enable' | 'disable' | 'revoke' | 'password' | 'unlock' | 'delete') {
    if (busy || loading) return;
    const self = user.id === storeAuth.identity?.subject;
    const label = authUserLabel(user);
    if (action === 'password') {
      formError = passwordPolicyError(resetPassword);
      if (formError) return;
    }
    const description = action === 'disable' ? 'Disable this account and end all its sessions? It cannot sign in until re-enabled.'
      : action === 'unlock' ? 'Clear the incorrect-password counter and allow password sign-in now?'
      : action === 'enable' ? 'Enable this account? Any existing sessions will also end.'
      : action === 'password' ? 'Replace this account password and end all its sessions? Its role and enabled status will not change.'
      : action === 'delete' ? 'Delete this account permanently? Its sessions, passkeys, linked identities, workspace memberships and Chats history are removed. This cannot be undone.'
      : 'End all sessions for this account? It can sign in again with its existing password.';
    if (!window.confirm(`${label}\n\n${description}${self && action !== 'unlock' ? '\n\nThis is your account. You will need to sign in again.' : ''}`)) return;
    busy = true;
    error = '';
    formError = '';
    try {
      if (action === 'password') await resetAuthUserPassword(user.id, resetPassword);
      else if (action === 'unlock') await unlockAuthUserLogin(user.id);
      else if (action === 'revoke') await revokeAuthUserSessions(user.id);
      else if (action === 'delete') await deleteAuthUser(user.id);
      else await setAuthUserEnabled(user.id, action === 'enable');
      if (self && action !== 'unlock') { returnToLogin(); return; }
      resetTarget = '';
      if (expanded === user.id && action === 'delete') expanded = '';
      addToast(action === 'unlock' ? `Password sign-in unlocked for ${label}.` : action === 'password' ? `Password reset for ${label}.` : action === 'revoke' ? `Sessions revoked for ${label}.` : action === 'delete' ? `${label} deleted.` : `${label} ${action}d.`);
      await load(page, cursors[page]);
    } catch (e) {
      if (isAuthUnauthorized(e)) returnToLogin();
      const message = authErrorMessage(e, 'Could not update user. Check your connection and retry.');
      if (action === 'password') formError = message;
      else error = message;
    } finally { resetPassword = ''; busy = false; }
  }

  const iconButton = 'p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors disabled:opacity-40 disabled:cursor-not-allowed';
  const smallButton = 'px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors disabled:opacity-50';
  const primaryButton = 'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50';
  const inputClass = 'w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus-visible:outline-2 focus-visible:outline-accent';
  const badge = 'inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium border';
</script>

<svelte:head><title>AT | Users</title></svelte:head>

{#if isNativeAdmin()}
  <div class="p-6 max-w-6xl mx-auto">
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-2">
        <UsersIcon size={16} class="text-gray-500 dark:text-dark-text-muted" />
        <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Users</h2>
        <span class="text-xs text-gray-400 dark:text-dark-text-muted">({users.length}{nextCursor ? '+' : ''})</span>
      </div>
      <div class="flex items-center gap-2">
        <button onclick={() => load(page, cursors[page])} disabled={busy || loading} class={iconButton} title="Refresh"><RefreshCw size={14} /></button>
        <button class={primaryButton} disabled={busy || loading} aria-expanded={creating} aria-controls="create-user" onclick={() => { creating = !creating; resetTarget = ''; password = resetPassword = formError = ''; }}><Plus size={12} />{creating ? 'Close' : 'New User'}</button>
      </div>
    </div>

    <p class="text-xs text-gray-500 dark:text-dark-text-muted mb-4">Installation accounts. Administrators configure this server; workspace roles and permissions decide what a member reaches. Expand an account to see its linked identities and workspaces.</p>

    {#if creating}
      <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface" id="create-user">
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
          <span class="text-sm font-medium text-gray-900 dark:text-dark-text">Create local user</span>
          <button onclick={() => { creating = false; password = formError = ''; }} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted"><X size={14} /></button>
        </div>
        <form onsubmit={create} aria-busy={busy} class="p-4 space-y-4">
          <fieldset disabled={busy} class="space-y-4">
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="user-username" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Username</label>
              <div class="col-span-3"><input id="user-username" bind:value={username} autocomplete="off" autocapitalize="none" spellcheck={false} required minlength={3} maxlength={128} pattern={'[a-zA-Z0-9._@+\\-]{3,128}'} class={inputClass} />
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-text-muted">3-128 letters, digits, or . _ @ + -. Stored in lowercase.</p></div>
            </div>
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="user-password" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Password</label>
              <div class="col-span-3"><input id="user-password" type="password" bind:value={password} autocomplete="new-password" required class={inputClass} />
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-text-muted">At least 8 characters. Share securely; passwords cannot be retrieved later.</p></div>
            </div>
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Access</span>
              <label class="col-span-3 flex items-start gap-2 text-sm"><input type="checkbox" bind:checked={admin} class="mt-0.5 size-4 accent-accent" /><span>Administrator<br /><span class="text-xs text-gray-500 dark:text-dark-text-muted">Full access to secrets, files, tools, organizations and users. Roles cannot be edited after creation.</span></span></label>
            </div>
          </fieldset>
          {#if formError && !resetTarget}<p role="alert" class="text-xs text-red-600 dark:text-red-400">{formError}</p>{/if}
          <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
            <button type="button" disabled={busy} class={smallButton} onclick={() => { creating = false; password = formError = ''; }}>Cancel</button>
            <button disabled={busy} class={primaryButton}>{busy ? 'Creating…' : 'Create user'}</button>
          </div>
        </form>
      </div>
    {/if}

    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface" aria-busy={loading || busy}>
      <div class="flex items-center gap-2 px-4 py-2 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <Search size={14} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
        <input type="search" bind:value={search} oninput={queueSearch} placeholder="Search username, email or account ID" aria-label="Search users" class="w-full bg-transparent text-sm focus-visible:outline-none" />
        <span role="status" class="text-xs text-gray-400 dark:text-dark-text-muted whitespace-nowrap">{loading ? 'Loading…' : busy ? 'Updating…' : `Page ${page + 1}`}</span>
      </div>

      {#if error}
        <div class="px-4 py-3 flex flex-wrap items-center gap-3 border-b border-gray-200 dark:border-dark-border"><p role="alert" class="text-xs text-red-600 dark:text-red-400">{error}</p><button class={smallButton} disabled={busy || loading} onclick={() => load(page, cursors[page])}>Retry</button></div>
      {/if}

      {#if loaded && !users.length && !loading}
        <p class="px-4 py-10 text-center text-sm text-gray-500 dark:text-dark-text-muted">{search.trim() ? 'No account matches this search.' : page ? 'No users on this page.' : 'No users yet. Create a local account to grant access.'}</p>
      {:else}
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead><tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
              <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Account</th>
              <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Access</th>
              <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider hidden md:table-cell">Last sign-in</th>
              <th class="px-4 py-2.5"><span class="sr-only">Actions</span></th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
              {#each users as user (user.id)}
                <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors align-top">
                  <td class="px-4 py-2.5 min-w-0">
                    <button class="flex items-start gap-1.5 text-left max-w-full" onclick={() => toggleDetail(user)} aria-expanded={expanded === user.id} title="Account detail">
                      {#if expanded === user.id}<ChevronDown size={14} class="mt-0.5 shrink-0 text-gray-400" />{:else}<ChevronRight size={14} class="mt-0.5 shrink-0 text-gray-400" />{/if}
                      <span class="min-w-0">
                        <span class="block font-medium text-gray-900 dark:text-dark-text break-all">{authUserLabel(user)}</span>
                        <span class="block text-xs text-gray-400 dark:text-dark-text-muted break-all">{authUserMeta(user)}</span>
                      </span>
                    </button>
                  </td>
                  <td class="px-4 py-2.5 whitespace-nowrap">
                    <div class="flex flex-wrap gap-1">
                      {#if user.id === storeAuth.identity?.subject}<span class={`${badge} bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border`}>You</span>{/if}
                      <span class={`${badge} ${user.admin ? 'bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-400 border-amber-200 dark:border-amber-900/40' : 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border'}`}>{user.admin ? 'Administrator' : 'User'}</span>
                      <span class={`${badge} ${user.disabled ? 'bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 border-red-200 dark:border-red-900/40' : 'bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 border-green-200 dark:border-green-900/40'}`}>{user.disabled ? 'Disabled' : 'Enabled'}</span>
                      {#if user.password_locked_until}<span class={`${badge} bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 border-red-200 dark:border-red-900/40`} title={`Password sign-in locked until ${new Date(user.password_locked_until).toLocaleString()}`}>Locked</span>{/if}
                      {#if user.identities?.length}<span class={`${badge} bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border`} title={user.identities.map(i => `${i.provider_id} · ${i.subject}`).join('\n')}>{user.identities[0].provider_id}</span>{/if}
                    </div>
                  </td>
                  <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted whitespace-nowrap hidden md:table-cell">
                    {#if user.last_login}<time datetime={user.last_login.at}>{new Date(user.last_login.at).toLocaleString()}</time>{#if user.last_login.source_ip}<span class="block font-mono">{user.last_login.source_ip}</span>{/if}{:else}Never{/if}
                  </td>
                  <td class="px-4 py-2.5">
                    <div class="flex justify-end gap-1">
                      {#if user.password_locked_until}
                        <button class={iconButton} disabled={busy || loading} title="Unlock password sign-in" onclick={() => mutate(user, 'unlock')}><Unlock size={14} /></button>
                      {/if}
                      <button class={iconButton} disabled={busy || loading} title="Reset password" onclick={() => { resetTarget = resetTarget === user.id ? '' : user.id; creating = false; password = resetPassword = formError = ''; }}><KeyRound size={14} /></button>
                      <button class={iconButton} disabled={busy || loading} title="Sign out all sessions" onclick={() => mutate(user, 'revoke')}><LogOut size={14} /></button>
                      <button class={iconButton} disabled={busy || loading || (!user.disabled && user.id === storeAuth.identity?.subject)} title={user.id === storeAuth.identity?.subject ? 'You cannot disable your own account' : user.disabled ? 'Enable account' : 'Disable account'} onclick={() => mutate(user, user.disabled ? 'enable' : 'disable')}><Power size={14} /></button>
                      <!-- Deletion confirms through the prompt rather than an
                           inline arm/confirm pair: it is irreversible, and the
                           prompt is where what it removes can be named. -->
                      <button class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 transition-colors disabled:opacity-40 disabled:cursor-not-allowed" disabled={busy || loading || user.id === storeAuth.identity?.subject} title={user.id === storeAuth.identity?.subject ? 'You cannot delete your own account' : 'Delete account permanently'} onclick={() => mutate(user, 'delete')}><Trash2 size={14} /></button>
                    </div>
                  </td>
                </tr>
                {#if resetTarget === user.id}
                  <tr class="bg-gray-50/70 dark:bg-dark-base/60"><td colspan="4" class="px-4 py-3">
                    <form onsubmit={(event) => { event.preventDefault(); void mutate(user, 'password'); }} class="max-w-md space-y-2">
                      <label for="reset-password" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">New password for {authUserLabel(user)}</label>
                      <input id="reset-password" type="password" required autocomplete="new-password" disabled={busy} bind:value={resetPassword} class={inputClass} />
                      <p class="text-xs text-gray-500 dark:text-dark-text-muted">At least 8 characters. All sessions end.{user.id === storeAuth.identity?.subject ? ' You will be signed out.' : ' Share the new password securely.'}</p>
                      {#if formError}<p role="alert" class="text-xs text-red-600 dark:text-red-400">{formError}</p>{/if}
                      <div class="flex gap-2"><button disabled={busy || loading} class={primaryButton}>{busy ? 'Resetting…' : 'Reset password'}</button><button type="button" disabled={busy} class={smallButton} onclick={() => { resetTarget = ''; resetPassword = formError = ''; }}>Cancel</button></div>
                    </form>
                  </td></tr>
                {/if}
                {#if expanded === user.id}
                  <tr class="bg-gray-50/70 dark:bg-dark-base/60"><td colspan="4" class="px-4 py-3 space-y-4">
                    {#if detailError}<p role="alert" class="text-xs text-red-600 dark:text-red-400">{detailError}</p>{/if}
                    <div class="grid gap-4 sm:grid-cols-2">
                      <div>
                        <h3 class="text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-dark-text-muted mb-2">Identity</h3>
                        <!-- A grid rather than a flex row with a fixed-width
                             term: the term used to be the provider's ULID in a
                             `w-20 shrink-0` box with no wrapping, so it
                             overflowed its column and ran over the value beside
                             it. Grid columns cannot overlap, and
                             `minmax(0,1fr)` is what lets the value wrap instead
                             of pushing the track wider. -->
                        <dl class="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
                          <dt class="text-gray-500 dark:text-dark-text-muted">Username</dt><dd class="font-mono break-all">{user.username}</dd>
                          <dt class="text-gray-500 dark:text-dark-text-muted">Account ID</dt><dd class="font-mono break-all">{user.id}</dd>
                        </dl>
                        <!-- Every value on a link is an opaque string, so each
                             one is named. Showing the subject as the value of a
                             term that was itself an ID read as two unrelated
                             IDs stacked on each other. -->
                        {#each detail?.identities || [] as identity}
                          <div class="mt-2 border-l-2 border-gray-200 dark:border-dark-border pl-3">
                            <p class="text-xs font-medium break-all">{providerLabel(identity.provider_id)}</p>
                            <dl class="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs mt-1">
                              {#if identity.username}<dt class="text-gray-500 dark:text-dark-text-muted">Username</dt><dd class="break-all">{identity.username}</dd>{/if}
                              {#if identity.email}<dt class="text-gray-500 dark:text-dark-text-muted">Email</dt><dd class="break-all">{identity.email}{identity.email_verified ? '' : ' (unverified)'}</dd>{/if}
                              <dt class="text-gray-500 dark:text-dark-text-muted">Subject</dt><dd class="font-mono break-all">{identity.subject}</dd>
                              {#if providerLabel(identity.provider_id) !== identity.provider_id}<dt class="text-gray-500 dark:text-dark-text-muted">Provider ID</dt><dd class="font-mono break-all text-gray-400 dark:text-dark-text-muted">{identity.provider_id}</dd>{/if}
                            </dl>
                          </div>
                        {/each}
                        {#if detail && !detail.identities?.length}<p class="text-xs text-gray-500 dark:text-dark-text-muted mt-1">Local account — no external identity linked.</p>{/if}
                      </div>
                      <div>
                        <h3 class="text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-dark-text-muted mb-2">Workspaces</h3>
                        {#if detail?.workspaces?.length}
                          <ul class="space-y-1 text-xs">{#each detail.workspaces as w}<li class="flex flex-wrap items-center gap-2"><span class="break-all">{w.name}</span><span class={`${badge} bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border`}>{w.role}</span>{#if w.status !== 'active'}<span class={`${badge} bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 border-red-200 dark:border-red-900/40`}>{w.status}</span>{/if}</li>{/each}</ul>
                        {:else if detail}
                          <p class="text-xs text-gray-500 dark:text-dark-text-muted">No workspace membership. This account can sign in but reaches nothing until a workspace owner admits it.</p>
                        {:else}
                          <p class="text-xs text-gray-400 dark:text-dark-text-muted">Loading…</p>
                        {/if}
                      </div>
                    </div>
                    {#if user.password_locked_until}
                      <p class="text-xs text-red-600 dark:text-red-400">Password sign-in locked until <time datetime={user.password_locked_until}>{new Date(user.password_locked_until).toLocaleString()}</time>. It unlocks automatically after 15 minutes.</p>
                    {/if}
                    <AdminRecovery userID={user.id} username={authUserLabel(user)} />
                    <UserLoginHistory userID={user.id} username={authUserLabel(user)} />
                  </td></tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      <nav aria-label="User list pagination" class="flex items-center justify-between gap-3 border-t border-gray-200 dark:border-dark-border px-4 py-2">
        <span class="text-xs text-gray-400 dark:text-dark-text-muted">50 per page</span>
        <div class="flex gap-2">
          <button class={smallButton} disabled={busy || loading || page === 0} onclick={() => load(page - 1, cursors[page - 1])}>Previous</button>
          <button class={smallButton} disabled={busy || loading || !nextCursor} onclick={() => load(page + 1, nextCursor)}>Next</button>
        </div>
      </nav>
    </div>
  </div>
{/if}
