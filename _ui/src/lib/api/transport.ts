import axios from 'axios';
import { createSessionTransport } from './session-transport';
import { createWorkspaceTransport } from './workspace-transport';
import { workspaceSelectionKey } from '../helper/workspace-selection';

let workspaceKey = '';
export const workspaceTransport = createWorkspaceTransport(document.baseURI);
export function bindWorkspaceIdentity(userID: string, sessionID: string) {
  const key = workspaceSelectionKey(new URL('.', document.baseURI).pathname, userID, sessionID);
  if (key === workspaceKey) return;
  workspaceKey = key;
  let selected = '';
  try { selected = sessionStorage.getItem(key) || ''; } catch { /* Keep selection in memory if storage is unavailable. */ }
  workspaceTransport.select(selected);
}
export function adoptWorkspaceSelection(id: string) {
  try { if (workspaceKey) sessionStorage.setItem(workspaceKey, id); } catch { /* Automatic admission can remain in memory. */ }
  workspaceTransport.select(id);
}
export async function switchWorkspace(id: string) {
  if (!workspaceKey) throw new Error('Verify your account before switching workspaces.');
  if (id) await axios.put('auth/workspaces/selection', { workspace_id: id });
  try { sessionStorage.setItem(workspaceKey, id); } catch { throw new Error('Allow session storage to switch workspaces safely.'); }
  workspaceTransport.select(id);
  navigator.serviceWorker?.controller?.postMessage({ type: 'at-cancel-workspace-media' });
  // A full remount clears every legacy domain cache and draft, including module stores.
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
