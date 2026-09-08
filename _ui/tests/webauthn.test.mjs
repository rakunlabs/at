import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { afterEach, beforeEach, test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/webauthn.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const helper = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
const bytes = Uint8Array.from({ length: 256 }, (_, index) => index);
const encoded = Buffer.from(bytes).toString('base64url');
const creation = {
  challenge: encoded, rp: { id: 'at.example', name: 'AT' }, user: { id: 'AP_-', name: 'user', displayName: 'User' },
  pubKeyCredParams: [{ type: 'public-key', alg: -7 }], timeout: 300000,
  excludeCredentials: [{ type: 'public-key', id: 'AP_-', transports: ['usb', 'hybrid'] }],
  authenticatorSelection: { residentKey: 'preferred', userVerification: 'required' }, attestation: 'none',
};
const request = { challenge: encoded, rpId: 'at.example', timeout: 300000, userVerification: 'required', allowCredentials: creation.excludeCredentials };

beforeEach(() => {
  Object.defineProperty(globalThis, 'window', { configurable: true, writable: true, value: { isSecureContext: true, PublicKeyCredential: function () {} } });
  Object.defineProperty(globalThis, 'navigator', { configurable: true, writable: true, value: { credentials: { create: async () => null, get: async () => null } } });
});
afterEach(() => {
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow); else delete globalThis.window;
  if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator); else delete globalThis.navigator;
});

test('base64url round-trips every byte, empty buffers and offset views without padding', () => {
  assert.equal(helper.bufferToBase64URL(bytes.buffer), encoded);
  assert.deepEqual(new Uint8Array(helper.base64URLToBuffer(encoded)), bytes);
  assert.equal(helper.bufferToBase64URL(bytes.subarray(251, 255)), Buffer.from(bytes.subarray(251, 255)).toString('base64url'));
  assert.equal(helper.bufferToBase64URL(new ArrayBuffer(0)), '');
  assert.equal(helper.base64URLToBuffer('').byteLength, 0);
  for (const bad of ['a', 'AA=', '+/8', 'AA\n', '*']) assert.throws(() => helper.base64URLToBuffer(bad));
});

test('support fails closed without secure context or APIs, never probes platform biometrics', () => {
  window.PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable = () => { throw new Error('Must not be called'); };
  assert.equal(helper.isWebAuthnSupported(), true);
  window.isSecureContext = false;
  assert.equal(helper.isWebAuthnSupported(), false);
  window.isSecureContext = undefined;
  assert.equal(helper.isWebAuthnSupported(), false);
  window.isSecureContext = true;
  window.PublicKeyCredential = undefined;
  assert.equal(helper.isWebAuthnSupported(), false);
  window.PublicKeyCredential = function () {};
  navigator.credentials.get = undefined;
  assert.equal(helper.isWebAuthnSupported(), false);
  delete globalThis.navigator;
  assert.equal(helper.isWebAuthnSupported(), false);
  delete globalThis.window;
  assert.equal(helper.isWebAuthnSupported(), false);
});

test('fallback options decode all binary IDs and preserve policy, transports, and input', () => {
  const before = structuredClone({ creation, request });
  const create = helper.creationOptions(creation);
  const get = helper.requestOptions(request);
  for (const options of [create, get]) {
    assert.ok(options.challenge instanceof ArrayBuffer);
    assert.deepEqual(new Uint8Array(options.challenge), bytes);
    assert.equal(options.timeout, 300000);
  }
  assert.deepEqual(new Uint8Array(create.user.id), Uint8Array.of(0, 255, 254));
  assert.deepEqual(new Uint8Array(create.excludeCredentials[0].id), Uint8Array.of(0, 255, 254));
  assert.deepEqual(new Uint8Array(get.allowCredentials[0].id), Uint8Array.of(0, 255, 254));
  assert.deepEqual(create.excludeCredentials[0].transports, ['usb', 'hybrid']);
  assert.deepEqual(get.allowCredentials[0].transports, ['usb', 'hybrid']);
  assert.deepEqual(create.authenticatorSelection, creation.authenticatorSelection);
  assert.equal(get.userVerification, 'required');
  assert.deepEqual({ creation, request }, before);
  assert.equal(helper.creationOptions({ ...creation, excludeCredentials: undefined }).excludeCredentials, undefined);
  assert.equal(helper.requestOptions({ ...request, allowCredentials: undefined }).allowCredentials, undefined);
});

test('native JSON parsers are used when present and parser failures do not silently downgrade', () => {
  const nativeCreation = { native: 'creation' }, nativeRequest = { native: 'request' };
  window.PublicKeyCredential.parseCreationOptionsFromJSON = input => { assert.equal(input, creation); return nativeCreation; };
  window.PublicKeyCredential.parseRequestOptionsFromJSON = input => { assert.equal(input, request); return nativeRequest; };
  assert.equal(helper.creationOptions(creation), nativeCreation);
  assert.equal(helper.requestOptions(request), nativeRequest);
  window.PublicKeyCredential.parseCreationOptionsFromJSON = () => { throw new Error('bad options'); };
  assert.throws(() => helper.creationOptions(creation), /bad options/);
});

test('registration sends raw exact bytes and optional transports, not a wrapper or session handle', async () => {
  const signal = new AbortController().signal;
  const credential = { id: encoded, rawId: bytes.buffer, type: 'public-key', authenticatorAttachment: 'cross-platform', response: { clientDataJSON: bytes.buffer, attestationObject: bytes.buffer, getTransports() { return ['usb', 'hybrid']; } } };
  navigator.credentials.create = async options => { assert.equal(options.signal, signal); assert.deepEqual(new Uint8Array(options.publicKey.challenge), bytes); return credential; };
  assert.deepEqual(await helper.startRegistration(creation, signal), {
    id: encoded, rawId: encoded, type: 'public-key', authenticatorAttachment: 'cross-platform', response: { clientDataJSON: encoded, attestationObject: encoded, transports: ['usb', 'hybrid'] },
  });
  delete credential.response.getTransports;
  delete credential.authenticatorAttachment;
  const raw = helper.serializeCredential(credential);
  assert.equal('transports' in raw.response, false);
  assert.equal('authenticatorAttachment' in raw, false);
});

test('assertion serializes signature, authenticator data and null/empty/nonempty user handles', async () => {
  const signal = new AbortController().signal;
  for (const userHandle of [null, undefined, new ArrayBuffer(0), bytes.buffer]) {
    navigator.credentials.get = async options => {
      assert.equal(options.signal, signal);
      assert.equal(options.publicKey.userVerification, 'required');
      return { id: encoded, rawId: bytes.buffer, type: 'public-key', response: { clientDataJSON: bytes.buffer, authenticatorData: bytes.buffer, signature: bytes.buffer, userHandle } };
    };
    assert.deepEqual(await helper.startAuthentication(request, signal), {
      id: encoded, rawId: encoded, type: 'public-key', response: { clientDataJSON: encoded, authenticatorData: encoded, signature: encoded, userHandle: userHandle == null ? null : userHandle.byteLength ? encoded : '' },
    });
  }
});

test('null credentials and cancellation never produce a finish payload; abort is checked before and after browser calls', async () => {
  for (const [start, options, method] of [[helper.startRegistration, creation, 'create'], [helper.startAuthentication, request, 'get']]) {
    assert.equal(await start(options, new AbortController().signal), null);
    let controller = new AbortController();
    controller.abort();
    navigator.credentials[method] = async () => { assert.fail('already aborted'); };
    await assert.rejects(start(options, controller.signal), { name: 'AbortError' });
    controller = new AbortController();
    navigator.credentials[method] = async () => { controller.abort(); return {}; };
    await assert.rejects(start(options, controller.signal), { name: 'AbortError' });
    for (const name of ['NotAllowedError', 'AbortError', 'InvalidStateError', 'SecurityError']) {
      const error = new DOMException('Browser message', name);
      navigator.credentials[method] = async () => { throw error; };
      await assert.rejects(start(options, new AbortController().signal), e => e === error);
      assert.equal(helper.isPasskeyCancellation(error), ['NotAllowedError', 'AbortError'].includes(name));
    }
  }
  assert.equal(helper.isPasskeyCancellation(new Error('network')), false);
  assert.equal(helper.isPasskeyCancellation(null), false);
});
