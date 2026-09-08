import axios from 'axios';
import { createSessionTransport } from './session-transport';

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
axios.defaults.adapter = authSession.wrapAdapter(axios.getAdapter(axios.defaults.adapter));
window.addEventListener('storage', event => authSession.storageChanged(event.key));

export const authFetch = authSession.fetch;
