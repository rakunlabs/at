<script lang="ts">
  import AuthShell from './lib/components/AuthShell.svelte';
  import Router, { location, querystring } from 'svelte-spa-router';
  import { onMount, untrack } from 'svelte';
  import { storeNavbar } from './lib/store/store.svelte';
  import Sidebar from './lib/components/Sidebar.svelte';
  import SettingsSidebar from './lib/components/SettingsSidebar.svelte';
  import Navbar from './lib/components/Navbar.svelte';
  import Toast from './lib/components/Toast.svelte';
  import NativeLogin from './lib/components/NativeLogin.svelte';
  import FirstSetup from './lib/components/FirstSetup.svelte';
  import AccountRecovery from './lib/components/AccountRecovery.svelte';
  import BackupCodes from './lib/components/BackupCodes.svelte';
  import MobileAuthorize from './pages/MobileAuthorize.svelte';
  import WorkspaceSettings from './pages/WorkspaceSettings.svelte';
  import { getAuthStatus, isAuthUnauthorized, isSetupRequired, logoutAuth } from './lib/api/auth';
  import { authSession } from './lib/api/transport';
  import { ReauthenticationRequired } from './lib/api/session-transport';
  import { storeAuth, authOrigins, returnToLogin, securityCodes } from './lib/store/auth.svelte';
  import { loadWorkspaceAccess } from './lib/store/workspace.svelte';
  import { routeAllowed, inSettingsArea, workspaceAdmitted } from './lib/helper/navigation';
  import { isFeatureEnabled } from './lib/store/features.svelte';
  import { FEATURE_WORKSPACE_MANAGEMENT } from './lib/api/features';
  import routes from './routes';
  import { pwa } from './lib/store/pwa.svelte';
  let mobileNavigation = $state<HTMLDialogElement>();
  function closeNavigation() { storeNavbar.sideBarOpen = false; }
  $effect(() => {
    if (storeNavbar.sideBarOpen && window.matchMedia('(max-width: 639px)').matches) mobileNavigation?.showModal();
    else mobileNavigation?.close();
  });
  $effect(() => { void $location; if (window.matchMedia('(max-width: 639px)').matches) closeNavigation(); });
  let { initialRecoveryTicket = '' }: { initialRecoveryTicket?: string } = $props();
  let ticket = $state(untrack(() => initialRecoveryTicket));
  let authState = $state<'loading'|'setup'|'login'|'ready'|'error'>('loading');
  let error = $state(''); let notice = $state(''); let loggingOut = $state(false); let checking = false;
  let revision = 0;
  let settingsArea = $derived(inSettingsArea($location));
  // routeAllowed already refuses every route an unadmitted account cannot use,
  // so the settings pages it *can* use (account, workspace, and the index that
  // reaches them) get the normal settings layout instead of being replaced by
  // the waiting screen — which is what made Settings unclickable.
  let settingsLayout = $derived(settingsArea && routeAllowed($location));
  let waiting = $derived(!workspaceAdmitted() && !settingsArea);
  async function checkSession() {
    if (checking || storeAuth.securityHold || ticket) return; checking = true; const start = revision;
    try { const identity = await authSession.checkSession(); if (start !== revision || storeAuth.securityHold) return; storeAuth.identity = identity; if (!identity) { authState = 'login'; return; } if (!window.location.hash.startsWith('#/mobile-authorize?')) await loadWorkspaceAccess(); if (start !== revision || storeAuth.securityHold) return; authState = 'ready'; error = ''; notice = ''; }
    catch (e) { if (start !== revision || storeAuth.securityHold) return; if (isSetupRequired(e)) { storeAuth.identity = null; authState = 'setup'; error = ''; } else if (isAuthUnauthorized(e) || e instanceof ReauthenticationRequired) { storeAuth.identity = null; authState = 'login'; } else { authState = 'error'; error = 'Cannot load your session or workspace access. Retry when the server is available.'; } }
    finally { checking = false; }
  }
  async function initialize() {
    if (ticket) return;
    authState = 'loading';
    try { const status = await getAuthStatus(); authSession.setEnabled(status.enabled); storeAuth.passkeys = status.passkeys; storeAuth.passkeyLogin = status.passkey_login_enabled !== false && status.passkeys; storeAuth.localLogin = status.local_login !== false; storeAuth.localLoginCollapsed = status.local_login_collapsed === true; storeAuth.title = status.display_title || 'AT'; authOrigins.primary = status.origin || ''; authOrigins.allowed = status.allowed_origins || [];
      if (status.setup_required === true) authState = 'setup'; else if (status.enabled) await checkSession(); else { authState = 'error'; error = 'Native authentication is unavailable. Ask the operator to enable runtime authentication.'; }
    }     catch (e) { if (isSetupRequired(e)) { authState = 'setup'; error = ''; return; } authState = 'error'; error = 'Cannot load authentication settings. Retry when the server is available.'; }
  }
  async function logout() { loggingOut = true; revision++; try { await logoutAuth(); returnToLogin(); } catch { error = 'Sign-out failed. Your session may still be active. Please retry.'; } finally { loggingOut = false; } }
  onMount(() => {
    if (window.matchMedia('(max-width: 639px)').matches) storeNavbar.sideBarOpen = false;
    const unsubscribe = authSession.subscribe((identity, message) => { if (storeAuth.securityHold || ticket) return; if (identity) storeAuth.identity = identity; else { revision++; storeAuth.identity = null; notice = message; authState = 'login'; } });
    void initialize(); const recheck = () => { if (authState === 'ready' && !storeAuth.securityHold) void checkSession(); };
    const timer = window.setInterval(recheck, 60000); window.addEventListener('focus', recheck);
    const online = () => { if (authState === 'error') void initialize(); else recheck(); };
    const breakpoint = window.matchMedia('(max-width: 639px)');
    const resize = () => { closeNavigation(); mobileNavigation?.close(); };
    breakpoint.addEventListener('change', resize);
    window.addEventListener('online', online);
    return () => { unsubscribe(); clearInterval(timer); window.removeEventListener('focus', recheck); window.removeEventListener('online', online); breakpoint.removeEventListener('change', resize); };
  });
</script>
<Toast />
{#if securityCodes.values.length}<BackupCodes />
{:else if ticket}<AccountRecovery {ticket} oncomplete={() => { ticket = ''; notice = 'Sign in with your current credentials to continue.'; void initialize(); }} />
{:else if authState === 'setup'}<FirstSetup oncomplete={async () => { notice = 'Administrator created. Sign in to continue.'; await initialize(); }} />
{:else if authState === 'login'}<NativeLogin onlogin={checkSession} sessionNotice={notice} />
{:else if authState === 'loading' || authState === 'error'}<AuthShell title={authState === 'loading' ? 'Connecting to AT…' : 'Connection unavailable'} subtitle={authState === 'loading' ? 'Checking your session with the server.' : ''}>{#if error}<p role="alert" class="settings-error">{error}</p><button class="settings-button w-full min-h-11 sm:min-h-0" onclick={initialize}>Retry connection</button>{:else}<p class="settings-note" role="status">One moment…</p>{/if}</AuthShell>
{:else if $location === '/mobile-authorize'}<MobileAuthorize query={$querystring || ''} enabled={true} onlogin={() => { revision++; storeAuth.identity = null; authState = 'login'; }} />
{:else}
<div class={['grid h-full w-full min-w-0 bg-gray-50 dark:bg-dark-base', storeNavbar.sideBarOpen ? 'grid-cols-[minmax(0,1fr)] sm:grid-cols-[9rem_minmax(0,1fr)]' : 'grid-cols-[minmax(0,1fr)]']}>
  {#if storeNavbar.sideBarOpen}<div class="hidden sm:block min-h-0"><Sidebar /></div>{/if}
  <dialog bind:this={mobileNavigation} class="mobile-navigation" aria-label="Navigation" onclose={closeNavigation} onclick={event => { if (event.target === mobileNavigation || (event.target instanceof Element && event.target.closest('a'))) closeNavigation(); }}>
    <div class="flex h-full flex-col bg-white dark:bg-dark-surface">
      <button class="settings-button m-2 min-h-11 self-end" onclick={closeNavigation}>Close navigation</button>
      <div class="min-h-0 flex-1"><Sidebar /></div>
    </div>
  </dialog>
  <div class="grid grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)] min-h-0 min-w-0"><div class="min-w-0"><Navbar onlogout={logout} {loggingOut} />{#if pwa.offline}<p role="status" class="border-b border-gray-200 dark:border-dark-border px-3 py-2 text-sm">You’re offline. Reconnect to send messages and save changes.</p>{/if}</div><div class={['min-h-0 min-w-0', settingsLayout ? 'flex flex-col overflow-hidden' : 'overflow-y-auto']}>
    {#if error}<p role="alert" class="settings-error px-5 py-2">{error}</p>{/if}
    {#if waiting}
      <div class="settings-page"><h1 class="settings-title">No workspace access yet</h1><p class="settings-note">You’re signed in, but your account is not a member of any workspace, so nothing in the application is available to you{$location !== '/' ? ' — including the page you opened' : ''}. Ask a workspace owner to admit your user ID <code class="break-all">{storeAuth.identity?.subject}</code>{isFeatureEnabled(FEATURE_WORKSPACE_MANAGEMENT) ? ', or accept an invitation below' : ''}.</p><div class="flex flex-wrap gap-3"><button class="settings-button" onclick={checkSession}>Check access again</button><a class="settings-button" href="#/settings/account">Account security</a></div></div>{#if isFeatureEnabled(FEATURE_WORKSPACE_MANAGEMENT)}<WorkspaceSettings />{/if}
    {:else if !routeAllowed($location)}<div class="settings-page"><h1 class="settings-title">Access unavailable</h1><p class="settings-note">Your selected workspace does not grant access to this section. A workspace owner can review your effective permissions.</p><a class="settings-button inline-block" href="#/settings">Open Settings</a></div>
    {:else if settingsArea}<div class="grid flex-1 min-h-0 min-w-0 grid-rows-[auto_minmax(0,1fr)] sm:grid-rows-[minmax(0,1fr)] sm:grid-cols-[11rem_minmax(0,1fr)]"><SettingsSidebar /><div class="min-h-0 min-w-0 overflow-y-auto overscroll-contain"><Router {routes} /></div></div>
    {:else}<Router {routes} />{/if}
  </div></div>
</div>
{/if}
