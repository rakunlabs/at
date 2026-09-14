import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import vm from 'node:vm';

const source = await readFile(new URL('../public/workspace-media.js', import.meta.url), 'utf8');
function worker(scope = 'https://at.example/at/') {
  const listeners = {}, entries = new Map(), writes = [], removed = [];
  let disconnected = false;
  const cache = {
    async put(key, response) { writes.push(key); entries.set(key, response.clone()); },
    async match(key) { return entries.get(key)?.clone(); },
  };
  vm.runInNewContext(source, {
    URL, Request, Response, Map,
    caches: { open: async () => cache, keys: async () => [`at-offline:${scope}:v0`, 'another-app', 'at-offline:https://at.example/other/:v1'], delete: async key => { removed.push(key); } },
    fetch: async request => { if (disconnected) throw new TypeError('offline'); return new Response(request.url.endsWith('offline.html') ? 'public offline page' : 'live app', { headers: { 'Content-Type': 'text/html' } }); },
    self: { registration: { scope }, addEventListener: (type, fn) => { listeners[type] = fn; }, skipWaiting: async () => {}, clients: { claim: async () => {} } },
  });
  return {
    writes, removed,
    offline() { disconnected = true; },
    lifecycle(type) { let promise; listeners[type]({ waitUntil(value) { promise = value; } }); return promise; },
    request(path, mode = 'navigate', method = 'GET') {
      let result;
      listeners.fetch({ request: { url: new URL(path, scope).href, method, mode, headers: new Headers() }, respondWith(value) { result = value; } });
      return result;
    },
  };
}

test('offline fallback is public-only, network-first, and works at root and prefixed scopes', async () => {
  for (const scope of ['https://at.example/', 'https://at.example/at/']) {
    const h = worker(scope);
    await h.lifecycle('install');
    assert.deepEqual(h.writes, [`${scope}offline.html`]);
    assert.equal(await (await h.request('./')).text(), 'live app');
    h.offline();
    for (const path of ['./#/sessions', 'index.html']) assert.equal(await (await h.request(path)).text(), 'public offline page');
    for (const path of ['auth/me', 'auth/callback', 'api/v1/tasks', 'api/v1/files/serve?workspace_id=a', 'gateway/v1/chat/completions', 'assets/private.png', 'https://foreign.example/']) {
      assert.equal(h.request(path), undefined, path);
    }
    assert.equal(h.request('./', 'cors'), undefined);
    assert.equal(h.request('./', 'navigate', 'POST'), undefined);
    assert.equal(h.writes.length, 1, 'no network response is cached at runtime');
    await h.lifecycle('activate');
    assert.deepEqual(h.removed, [`at-offline:${scope}:v0`]);
  }
});

test('navigation remains actionable if the offline cache has been evicted', async () => {
  const h = worker(); h.offline();
  const response = await h.request('./');
  assert.equal(response.status, 503);
  assert.match(await response.text(), /Reconnect/);
});

test('manifest stays in the deployment scope and ships correctly sized PNG icons', async () => {
  const manifest = JSON.parse(await readFile(new URL('../public/manifest.webmanifest', import.meta.url), 'utf8'));
  assert.equal(manifest.display, 'standalone');
  for (const path of [manifest.id, manifest.scope, manifest.start_url, ...manifest.shortcuts.map(s => s.url)]) {
    assert.ok(new URL(path, 'https://at.example/at/').href.startsWith('https://at.example/at/'));
  }
  for (const icon of [...manifest.icons, { src: 'icons/apple-touch-icon.png', sizes: '180x180' }]) {
    const png = await readFile(new URL(`../public/${icon.src}`, import.meta.url));
    assert.equal(png.subarray(1, 4).toString(), 'PNG');
    assert.equal(`${png.readUInt32BE(16)}x${png.readUInt32BE(20)}`, icon.sizes);
  }
});
