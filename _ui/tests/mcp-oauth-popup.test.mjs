import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/api/connections.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = { create: () => ({ post: (...args) => globalThis.mcpOAuthAPI.post(...args) }) };'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { connectMCPAccount, validMCPOAuthMessage } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

function browser(t, { channels = true, state = 'workspace.nonce' } = {}) {
  const originals = new Map(['window', 'location', 'BroadcastChannel', 'mcpOAuthAPI', 'clearInterval', 'clearTimeout'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  t.after(() => {
    for (const [key, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  });
  const listeners = new Map();
  const timers = new Map();
  const openedChannels = [];
  let navigated = false;
  const popup = {
    closed: false, document: {},
    close() { this.closed = true; },
    location: { replace() { navigated = true; assert.equal(openedChannels.length, channels ? 1 : 0); } },
  };
  globalThis.location = { origin: 'https://at.example' };
  globalThis.window = {
    open: () => popup,
    addEventListener: (name, fn) => listeners.set(name, fn),
    removeEventListener: name => listeners.delete(name),
    setInterval: fn => { timers.set('interval', fn); return 'interval'; },
    setTimeout: fn => { timers.set('timeout', fn); return 'timeout'; },
  };
  globalThis.clearInterval = id => timers.delete(id);
  globalThis.clearTimeout = id => timers.delete(id);
  globalThis.BroadcastChannel = channels ? class {
    constructor(name) { this.name = name; this.closed = false; openedChannels.push(this); }
    close() { this.closed = true; }
  } : undefined;
  globalThis.mcpOAuthAPI = { post: async () => ({ data: { authorize_url: `https://idp.example/authorize?state=${state}` } }) };
  const result = { type: 'at-mcp-oauth-result', ok: true, message: 'Account connected.', connection_id: 'c1', state };
  return { popup, listeners, timers, openedChannels, result, get navigated() { return navigated; } };
}

test('COOP-detached popup completes on the state-scoped channel and cleans up', async t => {
  const env = browser(t);
  const promise = connectMCPAccount({ set_id: 's1' });
  await Promise.resolve(); await Promise.resolve();
  assert.ok(env.navigated);
  const channel = env.openedChannels[0];
  assert.equal(channel.name, 'at-mcp-oauth:workspace.nonce');
  env.popup.closed = true; // COOP severs the WindowProxy before callback.
  env.timers.get('interval')();
  assert.equal(channel.closed, false);
  channel.onmessage({ data: env.result });
  assert.deepEqual(await promise, env.result);
  assert.equal(channel.closed, true);
  assert.equal(env.timers.size, 0);
  assert.equal(env.listeners.size, 0);
});

test('channel ignores foreign ceremonies and malformed results, then surfaces callback refusal', async t => {
  const env = browser(t);
  const promise = connectMCPAccount({ set_id: 's1' });
  await Promise.resolve(); await Promise.resolve();
  const channel = env.openedChannels[0];
  channel.onmessage({ data: { ...env.result, state: 'another.nonce' } });
  channel.onmessage({ data: { ...env.result, ok: 'true' } });
  assert.equal(channel.closed, false);
  channel.onmessage({ data: { ...env.result, ok: false, message: 'Authorization denied.' } });
  await assert.rejects(promise, /Authorization denied/);
  assert.equal(channel.closed, true);
});

test('original opener path still completes without BroadcastChannel', async t => {
  const env = browser(t, { channels: false });
  const promise = connectMCPAccount({ set_id: 's1' });
  await Promise.resolve(); await Promise.resolve();
  env.listeners.get('message')({ origin: location.origin, source: env.popup, data: env.result });
  assert.deepEqual(await promise, env.result);
  assert.equal(env.popup.closed, true);
});

test('closed popup still fails when no fallback is available', async t => {
  const env = browser(t, { channels: false });
  const promise = connectMCPAccount({ set_id: 's1' });
  await Promise.resolve(); await Promise.resolve();
  env.popup.closed = true;
  env.timers.get('interval')();
  await assert.rejects(promise, /closed before completion/);
});

test('detached or manually closed popup remains bounded by the authorization deadline', async t => {
  const env = browser(t);
  const promise = connectMCPAccount({ set_id: 's1' });
  await Promise.resolve(); await Promise.resolve();
  env.timers.get('timeout')();
  await assert.rejects(promise, /Authorization expired/);
  assert.equal(env.openedChannels[0].closed, true);
  assert.equal(env.timers.size, 0);
});

test('opener messages require exact origin and window', () => {
  const popup = {};
  const event = { origin: 'https://at.example', source: popup, data: { type: 'at-mcp-oauth-result', ok: true } };
  assert.ok(validMCPOAuthMessage(event, popup, event.origin));
  assert.equal(validMCPOAuthMessage({ ...event, source: {} }, popup, event.origin), false);
  assert.equal(validMCPOAuthMessage({ ...event, origin: 'https://evil.example' }, popup, event.origin), false);
});

const callbackSource = await readFile(new URL('../../internal/server/mcp-oauth-flow.go', import.meta.url), 'utf8');
const callbackScript = callbackSource.match(/<script nonce="%s">([\s\S]*?)<\/script>/)[1]
  .replace('var o=%s;', 'var o="https://at.example";').replace(',%s)', ',300)');

test('callback sends success and failure without an opener, then closes itself', () => {
  for (const ok of [true, false]) {
    const result = { type: 'at-mcp-oauth-result', ok, state: 'workspace.nonce', connection_id: ok ? 'c1' : '' };
    const sent = [];
    const timers = [];
    let windowClosed = false;
    let channelClosed = false;
    runInNewContext(callbackScript, {
      document: { getElementById: () => ({ textContent: JSON.stringify(result) }) },
      window: { opener: null, close: () => { windowClosed = true; } },
      BroadcastChannel: class {
        constructor(name) { assert.equal(name, 'at-mcp-oauth:workspace.nonce'); }
        postMessage(data) { sent.push(data); }
        close() { channelClosed = true; }
      },
      setTimeout: fn => timers.push(fn),
    });
    assert.equal(JSON.stringify(sent), JSON.stringify([result]));
    for (const fn of timers) fn();
    assert.ok(windowClosed);
    assert.ok(channelClosed);
  }
});

test('callback preserves exact-origin opener delivery if channels are blocked', () => {
  const result = { type: 'at-mcp-oauth-result', ok: true, state: 'workspace.nonce' };
  const sent = [];
  runInNewContext(callbackScript, {
    document: { getElementById: () => ({ textContent: JSON.stringify(result) }) },
    window: { opener: { postMessage: (data, origin) => sent.push({ data, origin }) } },
    BroadcastChannel: class { constructor() { throw new Error('Unavailable'); } },
    setTimeout: () => {},
  });
  assert.equal(JSON.stringify(sent), JSON.stringify([{ data: result, origin: 'https://at.example' }]));
});
