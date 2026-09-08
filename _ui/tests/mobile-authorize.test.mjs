import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/pages/MobileAuthorize.svelte', import.meta.url), 'utf8');
const script = source.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^\s*import .*;$/gm, '');
const code = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
const settle = async () => { await new Promise(resolve => setImmediate(resolve)); };
const id = 'A'.repeat(43);
const request = { request_id: id, device_name: '<img src=x onerror=alert(1)>', remember_me: true, expires_at: new Date(Date.now() + 60_000).toISOString(), issuer: 'https://at.example/at', callback_uri: 'atmobile://auth/callback' };

function harness({ enabled = true, valid = true, launchFails = false } = {}) {
  const read = deferred(), write = deferred(), calls = [], launches = [];
  let mount, cleanup, logins = 0;
  const window = { location: { hash: `#/mobile-authorize?request_id=${id}`, assign(url) { launches.push(url); if (launchFails) throw new Error('No handler'); } }, setInterval: () => 1, clearInterval() {} };
  class ReauthenticationRequired extends Error {}
  const args = {
    $props: () => ({ query: `request_id=${id}`, enabled, onlogin: () => { logins++; } }),
    $state: value => value, $derived: value => value, onMount: fn => { mount = fn; },
    axios: { isAxiosError: e => e?.response !== undefined },
    isAuthUnauthorized: e => e?.response?.status === 401, ReauthenticationRequired,
    mobileRequestID: () => valid ? id : null,
    getMobileAuthRequest: (...args) => { calls.push(['read', ...args]); return read.promise; },
    decideMobileAuthRequest: (...args) => { calls.push(['write', ...args]); return write.promise; },
    storeAuth: { identity: { name: 'User' } }, window, document: { baseURI: 'https://at.example/at/' },
  };
  const page = new Function(...Object.keys(args), `${code}\nreturn { decide, get phase() { return phase; }, get message() { return message; }, get request() { return request; } };`)(...Object.values(args));
  cleanup = mount();
  return { page, read, write, calls, launches, window, cleanup, get logins() { return logins; } };
}

test('consent is explicit, names remain text, and approval/denial are one-shot even without a handler', async () => {
  assert.ok(!source.includes('{@html'));
  assert.ok(source.includes('<bdi>{request.device_name'));
  for (const approve of [true, false]) {
    const h = harness({ launchFails: true });
    h.read.resolve(request); await settle();
    assert.equal(h.page.phase, 'ready');
    assert.equal(h.calls.length, 1, 'Loading must not decide');
    const pending = h.page.decide(approve);
    await h.page.decide(!approve);
    assert.equal(h.calls.length, 2);
    assert.equal(h.calls[1][2], approve);
    h.write.resolve('validated-callback'); await pending;
    assert.equal(h.page.phase, 'finished');
    assert.deepEqual(h.launches, ['validated-callback']);
    await h.page.decide(approve);
    assert.equal(h.calls.length, 2);
    h.cleanup();
  }
});

test('disabled native auth and invalid links do not load or approve', async () => {
  for (const options of [{ enabled: false }, { valid: false }]) {
    const h = harness(options);
    await h.page.decide(true);
    assert.equal(h.calls.length, 0);
    assert.equal(h.page.phase, 'error');
    h.cleanup();
  }
});

test('expired and mismatched server requests cannot be approved', async () => {
  for (const data of [{ ...request, expires_at: '2000-01-01T00:00:00Z' }, { ...request, issuer: 'https://evil.example' }, { ...request, request_id: 'other' }, { ...request, callback_uri: 'https://evil.example' }]) {
    const h = harness(); h.read.resolve(data); await settle();
    await h.page.decide(true);
    assert.equal(h.calls.length, 1);
    h.cleanup();
  }
});

test('unmount and hash changes suppress stale reads, decisions, and callback launches', async () => {
  for (const unmount of [true, false]) {
    for (const stage of ['read', 'write']) {
      const h = harness();
      let pending;
      if (stage === 'write') { h.read.resolve(request); await settle(); pending = h.page.decide(true); }
      if (unmount) h.cleanup(); else h.window.location.hash = '#/other';
      if (stage === 'read') { h.read.resolve(request); await settle(); assert.equal(h.page.request, null); }
      else { h.write.resolve('validated-callback'); await pending; }
      await h.page.decide(true);
      assert.equal(h.launches.length, 0);
      assert.equal(h.calls.length, stage === 'read' ? 1 : 2);
      if (!unmount) h.cleanup();
    }
  }
});

test('401 returns to login; conflict and uncertain decisions never retry or expose server errors', async () => {
  for (const status of [401, 400, 404, 409, 410, 503, undefined]) {
    const h = harness(); h.read.resolve(request); await settle();
    const pending = h.page.decide(true);
    h.write.reject({ response: { status, data: { message: 'secret-do-not-render' } } }); await pending;
    assert.equal(h.logins, status === 401 ? 1 : 0);
    assert.ok(!h.page.message.includes('secret-do-not-render'));
    await h.page.decide(true);
    assert.equal(h.calls.length, 2);
    assert.equal(h.launches.length, 0);
    h.cleanup();
  }
});

test('App handles only the exact consent route outside the admin Router and preserves the hash at login', async () => {
  const app = await readFile(new URL('../src/App.svelte', import.meta.url), 'utf8');
  assert.match(app, /\$location === '\/mobile-authorize' && \(authState === 'admin' \|\| authState === 'denied' \|\| authState === 'legacy'\)/);
  const branch = app.slice(app.indexOf('{:else if $location'), app.indexOf("{:else if authState === 'loading'"));
  assert.ok(branch.includes('<MobileAuthorize'));
  assert.ok(!branch.includes('<Router'));
  const login = await readFile(new URL('../src/lib/components/NativeLogin.svelte', import.meta.url), 'utf8');
  assert.ok(!login.includes('location.hash'));
});
