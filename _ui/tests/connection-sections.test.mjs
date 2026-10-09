import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/connection-sections.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { connectionSections } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const catalog = ['github', 'google', 'pexels', 'spotify'].map(slug => ({ slug, name: slug, builtin: true, auth_kind: 'token' }));

test('built-in templates never populate an empty account list', () => {
  assert.deepEqual(connectionSections([], catalog), []);
});

test('only saved external accounts appear, grouped with their credential definition', () => {
  const accounts = [{ id: '1', provider: 'google' }, { id: '2', provider: 'google' }];
  assert.deepEqual(connectionSections(accounts, catalog), [{ provider: 'google', connector: catalog[1], items: accounts }]);
});

test('MCP accounts stay in their own section and do not expose unused templates', () => {
  assert.deepEqual(connectionSections([{ id: 'gitlab', provider: 'mcp-gitlab', mcp_oauth: {} }], catalog), []);
});

test('saved accounts survive missing or removed template definitions', () => {
  const account = { id: '1', provider: 'custom' };
  assert.deepEqual(connectionSections([account], []), [{ provider: 'custom', connector: undefined, items: [account] }]);
});

test('sections are sorted by provider label and input accounts are untouched', () => {
  const accounts = [{ id: '1', provider: 'spotify' }, { id: '2', provider: 'github' }];
  assert.deepEqual(connectionSections(accounts, catalog).map(section => section.provider), ['github', 'spotify']);
  assert.equal(accounts[0].provider, 'spotify');
});

test('catalog entry point is separate from the saved-account list', async () => {
  const page = await readFile(new URL('../src/pages/Connections.svelte', import.meta.url), 'utf8');
  assert.doesNotMatch(page, /showAllProviders|Show all providers|Service accounts|allSections\(/);
  assert.match(page, /const sections = \$derived\(connectionSections\(connections, connectors\)\)/);
  assert.match(page, /onclick=\{openProviderCatalog\}[\s\S]*?Add connection/);
  assert.match(page, /<dialog[\s\S]*?Provider catalog[\s\S]*?\{#each catalogProviders/);
  assert.match(page, /providerDialog\.showModal\(\)/);
  assert.match(page, /function openCreate\(connector: Connector\) \{\s*showProviderCatalog = false;/);
});
