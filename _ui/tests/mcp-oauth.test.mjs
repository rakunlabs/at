import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const {
  mcpAuthForm, mcpUpstreamAuth, mcpProviderForURL, moveAccountSource, toggleAccountSource,
  withoutAuthorizationHeader, mcpAuthProblems, mcpUpstreamsForSave,
} = await load('../src/lib/helper/mcp-oauth.ts');

test('provider key matches the server derivation', () => {
  assert.equal(mcpProviderForURL('https://API.GitHubCopilot.com/mcp/'), 'mcp-api-githubcopilot-com');
  assert.equal(mcpProviderForURL('not a url'), 'mcp');
});

test('disabled form stores nothing', () => {
  assert.equal(mcpUpstreamAuth(mcpAuthForm()), undefined);
});

test('auth editor explicitly snapshots its editable initial state without compiler warnings', async () => {
  const { compile } = await import('svelte/compiler');
  const source = await readFile(new URL('../src/lib/components/McpUpstreamAuth.svelte', import.meta.url), 'utf8');
  const { warnings } = compile(source, { filename: 'McpUpstreamAuth.svelte', generate: 'client' });
  assert.equal(warnings.filter(w => w.code === 'state_referenced_locally').length, 0);
  assert.match(source, /untrack\(\(\) => mcpAuthForm\(upstream\.auth\)\)/);
});

test('defaults are not written back', () => {
  const form = { ...mcpAuthForm(), enabled: true };
  assert.deepEqual(mcpUpstreamAuth(form), { type: 'oauth2' });
});

test('round-trips stored settings', () => {
  const stored = { type: 'oauth2', provider: 'mcp-gh', accounts: ['agent', 'user', 'shared'], shared_connection_id: 'c1', scopes: ['repo', 'read:org'], client_id: 'abc' };
  assert.deepEqual(mcpUpstreamAuth(mcpAuthForm(stored)), stored);
});

test('shared connection is dropped when shared is not a source', () => {
  const form = { ...mcpAuthForm({ type: 'oauth2', accounts: ['shared'], shared_connection_id: 'c1' }), accounts: ['user'] };
  assert.deepEqual(mcpUpstreamAuth(form), { type: 'oauth2' });
});

test('scopes split on spaces and commas and dedupe', () => {
  const form = { ...mcpAuthForm(), enabled: true, scopes: 'repo, read:org repo' };
  assert.deepEqual(mcpUpstreamAuth(form).scopes, ['repo', 'read:org']);
});

test('account order editing', () => {
  assert.deepEqual(moveAccountSource(['user', 'agent'], 1, -1), ['agent', 'user']);
  assert.deepEqual(moveAccountSource(['user', 'agent'], 0, -1), ['user', 'agent']);
  assert.deepEqual(toggleAccountSource(['user'], 'shared'), ['user', 'shared']);
  assert.deepEqual(toggleAccountSource(['user', 'shared'], 'user'), ['shared']);
});

test('static Authorization is removed and reported', () => {
  const upstream = { url: 'https://x/mcp', headers: { Authorization: 'Bearer x', 'X-Other': '1' } };
  assert.deepEqual(withoutAuthorizationHeader(upstream).headers, { 'X-Other': '1' });
  const form = { ...mcpAuthForm(), enabled: true };
  assert.ok(mcpAuthProblems(upstream, form).some(p => p.includes('Authorization')));
  assert.ok(mcpAuthProblems({ url: 'https://x/mcp' }, { ...form, accounts: ['shared'] }).some(p => p.includes('shared account')));
  assert.deepEqual(mcpAuthProblems({ url: 'https://x/mcp' }, form), []);
});

test('MCP proxy is editable and changing it requires saving before OAuth', async () => {
  const source = await readFile(new URL('../src/pages/Mcps.svelte', import.meta.url), 'utf8');
  assert.match(source, /bind:value=\{formMCPUpstreams\[i\]\.proxy\}/);
  assert.match(source, /\(saved\.proxy \?\? ''\) !== \(current\.proxy \?\? ''\)/);
  assert.match(source, /proxy: upstream\.proxy \|\| ''/);
  assert.match(source, /browser sign-in page uses your device/);
  assert.match(source, /bind:checked=\{formMCPUpstreams\[i\]\.insecure_skip_verify\}/);
  assert.match(source, /Boolean\(saved\.insecure_skip_verify\) !== Boolean\(current\.insecure_skip_verify\)/);
  assert.match(source, /Disabling verification can expose credentials to interception/);
  assert.match(source, /mcp_upstreams: mcpUpstreamsForSave\(formMCPUpstreams\)/);
});

for (const oauth of [false, true]) {
  test(`save payload retains proxy and TLS settings for ${oauth ? 'OAuth' : 'static'} HTTP`, () => {
    const upstream = {
      url: ' https://mcp.example/mcp ', proxy: ' http://proxy:8080 ', insecure_skip_verify: true,
      headers: { Authorization: 'Bearer static', 'X-Other': 'kept' },
      ...(oauth ? { auth: { type: 'oauth2', provider: 'mcp-example', accounts: ['user'] } } : {}),
    };
    const snapshot = structuredClone(upstream);
    const saved = JSON.parse(JSON.stringify(mcpUpstreamsForSave([upstream])));
    assert.deepEqual(saved, [{
      ...upstream, url: 'https://mcp.example/mcp', proxy: 'http://proxy:8080',
      headers: oauth ? { 'X-Other': 'kept' } : upstream.headers,
    }]);
    assert.deepEqual(upstream, snapshot, 'save must not mutate editor state');
    assert.deepEqual(mcpUpstreamsForSave(saved), saved, 'loaded settings survive another save');

    const cleared = JSON.parse(JSON.stringify(mcpUpstreamsForSave([{ ...saved[0], proxy: '', insecure_skip_verify: false }])));
    assert.equal(cleared[0].proxy, '');
    assert.equal(cleared[0].insecure_skip_verify, false);
  });
}

test('save payload omits blank rows and HTTP options on local commands', () => {
  const saved = JSON.parse(JSON.stringify(mcpUpstreamsForSave([
    { url: ' ', headers: {} },
    { command: ' ' },
    { command: ' npx ', args: ['@example/mcp'], env: { TOKEN: 'secret' }, proxy: 'http://proxy:8080', insecure_skip_verify: true, auth: { type: 'oauth2' } },
    { url: ' https://legacy.example/mcp ', headers: { 'X-Other': '1' } },
  ])));
  assert.deepEqual(saved, [
    { command: 'npx', args: ['@example/mcp'], env: { TOKEN: 'secret' } },
    { url: 'https://legacy.example/mcp', headers: { 'X-Other': '1' } },
  ]);
});
