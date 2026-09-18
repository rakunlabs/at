import type { AuthIdentity } from '@/lib/api/auth';
import { identityAPI } from '@/lib/api/identity';

// App adopts verified /me and refresh identities in memory; legacy auth never grants access.
// localLoginCollapsed is presentation policy, not admission: the local form is
// reachable from the sign-in screen's reveal control whenever localLogin is true.
// passkeys is the subsystem capability (account management and step-up
// verification); passkeyLogin additionally reports that the installation
// accepts a passkey as a first factor, which an administrator can turn off.
export const storeAuth = $state<{ identity: AuthIdentity | null; passkeys: boolean; passkeyLogin: boolean; localLogin: boolean; localLoginCollapsed: boolean; title: string; securityHold: boolean }>({ identity: null, passkeys: false, passkeyLogin: false, localLogin: true, localLoginCollapsed: false, title: 'AT', securityHold: false });
export const securityCodes = $state<{ values: string[] }>({ values: [] });
export const authOrigins = $state<{ primary: string; allowed: string[] }>({ primary: '', allowed: [] });

// The configured external sign-in providers. They live here rather than inside
// NativeLogin because the sign-in card must be complete on its first paint: a
// component-local onMount fetch appended the buttons a beat later, so the card
// visibly grew right after it appeared.
export const loginProviders = $state<{ items: { id: string; label: string }[]; loaded: boolean; error: string }>({ items: [], loaded: false, error: '' });

export async function loadLoginProviders(force = false): Promise<void> {
  if (loginProviders.loaded && !force) return;
  try {
    // A hung public endpoint must not keep the sign-in screen from appearing.
    loginProviders.items = (await identityAPI.get('login-providers', { timeout: 4000 })).data || [];
    loginProviders.loaded = true;
    loginProviders.error = '';
  } catch {
    loginProviders.error = 'Sign-in providers are unavailable. Retry loading them.';
  }
}

export function isNativeAdmin(): boolean {
  return storeAuth.identity?.roles?.includes('admin') === true;
}

// A reload discards `notice`, so a message that explains why the sign-in screen
// is being shown has to survive it. sessionStorage is per tab and scoped to the
// deployment path, matching the other coordination keys.
const storageKey = (name: string) => `at-auth:${new URL('.', document.baseURI).pathname}:${name}`;
const noticeKey = storageKey('notice');
const signedOutKey = storageKey('signed-out');

export function takeLoginNotice(): string {
  try {
    const value = sessionStorage.getItem(noticeKey) || '';
    sessionStorage.removeItem(noticeKey);
    return value;
  } catch { return ''; }
}

// A deliberate sign-out already holds the answer `auth/session` would give, so
// the reload that follows can skip that probe and go straight to the sign-in
// screen. Consumed once, like the notice: any later load must probe again, and
// a stale hint can at worst show the sign-in card to a session established by
// another tab mid-reload.
export function takeSignedOut(): boolean {
  try {
    const value = sessionStorage.getItem(signedOutKey) === '1';
    sessionStorage.removeItem(signedOutKey);
    return value;
  } catch { return false; }
}

export function returnToLogin(notice = '', signedOut = false) {
  try {
    if (notice) sessionStorage.setItem(noticeKey, notice);
    else sessionStorage.removeItem(noticeKey);
    if (signedOut) sessionStorage.setItem(signedOutKey, '1');
  } catch { /* The reload still returns to sign-in; only the explanation is lost. */ }
  // The reload discards the whole heap, so clearing the identity here bought
  // nothing and cost a frame: Svelte flushes before the new document commits,
  // so the shell re-rendered signed-out — sidebar links and the account menu
  // visibly disappearing — for the entire duration of the navigation. Leave the
  // last painted frame intact and let the document swap be the only transition.
  window.location.reload();
}
