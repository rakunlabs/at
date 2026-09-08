<script lang="ts">
  import Router from "svelte-spa-router";
  import { location, querystring } from "svelte-spa-router";
  import { storeNavbar } from "@/lib/store/store.svelte";
  import Sidebar from "@/lib/components/Sidebar.svelte";
  import SettingsSidebar from "@/lib/components/SettingsSidebar.svelte";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import routes from "@/routes";
  import { onMount } from 'svelte';
  import NativeLogin from '@/lib/components/NativeLogin.svelte';
  import MobileAuthorize from '@/pages/MobileAuthorize.svelte';
  import ChangePassword from '@/lib/components/ChangePassword.svelte';
  import Passkeys from '@/lib/components/Passkeys.svelte';
  import { getAuthIdentity, getAuthStatus, isAuthUnauthorized, logoutAuth } from '@/lib/api/auth';
  import { authSession } from '@/lib/api/transport';
  import { ReauthenticationRequired } from '@/lib/api/session-transport';
  import { storeAuth, returnToLogin } from '@/lib/store/auth.svelte';

  let authState = $state<'loading' | 'legacy' | 'login' | 'admin' | 'denied' | 'error'>('loading');
  let authError = $state('');
  let loggingOut = $state(false);
  let sessionNotice = $state('');
  let checking: Promise<void> | undefined;
  let sessionRevision = 0;

  function mobileLogin() {
    sessionRevision++;
    storeAuth.identity = null;
    sessionNotice = 'Sign in again to review the mobile request. Signing in does not approve it.';
    authState = 'login';
  }

  function checkSession(): Promise<void> {
    if (checking) return checking;
    checking = readSession().finally(() => { checking = undefined; });
    return checking;
  }

  async function signedIn() {
    // A prior poll may still be unwinding after returning the UI to login.
    await checking;
    await checkSession();
  }

  async function readSession() {
    const revision = sessionRevision;
    try {
      const user = await getAuthIdentity();
      if (revision !== sessionRevision || loggingOut) return;
      storeAuth.identity = user;
      authState = user.roles?.includes('admin') ? 'admin' : 'denied';
      authError = '';
      sessionNotice = '';
    } catch (e) {
      if (revision !== sessionRevision || loggingOut) return;
      storeAuth.identity = null;
      if (e instanceof ReauthenticationRequired) { sessionNotice = e.message; authState = 'login'; return; }
      if (isAuthUnauthorized(e)) { authState = 'login'; return; }
      authState = 'error';
      authError = 'Cannot verify your session. Check your connection and retry.';
    }
  }

  async function initializeAuth() {
    storeAuth.passkeys = false;
    try {
      const status = await getAuthStatus();
      authSession.setEnabled(status.enabled);
      storeAuth.passkeys = status.passkeys;
      if (status.enabled === false) authState = 'legacy';
      else if (status.enabled === true) await checkSession();
      else throw new Error('Invalid authentication status');
    } catch {
      authState = 'error';
      authError = 'Cannot load the server authentication settings. Retry when the server is available.';
    }
  }

  async function logout() {
    if (loggingOut) return;
    loggingOut = true;
    sessionRevision++;
    authError = '';
    try {
      await logoutAuth();
      // A reload clears domain stores and in-memory management data too.
      returnToLogin();
    } catch {
      authError = 'Sign-out failed. Your session may still be active. Please retry.';
    } finally {
      loggingOut = false;
    }
  }

  onMount(() => {
    const unsubscribe = authSession.subscribe((identity, notice) => {
      if (identity) {
        if (loggingOut) return;
        storeAuth.identity = identity;
        authState = identity.roles?.includes('admin') ? 'admin' : 'denied';
      } else {
        sessionRevision++;
        storeAuth.identity = null;
        sessionNotice = notice;
        authState = 'login';
      }
    });
    void initializeAuth();
    const recheck = () => {
      if (authState === 'admin' || authState === 'denied') void checkSession();
    };
    const timer = window.setInterval(recheck, 60_000);
    window.addEventListener('focus', recheck);
    document.addEventListener('visibilitychange', recheck);
    return () => {
      unsubscribe();
      window.clearInterval(timer);
      window.removeEventListener('focus', recheck);
      document.removeEventListener('visibilitychange', recheck);
    };
  });
</script>

<Toast />

{#if authState === 'login'}
  <NativeLogin onlogin={signedIn} sessionNotice={sessionNotice || ($location === '/mobile-authorize' ? 'Sign in to review a mobile sign-in request. Any AT account can review it. Signing in does not approve the request.' : '')} />
{:else if $location === '/mobile-authorize' && (authState === 'admin' || authState === 'denied' || authState === 'legacy')}
  {#key `${$querystring}:${storeAuth.identity?.subject || ''}`}
    <MobileAuthorize query={$querystring || ''} enabled={authState !== 'legacy'} onlogin={mobileLogin} />
  {/key}
{:else if authState === 'loading' || authState === 'error' || authState === 'denied'}
  <main class="min-h-full flex items-center justify-center px-6 py-12 bg-gray-50 dark:bg-dark-base">
    <div class="w-full max-w-sm space-y-4">
      <h1 class="text-xl font-semibold">{authState === 'denied' ? 'Administrator access required' : authState === 'loading' ? 'Connecting to AT...' : 'Connection unavailable'}</h1>
      {#if authState === 'denied'}
        <p class="text-sm leading-6 text-gray-600 dark:text-dark-text-secondary">You are signed in, but this management app is administrator-only. Organization-scoped access is not available yet.</p>
        <button disabled={loggingOut} class="rounded-md bg-accent text-gray-950 px-4 py-3 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50" onclick={logout}>{loggingOut ? 'Signing out...' : 'Sign out'}</button>
        <div class="border-t border-gray-200 dark:border-dark-border pt-6"><ChangePassword /></div>
        <div class="border-t border-gray-200 dark:border-dark-border pt-6"><Passkeys /></div>
      {:else if authState === 'error'}
        <p role="alert" class="text-sm leading-6">{authError}</p>
        <button class="rounded-md bg-accent text-gray-950 px-4 py-3 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" onclick={initializeAuth}>Retry connection</button>
      {/if}
      {#if authState === 'denied' && authError}<p role="alert" class="text-sm text-red-700 dark:text-red-300">{authError}</p>{/if}
    </div>
  </main>
{:else}

<div
  class={[
    "grid grid-flow-col h-full w-full relative bg-gray-50 dark:bg-dark-base transition-colors",
    storeNavbar.sideBarOpen ? "grid-cols-[9rem]" : "grid-cols-[0]",
  ]}
>
  <Sidebar />
  <div class="h-full w-full grid grid-rows-[2rem_1fr] min-h-0 min-w-0">
    <Navbar onlogout={authState === 'admin' ? logout : undefined} {loggingOut} />
    <div class="overflow-y-auto min-h-0">
      {#if authError}<p role="alert" class="px-4 py-2 text-sm text-red-700 dark:text-red-300">{authError}</p>{/if}
      {#if $location === '/settings' || $location.startsWith('/settings/')}
        <div class="grid grid-cols-[11rem_1fr] min-h-full">
          <SettingsSidebar />
          <div class="min-w-0">
            <Router {routes} />
          </div>
        </div>
      {:else}
        <Router {routes} />
      {/if}
    </div>
  </div>
</div>
{/if}
