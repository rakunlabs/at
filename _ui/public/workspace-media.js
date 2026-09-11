/* Only bridges native media requests to the header-based workspace file API.
 * No credential, workspace, or response is persisted or cached by this worker. */
const pending = new Map();
self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
self.addEventListener('message', event => {
  if (event.data?.type !== 'at-cancel-workspace-media' || !event.source?.id) return;
  for (const controller of pending.get(event.source.id) || []) controller.abort();
});

function workspaceFor(client, signal) {
  return new Promise((resolve, reject) => {
    const channel = new MessageChannel();
    const finish = (id, error) => {
      clearTimeout(timer); signal.removeEventListener('abort', abort);
      channel.port1.close(); channel.port2.close();
      if (error) reject(error); else resolve(id);
    };
    const abort = () => finish('', new Error('Workspace changed'));
    const timer = setTimeout(() => finish('', new Error('Workspace unavailable')), 3000);
    channel.port1.onmessage = event => {
      const id = event.data?.workspace_id;
      if (typeof id !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(id)) finish('', new Error('Workspace required'));
      else finish(id);
    };
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) { abort(); return; }
    client.postMessage({ type: 'at-workspace-media-context' }, [channel.port2]);
  });
}

async function serve(event) {
  const controller = new AbortController();
  const clientID = event.clientId;
  const controllers = pending.get(clientID) || new Set();
  pending.set(clientID, controllers); controllers.add(controller);
  const release = () => { controllers.delete(controller); if (!controllers.size) pending.delete(clientID); };
  try {
    const client = clientID && await self.clients.get(clientID);
    if (!client || !client.url.startsWith(self.registration.scope)) throw new Error('Workspace required');
    const workspace = await workspaceFor(client, controller.signal);
    if (controller.signal.aborted) throw new Error('Workspace changed');
    const headers = new Headers(event.request.headers);
    headers.set('X-AT-Workspace-ID', workspace);
    // Range/If-Range and same-origin HttpOnly session cookies are preserved.
    const response = await fetch(new Request(event.request, { headers, credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: controller.signal }));
    if (controller.signal.aborted) { void response.body?.cancel(); throw new Error('Workspace changed'); }
    // Keep the controller registered until the body finishes, including video streams.
    if (!response.body) { release(); return response; }
    const reader = response.body.getReader();
    const stream = new ReadableStream({
      async pull(output) {
        try {
          const result = await reader.read();
          if (controller.signal.aborted) throw new Error('Workspace changed');
          if (result.done) { release(); output.close(); }
          else output.enqueue(result.value);
        } catch { release(); output.error(new Error('Media request ended')); }
      },
      cancel() { release(); controller.abort(); return reader.cancel(); },
    });
    return new Response(stream, { status: response.status, statusText: response.statusText, headers: response.headers });
  } catch {
    release();
    return new Response('Workspace media unavailable. Return to the app and select a workspace.', { status: 403, headers: { 'Cache-Control': 'no-store', 'Content-Type': 'text/plain' } });
  }
}

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  const target = new URL('api/v1/files/serve', self.registration.scope);
  if (url.origin !== target.origin || url.pathname !== target.pathname || !['GET', 'HEAD'].includes(event.request.method) || event.request.headers.has('X-AT-Workspace-ID')) return;
  event.respondWith(serve(event));
});
