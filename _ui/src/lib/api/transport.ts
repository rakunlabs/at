import axios from 'axios';
import { createSessionTransport } from './session-transport';
import { createWorkspaceTransport } from './workspace-transport';

const workspaceKey = `at-workspace:${new URL('.', document.baseURI).pathname}`;
let selected = '';
try { selected = sessionStorage.getItem(workspaceKey) || ''; } catch { /* In-memory selection remains available. */ }
export const workspaceTransport = createWorkspaceTransport(document.baseURI, selected);
export function switchWorkspace(id: string) {
  try { sessionStorage.setItem(workspaceKey, id); } catch { throw new Error('Allow session storage to switch workspaces safely.'); }
  workspaceTransport.select(id);
  navigator.serviceWorker?.controller?.postMessage({ type: 'at-cancel-workspace-media' });
  // A full remount clears every legacy domain cache and draft, including module stores.
  window.location.hash = '#/settings/workspace';
  window.location.reload();
}

let storage: Storage | undefined;
try { storage = window.localStorage; } catch { /* Refresh fails closed without shared coordination state. */ }

export const authSession = createSessionTransport({
  baseURL: document.baseURI,
  fetch: window.fetch.bind(window),
  locks: navigator.locks,
  storage,
});

// Imported before App/routes: every existing axios.create inherits this adapter.
// Interceptors on the default axios instance would NOT propagate to those clients.
axios.defaults.adapter = workspaceTransport.wrapAdapter(authSession.wrapAdapter(axios.getAdapter(axios.defaults.adapter)));
window.addEventListener('storage', event => authSession.storageChanged(event.key));

export const authFetch = workspaceTransport.wrapFetch(authSession.fetch);
// Native fetch callers and streams share the same scope boundary as axios.
window.fetch = authFetch;
