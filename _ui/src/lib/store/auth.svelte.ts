import type { AuthIdentity } from '@/lib/api/auth';

// App adopts verified /me and refresh identities in memory; legacy auth never grants access.
export const storeAuth = $state<{ identity: AuthIdentity | null; passkeys: boolean }>({ identity: null, passkeys: false });

export function isNativeAdmin(): boolean {
  return storeAuth.identity?.roles?.includes('admin') === true;
}

export function returnToLogin() {
  storeAuth.identity = null;
  // Match logout: discard all in-memory management data, not just the identity.
  window.location.reload();
}
