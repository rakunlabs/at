import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(relativePath) {
  const source = await readFile(new URL(relativePath, import.meta.url), 'utf8');
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const nav = await load('../src/lib/helper/docs-nav.ts');
const snippets = await load('../src/lib/components/docs/snippets.ts');

// ─── URL scheme ───

test('parseDocsQuery reads the current API section scheme', () => {
  assert.deepEqual(nav.parseDocsQuery('section=authentication'), {
    kind: 'api',
    id: 'authentication',
  });
});

test('parseDocsQuery reads the current guide scheme', () => {
  assert.deepEqual(nav.parseDocsQuery('guide=01H'), { kind: 'guides', id: '01H' });
});

test('parseDocsQuery keeps the legacy ?g= and ?section=guides links working', () => {
  assert.deepEqual(nav.parseDocsQuery('g=whisper'), { kind: 'guides', id: 'whisper' });
  assert.deepEqual(nav.parseDocsQuery('section=guides&g=whisper'), {
    kind: 'guides',
    id: 'whisper',
  });
  assert.deepEqual(nav.parseDocsQuery('section=guides'), { kind: 'guides', id: '' });
});

test('parseDocsQuery returns null with no selection', () => {
  assert.equal(nav.parseDocsQuery(''), null);
  assert.equal(nav.parseDocsQuery('other=1'), null);
});

test('docsPath is the inverse of parseDocsQuery', () => {
  for (const selection of [
    { kind: 'api', id: 'overview' },
    { kind: 'api', id: 'claude-marketplace' },
    { kind: 'guides', id: 'telegram' },
  ]) {
    const path = nav.docsPath(selection);
    assert.deepEqual(nav.parseDocsQuery(path.split('?')[1] ?? ''), selection);
  }
});

test('docsPath encodes ids that need it', () => {
  assert.equal(nav.docsPath({ kind: 'guides', id: 'a b/c' }), '/docs?guide=a%20b%2Fc');
});

test('docsShareUrl builds an absolute hash link', () => {
  assert.equal(
    nav.docsShareUrl({ kind: 'api', id: 'endpoints' }, 'https://at.example', '/ui/'),
    'https://at.example/ui/#/docs?section=endpoints',
  );
});

// ─── Search ───

const entries = [
  { id: 'overview', title: 'Overview', description: 'OpenAI compatible', body: 'gateway' },
  { id: 'whisper', title: 'Speech-to-Text', description: 'Transcription', body: 'ffmpeg' },
];

test('filterEntries matches title, description and body', () => {
  assert.deepEqual(
    nav.filterEntries(entries, 'openai').map((e) => e.id),
    ['overview'],
  );
  assert.deepEqual(
    nav.filterEntries(entries, 'FFMPEG').map((e) => e.id),
    ['whisper'],
  );
  assert.deepEqual(
    nav.filterEntries(entries, 'speech').map((e) => e.id),
    ['whisper'],
  );
});

test('filterEntries returns everything for a blank query', () => {
  assert.equal(nav.filterEntries(entries, '   ').length, 2);
});

test('filterEntries returns nothing when there is no hit', () => {
  assert.deepEqual(nav.filterEntries(entries, 'kubernetes'), []);
});

// ─── Persisted group state ───

test('parseGroupState round-trips', () => {
  const state = { api: false, guides: true };
  assert.deepEqual(nav.parseGroupState(nav.serializeGroupState(state)), state);
});

test('parseGroupState falls back to both open on junk', () => {
  for (const raw of [null, '', 'not json', '[1,2]', '{"api":"yes"}']) {
    assert.deepEqual(nav.parseGroupState(raw), { api: true, guides: true });
  }
});

// ─── Keyboard row model ───

const apiEntries = [{ id: 'a' }, { id: 'b' }];
const guideEntries = [{ id: 'g1' }];

test('buildNavRows lists group headers plus expanded children in order', () => {
  const rows = nav.buildNavRows(apiEntries, guideEntries, { api: true, guides: true });
  assert.deepEqual(
    rows.map((r) => r.key),
    ['group:api', 'api:a', 'api:b', 'group:guides', 'guides:g1'],
  );
});

test('buildNavRows hides the children of a collapsed group', () => {
  const rows = nav.buildNavRows(apiEntries, guideEntries, { api: false, guides: true });
  assert.deepEqual(
    rows.map((r) => r.key),
    ['group:api', 'group:guides', 'guides:g1'],
  );
});

test('stepIndex clamps at both ends', () => {
  const rows = nav.buildNavRows(apiEntries, guideEntries, { api: true, guides: true });
  assert.equal(nav.stepIndex(rows, 0, -1), 0);
  assert.equal(nav.stepIndex(rows, 0, 1), 1);
  assert.equal(nav.stepIndex(rows, rows.length - 1, 1), rows.length - 1);
  assert.equal(nav.stepIndex([], 0, 1), -1);
});

// ─── Snippets ───

test('opencodeMcpConfig omits the Authorization header for public servers', () => {
  const priv = JSON.parse(
    snippets.opencodeMcpConfig({ baseUrl: 'https://at', serverName: 'ops', isPublic: false }),
  );
  assert.equal(priv.mcp['at-ops'].headers.Authorization, 'Bearer at_xxxxx');
  assert.equal(priv.mcp['at-ops'].url, 'https://at/gateway/v1/mcp/ops');

  const pub = JSON.parse(
    snippets.opencodeMcpConfig({ baseUrl: 'https://at', serverName: 'ops', isPublic: true }),
  );
  assert.equal(pub.mcp['at-ops'].headers, undefined);
});

test('opencodeMcpConfig falls back to the builtin management server', () => {
  const cfg = JSON.parse(
    snippets.opencodeMcpConfig({ baseUrl: 'https://at', serverName: '', isPublic: false }),
  );
  assert.ok(cfg.mcp['at-management']);
});

test('opencodeProviderConfig sorts models and slugs the instance name', () => {
  const cfg = JSON.parse(
    snippets.opencodeProviderConfig({
      baseUrl: 'https://at',
      instanceName: 'My Gateway',
      models: ['b/2', 'a/1'],
    }),
  );
  assert.deepEqual(Object.keys(cfg.provider['my-gateway'].models), ['a/1', 'b/2']);
  assert.equal(cfg.provider['my-gateway'].options.baseURL, 'https://at/gateway/v1');
});

test('codeExampleFor covers every advertised tab', () => {
  for (const tab of snippets.codeExampleTabs) {
    const code = snippets.codeExampleFor(tab.id, 'openai/gpt-4o', 'https://at');
    assert.ok(code.includes('https://at/gateway/v1'), `${tab.id} must use the gateway base URL`);
    assert.ok(code.includes('openai/gpt-4o'), `${tab.id} must use the selected model`);
  }
});
