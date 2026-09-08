import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

let calls = [];
let response;
let failure;
globalThis.authAxiosMock = {
  create(config) {
    assert.deepEqual(config, { baseURL: 'auth' });
    return Object.fromEntries(['get', 'post'].map(method => [method, async (...args) => {
      calls.push([method, ...args]);
      if (failure) throw failure;
      return { data: response };
    }]));
  },
  isAxiosError: error => error?.isAxiosError === true,
};
const source = await readFile(new URL('../src/lib/api/auth.ts', import.meta.url), 'utf8');
assert.ok(source.includes("import axios from 'axios';"));
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = globalThis.authAxiosMock;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.authAxiosMock;

beforeEach(() => { calls = []; response = undefined; failure = undefined; });

test('mobile request IDs require one canonical 32-byte identifier', () => {
  const id = 'A'.repeat(43);
  assert.equal(api.mobileRequestID(`request_id=${id}`), id);
  for (const query of ['', 'request_id=', `request_id=${id}&request_id=${id}`, 'request_id=../me', `request_id=${'A'.repeat(42)}B`]) assert.equal(api.mobileRequestID(query), null);
});

test('mobile callbacks allow only the fixed target and exact code/state or denial/state pair', () => {
  const secret = 'A'.repeat(43);
  const base = 'atmobile://auth/callback';
  const query = `code=${secret}&state=${secret}`;
  assert.equal(api.validateMobileCallback(`${base}?${query}`, true), `${base}?${query}`);
  assert.equal(api.validateMobileCallback(`${base}?state=${secret}&error=access_denied`, false), `${base}?state=${secret}&error=access_denied`);
  for (const value of [undefined, null, {}, `https://auth/callback?${query}`, `atmobile://evil/callback?${query}`, `atmobile://user@auth/callback?${query}`, `atmobile://auth:80/callback?${query}`, `${base}/?${query}`, `atmobile://auth/a/../callback?${query}`, `${base}?${query}#`, `${base}?${query}#fragment`, ` ${base}?${query}`, `${base}?${query}\n`, `${base}?${query}&state=${secret}`, `${base}?${query}&code=${secret}`, `${base}?${query}&token=secret`, `${base}?code=${secret}`, `${base}?code=bad&state=${secret}`, `${base}?code=${secret}&state=bad`, `${base}?state=${secret}&error=access_denied`]) {
    assert.throws(() => api.validateMobileCallback(value, true));
  }
  assert.throws(() => api.validateMobileCallback(`${base}?${query}`, false));
  assert.throws(() => api.validateMobileCallback(`${base}?error=other&state=${secret}`, false));
});

test('mobile web endpoints send only the request ID, with no retry or credentials in the callback', async () => {
  const signal = new AbortController().signal;
  response = { request_id: 'id', device_name: '<script>untrusted</script>' };
  assert.deepEqual(await api.getMobileAuthRequest('a/b', signal), response);
  const secret = 'A'.repeat(43);
  response = { redirect_url: `atmobile://auth/callback?code=${secret}&state=${secret}` };
  assert.equal(await api.decideMobileAuthRequest('id', true, signal), response.redirect_url);
  response = { redirect_url: `atmobile://auth/callback?error=access_denied&state=${secret}` };
  await api.decideMobileAuthRequest('id', false, signal);
  assert.deepEqual(calls, [
    ['get', 'mobile/requests/a%2Fb', { signal, headers: { 'Cache-Control': 'no-cache' } }],
    ['post', 'mobile/approve', { request_id: 'id' }, { signal }],
    ['post', 'mobile/deny', { request_id: 'id' }, { signal }],
  ]);
  for (const status of [400, 401, 404, 409, 410, 429, 503]) {
    failure = { isAxiosError: true, response: { status } };
    const before = calls.length;
    await assert.rejects(api.decideMobileAuthRequest('id', true, signal));
    assert.equal(calls.length, before + 1);
  }
});

test('user pages preserve DTOs and send only the bounded cursor contract', async () => {
  response = { data: [{ id: 'u1', username: 'operator', admin: true, disabled: false }], next_cursor: 'u1' };
  assert.deepEqual(await api.listAuthUsers(), response);
  await api.listAuthUsers('opaque/after');
  assert.deepEqual(calls, [
    ['get', 'users', { params: { limit: 50, after: '' } }],
    ['get', 'users', { params: { limit: 50, after: 'opaque/after' } }],
  ]);
  response = { data: [], next_cursor: '' };
  assert.deepEqual(await api.listAuthUsers(), response);
});

test('creation returns an identity, not a list user', async () => {
  response = { subject: 'u1', name: 'operator', provider: 'native', roles: ['admin'] };
  const input = { username: 'operator', password: 'long test passphrase', admin: true };
  assert.deepEqual(await api.createAuthUser(input), response);
  assert.deepEqual(calls, [['post', 'users', input]]);
});

test('mutations encode IDs, send exact bodies and accept empty 204 responses', async () => {
  assert.equal(await api.setAuthUserEnabled('a/b', true), undefined);
  await api.setAuthUserEnabled('a/b', false);
  await api.revokeAuthUserSessions('a/b');
  await api.resetAuthUserPassword('a/b', 'new passphrase here');
  await api.changeAuthPassword('old passphrase here', 'new passphrase here');
  assert.deepEqual(calls, [
    ['post', 'users/a%2Fb/enable'], ['post', 'users/a%2Fb/disable'],
    ['post', 'users/a%2Fb/revoke-sessions'],
    ['post', 'users/a%2Fb/password', { password: 'new passphrase here' }],
    ['post', 'password', { current_password: 'old passphrase here', new_password: 'new passphrase here' }],
  ]);
});

test('policy counts Unicode characters and UTF-8 bytes without trimming passwords', () => {
  assert.ok(api.passwordPolicyError('a'.repeat(14)));
  assert.equal(api.passwordPolicyError('a'.repeat(15)), '');
  assert.equal(api.passwordPolicyError('a'.repeat(1024)), '');
  assert.ok(api.passwordPolicyError('a'.repeat(1025)));
  assert.ok(api.passwordPolicyError('😀'.repeat(14)));
  assert.equal(api.passwordPolicyError('😀'.repeat(256)), '');
  assert.ok(api.passwordPolicyError('😀'.repeat(257)));
});

test('errors preserve backend messages and distinguish authorization from other failures', async () => {
  for (const status of [400, 401, 403, 404, 409, 413, 415, 429, 503]) {
    failure = { isAxiosError: true, response: { status, data: { message: `failure ${status}` } } };
    await assert.rejects(api.listAuthUsers(), error => error === failure);
    assert.equal(api.authErrorMessage(failure, 'fallback'), `failure ${status}`);
    assert.equal(api.isAuthUnauthorized(failure), status === 401);
  }
  assert.equal(api.authErrorMessage(new Error('network'), 'Try again'), 'Try again');
  assert.equal(api.isAuthUnauthorized(new Error('network')), false);
});

test('relative auth URLs retain root and prefixed SPA base paths', () => {
  for (const path of ['/', '/at/', '/nested/at/']) {
    for (const endpoint of ['users', 'status', 'login', 'passkeys', 'passkeys/enroll/begin', 'passkeys/enroll/finish', 'passkeys/login/begin', 'passkeys/login/finish', 'passkeys/a%2Fb/delete']) {
      assert.equal(new URL(`auth/${endpoint}`, `https://at.example${path}#/users`).pathname, `${path}auth/${endpoint}`);
    }
  }
});

test('public status strictly opts into passkeys and rejects invalid native mode', async () => {
  for (const enabled of [false, true]) {
    for (const passkeys of [undefined, null, false, true, 'true', 1]) {
      response = { enabled, passkeys, remember_me: true, passkey_login: 'username-first' };
      assert.deepEqual(await api.getAuthStatus(), { enabled, passkeys: enabled && passkeys === true });
    }
  }
  for (response of [undefined, null, {}, { enabled: 'true' }, { enabled: 1 }]) await assert.rejects(api.getAuthStatus());
  assert.deepEqual(calls[0], ['get', 'status', { headers: { 'Cache-Control': 'no-cache' } }]);
});

test('password and username-first passkey login send explicit remember choices only at begin', async () => {
  const signal = new AbortController().signal;
  response = { subject: 'u1', name: 'operator', provider: 'native' };
  assert.deepEqual(await api.loginWithPassword('operator', ' password '), response);
  await api.loginWithPassword('operator', ' password ', true, signal);
  const publicKey = { challenge: 'AP_-', allowCredentials: [{ id: 'AP_-', type: 'public-key' }] };
  response = { publicKey };
  assert.deepEqual(await api.beginPasskeyLogin('operator', false, signal), { publicKey });
  await api.beginPasskeyLogin('operator', true, signal);
  const raw = { id: 'AP_-', rawId: 'AP_-', type: 'public-key', response: { clientDataJSON: 'AA', authenticatorData: '_w', signature: '-w', userHandle: null } };
  response = { subject: 'u1', name: 'operator', provider: 'native' };
  assert.deepEqual(await api.finishPasskeyLogin(raw, signal), response);
  assert.deepEqual(calls, [
    ['post', 'login', { username: 'operator', password: ' password ', remember_me: false }, { signal: undefined }],
    ['post', 'login', { username: 'operator', password: ' password ', remember_me: true }, { signal }],
    ['post', 'passkeys/login/begin', { username: 'operator', remember_me: false }, { signal }],
    ['post', 'passkeys/login/begin', { username: 'operator', remember_me: true }, { signal }],
    ['post', 'passkeys/login/finish', raw, { signal }],
  ]);
});

test('own passkeys preserve metadata, raw enrollment, encoded record ID and empty 204s', async () => {
  const signal = new AbortController().signal;
  response = { items: [{ id: 'record/id', name: 'Laptop', created_at: '2026-09-06T00:00:00Z', last_used_at: null }] };
  assert.deepEqual(await api.listAuthPasskeys(signal), response);
  response = { items: [] };
  assert.deepEqual(await api.listAuthPasskeys(signal), response);
  response = { publicKey: { challenge: 'AP_-', user: { id: 'AA' } } };
  assert.deepEqual(await api.beginPasskeyEnrollment('Laptop', ' password ', signal), response);
  const raw = { id: 'AP_-', rawId: 'AP_-', type: 'public-key', response: { clientDataJSON: 'AA', attestationObject: '_w', transports: ['internal', 'usb'] } };
  response = undefined;
  assert.equal(await api.finishPasskeyEnrollment(raw, signal), undefined);
  assert.equal(await api.deleteAuthPasskey('record/id', ' password ', signal), undefined);
  assert.deepEqual(calls, [
    ['get', 'passkeys', { signal }], ['get', 'passkeys', { signal }],
    ['post', 'passkeys/enroll/begin', { name: 'Laptop', current_password: ' password ' }, { signal }],
    ['post', 'passkeys/enroll/finish', raw, { signal }],
    ['post', 'passkeys/record%2Fid/delete', { current_password: ' password ' }, { signal }],
  ]);
});

test('passkey failures propagate without retries or automatic completion', async () => {
  const signal = new AbortController().signal;
  for (const status of [400, 401, 403, 409, 413, 415, 429, 503]) {
    failure = { isAxiosError: true, response: { status, data: { message: `failure ${status}` } } };
    for (const operation of [() => api.listAuthPasskeys(signal), () => api.beginPasskeyEnrollment('Laptop', 'password', signal), () => api.finishPasskeyEnrollment({}, signal), () => api.beginPasskeyLogin('operator', false, signal), () => api.finishPasskeyLogin({}, signal), () => api.deleteAuthPasskey('id', 'password', signal)]) {
      const before = calls.length;
      await assert.rejects(operation, error => error === failure);
      assert.equal(calls.length, before + 1);
    }
  }
});

test('native admin gating fails closed and self-mutations clear identity before reload', async () => {
  const source = await readFile(new URL('../src/lib/store/auth.svelte.ts', import.meta.url), 'utf8');
  const code = ts.transpileModule(`const $state = value => value;\n${source}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  const state = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
  assert.equal(state.isNativeAdmin(), false, 'Legacy or unauthenticated state must not grant access');
  state.storeAuth.identity = { subject: 'u1', name: 'user', provider: 'native' };
  assert.equal(state.isNativeAdmin(), false);
  state.storeAuth.identity.roles = ['user'];
  assert.equal(state.isNativeAdmin(), false);
  state.storeAuth.identity.roles = ['admin'];
  assert.equal(state.isNativeAdmin(), true);
  const previousWindow = globalThis.window;
  let reloaded = false;
  globalThis.window = { location: { reload() {
    assert.equal(state.storeAuth.identity, null);
    assert.equal(state.isNativeAdmin(), false);
    reloaded = true;
  } } };
  try { state.returnToLogin(); assert.equal(reloaded, true); }
  finally { globalThis.window = previousWindow; }
});
