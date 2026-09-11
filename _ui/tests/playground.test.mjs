import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

// The harness compiles a single `.ts` module to a data URL, so the module under
// test must not import another relative module at runtime.
let calls = [];
let response;
let failure;
globalThis.playgroundAxiosMock = {
  create(config) {
    assert.deepEqual(config, { baseURL: 'api/v1' });
    return Object.fromEntries(['get', 'post', 'patch', 'delete'].map(method => [method, async (...args) => {
      calls.push([method, ...args]);
      if (failure) throw failure;
      return { data: response };
    }]));
  },
};

const source = await readFile(new URL('../src/lib/api/playground.ts', import.meta.url), 'utf8');
assert.ok(source.includes("import axios from 'axios';"));
assert.doesNotMatch(source, /^import\s+(?!type\b)[^;]*from\s+'\.\.?\//m, 'playground.ts must stay free of runtime relative imports');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = globalThis.playgroundAxiosMock;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.playgroundAxiosMock;

beforeEach(() => { calls = []; response = undefined; failure = undefined; });

const conversation = (overrides = {}) => ({
  id: 'c1', owner_user_id: 'u1', title: 'Hello', system_prompt: '', provider_key: 'openai', model: 'gpt-4o',
  config: {}, created_at: '2026-09-10T00:00:00Z', updated_at: '2026-09-10T00:00:00Z', ...overrides,
});
const message = (overrides = {}) => ({
  id: 'm1', conversation_id: 'c1', sequence: 1, role: 'user', provider_key: 'openai', model: 'gpt-4o',
  data: { content: 'hi' }, created_at: '2026-09-10T00:00:00Z', ...overrides,
});

test('conversation reads use the recency cursor and preserve the DTO verbatim', async () => {
  response = { data: [conversation(), conversation({ id: 'c2', forked_from_id: 'c1', forked_from_sequence: 4 })], meta: { limit: 50, next_before: 'c2' } };
  assert.deepEqual(await api.listPlaygroundConversations({ limit: 50 }), response);
  // `before` is a conversation id and is omitted when empty, never sent blank.
  await api.listPlaygroundConversations({ limit: 200, before: 'c2' });
  await api.listPlaygroundConversations({ before: '' });
  await api.listPlaygroundConversations();
  response = conversation();
  assert.deepEqual(await api.getPlaygroundConversation('a/b'), response);
  assert.deepEqual(calls, [
    ['get', '/playground/conversations?limit=50'],
    ['get', '/playground/conversations?limit=200&before=c2'],
    ['get', '/playground/conversations'],
    ['get', '/playground/conversations'],
    ['get', '/playground/conversations/a%2Fb'],
  ]);
});

test('absent fork lineage stays absent — never null', async () => {
  response = { data: [conversation()], meta: {} };
  const [plain] = (await api.listPlaygroundConversations()).data;
  assert.equal('forked_from_id' in plain, false);
  assert.equal('forked_from_sequence' in plain, false);
  // A parent deleted after the fork drops the id but keeps the branch point.
  response = { data: [conversation({ forked_from_sequence: 7 })], meta: {} };
  const [orphan] = (await api.listPlaygroundConversations()).data;
  assert.equal(orphan.forked_from_id, undefined);
  assert.equal(orphan.forked_from_sequence, 7);
});

test('meta.next_before is only trusted when the server sends it', async () => {
  for (const meta of [{}, { limit: 50 }, undefined]) {
    response = { data: [], meta };
    const page = await api.listPlaygroundConversations();
    assert.equal(page.meta?.next_before, undefined);
  }
});

test('create and patch send only allowlisted keys, and never an empty patch', async () => {
  response = conversation();
  await api.createPlaygroundConversation({ title: 'T', system_prompt: 'S', provider_key: 'p', model: 'm', config: { skills: ['a'] } });
  // Unknown keys are dropped: the backend decodes with DisallowUnknownFields.
  await api.createPlaygroundConversation({ title: 'T', id: 'nope', owner_user_id: 'nope', forked_from_id: 'nope', created_at: 'nope' });
  // Undefined values collapse away rather than serialising as null.
  await api.createPlaygroundConversation({ title: 'T', model: undefined });
  await api.createPlaygroundConversation();
  await api.patchPlaygroundConversation('c1', { title: 'Renamed' });
  await api.patchPlaygroundConversation('a/b', { config: {}, provider_key: 'p', bogus: 1 });
  assert.deepEqual(calls, [
    ['post', '/playground/conversations', { title: 'T', system_prompt: 'S', provider_key: 'p', model: 'm', config: { skills: ['a'] } }],
    ['post', '/playground/conversations', { title: 'T' }],
    ['post', '/playground/conversations', { title: 'T' }],
    ['post', '/playground/conversations', {}],
    ['patch', '/playground/conversations/c1', { title: 'Renamed' }],
    ['patch', '/playground/conversations/a%2Fb', { config: {}, provider_key: 'p' }],
  ]);
});

test('a no-op patch is skipped entirely: PATCH {} is a 400', async () => {
  assert.equal(await api.patchPlaygroundConversation('c1', {}), null);
  assert.equal(await api.patchPlaygroundConversation('c1', { title: undefined, model: undefined }), null);
  assert.equal(await api.patchPlaygroundConversation('c1', { bogus: 1 }), null);
  assert.deepEqual(calls, []);
  assert.equal(api.playgroundConversationBody({}), null);
  // An empty string is a real value and must still be sent.
  assert.deepEqual(api.playgroundConversationBody({ title: '' }), { title: '' });
});

test('delete, fork and truncate carry exact paths and bodies', async () => {
  assert.equal(await api.deletePlaygroundConversation('a/b'), undefined);
  response = conversation({ id: 'c2', forked_from_id: 'c1', forked_from_sequence: 4 });
  assert.deepEqual(await api.forkPlaygroundConversation('c1', 4), response);
  await api.forkPlaygroundConversation('c1', 4, 'Branch');
  assert.equal(await api.truncatePlaygroundMessages('a/b', 12), undefined);
  assert.deepEqual(calls, [
    ['delete', '/playground/conversations/a%2Fb'],
    ['post', '/playground/conversations/c1/fork', { from_sequence: 4 }],
    ['post', '/playground/conversations/c1/fork', { from_sequence: 4, title: 'Branch' }],
    ['delete', '/playground/conversations/a%2Fb/messages?from_sequence=12'],
  ]);
});

test('message reads page upward from the oldest returned id', async () => {
  response = { data: [message(), message({ id: 'm2', sequence: 2, role: 'assistant' })], meta: { limit: 200, next_before: 'm1' } };
  const page = await api.listPlaygroundMessages('c1', { limit: 200 });
  assert.deepEqual(page, response);
  // The cursor is the OLDEST message, so the next page is strictly older.
  await api.listPlaygroundMessages('c1', { limit: 200, before: page.meta.next_before });
  await api.listPlaygroundMessages('a/b');
  assert.deepEqual(calls, [
    ['get', '/playground/conversations/c1/messages?limit=200'],
    ['get', '/playground/conversations/c1/messages?limit=200&before=m1'],
    ['get', '/playground/conversations/a%2Fb/messages'],
  ]);
});

test('append sends role, provider, model and data only, in order', async () => {
  const stored = [message(), message({ id: 'm2', sequence: 2, role: 'assistant' }), message({ id: 'm3', sequence: 3, role: 'tool' })];
  response = { data: stored, meta: {} };
  const result = await api.appendPlaygroundMessages('a/b', [
    { role: 'user', provider_key: 'openai', model: 'gpt-4o', data: { content: 'hi' } },
    { role: 'assistant', provider_key: 'anthropic', model: 'claude', data: { content: '', tool_calls: [{ id: 't1' }] }, sequence: 99, id: 'nope' },
    { role: 'tool', provider_key: 'anthropic', model: 'claude', data: { content: 'ok', tool_call_id: 't1' } },
  ]);
  assert.deepEqual(result, stored);
  assert.deepEqual(calls, [['post', '/playground/conversations/a%2Fb/messages', { messages: [
    { role: 'user', provider_key: 'openai', model: 'gpt-4o', data: { content: 'hi' } },
    { role: 'assistant', provider_key: 'anthropic', model: 'claude', data: { content: '', tool_calls: [{ id: 't1' }] } },
    { role: 'tool', provider_key: 'anthropic', model: 'claude', data: { content: 'ok', tool_call_id: 't1' } },
  ] }]]);
  // A 201 with no body must not explode the caller.
  response = undefined;
  assert.deepEqual(await api.appendPlaygroundMessages('c1', [{ role: 'user', provider_key: 'p', model: 'm', data: {} }]), []);
  assert.equal(api.PLAYGROUND_MESSAGE_BATCH_MAX, 200);
});

test('image data-URIs are replaced by a descriptor, never silently dropped', () => {
  const png = `data:image/png;base64,${'A'.repeat(64)}`;
  const jpeg = 'data:image/jpeg;base64,BBBB';
  const data = { content: [
    { type: 'image_url', image_url: { url: png } },
    { type: 'image_url', image_url: { url: jpeg } },
    { type: 'text', text: 'describe these' },
  ] };
  const stripped = api.stripPlaygroundImages(data, ['shot.png']);
  assert.deepEqual(stripped.content, [
    { type: 'image', name: 'shot.png', bytes: png.length, omitted: true },
    { type: 'image', name: 'image-2.jpeg', bytes: jpeg.length, omitted: true },
    { type: 'text', text: 'describe these' },
  ]);
  // The in-memory transcript keeps its images: the input is never mutated.
  assert.equal(data.content[0].image_url.url, png);
  assert.notEqual(stripped, data);
  assert.ok(api.isOmittedImage(stripped.content[0]));
  assert.equal(api.isOmittedImage(stripped.content[2]), false);
  assert.equal(api.isOmittedImage(null), false);
  assert.equal(api.isOmittedImage('image'), false);
});

test('stripping leaves remote URLs, plain strings and foreign shapes alone', () => {
  const remote = { content: [{ type: 'image_url', image_url: { url: 'https://cdn.example/a.png' } }] };
  assert.deepEqual(api.stripPlaygroundImages(remote).content, remote.content);
  assert.deepEqual(api.stripPlaygroundImages({ content: 'plain text' }), { content: 'plain text' });
  assert.deepEqual(api.stripPlaygroundImages({ content: 'x', tool_call_id: 't1' }), { content: 'x', tool_call_id: 't1' });
  assert.deepEqual(api.stripPlaygroundImages({}), {});
  // Already-restored descriptors survive a second round trip unchanged.
  const restored = { content: [{ type: 'image', name: 'a.png', bytes: 12, omitted: true }] };
  assert.deepEqual(api.stripPlaygroundImages(restored), restored);
  // Non-content keys are preserved verbatim alongside the rewritten content.
  const mixed = api.stripPlaygroundImages({ content: [{ type: 'image_url', image_url: { url: 'data:image/webp;base64,AA' } }], tool_calls: [{ id: 't' }] });
  assert.deepEqual(mixed.tool_calls, [{ id: 't' }]);
  assert.equal(mixed.content[0].name, 'image-1.webp');
});

test('a stored image keeps a media reference, and only an unstorable one is omitted', async () => {
  const png = `data:image/png;base64,${'A'.repeat(64)}`;
  const jpeg = 'data:image/jpeg;base64,BBBB';
  const data = () => ({ content: [
    { type: 'image_url', image_url: { url: png } },
    { type: 'image_url', image_url: { url: jpeg } },
    { type: 'text', text: 'describe these' },
  ] });

  const uploaded = [];
  const source = data();
  const stored = await api.persistPlaygroundImages(source, ['shot.png'], async (url, name) => {
    uploaded.push([url, name]);
    return `01MEDIA${uploaded.length}`;
  });
  assert.deepEqual(stored.content, [
    { type: 'image', media_id: '01MEDIA1', name: 'shot.png', bytes: png.length },
    { type: 'image', media_id: '01MEDIA2', name: 'image-2.jpeg', bytes: jpeg.length },
    { type: 'text', text: 'describe these' },
  ]);
  // The uploader sees the original bytes and the positional name, in order.
  assert.deepEqual(uploaded, [[png, 'shot.png'], [jpeg, 'image-2.jpeg']]);
  // The in-memory transcript keeps its inline images: the input is never mutated.
  assert.equal(source.content[0].image_url.url, png);
  assert.notEqual(stored, source);
  assert.ok(api.isStoredImage(stored.content[0]));
  assert.equal(api.isOmittedImage(stored.content[0]), false);

  // 503 (storage disabled) must not lose the turn: fall back to the descriptor.
  const disabled = await api.persistPlaygroundImages(data(), ['shot.png'], async () => { throw { response: { status: 503, data: { message: 'media storage is disabled' } } }; });
  assert.deepEqual(disabled.content, [
    { type: 'image', name: 'shot.png', bytes: png.length, omitted: true },
    { type: 'image', name: 'image-2.jpeg', bytes: jpeg.length, omitted: true },
    { type: 'text', text: 'describe these' },
  ]);
  assert.ok(api.isOmittedImage(disabled.content[0]));
  assert.equal(api.isStoredImage(disabled.content[0]), false);

  // A reported-and-swallowed failure (413/415) is the same empty-id fallback.
  const rejected = await api.persistPlaygroundImages(data(), [], async () => '');
  assert.deepEqual(rejected.content.map(p => p.omitted ?? null), [true, true, null]);
  assert.deepEqual(rejected.content[0], { type: 'image', name: 'image-1.png', bytes: png.length, omitted: true });

  // A partial outcome keeps each image's own result — never all-or-nothing.
  const partial = await api.persistPlaygroundImages(data(), ['a.png', 'b.jpeg'], async (_, name) => (name === 'a.png' ? '01OK' : ''));
  assert.deepEqual(partial.content, [
    { type: 'image', media_id: '01OK', name: 'a.png', bytes: png.length },
    { type: 'image', name: 'b.jpeg', bytes: jpeg.length, omitted: true },
    { type: 'text', text: 'describe these' },
  ]);
});

test('persisting leaves remote URLs, plain strings and restored parts unuploaded', async () => {
  let uploads = 0;
  const upload = async () => { uploads += 1; return '01NOPE'; };
  const remote = { content: [{ type: 'image_url', image_url: { url: 'https://cdn.example/a.png' } }] };
  assert.deepEqual((await api.persistPlaygroundImages(remote, [], upload)).content, remote.content);
  assert.deepEqual(await api.persistPlaygroundImages({ content: 'plain text' }, [], upload), { content: 'plain text' });
  assert.deepEqual(await api.persistPlaygroundImages({}, [], upload), {});
  // Already-persisted parts survive a second round trip untouched, either shape.
  const restored = { content: [{ type: 'image', media_id: '01OLD', name: 'a.png', bytes: 12 }, { type: 'image', name: 'b.png', bytes: 12, omitted: true }] };
  assert.deepEqual(await api.persistPlaygroundImages(restored, [], upload), restored);
  assert.equal(uploads, 0, 'nothing without an inline data-URI is ever uploaded');
  // Non-content keys ride along verbatim.
  const mixed = await api.persistPlaygroundImages({ content: [{ type: 'image_url', image_url: { url: 'data:image/webp;base64,AA' } }], tool_calls: [{ id: 't' }] }, [], async () => '01W');
  assert.deepEqual(mixed.tool_calls, [{ id: 't' }]);
  assert.deepEqual(mixed.content, [{ type: 'image', media_id: '01W', name: 'image-1.webp', bytes: 25 }]);
  // Without an uploader it degrades to the strip-and-omit behaviour exactly.
  const noUploader = await api.persistPlaygroundImages({ content: [{ type: 'image_url', image_url: { url: 'data:image/png;base64,AA' } }] }, ['x.png']);
  assert.deepEqual(noUploader, api.stripPlaygroundImages({ content: [{ type: 'image_url', image_url: { url: 'data:image/png;base64,AA' } }] }, ['x.png']));
});

test('image guards reject foreign shapes rather than half-matching them', () => {
  for (const value of [null, undefined, 'image', 42, {}, { type: 'text' }, { type: 'image' }, { type: 'image', media_id: '' }, { type: 'image', media_id: 5 }, { type: 'image', media_id: '01K', omitted: true }]) {
    assert.equal(api.isStoredImage(value), false, JSON.stringify(value) ?? String(value));
  }
  assert.equal(api.isStoredImage({ type: 'image', media_id: '01K' }), true);
  assert.equal(api.isOmittedImage({ type: 'image', media_id: '01K' }), false);
});

test('errors surface the backend message and status, with a fallback otherwise', () => {
  for (const status of [400, 401, 403, 404, 409, 413, 503]) {
    const error = { response: { status, data: { message: `failure ${status}` } } };
    assert.equal(api.playgroundErrorMessage(error, 'fallback'), `failure ${status}`);
    assert.equal(api.playgroundErrorStatus(error), status);
  }
  for (const error of [new Error('network'), undefined, null, {}, { response: {} }, { response: { data: {} } }, { response: { data: { message: '' } } }, { response: { data: { message: 42 } } }]) {
    assert.equal(api.playgroundErrorMessage(error, 'Try again'), 'Try again');
    assert.equal(api.playgroundErrorStatus(error), 0);
  }
});

test('failures propagate without retrying or swallowing the request', async () => {
  failure = { response: { status: 503, data: { message: 'store unavailable' } } };
  const operations = [
    () => api.listPlaygroundConversations(),
    () => api.createPlaygroundConversation({ title: 'T' }),
    () => api.getPlaygroundConversation('c1'),
    () => api.patchPlaygroundConversation('c1', { title: 'T' }),
    () => api.deletePlaygroundConversation('c1'),
    () => api.forkPlaygroundConversation('c1', 2),
    () => api.listPlaygroundMessages('c1'),
    () => api.appendPlaygroundMessages('c1', [{ role: 'user', provider_key: 'p', model: 'm', data: {} }]),
    () => api.truncatePlaygroundMessages('c1', 2),
  ];
  assert.equal(operations.length, 9, 'all nine endpoints are covered');
  for (const operation of operations) {
    const before = calls.length;
    await assert.rejects(operation, error => error === failure);
    assert.equal(calls.length, before + 1);
  }
});

test('routes and derived titles stay bounded and URL safe', () => {
  assert.equal(api.playgroundRoute('c1'), '/playground/c1');
  assert.equal(api.playgroundRoute('a/b'), '/playground/a%2Fb');
  assert.equal(api.playgroundTitleFrom('  hello   there \n world '), 'hello there world');
  assert.equal(api.playgroundTitleFrom(''), 'Untitled conversation');
  assert.equal(api.playgroundTitleFrom('   '), 'Untitled conversation');
  assert.equal(api.playgroundTitleFrom(undefined), 'Untitled conversation');
  const long = api.playgroundTitleFrom('x'.repeat(500));
  assert.equal(long.length, 60);
  assert.ok(long.endsWith('…'));
  assert.equal(api.playgroundTitleFrom('x'.repeat(60)).length, 60);
});
