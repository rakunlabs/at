import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { beforeEach, test } from 'node:test';
import ts from 'typescript';

// Exercise the production controller and page loader with isolated HTTP APIs.
// Runes are identities here; the production Svelte compiler checks reactivity.
const calls = [];
let enabled, responses;
const api = {};
for (const name of ['listAgents', 'listProviders', 'listSkills', 'listMCPSets',
  'listWorkflows', 'listBuiltinTools', 'listConnections', 'loadFeatures']) {
  api[name] = async (...args) => {
    calls.push(name);
    const response = responses[name];
    return typeof response === 'function' ? response(...args) : response;
  };
}
api.isFeatureEnabled = key => enabled.has(key);
api.authErrorMessage = (error, fallback) => error.message || fallback;
globalThis.agentPageTestAPI = api;

async function loadSource(path, prelude) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const stripped = source.replace(/^import[\s\S]*?from ['"][^'"]+['"];\n/gm, '');
  const code = ts.transpileModule(`const $state = value => value;\n${prelude}\n${stripped}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}
const { createPageLoader } = await loadSource('../src/lib/helper/page-load.svelte.ts',
  'const { loadFeatures, isFeatureEnabled, authErrorMessage } = globalThis.agentPageTestAPI;');
api.createPageLoader = createPageLoader;
const { createAgentPage } = await loadSource('../src/lib/helper/agent-page.svelte.ts',
  `const { ${Object.keys(api).join(', ')} } = globalThis.agentPageTestAPI;`);
delete globalThis.agentPageTestAPI;

// Older agents legitimately omit skill/MCP arrays and pagination offset.
const records = ['Narendra', 'Michiel'].map((name, index) => ({
  id: `agent-${index}`, workspace_id: 'legacy-default', name, scope: 'workspace',
  config: { description: name, provider: 'google-ai', model: 'gemini-pro-latest',
    system_prompt: '', builtin_tools: ['http_request'], max_iterations: 10, tool_timeout: 60 },
}));

beforeEach(() => {
  calls.length = 0;
  enabled = new Set();
  responses = {
    listAgents: { data: records, meta: { total: 2, limit: 1000 } },
    listProviders: { data: [] }, listSkills: { data: [] }, listMCPSets: { data: [] },
    listWorkflows: { data: [] }, listBuiltinTools: { tools: [] }, listConnections: [],
  };
});

test('legacy agents load without feature discovery or any editor requests', async () => {
  responses.loadFeatures = () => new Promise(() => {});
  const page = createAgentPage();
  await page.loadList();
  assert.deepEqual(calls, ['listAgents']);
  assert.deepEqual(page.data.agents.map(agent => agent.name), ['Narendra', 'Michiel']);
  assert.equal(page.list.loading('Agents'), false);
  assert.equal(page.list.error('Agents'), '');
});

test('opening the editor skips disabled catalogs without presenting them as errors', async () => {
  const page = createAgentPage();
  await page.loadList();
  await page.loadEditor();
  assert.deepEqual(calls, ['listAgents', 'loadFeatures', 'listProviders']);
  assert.deepEqual(page.editor.issues, []);
  assert.deepEqual(page.data.agents, records);
});

test('an enabled catalog failure remains local to the editor', async () => {
  enabled.add('skills');
  enabled.add('workflow_builder');
  responses.listSkills = () => { throw { response: { status: 403 } }; };
  responses.listWorkflows = { data: [{ id: 'workflow', name: 'Available' }] };
  const page = createAgentPage();
  await Promise.all([page.loadList(), page.loadEditor()]);
  assert.deepEqual(page.data.agents, records);
  assert.equal(page.data.workflows[0].name, 'Available');
  assert.match(page.editor.error('Skills'), /access/);
  assert.equal(page.list.error('Agents'), '');
  assert.ok(!calls.includes('listMCPSets'));
});

test('failed feature discovery does not fan out requests or block the list', async () => {
  responses.loadFeatures = () => { throw new Error('catalog offline'); };
  const page = createAgentPage();
  await Promise.all([page.loadList(), page.loadEditor()]);
  assert.deepEqual(calls, ['listAgents', 'loadFeatures']);
  assert.deepEqual(page.data.agents, records);
  assert.match(page.editor.error('Feature availability'), /catalog offline/);
});

test('closing an editor while discovering features prevents late catalog requests', async () => {
  let finish;
  responses.loadFeatures = () => new Promise(resolve => { finish = resolve; });
  const page = createAgentPage();
  const pending = page.loadEditor();
  page.closeEditor();
  finish();
  await pending;
  assert.deepEqual(calls, ['loadFeatures']);
});

test('a failed list refresh retains records and reports the list error', async () => {
  const page = createAgentPage();
  await page.loadList();
  responses.listAgents = () => { throw new Error('database unavailable'); };
  await page.loadList();
  assert.deepEqual(page.data.agents, records);
  assert.match(page.list.error('Agents'), /database unavailable/);
});
