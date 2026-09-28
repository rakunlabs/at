import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = (await readFile(new URL('../src/lib/helper/resource-builders.ts', import.meta.url), 'utf8'))
  .replace(/^import type[^\n]+\n/gm, '')
  .replace(/^import \{ formBuilderTools \}[^\n]+\n/m, 'const formBuilderTools = () => ({ names: {}, tools: [] });\n');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { applySkillBuilderPatch, applyMCPSetBuilderPatch } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

const skill = () => ({
  name: 'review', description: 'Review changes', category: 'Engineering', tags: ['review'],
  system_prompt: '# Review\nCheck correctness.', context: '', agent: '',
});
const skillCatalog = { agents: [{ id: 'agent-1', name: 'Reviewer' }] };

test('skill builder preserves omitted fields and validates fork targets', () => {
  const current = skill();
  assert.deepEqual(applySkillBuilderPatch(current, { system_prompt: '# Better review' }, skillCatalog), { ...current, system_prompt: '# Better review' });
  assert.deepEqual(applySkillBuilderPatch(current, { context: 'fork', agent: 'agent-1' }, skillCatalog), { ...current, context: 'fork', agent: 'agent-1' });
  assert.throws(() => applySkillBuilderPatch(current, { context: 'fork', agent: 'missing' }, skillCatalog), /Choose an agent ID/);
  assert.throws(() => applySkillBuilderPatch(current, { resources: [] }, skillCatalog), /Unsupported field/);
  assert.deepEqual(current, skill());
});

const mcp = () => ({
  name: 'dev_api', description: 'Developer API', category: 'Development', tags: [],
  http_tools: [], builtin_tools: ['file_read'], workflow_ids: [], mcp_upstreams: [],
});
const mcpCatalog = {
  builtin_tools: [{ id: 'file_read', name: 'file_read' }, { id: 'http_request', name: 'http_request' }],
  workflows: [{ id: 'workflow-1', name: 'Deploy' }],
};

test('MCP Set builder validates selections and builds complete HTTP tools', () => {
  const current = mcp();
  const next = applyMCPSetBuilderPatch(current, {
    builtin_tools: ['http_request'],
    workflow_ids: ['workflow-1'],
    http_tools: [{
      name: 'get_issue', description: 'Read an issue', method: 'get',
      url: 'https://api.example.test/issues/{{.id}}', headers: { Authorization: 'Bearer {{var:api_token}}' },
      input_schema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'] },
    }],
  }, mcpCatalog);
  assert.deepEqual(next.builtin_tools, ['http_request']);
  assert.deepEqual(next.workflow_ids, ['workflow-1']);
  assert.equal(next.http_tools[0].method, 'GET');
  assert.equal(next.http_tools[0].body_template, '');
  assert.deepEqual(current, mcp());
  assert.throws(() => applyMCPSetBuilderPatch(current, { builtin_tools: ['invented'] }, mcpCatalog), /Unknown built-in tool/);
  assert.throws(() => applyMCPSetBuilderPatch(current, { http_tools: [{ name: 'broken', method: 'GET', url: '', input_schema: {} }] }, mcpCatalog), /needs a URL/);
});

test('MCP Set builder accepts one transport per upstream and rejects ambiguous entries', () => {
  const next = applyMCPSetBuilderPatch(mcp(), {
    mcp_upstreams: [
      { url: 'https://example.test/mcp', headers: { Authorization: 'Bearer {{var:key}}' } },
      { command: 'npx', args: ['@example/mcp@latest', '--headless'], env: { TOKEN: '{{var:key}}' } },
    ],
  }, mcpCatalog);
  assert.equal(next.mcp_upstreams.length, 2);
  assert.throws(() => applyMCPSetBuilderPatch(mcp(), { mcp_upstreams: [{ url: 'https://example.test/mcp', command: 'npx' }] }, mcpCatalog), /either url or command/);
  assert.throws(() => applyMCPSetBuilderPatch(mcp(), { inline_tools: [] }, mcpCatalog), /Unsupported field/);
});
