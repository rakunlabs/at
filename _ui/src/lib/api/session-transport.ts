import axios, { type AxiosAdapter, type AxiosResponse } from 'axios';
import type { AuthIdentity } from './auth';

export class ReauthenticationRequired extends Error {}

// The server answers 403 on an unclaimed installation. That is not a broken
// connection: the app must send the operator to first-run setup instead of
// reporting a transport failure.
export class InstallationSetupRequired extends Error {
  // Explicit name so detection survives minification.
  name = 'InstallationSetupRequired';
}

function setupRequiredError(response: Response): Error {
  if (response.status === 403) return new InstallationSetupRequired('Installation setup required');
  return new Error('Cannot verify your session');
}

interface Environment {
  baseURL: string;
  fetch: typeof fetch;
  locks?: Pick<LockManager, 'request'>;
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
}

// No credential is read by JS. Storage contains only coordination state, never identity.
export function createSessionTransport(env: Environment) {
  const base = new URL('.', env.baseURL);
  const lockName = `at-auth:${base.pathname}`;
  const blockedKey = `${lockName}:blocked`;
  const revisionKey = `${lockName}:revision`;
  let enabled = false;
  let flight: Promise<boolean> | undefined;
  let localBlocked = false;
  let blockedFamily = '';
  let family = '';
  let localRevision = 0;
  let subject = '';
  const listeners = new Set<(identity: AuthIdentity | null, notice: string) => void>();
  const publish = (identity: AuthIdentity | null, notice = '') => {
    const verifiedFamily = identity?.claims?.session_id;
    if (verifiedFamily) {
      if (localBlocked && blockedFamily && verifiedFamily !== blockedFamily) {
        try {
          if (env.storage && !env.storage.getItem(blockedKey)) {
            localBlocked = false;
            blockedFamily = '';
          }
        } catch { /* A new identity alone cannot supersede shared uncertainty. */ }
      }
      family = verifiedFamily;
    }
    subject = identity?.subject || '';
    for (const listener of listeners) listener(identity, notice);
  };
  const revision = () => {
    try { return `${localRevision}:${env.storage?.getItem(revisionKey) || ''}`; }
    catch { return String(localRevision); }
  };
  const failClosed = (message: string): never => {
    publish(null, message);
    throw new ReauthenticationRequired(message);
  };
  const path = (url: string) => {
    const target = new URL(url, base);
    if (target.origin !== base.origin || target.username || target.password || !target.pathname.startsWith(base.pathname)) return '';
    return target.pathname.slice(base.pathname.length);
  };
  const selfPath = (value: string) => /^auth\/(workspaces|identities|identity-providers|settings|totp)(\/|$)/.test(value);
  const protectedPath = (value: string) => selfPath(value) || value.startsWith('api/') || value === 'auth/me' || value === 'auth/users' || value.startsWith('auth/users/') || value === 'auth/passkeys' || /^auth\/mobile\/requests\/[^/]+$/.test(value);
  const protectedMutation = (value: string) => selfPath(value) || value.startsWith('auth/reauth/') || value === 'auth/invitations/accept' || value === 'auth/password' || value === 'auth/users' || value.startsWith('auth/users/') || value.startsWith('auth/passkeys/enroll/') || /^auth\/passkeys\/[^/]+\/delete$/.test(value) || value === 'auth/mobile/approve' || value === 'auth/mobile/deny';
  const rawMe = () => env.fetch(new URL('auth/me', base), { credentials: 'same-origin', cache: 'no-store', redirect: 'error' });
  const readIdentity = async (response: Response) => {
    const identity = await response.json() as AuthIdentity;
    if (!identity || typeof identity.subject !== 'string' || !identity.subject) throw new Error('Invalid authentication identity');
    return identity;
  };

  // Caller owns the credential lock. Do not join flight here: it may be waiting for this lock.
  async function recoverLocked(start: string, expectedFamily?: string): Promise<boolean> {
    const verify = (identity?: AuthIdentity) => {
      if (start !== revision() || (identity && expectedFamily !== undefined && identity.claims?.session_id !== expectedFamily)) {
        return failClosed('Your session changed in another tab. Sign in again.');
      }
    };
    verify();
    // Always re-read under the shared lock: another tab may have rotated already.
    const response = await rawMe();
    verify();
    if (response.ok) {
      const identity = await readIdentity(response);
      verify(identity);
      publish(identity);
      return true;
    }
    if (response.status !== 401) throw setupRequiredError(response);
    if (!env.locks) return failClosed('Your session expired. This browser cannot safely renew sessions across tabs. Sign in again, or use a browser with Web Locks support.');
    try {
      if (!env.storage || localBlocked || env.storage.getItem(blockedKey)) {
        return failClosed('Your session could not be safely renewed. Sign in again.');
      }
      // Persist BEFORE dispatch. A lost response or closed tab must not replay a consumed cookie.
      env.storage.setItem(blockedKey, '1');
      localBlocked = true;
      blockedFamily = family;
    } catch (error) {
      if (error instanceof ReauthenticationRequired) throw error;
      return failClosed('Session renewal needs browser storage. Sign in again.');
    }
    try {
      const refreshed = await env.fetch(new URL('auth/refresh', base), {
        method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error',
        headers: { 'Content-Type': 'application/json' }, body: '{}',
      });
      if (!refreshed.ok) throw new Error('Refresh rejected');
      const identity = await readIdentity(refreshed);
      verify(identity);
      env.storage.removeItem(blockedKey);
      localBlocked = false;
      blockedFamily = '';
      publish(identity);
      return true;
    } catch {
      return failClosed('Your session could not be renewed. Sign in again. Renewal will not be retried automatically.');
    }
  }

  async function recover(isMe: boolean): Promise<boolean> {
    if (flight) return flight;
    const start = revision();
    flight = (async () => {
      // A handler's 401 (provider credentials, etc.) must not refresh a live session.
      if (!isMe) {
        const response = await rawMe();
        if (response.ok) return false;
        if (response.status !== 401) throw setupRequiredError(response);
      }
      if (!env.locks) return failClosed('Your session expired. This browser cannot safely renew sessions across tabs. Sign in again, or use a browser with Web Locks support.');
      return env.locks.request(lockName, () => recoverLocked(start));
    })();
    try { return await flight; }
    finally { flight = undefined; }
  }

  async function mutation<T>(value: string, send: () => Promise<T>, successful: (result: T) => boolean): Promise<T> {
    const start = revision();
    const initiatingFamily = family;
    const run = async () => {
      if (protectedMutation(value)) {
        if (!initiatingFamily) return failClosed('Verify your session before trying this operation again.');
        await recoverLocked(start, initiatingFamily);
      }
      const login = value === 'auth/login' || value === 'auth/passkeys/login/finish' || value === 'auth/mfa/verify';
      const changesSession = login || value === 'auth/logout' || value === 'auth/password' || /^auth\/passkeys\/[^/]+\/delete$/.test(value) || (subject !== '' && value.startsWith(`auth/users/${encodeURIComponent(subject)}/`));
      if (changesSession) {
        localRevision++;
        // On storage failure automatic refresh is disabled locally; it will also fail closed in other tabs without storage.
        try { env.storage?.setItem(revisionKey, crypto.randomUUID()); } catch { localBlocked = true; }
      }
      if (value === 'auth/logout' || login) {
        localBlocked = true;
        blockedFamily = family;
        try { env.storage?.setItem(blockedKey, '1'); } catch { /* Automatic refresh stays disabled. */ }
      }
      const result = await send();
      if (successful(result)) {
        if (login) {
          try { env.storage?.removeItem(blockedKey); localBlocked = false; blockedFamily = ''; } catch { localBlocked = true; }
        }
        if (value === 'auth/logout') publish(null, 'You have signed out.');
      }
      return result;
    };
    return env.locks ? env.locks.request(lockName, run) : run();
  }

  async function preflight() {
    // Without a middleware-only error code, a POST cannot be safely replayed.
    // Check before dispatch instead, including streaming POSTs with one-shot bodies.
    const response = await rawMe();
    if (response.ok) return;
    if (response.status !== 401) throw setupRequiredError(response);
    await recover(true);
  }

  return {
    async adoptExternalLogin(expectedSubject: string) {
      const adopt = async () => {
        const start = revision();
        const response = await rawMe();
        if (!response.ok) throw new ReauthenticationRequired('Sign-in could not be verified.');
        const identity = await readIdentity(response);
        if (identity.subject !== expectedSubject || start !== revision()) throw new ReauthenticationRequired('The signed-in account changed. Start again.');
        localRevision++;
        try { env.storage?.setItem(revisionKey, crypto.randomUUID()); env.storage?.removeItem(blockedKey); localBlocked = false; blockedFamily = ''; } catch { localBlocked = true; }
        publish(identity);
      };
      return env.locks ? env.locks.request(lockName, adopt) : adopt();
    },
    setEnabled(value: boolean) { enabled = value; },
    subscribe(listener: (identity: AuthIdentity | null, notice: string) => void) {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    storageChanged(key: string | null) {
      if (enabled && (key === revisionKey || key === null)) publish(null, 'Your session changed in another tab. Sign in again.');
    },
    wrapAdapter(adapter: AxiosAdapter): AxiosAdapter {
      return async config => {
        const value = path(axios.getUri(config));
        const method = (config.method || 'get').toUpperCase();
        const start = revision();
        if (enabled && value.startsWith('auth/') && method === 'POST') {
          return mutation(value, () => {
            if (config.signal?.aborted) throw new axios.CanceledError();
            // Do not release a credential lock while a cancelled request can still set cookies.
            return adapter({ ...config, signal: undefined, cancelToken: undefined, timeout: 0 });
          }, response => response.status < 400);
        }
        const eligible = enabled && protectedPath(value) && !config.auth && !config.headers.has('Authorization');
        if (eligible && method !== 'GET' && method !== 'HEAD') {
          await preflight();
          if (start !== revision()) throw new ReauthenticationRequired('Your session changed. Check the operation before trying again.');
          if (config.signal?.aborted) throw new axios.CanceledError();
        }
        let result: AxiosResponse | undefined;
        let failure: unknown;
        try { result = await adapter(config); } catch (error) {
          if (!axios.isAxiosError(error) || error.response?.status !== 401) throw error;
          result = error.response;
          failure = error;
        }
        if (eligible && result.status === 401 && start === revision()) {
          const recovered = await recover(value === 'auth/me');
          if (recovered && start === revision() && (method === 'GET' || method === 'HEAD')) {
            if (config.signal?.aborted) throw new axios.CanceledError();
            result = await adapter(config); // One retry, below interceptors and request transforms.
            failure = undefined;
          }
        }
        if (enabled && value === 'auth/me' && start !== revision()) throw new ReauthenticationRequired('Your session changed. Sign in again.');
        if (failure) throw failure;
        if (enabled && value === 'auth/me' && result.status === 200) {
          const identity = typeof result.data === 'string' ? JSON.parse(result.data) : result.data;
          if (identity?.subject) publish(identity);
        }
        return result;
      };
    },
    async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
      const url = input instanceof Request ? input.url : new URL(String(input), base).href;
      const value = path(url);
      // Leave independent gateway credentials, foreign origins and public endpoints untouched.
      if (!enabled || (!protectedPath(value) && !value.startsWith('auth/'))) return env.fetch(input, init);
      const original = new Request(input instanceof Request ? input : url, init);
      if (value.startsWith('auth/') && original.method === 'POST') {
        return mutation(value, () => {
          original.signal.throwIfAborted();
          return env.fetch(new Request(original, { signal: new AbortController().signal }));
        }, response => response.ok);
      }
      if (!protectedPath(value)) return env.fetch(original);
      if (original.headers.has('Authorization') || original.credentials === 'omit') return env.fetch(original);
      const start = revision();
      const replay = original.method === 'GET' || original.method === 'HEAD' ? original.clone() : undefined;
      if (!replay) {
        await preflight();
        if (start !== revision()) throw new ReauthenticationRequired('Your session changed. Check the operation before trying again.');
        original.signal.throwIfAborted();
      }
      let response = await env.fetch(original);
      if (response.status === 401 && start === revision()) {
        const recovered = await recover(value === 'auth/me');
        if (recovered && replay && start === revision()) {
          replay.signal.throwIfAborted();
          void response.body?.cancel();
          response = await env.fetch(replay);
        }
      }
      if (value === 'auth/me' && response.ok) {
        const identity = await readIdentity(response.clone());
        if (start !== revision()) throw new ReauthenticationRequired('Your session changed. Sign in again.');
        publish(identity);
      }
      return response;
    },
  };
}
