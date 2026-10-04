import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

// local-providers.ts imports ./local-mcp; inline both as data: modules.
async function transpile(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  return ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
}

const dataUrl = code => `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
const localMCP = dataUrl(await transpile('../src/lib/helper/local-mcp.ts'));
const helper = await import(dataUrl((await transpile('../src/lib/helper/local-providers.ts')).replace("'./local-mcp'", `'${localMCP}'`)));

test('model references round-trip and keep slashes in the model', () => {
  const ref = helper.localModelRef('ollama', 'library/llama3:8b');
  assert.equal(ref, 'local:ollama/library/llama3:8b');
  assert.ok(helper.isLocalModelRef(ref));
  assert.deepEqual(helper.parseLocalModelRef(ref), { provider: 'ollama', model: 'library/llama3:8b' });
  assert.equal(helper.isLocalModelRef('openai/gpt-4o'), false);
  assert.equal(helper.parseLocalModelRef('local:ollama'), null);
  assert.equal(helper.parseLocalModelRef('local:ollama/'), null);
});

test('plain http is accepted only for local addresses', () => {
  assert.equal(helper.localProviderUrlProblem('http://127.0.0.1:11434/v1'), '');
  assert.equal(helper.localProviderUrlProblem('http://192.168.1.5:8000/v1'), '');
  assert.equal(helper.localProviderUrlProblem('https://api.openai.com/v1'), '');
  assert.match(helper.localProviderUrlProblem('http://api.example.com/v1'), /use https/);
  assert.match(helper.localProviderUrlProblem('https://u:p@api.example.com/v1'), /credentials/);
  assert.match(helper.localProviderUrlProblem('https://api.example.com/v1?x=1'), /query/);
  assert.match(helper.localProviderNameProblem('a/b'), /lowercase/);
  assert.equal(helper.localProviderNameProblem('LM-Studio'), '');
});

test('endpoint joining and auth header', () => {
  assert.equal(helper.localProviderEndpoint('http://h/v1/', '/chat/completions'), 'http://h/v1/chat/completions');
  assert.deepEqual(helper.localProviderHeaders({ api_key: 'k', headers: {} }), { Authorization: 'Bearer k' });
  // An explicit Authorization header wins over the key.
  assert.deepEqual(helper.localProviderHeaders({ api_key: 'k', headers: { authorization: 'Basic x' } }), { authorization: 'Basic x' });
  assert.deepEqual(helper.localProviderHeaders({ api_key: '', headers: {} }), {});
});

test('model list parsing', () => {
  assert.deepEqual(helper.parseModelList({ data: [{ id: 'b' }, { id: 'a' }, { id: 'a' }, { nope: 1 }] }), ['a', 'b']);
  assert.throws(() => helper.parseModelList({ models: [] }), /OpenAI-compatible/);
});

test('a network failure names CORS as a possible cause', () => {
  const err = helper.describeLocalProviderError(new TypeError('Failed to fetch'), 'ollama');
  assert.match(err.message, /Access-Control-Allow-Origin/);
  assert.match(err.message, /OLLAMA_ORIGINS/);
  const plain = new Error('HTTP 401');
  assert.equal(helper.describeLocalProviderError(plain, 'x'), plain);
});

test('enabling is per device', () => {
  const store = new Map();
  const storage = { getItem: k => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
  assert.equal(helper.localProviderEnabled('p1', storage), false);
  helper.enableLocalProvider('p1', storage);
  helper.enableLocalProvider('p1', storage);
  assert.equal(helper.localProviderEnabled('p1', storage), true);
  assert.deepEqual(JSON.parse(store.get('at.local-providers.enabled')), ['p1']);
  helper.disableLocalProvider('p1', storage);
  assert.equal(helper.localProviderEnabled('p1', storage), false);
  assert.equal(helper.localProviderEnabled('p1', undefined), false);
});
