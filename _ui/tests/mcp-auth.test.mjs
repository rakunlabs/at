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

const { mcpAuthParams, safeMCPAuthRedirect } = await load('../src/lib/helper/mcp-auth.ts');

test('consent forwards only OAuth parameters', () => {
  const params = mcpAuthParams('response_type=code&client_id=c&redirect_uri=http%3A%2F%2F127.0.0.1%3A9%2Fcb&state=s&code_challenge=x&code_challenge_method=S256&resource=r&at_workspace=w&evil=1');
  assert.deepEqual(params, {
    response_type: 'code', client_id: 'c', redirect_uri: 'http://127.0.0.1:9/cb', state: 's',
    code_challenge: 'x', code_challenge_method: 'S256', resource: 'r',
  });
});

test('only http(s) redirects are followed', () => {
  assert.equal(safeMCPAuthRedirect('http://127.0.0.1:3000/cb?code=1'), 'http://127.0.0.1:3000/cb?code=1');
  assert.equal(safeMCPAuthRedirect('https://claude.ai/api/mcp/auth_callback?code=1'), 'https://claude.ai/api/mcp/auth_callback?code=1');
  assert.equal(safeMCPAuthRedirect('javascript:alert(1)'), null);
  assert.equal(safeMCPAuthRedirect('not a url'), null);
});
