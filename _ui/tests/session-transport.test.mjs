import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import axios from 'axios';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/api/session-transport.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText.replace("from 'axios'", `from '${import.meta.resolve('axios')}'`);
const { createSessionTransport, ReauthenticationRequired } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const identity = { subject: 'u1', name: 'operator', roles: ['admin'], expires_at: '2026-09-07T12:10:00Z', claims: { session_id: 'non-bearer-id', session_expires_at: '2026-10-07T12:00:00Z', remember_me: true } };
const json = (status, data = {}) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };

test('external popup completion adopts only matching live me and releases refresh uncertainty', async () => {
  const h = harness(); const {transport} = h.tab();
  h.values.set('at-auth:/at/:blocked', '1');
  await assert.rejects(transport.adoptExternalLogin(identity.subject), ReauthenticationRequired);
  h.setLive(true);
  await assert.rejects(transport.adoptExternalLogin('different-user'), ReauthenticationRequired);
  assert.equal(h.values.get('at-auth:/at/:blocked'),'1');
  const observed = []; transport.subscribe(value => observed.push(value));
  await transport.adoptExternalLogin(identity.subject);
  assert.equal(h.values.has('at-auth:/at/:blocked'),false);
  assert.deepEqual(observed.at(-1),identity);
  assert.equal(h.calls.some(c => c.path.endsWith('/refresh')),false);
});

function harness(options = {}) {
  let live = false;
  const calls = [];
  const values = new Map();
  let queue = Promise.resolve();
  let held = false;
  const locks = { request(name, fn) {
    assert.equal(name, 'at-auth:/at/');
    const task = queue.then(async () => {
      assert.equal(held, false);
      held = true;
      try { return await fn(); } finally { held = false; }
    });
    queue = task.catch(() => {});
    return task;
  } };
  const storage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
  const rawFetch = async (input, init) => {
    const request = input instanceof Request ? input : new Request(new URL(String(input), 'https://at.example/at/'), init);
    const path = new URL(request.url).pathname;
    calls.push({ path, method: request.method, body: await request.clone().text(), held, credentials: request.credentials });
    if (options.handle) {
      const result = await options.handle(request, { path, live, setLive: value => { live = value; }, held });
      if (result) return result;
    }
    if (path === '/at/auth/me') return json(live ? 200 : 401, live ? identity : {});
    if (path === '/at/auth/refresh') {
      assert.equal(held, true);
      live = true;
      return json(200, identity);
    }
    return json(live ? 200 : 401, { ok: live });
  };
  const adapter = async config => {
    const response = await rawFetch(new URL(axios.getUri(config), 'https://at.example/at/'), {
      method: config.method.toUpperCase(), headers: config.headers.toJSON(), body: config.data,
    });
    const result = { status: response.status, statusText: '', data: await response.text(), headers: {}, config };
    if (!config.validateStatus || config.validateStatus(result.status)) return result;
    throw new axios.AxiosError('Rejected', 'ERR_BAD_REQUEST', config, {}, result);
  };
  const tab = () => {
    const transport = createSessionTransport({ baseURL: 'https://at.example/at/#/chat', fetch: rawFetch, storage: options.noStorage ? undefined : storage, locks: options.noLocks ? undefined : locks });
    transport.setEnabled(true);
    const api = axios.create({ baseURL: 'api/v1', adapter: transport.wrapAdapter(adapter) });
    return { transport, api, auth: axios.create({ baseURL: 'auth', adapter: transport.wrapAdapter(adapter) }) };
  };
  return { tab, calls, values, setLive: value => { live = value; }, locks };
}

test('axios clients and native fetch share a single rotation and adopt only identity', async () => {
  const h = harness();
  const { transport, api, auth } = h.tab();
  const adopted = [];
  transport.subscribe(value => adopted.push(value));
  const responses = await Promise.all([api.get('tasks'), api.get('providers'), transport.fetch('api/v1/files'), auth.get('me')]);
  assert.ok(responses.every(response => response.status === 200));
  const refreshes = h.calls.filter(call => call.path.endsWith('/refresh'));
  assert.equal(refreshes.length, 1);
  assert.equal(refreshes[0].body, '{}');
  assert.equal(refreshes[0].credentials, 'same-origin');
  assert.deepEqual(adopted.at(-1), identity);
  assert.equal(h.values.size, 0, 'Neither identity nor tokens are persisted');
  assert.ok(h.calls.some(call => call.path.endsWith('/me') && call.held));
});

test('mobile request lookup recovers a browser session, while decisions preflight under the lock and never replay', async () => {
  for (const decision of ['approve', 'deny']) {
    for (const status of [200, 401, 409, 503]) {
      const h = harness({ handle: async (_, { path, held }) => {
        if (path.endsWith(`/mobile/${decision}`)) {
          assert.equal(held, true);
          return json(status);
        }
      } });
      const { auth } = h.tab();
      await auth.get(`mobile/requests/${'A'.repeat(43)}`);
      h.setLive(false);
      const operation = auth.post(`mobile/${decision}`, { request_id: 'request' });
      if (status === 200) await operation;
      else await assert.rejects(operation);
      const writes = h.calls.filter(call => call.path.endsWith(`/mobile/${decision}`));
      assert.equal(writes.length, 1);
      assert.equal(writes[0].body, '{"request_id":"request"}');
      assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 2);
      assert.equal(h.calls.at(-1), writes[0], 'No post-decision refresh or retry');
    }
  }
});

test('mobile decision does not dispatch after account switch or cancellation during preflight', async () => {
  for (const changed of [true, false]) {
    let preflighting = false;
    const controller = new AbortController();
    const h = harness({ handle: async (_, { path }) => {
      if (preflighting && path.endsWith('/me')) {
        if (changed) return json(200, { ...identity, claims: { session_id: 'another-session' } });
        controller.abort();
      }
    } });
    h.setLive(true);
    const { auth } = h.tab();
    await auth.get('me');
    preflighting = true;
    await assert.rejects(auth.post('mobile/approve', { request_id: 'request' }, { signal: controller.signal }));
    assert.equal(h.calls.filter(call => call.path.endsWith('/mobile/approve')).length, 0);
  }
});

test('two browser tabs re-read me under one shared lock, rotating only once', async () => {
  const h = harness();
  const a = h.tab(), b = h.tab();
  const result = await Promise.all([a.auth.get('me'), b.auth.get('me')]);
  assert.ok(result.every(response => response.status === 200));
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
  assert.ok(h.calls.filter(call => call.path.endsWith('/me') && call.held).length >= 2);
});

test('a live me prevents retries or refresh for downstream 401, including admin chat', async () => {
  const h = harness({ handle: async (_, { path }) => path.includes('/api/') ? json(401, { error: { message: 'provider key rejected' } }) : undefined });
  h.setLive(true);
  const { api, transport } = h.tab();
  await assert.rejects(api.get('providers'), error => error.response.status === 401);
  await assert.rejects(api.post('chat/completions', { prompt: 'once' }), error => error.response.status === 401);
  const result = await transport.fetch('api/v1/chat/completions', { method: 'POST', body: '{"prompt":"once"}' });
  assert.equal(result.status, 401);
  assert.equal(h.calls.filter(call => call.path.includes('/api/')).length, 3);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
});

test('writes refresh before dispatch; JSON, multipart, and streaming request bodies are sent once', async () => {
  const h = harness();
  const { api, transport } = h.tab();
  await api.post('tasks', { title: 'one task' });
  const form = new FormData();
  form.append('file', new Blob(['file contents']), 'sample.txt');
  h.setLive(false);
  await transport.fetch(new Request('https://at.example/at/api/v1/files/upload', { method: 'POST', body: form }));
  h.setLive(false);
  await transport.fetch(new Request('https://at.example/at/api/v1/chat/completions', {
    method: 'POST', body: new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('stream body')); controller.close(); } }), duplex: 'half',
  }));
  const writes = h.calls.filter(call => call.path.includes('/api/'));
  assert.equal(writes.length, 3);
  assert.equal(writes[0].body, '{"title":"one task"}');
  assert.match(writes[1].body, /file contents/);
  assert.equal(writes[2].body, 'stream body');
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 3);
});

test('an ambiguous POST 401 is not replayed even if the session expires during execution', async () => {
  const h = harness({ handle: async (_, { path, setLive }) => {
    if (path.includes('/api/')) { setLive(false); return json(401); }
  } });
  h.setLive(true);
  const { api } = h.tab();
  await assert.rejects(api.post('tasks', { title: 'possibly created' }), error => error.response.status === 401);
  assert.equal(h.calls.filter(call => call.path.includes('/api/')).length, 1);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
});

test('foreign origins, gateway, bearer auth, public auth, password errors and legacy mode bypass recovery', async () => {
  const h = harness({ handle: async () => json(401) });
  const { transport, api, auth } = h.tab();
  for (const url of ['https://other.example/at/api/v1/tasks', 'https://at.example/other/api/v1/tasks', 'gateway/v1/chat/completions', 'auth/status']) {
    assert.equal((await transport.fetch(url)).status, 401);
    await assert.rejects(api.get(new URL(url, 'https://at.example/at/').href));
  }
  await assert.rejects(api.get('tasks', { headers: { Authorization: 'Bearer independent' } }));
  assert.equal((await transport.fetch('api/v1/tasks', { credentials: 'omit' })).status, 401);
  for (const url of ['login', 'refresh', 'logout', 'password', 'passkeys/login/begin', 'passkeys/login/finish', 'passkeys/enroll/begin']) await assert.rejects(auth.post(url, {}));
  transport.setEnabled(false);
  await assert.rejects(api.get('tasks'));
  assert.equal((await transport.fetch('api/v1/tasks')).status, 401);
  assert.equal(h.calls.filter(call => call.path.endsWith('/me')).length, 0);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1, 'Only the explicitly requested refresh was sent');
});

for (const failure of [401, 429, 503, 'network', 'malformed']) test(`refresh ${failure} blocks repeated rotation across tabs and reloads`, async () => {
  const h = harness({ handle: async (_, { path }) => {
    if (path.endsWith('/refresh')) {
      if (failure === 'network') throw new TypeError('Connection lost after possible commit');
      return failure === 'malformed' ? new Response('not JSON') : json(failure);
    }
  } });
  const a = h.tab(), b = h.tab();
  await Promise.all([assert.rejects(a.auth.get('me'), ReauthenticationRequired), assert.rejects(b.auth.get('me'), ReauthenticationRequired)]);
  await assert.rejects(h.tab().auth.get('me'), ReauthenticationRequired);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
  assert.equal(h.values.get('at-auth:/at/:blocked'), '1');
});

test('me network/503 failures do not rotate, and a later read may recover', async () => {
  let failure = true;
  const h = harness({ handle: async (_, { path }) => failure && path.endsWith('/me') ? json(503) : undefined });
  const { api } = h.tab();
  await assert.rejects(api.get('tasks'));
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
  failure = false;
  assert.equal((await api.get('tasks')).status, 200);
});

for (const option of ['noLocks', 'noStorage']) test(`${option} fails closed with reauthentication guidance`, async () => {
  const h = harness({ [option]: true });
  const { auth } = h.tab();
  await assert.rejects(auth.get('me'), ReauthenticationRequired);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
});

test('a second 401 is returned, not recursively retried', async () => {
  const h = harness({ handle: async (_, { path }) => path.includes('/api/') ? json(401) : undefined });
  const { api } = h.tab();
  await assert.rejects(api.get('tasks'), error => error.response.status === 401);
  assert.equal(h.calls.filter(call => call.path.includes('/api/')).length, 2);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
});

test('refresh and logout serialize; logout blocks later refresh and stale identity replay', async () => {
  const entered = deferred(), release = deferred();
  const h = harness({ handle: async (_, { path, setLive, held }) => {
    if (path.endsWith('/refresh')) { entered.resolve(); await release.promise; }
    if (path.endsWith('/logout')) { assert.equal(held, true); setLive(false); return new Response(null, { status: 204 }); }
  } });
  const a = h.tab(), b = h.tab();
  const refresh = a.auth.get('me').catch(error => error);
  await entered.promise;
  const logout = b.auth.post('logout', {});
  release.resolve();
  await Promise.all([refresh, logout]);
  await assert.rejects(a.auth.get('me'), ReauthenticationRequired);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
  assert.ok(h.calls.findIndex(call => call.path.endsWith('/logout')) > h.calls.findIndex(call => call.path.endsWith('/refresh')));
});

for (const endpoint of ['login', 'passkeys/login/finish']) test(`${endpoint} uses credential lock and clears a failed-rotation marker only on success`, async () => {
  const h = harness({ handle: async (_, { path, held, setLive }) => {
    if (path.endsWith(`/${endpoint}`)) { assert.equal(held, true); setLive(true); return json(200, identity); }
  } });
  h.values.set('at-auth:/at/:blocked', '1');
  const { auth } = h.tab();
  const body = endpoint === 'login' ? { username: 'operator', password: 'test only', remember_me: true } : { id: 'raw-id', response: { signature: 'raw-signature' } };
  assert.deepEqual((await auth.post(endpoint, body)).data, identity);
  assert.equal(h.values.has('at-auth:/at/:blocked'), false);
  assert.equal(h.calls[0].body, JSON.stringify(body));
  h.setLive(false);
  assert.equal((await auth.get('me')).status, 200);
});

test('aborting one waiter does not cancel shared refresh or replay its request', async () => {
  const entered = deferred(), release = deferred();
  const h = harness({ handle: async (_, { path }) => {
    if (path.endsWith('/refresh')) { entered.resolve(); await release.promise; }
  } });
  const { transport, api } = h.tab();
  const controller = new AbortController();
  const first = transport.fetch('api/v1/tasks', { signal: controller.signal });
  const second = api.get('providers');
  await entered.promise;
  controller.abort();
  release.resolve();
  await assert.rejects(first, error => error.name === 'AbortError');
  assert.equal((await second).status, 200);
  assert.equal(h.calls.filter(call => call.path.endsWith('/tasks')).length, 1);
});

test('default adapter inheritance covers pre-existing per-domain axios.create usage', async () => {
  const bootstrap = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8');
  assert.ok(bootstrap.indexOf("import '@/lib/api/transport'") < bootstrap.indexOf('import App'));
  const original = axios.defaults.adapter;
  try {
    let calls = 0;
    axios.defaults.adapter = async config => { calls++; return { status: 200, data: '{}', headers: {}, config }; };
    await Promise.all([axios.create({ baseURL: 'api/v1' }).get('/tasks'), axios.create({ baseURL: 'api/v1' }).get('/providers')]);
    assert.equal(calls, 2);
  } finally { axios.defaults.adapter = original; }
});

test('a delayed old me response cannot replace identity after logout in another tab', async () => {
  const entered = deferred(), release = deferred();
  let delayed = true;
  const h = harness({ handle: async (_, { path, setLive }) => {
    if (path.endsWith('/me') && delayed) {
      delayed = false;
      entered.resolve();
      await release.promise;
      return json(200, identity);
    }
    if (path.endsWith('/logout')) { setLive(false); return new Response(null, { status: 204 }); }
  } });
  h.setLive(true);
  const a = h.tab(), b = h.tab();
  const adopted = [];
  a.transport.subscribe(value => adopted.push(value));
  const pending = a.auth.get('me');
  await entered.promise;
  await b.transport.fetch('auth/logout', { method: 'POST', body: '{}' });
  release.resolve();
  await assert.rejects(pending, ReauthenticationRequired);
  assert.equal(adopted.length, 0);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
});

test('cancelled credential mutations retain the lock until their HTTP response settles', async () => {
  const entered = deferred(), release = deferred();
  const h = harness({ handle: async (_, { path, held }) => {
    if (path.endsWith('/login')) { assert.equal(held, true); entered.resolve(); await release.promise; return json(200, identity); }
    if (path.endsWith('/logout')) { assert.equal(held, true); return new Response(null, { status: 204 }); }
  } });
  const a = h.tab(), b = h.tab();
  const controller = new AbortController();
  const login = a.auth.post('login', {}, { signal: controller.signal });
  await entered.promise;
  controller.abort();
  const logout = b.transport.fetch('auth/logout', { method: 'POST' });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.calls.some(call => call.path.endsWith('/logout')), false);
  release.resolve();
  await assert.rejects(login, axios.isCancel);
  assert.equal((await logout).status, 204);
});

test('absolute session expiry rejected by refresh produces sign-in notice, never identity', async () => {
  const h = harness({ handle: async (_, { path }) => path.endsWith('/refresh') ? json(401, { message: 'refresh rejected; sign in again' }) : undefined });
  const { auth, transport } = h.tab();
  const notices = [];
  transport.subscribe((value, notice) => notices.push({ value, notice }));
  await assert.rejects(auth.get('me'), ReauthenticationRequired);
  assert.equal(notices.at(-1).value, null);
  assert.match(notices.at(-1).notice, /Sign in again/);
});

test('refresh resolves against root and nested BasePath, never another installation', async () => {
  for (const base of ['/', '/nested/at/']) {
    let live = false;
    const urls = [];
    const storage = new Map();
    const transport = createSessionTransport({
      baseURL: `https://at.example${base}#/tasks`,
      locks: { request: async (name, run) => { assert.equal(name, `at-auth:${base}`); return run(); } },
      storage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) },
      fetch: async input => {
        const url = input instanceof Request ? input.url : String(input);
        urls.push(url);
        if (url.endsWith('/refresh')) live = true;
        return json(live ? 200 : 401, identity);
      },
    });
    transport.setEnabled(true);
    assert.equal((await transport.fetch('auth/me')).status, 200);
    assert.deepEqual(urls, ['auth/me', 'auth/me', 'auth/refresh', 'auth/me'].map(path => `https://at.example${base}${path}`));
  }
});

for (const transport of ['axios', 'fetch']) {
  test(`${transport}: a reset queued during another tab's delayed login never uses the new family`, async () => {
    const entered = deferred(), release = deferred();
    let current = identity;
    const replacement = { ...identity, claims: { ...identity.claims, session_id: 'replacement-family' } };
    const h = harness({ handle: async (_, { path, live }) => {
      if (path.endsWith('/login')) {
        entered.resolve();
        await release.promise;
        current = replacement;
        return json(200, current);
      }
      if (path.endsWith('/me') && live) return json(200, current);
    } });
    h.setLive(true);
    const a = h.tab(), b = h.tab();
    await a.auth.get('me');
    const login = b.auth.post('login', {});
    await entered.promise; // Login has already updated the shared revision, but not the cookies.
    const reset = transport === 'axios'
      ? a.auth.post('users/other/password', { password: 'new password' })
      : a.transport.fetch('auth/users/other/password', { method: 'POST', body: '{"password":"new password"}' });
    const rejected = assert.rejects(reset, ReauthenticationRequired);
    release.resolve();
    await Promise.all([login, rejected]);
    assert.equal(h.calls.some(call => call.path.endsWith('/other/password')), false);
    assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
  });

  test(`${transport}: enrollment finish refreshes expired access under its lock and dispatches only once`, { timeout: 2000 }, async () => {
    const h = harness({ handle: async (_, { path, live, held }) => {
      if (path.includes('/enroll/')) {
        assert.equal(held, true);
        assert.equal(live, true, 'The challenge must not be consumed with expired access');
        return path.endsWith('/begin') ? json(200, { publicKey: {} }) : new Response(null, { status: 204 });
      }
    } });
    h.setLive(true);
    const a = h.tab();
    await a.auth.get('me');
    await a.auth.post('passkeys/enroll/begin', { name: 'Laptop', current_password: 'test password' });
    h.setLive(false); // Access expires while navigator.credentials.create is showing its prompt.
    const body = { id: 'raw-id', response: { attestationObject: 'raw-attestation' } };
    const response = transport === 'axios'
      ? await a.auth.post('passkeys/enroll/finish', body)
      : await a.transport.fetch('auth/passkeys/enroll/finish', { method: 'POST', body: JSON.stringify(body) });
    assert.equal(response.status, 204);
    const refresh = h.calls.findIndex(call => call.path.endsWith('/refresh'));
    const finish = h.calls.findIndex(call => call.path.endsWith('/enroll/finish'));
    assert.ok(refresh >= 0 && refresh < finish);
    assert.equal(h.calls[refresh].held, true);
    assert.equal(h.calls[finish].body, JSON.stringify(body));
    assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
    assert.equal(h.calls.filter(call => call.path.endsWith('/enroll/finish')).length, 1);
  });
}

for (const transport of ['axios', 'fetch']) test(`${transport}: a verified replacement family clears the old local block after another tab logs in`, async () => {
  let current = identity;
  let failed = false;
  const replacement = { ...identity, claims: { ...identity.claims, session_id: 'fresh-family' } };
  const h = harness({ handle: async (_, { path, live, setLive }) => {
    if (path.endsWith('/me') && live) return json(200, current);
    if (path.endsWith('/refresh')) {
      if (!failed) { failed = true; throw new TypeError('Lost refresh response'); }
      setLive(true);
      return json(200, current);
    }
    if (path.endsWith('/login')) { current = replacement; setLive(true); return json(200, current); }
  } });
  h.setLive(true);
  const a = h.tab(), b = h.tab();
  await a.auth.get('me');
  h.setLive(false);
  await assert.rejects(a.auth.get('me'), ReauthenticationRequired);
  assert.equal(h.values.get('at-auth:/at/:blocked'), '1');
  await b.auth.post('login', {});
  assert.equal(h.values.has('at-auth:/at/:blocked'), false);
  const verified = transport === 'axios' ? (await a.auth.get('me')).data : await (await a.transport.fetch('auth/me')).json();
  assert.deepEqual(verified, replacement);
  h.setLive(false);
  assert.deepEqual((await a.auth.get('me')).data, replacement);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 2);
});

for (const variant of ['same-family', 'missing-family', 'shared-block']) test(`${variant}: a 200 me does not erase refresh uncertainty`, async () => {
  let current = identity;
  const h = harness({ handle: async (_, { path, live }) => {
    if (path.endsWith('/me') && live) return json(200, current);
    if (path.endsWith('/refresh')) throw new TypeError('Lost refresh response');
  } });
  h.setLive(true);
  const a = h.tab();
  await a.auth.get('me');
  h.setLive(false);
  await assert.rejects(a.auth.get('me'), ReauthenticationRequired);
  if (variant !== 'shared-block') h.values.delete('at-auth:/at/:blocked');
  if (variant === 'missing-family') current = { ...identity, claims: {} };
  if (variant === 'shared-block') current = { ...identity, claims: { session_id: 'other-family' } };
  h.setLive(true);
  await a.auth.get('me');
  h.setLive(false);
  await assert.rejects(a.auth.get('me'), ReauthenticationRequired);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 1);
});

test('protected auth writes preflight but never replay a handler credential/challenge 401', async () => {
  const h = harness({ handle: async (_, { path }) => {
    if (path.endsWith('/password') || path.endsWith('/enroll/finish')) return json(401, { message: 'credentials rejected' });
  } });
  h.setLive(true);
  const a = h.tab();
  await a.auth.get('me');
  await assert.rejects(a.auth.post('password', {}), error => error.response.status === 401);
  await assert.rejects(a.auth.post('passkeys/enroll/finish', {}), error => error.response.status === 401);
  assert.equal(h.calls.filter(call => call.path.endsWith('/me') && call.held).length, 2);
  assert.equal(h.calls.filter(call => call.path.endsWith('/password')).length, 1);
  assert.equal(h.calls.filter(call => call.path.endsWith('/enroll/finish')).length, 1);
  assert.equal(h.calls.filter(call => call.path.endsWith('/refresh')).length, 0);
});
