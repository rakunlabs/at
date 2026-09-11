import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

// The harness compiles a single `.ts` module to a data URL, so the module under
// test must not import another relative module at runtime.
let calls = [];
let response;
let failure;
globalThis.mediaAxiosMock = {
  create(config) {
    assert.deepEqual(config, { baseURL: 'api/v1' });
    return Object.fromEntries(['get', 'post', 'put', 'delete'].map(method => [method, async (...args) => {
      calls.push([method, ...args]);
      if (failure) throw failure;
      return { data: response };
    }]));
  },
};

const source = await readFile(new URL('../src/lib/api/media.ts', import.meta.url), 'utf8');
assert.ok(source.includes("import axios from 'axios';"));
assert.doesNotMatch(source, /^import\s+(?!type\b)[^;]*from\s+'\.\.?\//m, 'media.ts must stay free of runtime relative imports');
const code = ts.transpileModule(source.replace("import axios from 'axios';", 'const axios = globalThis.mediaAxiosMock;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
delete globalThis.mediaAxiosMock;

beforeEach(() => { calls = []; response = undefined; failure = undefined; });

const s3 = (overrides = {}) => ({
  endpoint: 'https://s3.example', region: 'us-east-1', bucket: 'b', prefix: 'playground/',
  access_key_id: 'AKIA…', secret_access_key: '', use_path_style: true, ...overrides,
});
const settings = (overrides = {}) => ({
  version: 2, backend: 's3', filesystem: { root: '' }, s3: s3(), secret_access_key_set: true, ...overrides,
});
const error = (status, message) => ({ response: { status, ...(message === undefined ? {} : { data: { message } }) } });

test('settings reads preserve the redacted document verbatim', async () => {
  response = settings();
  assert.deepEqual(await api.getMediaSettings(), response);
  // The secret is never returned: `secret_access_key_set` is the only signal.
  assert.equal(response.s3.secret_access_key, '');
  assert.equal(response.secret_access_key_set, true);
  assert.deepEqual(calls, [['get', '/media/settings']]);
});

test('writes rebuild the document from the allowlist and never invent a field', async () => {
  const stored = settings();
  response = settings({ version: 3 });
  await api.putMediaSettings(stored);
  await api.testMediaSettings(stored);
  const body = { version: 2, backend: 's3', filesystem: { root: '' }, s3: s3() };
  assert.deepEqual(calls, [
    ['put', '/media/settings', body],
    ['post', '/media/settings/test', body],
  ]);
  // Any unknown key is a 400, and `secret_access_key_set` is read-only.
  for (const [, , sent] of calls) {
    assert.equal('secret_access_key_set' in sent, false);
    assert.deepEqual(Object.keys(sent).sort(), ['backend', 'filesystem', 's3', 'version']);
    assert.deepEqual(Object.keys(sent.s3).sort(), ['access_key_id', 'bucket', 'endpoint', 'prefix', 'region', 'secret_access_key', 'use_path_style']);
    assert.deepEqual(Object.keys(sent.filesystem), ['root']);
  }
});

test('an empty secret is sent as "" to keep the stored one, never omitted', () => {
  const body = api.mediaSettingsBody(settings());
  assert.equal('secret_access_key' in body.s3, true);
  assert.equal(body.s3.secret_access_key, '');
  // A typed replacement rides through verbatim, including surrounding spaces.
  assert.equal(api.mediaSettingsBody(settings({ s3: s3({ secret_access_key: ' s3cret ' }) })).s3.secret_access_key, ' s3cret ');
  // Missing or wrongly typed values normalise instead of serialising as null.
  assert.deepEqual(api.mediaSettingsBody({}), {
    version: 0, backend: '', filesystem: { root: '' },
    s3: { endpoint: '', region: '', bucket: '', prefix: '', access_key_id: '', secret_access_key: '', use_path_style: false },
  });
  assert.deepEqual(api.mediaSettingsBody(api.emptyMediaSettings()), api.mediaSettingsBody({}));
  // `use_path_style` is strictly boolean: a truthy string is not a yes.
  assert.equal(api.mediaSettingsBody(settings({ s3: s3({ use_path_style: 'true' }) })).s3.use_path_style, false);
  assert.equal(api.mediaSettingsBody({ version: 1.5, backend: 'filesystem', filesystem: { root: '/srv/media' } }).filesystem.root, '/srv/media');
});

test('uploads are multipart under the field name file', async () => {
  response = {
    id: '01K', owner_user_id: '01U', backend: 'filesystem', storage_key: 'a/b',
    content_type: 'image/png', size_bytes: 3, checksum: 'sha', created_at: '2026-09-10T00:00:00Z',
  };
  const blob = new Blob([new Uint8Array([1, 2, 3])], { type: 'image/png' });
  assert.deepEqual(await api.uploadMedia(blob, 'shot.png'), response);
  await api.uploadMedia(blob);
  assert.equal(calls.length, 2);
  for (const [method, path, form] of calls) {
    assert.equal(method, 'post');
    assert.equal(path, '/media');
    assert.ok(form instanceof FormData);
    assert.deepEqual([...form.keys()], ['file']);
  }
  const sent = calls[0][2].get('file');
  assert.equal(sent.name, 'shot.png');
  assert.equal(sent.type, 'image/png');
  assert.equal(sent.size, 3);
  assert.equal(calls[1][2].get('file').name, 'image');
});

test('object reads ask for raw bytes and every id is path encoded', async () => {
  response = new Blob(['x']);
  assert.equal(await api.getMediaBlob('a/b'), response);
  assert.equal(await api.deleteMedia('a/b'), undefined);
  assert.deepEqual(calls, [
    ['get', '/media/a%2Fb', { responseType: 'blob' }],
    ['delete', '/media/a%2Fb'],
  ]);
  // Relative, so an `<img src>` resolves against the SPA base exactly like axios does.
  assert.equal(api.mediaImageURL('01K'), 'api/v1/media/01K');
  assert.equal(api.mediaImageURL('a/b'), 'api/v1/media/a%2Fb');
});

test('data URIs convert to typed blobs and reject anything that is not base64', async () => {
  const blob = api.dataUrlToBlob('data:image/png;base64,AQID');
  assert.equal(blob.type, 'image/png');
  assert.deepEqual([...new Uint8Array(await blob.arrayBuffer())], [1, 2, 3]);
  assert.equal(api.dataUrlToBlob('data:;base64,AQID').type, 'application/octet-stream');
  for (const value of ['data:image/png,AQID', 'data:image/png;base64', 'https://cdn.example/a.png', '']) {
    assert.throws(() => api.dataUrlToBlob(value), undefined, value);
  }
});

test('errors name the limit that was hit instead of failing generically', () => {
  assert.equal(api.mediaErrorStatus(error(415)), 415);
  for (const value of [new Error('network'), undefined, null, {}, { response: {} }]) assert.equal(api.mediaErrorStatus(value), 0);

  assert.match(api.mediaUploadErrorMessage(error(413), 'shot.png'), /larger than 16 MB/);
  assert.match(api.mediaUploadErrorMessage(error(415), 'shot.png'), /not a supported image type/);
  assert.match(api.mediaUploadErrorMessage(error(415), 'shot.png'), /PNG, JPEG, GIF or WebP/);
  assert.match(api.mediaUploadErrorMessage(error(503), 'shot.png'), /not configured/);
  // The named limit wins over a vague upstream message; other codes defer to it.
  assert.match(api.mediaUploadErrorMessage(error(413, 'request entity too large'), 'a.png'), /larger than 16 MB/);
  assert.match(api.mediaUploadErrorMessage(error(415, 'unsupported media type'), 'a.png'), /not a supported image type/);
  assert.equal(api.mediaUploadErrorMessage(error(400, 'file field missing'), 'a.png'), 'file field missing');
  assert.equal(api.mediaUploadErrorMessage(new Error('network'), 'a.png'), 'Could not save "a.png" to history');
  assert.equal(api.mediaUploadErrorMessage(error(500), ''), 'Could not save "image" to history');
  assert.equal(api.isMediaStorageDisabled(error(503)), true);
  for (const status of [400, 401, 403, 409, 413, 415, 502]) assert.equal(api.isMediaStorageDisabled(error(status)), false);

  // A 409 is a stale local version, not a bad request: it must offer a reload.
  assert.equal(api.isMediaSettingsConflict(error(409)), true);
  assert.equal(api.isMediaSettingsConflict(error(400)), false);
  assert.match(api.mediaSettingsErrorMessage(error(409, 'version conflict')), /changed elsewhere/);
  assert.match(api.mediaSettingsErrorMessage(error(413)), /too large/);
  assert.equal(api.mediaSettingsErrorMessage(error(503, 'media storage is disabled')), 'media storage is disabled');
  assert.match(api.mediaSettingsErrorMessage(error(503)), /unavailable right now/);
  assert.equal(api.mediaSettingsErrorMessage(error(400, 'backend must be filesystem or s3')), 'backend must be filesystem or s3');
  assert.equal(api.mediaSettingsErrorMessage(error(500)), 'Could not save media storage settings.');
});

test('limits mirror the server contract', () => {
  assert.equal(api.MEDIA_MAX_UPLOAD_BYTES, 16 * 1024 * 1024);
  assert.deepEqual([...api.MEDIA_ALLOWED_TYPES], ['image/png', 'image/jpeg', 'image/gif', 'image/webp']);
  assert.equal(api.MEDIA_ALLOWED_LABEL, 'PNG, JPEG, GIF or WebP');
});

test('every endpoint propagates failures once, without retrying', async () => {
  failure = error(503, 'media storage is disabled');
  const operations = [
    () => api.getMediaSettings(),
    () => api.putMediaSettings(api.emptyMediaSettings()),
    () => api.testMediaSettings(api.emptyMediaSettings()),
    () => api.uploadMedia(new Blob(['x'])),
    () => api.getMediaBlob('id'),
    () => api.deleteMedia('id'),
  ];
  assert.equal(operations.length, 6, 'all six endpoints are covered');
  for (const operation of operations) {
    const before = calls.length;
    await assert.rejects(operation, err => err === failure);
    assert.equal(calls.length, before + 1);
  }
});
