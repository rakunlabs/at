import { identityAPI, type RecentProof } from '../api/identity';
import type { LoginResult } from '../api/auth';
export interface AuthBridge { type: 'at-auth-result'; result: LoginResult | RecentProof | { linked?: boolean; error?: boolean; message?: string }; continue?: string }
export function validAuthMessage(event: Pick<MessageEvent, 'origin' | 'source' | 'data'>, popup: Window, origin: string): event is MessageEvent<AuthBridge> {
  return event.origin === origin && event.source === popup && event.data?.type === 'at-auth-result' && event.data.result !== null && typeof event.data.result === 'object';
}
export function localContinuation(value: unknown, base: string): string | undefined {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || /[\\\r\n]/.test(value)) return;
  const url = new URL(value, base);
  if (url.origin === new URL(base).origin) return url.pathname + url.search + url.hash;
}
// Call directly inside a click handler: opening after await loses user activation.
export function externalPopup(provider: string, body: Record<string, unknown>, signal?: AbortSignal): Promise<AuthBridge> {
  const popup = window.open('about:blank', '_blank', 'popup,width=520,height=720');
  if (!popup) return Promise.reject(new Error('Allow popups for this site, then try again.'));
  popup.document.title = 'Connecting to your identity provider…';
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (error?: Error, result?: AuthBridge) => {
      if (settled) return; settled = true;
      clearInterval(timer); clearTimeout(timeout); window.removeEventListener('message', receive);
      signal?.removeEventListener('abort', abort); popup.close();
      if (error) reject(error); else resolve(result!);
    };
    const receive = (event: MessageEvent) => {
      if (!validAuthMessage(event, popup, location.origin)) return;
      if ('error' in event.data.result && event.data.result.error) finish(new Error('The identity provider could not complete this request. Start again.'));
      else finish(undefined, { ...event.data, continue: localContinuation(event.data.continue, location.href) });
    };
    const abort = () => finish(new Error('Sign-in cancelled.'));
    const timer = window.setInterval(() => { if (popup.closed) finish(new Error('The sign-in window closed before completion. Try again.')); }, 500);
    const timeout = window.setTimeout(() => finish(new Error('Sign-in expired. Start again.')), 300_000);
    window.addEventListener('message', receive); signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) { abort(); return; }
    identityAPI.post(`external/${encodeURIComponent(provider)}/begin`, body, { signal }).then(({ data }) => {
      if (settled) return;
      const url = new URL(data.authorization_url);
      if (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) throw new Error('Invalid authorization URL');
      popup.location.replace(url.href);
    }).catch(() => finish(new Error('Cannot start identity-provider sign-in. Check the provider configuration and try again.')));
  });
}
