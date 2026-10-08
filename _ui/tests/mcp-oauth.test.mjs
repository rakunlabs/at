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
  withoutAuthorizationHeader, mcpAuthProblems,
} = await load('../src/lib/helper/mcp-oauth.ts');

test('provider key matches the server derivation', () => {
  assert.equal(mcpProviderForURL('https://API.GitHubCopilot.com/mcp/'), 'mcp-api-githubcopilot-com');
  assert.equal(mcpProviderForURL('not a url'), 'mcp');
});

test('disabled form stores nothing', () => {
  assert.equal(mcpUpstreamAuth(mcpAuthForm()), undefined);
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
