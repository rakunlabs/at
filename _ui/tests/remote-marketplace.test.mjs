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
  describeSourceURL, selectedVariables, needsStdioConfirmation, installableMCP, mcpSummary, variableMapping,
} = await load('../src/lib/helper/remote-marketplace.ts');
const { mcpUpstreamsForSave } = await load('../src/lib/helper/mcp-oauth.ts');

const detail = {
  plugin: { name: 'p', source_type: 'relative', installable: true },
  skills: [],
  mcp_servers: [
    { name: 'docs', type: 'http', upstream: { url: 'https://d/mcp' }, variables: ['TOKEN'] },
    { name: 'local', type: 'stdio', upstream: { command: 'npx', args: ['-y', 'x'] }, variables: ['REGION', 'TOKEN'] },
    { name: 'bundled', type: 'stdio', upstream: { command: '${CLAUDE_PLUGIN_ROOT}/s' }, plugin_relative: true },
  ],
};

test('source URL kinds', () => {
  assert.match(describeSourceURL('acme/tools'), /GitHub/);
  assert.match(describeSourceURL('acme/tools@v2'), /GitHub/);
  assert.equal(describeSourceURL('https://x.example/marketplace.json'), 'marketplace.json file');
  assert.equal(describeSourceURL('git@git.example:org/repo.git'), 'Git repository');
  assert.equal(describeSourceURL('/etc/passwd'), '');
});

test('variables and stdio follow the selection', () => {
  assert.deepEqual(selectedVariables(detail, ['docs']), ['TOKEN']);
  assert.deepEqual(selectedVariables(detail, ['docs', 'local']), ['REGION', 'TOKEN']);
  assert.equal(needsStdioConfirmation(detail, ['docs']), false);
  assert.equal(needsStdioConfirmation(detail, ['local']), true);
  assert.equal(installableMCP(detail.mcp_servers[2]), false);
  assert.equal(mcpSummary(detail.mcp_servers[1]), 'npx -y x');
});

test('variable mapping references AT variables and drops blanks', () => {
  assert.deepEqual(variableMapping({
    TOKEN: { mode: 'var', value: ' docs_token ' },
    REGION: { mode: 'literal', value: 'eu' },
    EMPTY: { mode: 'literal', value: '  ' },
  }), { TOKEN: 'var:docs_token', REGION: 'eu' });
});

test('saving an MCP set keeps the marketplace origin of every upstream', () => {
  const origin = { source_id: 's', plugin: 'p', server: 'docs', checksum: 'c' };
  const saved = mcpUpstreamsForSave([
    { url: 'https://d/mcp', origin },
    { command: 'npx', args: [], origin: { ...origin, server: 'local' } },
  ]);
  assert.deepEqual(saved[0].origin, origin);
  assert.equal(saved[1].origin.server, 'local');
});
