import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import vm from 'node:vm';
import { brandAssets, loadBrandAssets } from '../brand-assets.js';
import { createServer } from 'vite';

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
  const assets = await loadBrandAssets();
  for (const icon of [...manifest.icons, { src: 'brand/favicon-192x192.png', sizes: '192x192' }]) {
    const png = assets.get(icon.src);
    assert.ok(png, `missing brand asset: ${icon.src}`);
    assert.equal(png.subarray(1, 4).toString(), 'PNG');
    assert.equal(`${png.readUInt32BE(16)}x${png.readUInt32BE(20)}`, icon.sizes);
  }
});

test('brand assets come from the shared source and offline branding needs no network', async () => {
  const assets = await loadBrandAssets();
  for (const [path, bytes] of assets) {
    if (path.startsWith('brand/')) {
      assert.deepEqual(bytes, await readFile(new URL(`../../assets/${path.slice(6)}`, import.meta.url)));
    }
  }
  assert.deepEqual(assets.get('favicon.ico'), assets.get('brand/favicon.ico'));
  const offline = assets.get('offline.html').toString();
  assert.ok(!offline.includes('__AT_BRAND_LOGO__'));
  const embedded = offline.match(/src="data:image\/svg\+xml;base64,([^"]+)"/);
  assert.ok(embedded, 'offline logo must be self-contained');
  assert.deepEqual(Buffer.from(embedded[1], 'base64'), assets.get('brand/favicon.svg'));
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
  for (const [, path] of html.matchAll(/href="\.\/(brand\/[^"]+)"/g)) {
    assert.ok(assets.has(path), `missing linked icon: ${path}`);
  }
});

test('development server serves the shared branding with correct MIME types and HEAD support', async () => {
  const server = await createServer({
    configFile: false,
    plugins: [brandAssets()],
    server: { host: '127.0.0.1', port: 0 },
    optimizeDeps: { noDiscovery: true, include: [] },
  });
  try {
    await server.listen();
    const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
    for (const [path, data] of await loadBrandAssets()) {
      const response = await fetch(`${origin}/${path}`);
      assert.equal(response.status, 200, path);
      assert.match(response.headers.get('content-type'), path.endsWith('.html') ? /text\/html/ : /image\//);
      assert.deepEqual(Buffer.from(await response.arrayBuffer()), data, path);
    }
    const head = await fetch(`${origin}/brand/favicon.svg`, { method: 'HEAD' });
    assert.equal(head.status, 200);
    assert.equal(await head.text(), '');
    assert.equal((await fetch(`${origin}/brand/missing.svg`)).status, 404);
  } finally { await server.close(); }
});
