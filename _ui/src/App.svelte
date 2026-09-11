<script lang="ts">
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
  import { getAuthIdentity, getAuthStatus, isAuthUnauthorized, isSetupRequired, logoutAuth } from './lib/api/auth';
  import { authSession } from './lib/api/transport';
  import { ReauthenticationRequired } from './lib/api/session-transport';
  import { storeAuth, isNativeAdmin, returnToLogin, securityCodes } from './lib/store/auth.svelte';
  import { workspaceState, loadWorkspaceAccess } from './lib/store/workspace.svelte';
  import { routeAllowed, configurationLinks } from './lib/helper/navigation';
  import routes from './routes';
  let { initialRecoveryTicket = '' }: { initialRecoveryTicket?: string } = $props();
  let ticket = $state(untrack(() => initialRecoveryTicket));
  let authState = $state<'loading'|'setup'|'login'|'ready'|'error'>('loading');
  let error = $state(''); let notice = $state(''); let loggingOut = $state(false); let checking = false;
  let revision = 0;
  let settingsArea = $derived($location.startsWith('/settings') || configurationLinks.some(l => l.path === $location));
  async function checkSession() {
    if (checking || storeAuth.securityHold || ticket) return; checking = true; const start = revision;
    try { const identity = await getAuthIdentity(); if (start !== revision || storeAuth.securityHold) return; storeAuth.identity = identity; if (!window.location.hash.startsWith('#/mobile-authorize?')) await loadWorkspaceAccess(); if (start !== revision || storeAuth.securityHold) return; authState = 'ready'; error = ''; }
    catch (e) { if (start !== revision || storeAuth.securityHold) return; if (isSetupRequired(e)) { storeAuth.identity = null; authState = 'setup'; error = ''; } else if (isAuthUnauthorized(e) || e instanceof ReauthenticationRequired) { storeAuth.identity = null; authState = 'login'; } else { authState = 'error'; error = 'Cannot load your session or workspace access. Retry when the server is available.'; } }
    finally { checking = false; }
  }
  async function initialize() {
    if (ticket) return;
    authState = 'loading';
    try { const status = await getAuthStatus(); authSession.setEnabled(status.enabled); storeAuth.passkeys = status.passkeys; storeAuth.localLogin = status.local_login !== false; storeAuth.title = status.display_title || 'AT';
      if (status.setup_required === true) authState = 'setup'; else if (status.enabled) await checkSession(); else { authState = 'error'; error = 'Native authentication is unavailable. Ask the operator to enable runtime authentication.'; }
    }     catch (e) { if (isSetupRequired(e)) { authState = 'setup'; error = ''; return; } authState = 'error'; error = 'Cannot load authentication settings. Retry when the server is available.'; }
  }
  async function logout() { loggingOut = true; revision++; try { await logoutAuth(); returnToLogin(); } catch { error = 'Sign-out failed. Your session may still be active. Please retry.'; } finally { loggingOut = false; } }
  onMount(() => {
    if (window.matchMedia('(max-width: 639px)').matches) storeNavbar.sideBarOpen = false;
    const unsubscribe = authSession.subscribe((identity, message) => { if (storeAuth.securityHold || ticket) return; if (identity) storeAuth.identity = identity; else { revision++; storeAuth.identity = null; notice = message; authState = 'login'; } });
    void initialize(); const recheck = () => { if (authState === 'ready' && !storeAuth.securityHold) void checkSession(); };
    const timer = window.setInterval(recheck, 60000); window.addEventListener('focus', recheck);
    return () => { unsubscribe(); clearInterval(timer); window.removeEventListener('focus', recheck); };
  });
</script>
<Toast />
{#if securityCodes.values.length}<BackupCodes />
{:else if ticket}<AccountRecovery {ticket} oncomplete={() => { ticket = ''; notice = 'Sign in with your current credentials to continue.'; void initialize(); }} />
{:else if authState === 'setup'}<FirstSetup oncomplete={async () => { notice = 'Administrator created. Sign in to continue.'; await initialize(); }} />
{:else if authState === 'login'}<NativeLogin onlogin={checkSession} sessionNotice={notice} />
{:else if authState === 'loading' || authState === 'error'}<main class="min-h-full flex items-center justify-center p-6"><div class="max-w-md space-y-4"><h1 class="text-xl font-semibold">{authState === 'loading' ? 'Connecting to AT…' : 'Connection unavailable'}</h1>{#if error}<p role="alert" class="settings-error">{error}</p><button class="settings-button" onclick={initialize}>Retry connection</button>{/if}</div></main>
{:else if $location === '/mobile-authorize'}<MobileAuthorize query={$querystring || ''} enabled={true} onlogin={() => { revision++; storeAuth.identity = null; authState = 'login'; }} />
{:else}
<div class={['grid h-full w-full min-w-0 bg-gray-50 dark:bg-dark-base', storeNavbar.sideBarOpen ? 'grid-cols-[9rem_minmax(0,1fr)]' : 'grid-cols-[minmax(0,1fr)]']}>
  {#if storeNavbar.sideBarOpen}<Sidebar />{/if}
  <div class="grid grid-rows-[3rem_minmax(0,1fr)] min-h-0 min-w-0"><Navbar onlogout={logout} {loggingOut} /><div class="overflow-y-auto min-h-0 min-w-0">
    {#if error}<p role="alert" class="settings-error px-5 py-2">{error}</p>{/if}
    {#if !workspaceState.access && !isNativeAdmin() && $location !== '/settings/account'}
      <div class="settings-page"><h1 class="text-2xl font-semibold">Waiting for workspace access</h1><p class="settings-note">You’re signed in. Ask a workspace owner to admit your user ID <code class="break-all">{storeAuth.identity?.subject}</code>, or accept an invitation below.</p><div class="flex flex-wrap gap-3"><button class="settings-button" onclick={checkSession}>Check access again</button><a class="settings-button" href="#/settings/account">Account security</a></div></div><WorkspaceSettings />
    {:else if !routeAllowed($location)}<div class="settings-page"><h1 class="text-2xl font-semibold">Access unavailable</h1><p class="settings-note">Your selected workspace does not grant access to this section. A workspace owner can review your effective permissions.</p><a class="settings-button inline-block" href="#/settings">Open Settings</a></div>
    {:else if settingsArea}<div class="grid sm:grid-cols-[11rem_minmax(0,1fr)] min-h-full"><SettingsSidebar /><div class="min-w-0"><Router {routes} /></div></div>
    {:else if $location === '/' && !isNativeAdmin()}<WorkspaceSettings />
    {:else}<Router {routes} />{/if}
  </div></div>
</div>
{/if}
