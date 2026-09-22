import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

// The harness compiles a single `.ts` module to a data URL, so the module under
// test must not import another relative module at runtime.
const source = await readFile(new URL('../src/lib/helper/extension-bridge.ts', import.meta.url), 'utf8');
assert.doesNotMatch(source, /^import\s+(?!type\b)[^;]*from\s+'\.\.?\//m, 'extension-bridge.ts must stay free of runtime relative imports');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

const ORIGIN = 'https://at.example';

function fakeWindow(origin = ORIGIN) {
  const handlers = new Set();
  const win = {
    posted: [],
    addEventListener(type, handler) {
      if (type === 'message') handlers.add(handler);
    },
    removeEventListener(type, handler) {
      if (type === 'message') handlers.delete(handler);
    },
    postMessage(message, targetOrigin) {
      win.posted.push({ message, targetOrigin });
    },
    listenerCount: () => handlers.size,
    /** Deliver a message as the browser would, defaulting to a legitimate sender. */
    deliver(data, options = {}) {
      const event = {
        source: 'source' in options ? options.source : win,
        origin: options.origin ?? origin,
        data,
      };
      for (const handler of [...handlers]) handler(event);
    },
  };

  return win;
}

const envelope = (extra) => ({
  channel: api.EXTENSION_BRIDGE_CHANNEL,
  v: api.EXTENSION_BRIDGE_VERSION,
  ...extra,
});

/** The id of the request the page most recently posted. */
const lastRequestId = (win) => win.posted[win.posted.length - 1].message.id;

function newBridge(win, options = {}) {
  return new api.ExtensionBridge({ window: win, origin: ORIGIN, discoveryTimeoutMs: 20, requestTimeoutMs: 20, ...options });
}

test('discovery broadcasts, collects every answer and keeps the first of a repeated id', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const found = bridge.discover();

  const request = win.posted[0].message;
  assert.equal(request.channel, api.EXTENSION_BRIDGE_CHANNEL);
  assert.equal(request.v, 1);
  assert.equal(request.dir, 'request');
  assert.equal(request.method, 'describe');
  // A broadcast carries no addressee; that is what makes every extension answer.
  assert.equal('extension' in request, false);
  // Never posted to a wildcard target: the payload is addressed to this origin.
  assert.equal(win.posted[0].targetOrigin, ORIGIN);

  win.deliver(envelope({
    dir: 'response',
    id: request.id,
    extension: 'page-bridge',
    result: { name: 'Page Bridge', version: '1.2.3', capabilities: ['tools'], notice: 'No tab connected' },
  }));
  win.deliver(envelope({
    dir: 'response',
    id: request.id,
    extension: 'notes-ext',
    result: { id: 'ignored-in-favour-of-the-envelope', name: 'Notes', capabilities: ['tools'] },
  }));
  // A second answer under a known id must not redefine what was already shown.
  win.deliver(envelope({ dir: 'response', id: request.id, extension: 'page-bridge', result: { name: 'Impostor' } }));

  const descriptors = await found;
  assert.deepEqual(descriptors.map(d => d.id), ['page-bridge', 'notes-ext']);
  assert.deepEqual(descriptors[0], {
    id: 'page-bridge',
    name: 'Page Bridge',
    version: '1.2.3',
    capabilities: ['tools'],
    description: '',
    notice: 'No tab connected',
  });
  // The envelope is the routing identity, so it wins over a conflicting body id.
  assert.equal(descriptors[1].id, 'notes-ext');
  bridge.dispose();
});

test('discovery ignores messages that are not this protocol, this window or this origin', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const found = bridge.discover();
  const id = lastRequestId(win);
  const good = { dir: 'response', id, extension: 'good', result: { name: 'Good', capabilities: ['tools'] } };

  win.deliver({ ...envelope({ ...good }), channel: 'some.other.bus' });
  win.deliver({ ...envelope({ ...good }), v: 2 });
  win.deliver(envelope({ ...good }), { origin: 'https://evil.example' });
  win.deliver(envelope({ ...good }), { source: { other: 'frame' } });
  win.deliver(envelope({ dir: 'response', id: 'some-other-request', extension: 'good', result: { name: 'Good' } }));
  // Answered correctly at the end, so the assertion distinguishes "all
  // rejected" from "nothing was ever delivered".
  win.deliver(envelope({ ...good }));

  assert.deepEqual((await found).map(d => d.id), ['good']);
  bridge.dispose();
});

test('a malformed descriptor is dropped without hiding the extensions that answered correctly', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const found = bridge.discover();
  const id = lastRequestId(win);

  for (const result of [
    { name: 'No id at all' },
    { id: '../../etc/passwd', name: 'Traversal' },
    { id: 'has space', name: 'Spaces' },
    'not an object',
    null,
  ]) {
    win.deliver(envelope({ dir: 'response', id, result }));
  }
  win.deliver(envelope({ dir: 'response', id, extension: 'real', result: { capabilities: 'not-an-array' } }));

  const descriptors = await found;
  assert.deepEqual(descriptors.map(d => d.id), ['real']);
  // An extension that reports no name is listed under its id, not blank.
  assert.equal(descriptors[0].name, 'real');
  assert.deepEqual(descriptors[0].capabilities, []);
  bridge.dispose();
});

test('tools are addressed to one extension and answers from another are ignored', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const listed = bridge.listTools('page-bridge');
  const request = win.posted[0].message;
  assert.equal(request.method, 'tools/list');
  assert.equal(request.extension, 'page-bridge');

  win.deliver(envelope({ dir: 'response', id: request.id, extension: 'notes-ext', result: { tools: [{ name: 'steal' }] } }));
  win.deliver(envelope({
    dir: 'response',
    id: request.id,
    extension: 'page-bridge',
    result: {
      tools: [
        { name: 'click', description: 'Click an element', inputSchema: { type: 'object' } },
        { name: 'click', description: 'duplicate' },
        { name: 'bad name' },
        { description: 'nameless' },
        'not an object',
      ],
    },
  }));

  assert.deepEqual(await listed, [
    { name: 'click', description: 'Click an element', inputSchema: { type: 'object' } },
  ]);
  bridge.dispose();
});

test('a tool call sends name and arguments, and flattens the result to text', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const called = bridge.callTool('page-bridge', 'click', { uid: 'a1' });
  const request = win.posted[0].message;
  assert.equal(request.method, 'tools/call');
  assert.deepEqual(request.params, { name: 'click', arguments: { uid: 'a1' } });

  win.deliver(envelope({
    dir: 'response',
    id: request.id,
    extension: 'page-bridge',
    result: { content: [{ type: 'text', text: 'clicked' }, { type: 'image' }, { type: 'text', text: 'settled' }] },
  }));
  assert.equal(await called, 'clicked\nsettled');
  bridge.dispose();
});

test('both failure shapes reach the caller as errors rather than as content', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);

  // 1. A transport-level error envelope.
  const rejected = bridge.callTool('page-bridge', 'click', {});
  win.deliver(envelope({
    dir: 'response',
    id: lastRequestId(win),
    extension: 'page-bridge',
    error: { message: 'no tab is connected' },
  }));
  await assert.rejects(rejected, (e) => e.name === 'ExtensionBridgeError' && /no tab is connected/.test(e.message));

  // 2. An MCP-shaped result that reports isError. Returning this as text would
  //    make a failure read like a successful answer.
  const reportedError = bridge.callTool('page-bridge', 'click', {});
  win.deliver(envelope({
    dir: 'response',
    id: lastRequestId(win),
    extension: 'page-bridge',
    result: { isError: true, content: [{ type: 'text', text: 'element not found' }] },
  }));
  await assert.rejects(reportedError, (e) => e.kind === 'tool' && /element not found/.test(e.message));
  bridge.dispose();
});

test('an unanswered call times out, while an unanswered broadcast is simply empty', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);

  await assert.rejects(bridge.listTools('ghost'), (e) => e.kind === 'timeout');
  // Silence is the documented answer to discovery: an extension that was never
  // connected to this origin does not reply, and that is not an error.
  assert.deepEqual(await bridge.discover(), []);
  bridge.dispose();
});

test('a cancelled turn cancels the call in flight', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win, { requestTimeoutMs: 5000 });
  const controller = new AbortController();
  const called = bridge.callTool('page-bridge', 'click', {}, controller.signal);
  controller.abort();
  await assert.rejects(called, (e) => e.kind === 'unavailable' && /cancelled/.test(e.message));

  // An already-aborted signal fails immediately rather than waiting out the deadline.
  await assert.rejects(bridge.callTool('page-bridge', 'click', {}, controller.signal), (e) => e.kind === 'unavailable');
  bridge.dispose();
});

test('events reach subscribers until they unsubscribe, and only for this protocol', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win);
  const seen = [];
  const stop = bridge.subscribe(event => seen.push(event));

  win.deliver(envelope({ dir: 'event', extension: 'page-bridge', event: 'announce' }));
  win.deliver(envelope({ dir: 'event', extension: 'page-bridge', event: 'tools_changed' }));
  win.deliver(envelope({ dir: 'event', extension: 'page-bridge', event: 'made-up' }));
  win.deliver(envelope({ dir: 'event', event: 'announce' }));
  stop();
  win.deliver(envelope({ dir: 'event', extension: 'page-bridge', event: 'goodbye' }));

  assert.deepEqual(seen, [
    { extension: 'page-bridge', event: 'announce' },
    { extension: 'page-bridge', event: 'tools_changed' },
  ]);
  bridge.dispose();
});

test('dispose stops listening and settles what was in flight', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win, { requestTimeoutMs: 5000, discoveryTimeoutMs: 5000 });
  const listed = bridge.listTools('page-bridge');
  const found = bridge.discover();

  bridge.dispose();
  assert.equal(win.listenerCount(), 0);
  await assert.rejects(listed, (e) => e.kind === 'unavailable');
  // A broadcast has no failure mode; it resolves with what it had.
  assert.deepEqual(await found, []);
  await assert.rejects(bridge.listTools('page-bridge'), (e) => e.kind === 'unavailable');
});

test('a page with no verifiable origin refuses to speak at all', async () => {
  const win = fakeWindow();
  const bridge = newBridge(win, { origin: '' });
  await assert.rejects(bridge.listTools('page-bridge'), (e) => e.kind === 'unavailable');
  assert.deepEqual(win.posted, []);
  bridge.dispose();
});

test('a bare string result is accepted, because the protocol must be cheap to implement', () => {
  assert.deepEqual(api.parseToolResult('done'), { text: 'done', isError: false });
  assert.deepEqual(api.parseToolResult({ text: 'done' }), { text: 'done', isError: false });
  assert.deepEqual(api.parseToolResult({ content: ['a', { text: 'b' }] }), { text: 'a\nb', isError: false });
  assert.deepEqual(api.parseToolResult({ content: [], isError: true }), { text: '', isError: true });
  assert.deepEqual(api.parseToolResult(null), { text: '', isError: false });
});

test('approvals are per extension, survive a rewrite and keep their own key space', () => {
  const store = new Map();
  const storage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
  };

  assert.equal(api.extensionApprovalFor('page-bridge', storage), null);
  api.approveExtension('page-bridge', ['click', 'type_text'], storage);
  api.approveExtension('notes-ext', ['save'], storage);

  const approval = api.extensionApprovalFor('page-bridge', storage);
  assert.deepEqual(approval.tools, ['click', 'type_text']);
  assert.ok(Date.parse(approval.at) > 0);

  api.revokeExtension('page-bridge', storage);
  assert.equal(api.extensionApprovalFor('page-bridge', storage), null);
  // Revoking one extension must not revoke another.
  assert.deepEqual(api.extensionApprovalFor('notes-ext', storage).tools, ['save']);
  // The local-MCP approvals live under their own key and are untouched.
  assert.deepEqual([...store.keys()], ['at.extensions.approved']);

  // Unusable storage means the approval does not persist and is asked for
  // again, which is the safe direction.
  assert.equal(api.extensionApprovalFor('page-bridge', undefined), null);
  api.approveExtension('page-bridge', ['click'], undefined);
  assert.equal(api.extensionApprovalFor('page-bridge', undefined), null);
});
