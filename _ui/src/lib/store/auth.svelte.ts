import type { AuthIdentity } from '@/lib/api/auth';

// App adopts verified /me and refresh identities in memory; legacy auth never grants access.
// localLoginCollapsed is presentation policy, not admission: the local form is
// reachable from the sign-in screen's reveal control whenever localLogin is true.
// passkeys is the subsystem capability (account management and step-up
// verification); passkeyLogin additionally reports that the installation
// accepts a passkey as a first factor, which an administrator can turn off.
export const storeAuth = $state<{ identity: AuthIdentity | null; passkeys: boolean; passkeyLogin: boolean; localLogin: boolean; localLoginCollapsed: boolean; title: string; securityHold: boolean }>({ identity: null, passkeys: false, passkeyLogin: false, localLogin: true, localLoginCollapsed: false, title: 'AT', securityHold: false });
export const securityCodes = $state<{ values: string[] }>({ values: [] });
export const authOrigins = $state<{ primary: string; allowed: string[] }>({ primary: '', allowed: [] });

export function isNativeAdmin(): boolean {
  return storeAuth.identity?.roles?.includes('admin') === true;
}

export function returnToLogin() {
  storeAuth.identity = null;
  // Match logout: discard all in-memory management data, not just the identity.
  window.location.reload();
}
