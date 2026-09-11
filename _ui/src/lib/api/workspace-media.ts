import { workspaceTransport } from './transport';

export async function initializeWorkspaceMedia(): Promise<void> {
  if (!('serviceWorker' in navigator) || !window.isSecureContext) return;
  const workerURL = new URL('workspace-media.js', document.baseURI);
  navigator.serviceWorker.addEventListener('message', event => {
    if (event.source !== navigator.serviceWorker.controller || event.data?.type !== 'at-workspace-media-context' || event.ports.length !== 1) return;
    event.ports[0].postMessage({ workspace_id: workspaceTransport.selected });
    event.ports[0].close();
  });
  try {
    await navigator.serviceWorker.register(workerURL.href, { scope: new URL('.', document.baseURI).pathname });
    await navigator.serviceWorker.ready;
    if (!navigator.serviceWorker.controller) await new Promise<void>(resolve => {
      const changed = () => { clearTimeout(timer); navigator.serviceWorker.removeEventListener('controllerchange', changed); resolve(); };
      const timer = window.setTimeout(changed, 3000);
      navigator.serviceWorker.addEventListener('controllerchange', changed);
    });
  } catch { /* Scoped media fails closed; ordinary application requests remain usable. */ }
}
