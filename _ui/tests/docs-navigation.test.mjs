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

// ─── Section aliases ───

test('parseDocsQuery maps merged sections onto the page that replaced them', () => {
  assert.deepEqual(nav.parseDocsQuery('section=code-examples'), { kind: 'api', id: 'quickstart' });
  assert.deepEqual(nav.parseDocsQuery('section=list-models'), { kind: 'api', id: 'available-models' });
});

test('filterEntries requires every search term, in any field', () => {
  assert.deepEqual(
    nav.filterEntries(entries, 'speech ffmpeg').map((e) => e.id),
    ['whisper'],
  );
  assert.deepEqual(nav.filterEntries(entries, 'speech gateway'), []);
});

// ─── Persisted group state ───

test('parseGroupState round-trips', () => {
  const state = { start: false, guides: true };
  assert.deepEqual(nav.parseGroupState(nav.serializeGroupState(state)), state);
});

test('parseGroupState keeps only boolean entries and falls back to all open on junk', () => {
  for (const raw of [null, '', 'not json', '[1,2]', '"x"']) {
    assert.deepEqual(nav.parseGroupState(raw), {});
  }
  assert.deepEqual(nav.parseGroupState('{"api":"yes","guides":false}'), { guides: false });
});

test('groupExpanded treats an unknown group as open', () => {
  assert.equal(nav.groupExpanded({}, 'start'), true);
  assert.equal(nav.groupExpanded({ start: false }, 'start'), false);
});

// ─── Keyboard row model ───

const groups = [
  { id: 'start', kind: 'api', entries: [{ id: 'a' }, { id: 'b' }] },
  { id: 'guides', kind: 'guides', entries: [{ id: 'g1' }] },
];

test('buildNavRows lists group headers plus expanded children in order', () => {
  const rows = nav.buildNavRows(groups, {});
  assert.deepEqual(
    rows.map((r) => r.key),
    ['group:start', 'api:a', 'api:b', 'group:guides', 'guides:g1'],
  );
});

test('buildNavRows hides the children of a collapsed group', () => {
  const rows = nav.buildNavRows(groups, { start: false });
  assert.deepEqual(
    rows.map((r) => r.key),
    ['group:start', 'group:guides', 'guides:g1'],
  );
});

test('stepIndex clamps at both ends', () => {
  const rows = nav.buildNavRows(groups, {});
  assert.equal(nav.stepIndex(rows, 0, -1), 0);
  assert.equal(nav.stepIndex(rows, 0, 1), 1);
  assert.equal(nav.stepIndex(rows, rows.length - 1, 1), rows.length - 1);
  assert.equal(nav.stepIndex([], 0, 1), -1);
});

test('adjacentEntries returns neighbours and nulls at the ends', () => {
  const order = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  assert.deepEqual(nav.adjacentEntries(order, 'b'), { prev: { id: 'a' }, next: { id: 'c' } });
  assert.deepEqual(nav.adjacentEntries(order, 'a'), { prev: null, next: { id: 'b' } });
  assert.deepEqual(nav.adjacentEntries(order, 'zzz'), { prev: null, next: null });
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
  assert.equal(cfg.plugins, undefined);
  assert.equal(cfg.provider, undefined);
  assert.deepEqual(Object.keys(cfg.providers['my-gateway'].models), ['a/1', 'b/2']);
  assert.deepEqual(cfg.providers['my-gateway'].settings, { baseURL: 'https://at/gateway/v1' });
});

test('opencodeDiscoveryConfig leaves models empty and wires the discovery plugin', () => {
  const cfg = JSON.parse(
    snippets.opencodeDiscoveryConfig({ baseUrl: 'https://at', instanceName: 'My Gateway' }),
  );
  assert.equal(cfg.plugin, undefined);
  assert.deepEqual(cfg.plugins, [{ package: 'opencode-models-discovery@1.9.0', options: {} }]);
  assert.deepEqual(cfg.providers['my-gateway'], {
    name: 'My Gateway',
    package: '@opencode/ai/providers/openai-compatible',
    settings: {
      baseURL: 'https://at/gateway/v1',
      modelsDiscovery: {
        enabled: true,
        endpoint: '/gateway/v1/models',
        smartModelName: true,
        modelInfoFormat: 'litellm',
        modelInfoEndpoint: '/gateway/v1/model/info',
        cache: {
          enabled: true,
          ttlSeconds: 86400,
        },
      },
    },
    models: {},
  });
});

test('opencodeDiscoveryConfig keeps the deployment path prefix in the endpoint', () => {
  const cfg = JSON.parse(
    snippets.opencodeDiscoveryConfig({ baseUrl: 'https://host/at', instanceName: 'AT' }),
  );
  assert.equal(cfg.providers.at.settings.modelsDiscovery.endpoint, '/at/gateway/v1/models');
  assert.equal(cfg.providers.at.settings.modelsDiscovery.modelInfoEndpoint, '/at/gateway/v1/model/info');
  assert.equal(cfg.providers.at.settings.baseURL, 'https://host/at/gateway/v1');
});

test('codeExampleFor covers every advertised tab', () => {
  for (const tab of snippets.codeExampleTabs) {
    const code = snippets.codeExampleFor(tab.id, 'openai/gpt-4o', 'https://at');
    assert.ok(code.includes('https://at/gateway/v1'), `${tab.id} must use the gateway base URL`);
    assert.ok(code.includes('openai/gpt-4o'), `${tab.id} must use the selected model`);
  }
});

test('every gateway snippet targets the deployment base URL', () => {
  const builders = [
    snippets.curlChatExtensionsExample('https://h/at', 'a/b', 'c/d'),
    snippets.curlResponsesExample('https://h/at', 'a/b'),
    snippets.curlImageExample('https://h/at'),
    snippets.curlSpeechExample('https://h/at'),
    snippets.curlTranscriptionExample('https://h/at'),
    snippets.curlModerationExample('https://h/at'),
    snippets.curlRerankExample('https://h/at'),
    snippets.curlDecisionExample('https://h/at'),
    snippets.curlProxyExample('https://h/at'),
    snippets.curlScoreExample('https://h/at'),
  ];
  for (const code of builders) assert.ok(code.includes('https://h/at/gateway/v1/'), code);
});

test('Anthropic snippets use the /gateway base without /v1', () => {
  assert.match(snippets.claudeCodeEnv('https://h', 'claude-x'), /ANTHROPIC_BASE_URL="https:\/\/h\/gateway"/);
  assert.match(snippets.anthropicPythonExample('https://h', 'm'), /base_url="https:\/\/h\/gateway"/);
});

test('the chat extensions example is valid JSON inside the -d payload', () => {
  const code = snippets.curlChatExtensionsExample('https://h', 'a/b', 'c/d');
  const body = JSON.parse(code.slice(code.indexOf("-d '") + 4, code.lastIndexOf("'")));
  assert.deepEqual(body.at_fallbacks, ['c/d']);
  assert.equal(body.model, 'a/b');
});

test('every reference section belongs to a group and is rendered by the dispatcher', async () => {
  const pane = await readFile(new URL('../src/lib/components/docs/DocsApiPane.svelte', import.meta.url), 'utf8');
  const src = await readFile(new URL('../src/lib/components/docs/api-sections.ts', import.meta.url), 'utf8');
  const ids = [...src.matchAll(/^\s+id: '([a-z-]+)',\n\s+group:/gm)].map((m) => m[1]);
  const groups = [...src.matchAll(/^\s+id: '([a-z-]+)',\n\s+title:/gm)].map((m) => m[1]);
  assert.ok(ids.length > 10);
  assert.deepEqual(new Set(ids).size, ids.length, 'section ids must be unique');
  for (const id of ids) assert.ok(pane.includes(`sectionId === '${id}'`), `${id} has no renderer`);
  for (const target of Object.values(nav.DOCS_SECTION_ALIASES)) assert.ok(ids.includes(target));
  for (const m of src.matchAll(/group: '([a-z-]+)'/g)) assert.ok(groups.includes(m[1]), m[1]);
});
