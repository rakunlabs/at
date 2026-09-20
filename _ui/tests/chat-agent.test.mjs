import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/chat-agent.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { agentSelections, mergeChatSelections } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('binding supplies tools without marking them as personal selections', () => {
  const user = { mcp_sets: ['mine'], skills: ['notes'], builtin_tools: [] };
  const before = structuredClone(user);
  const agent = agentSelections({ mcp_sets: ['research'], skills: [{ id: 'browser' }], builtin_tools: ['search'] });
  assert.deepEqual(mergeChatSelections(user, agent), {
    mcp_sets: ['mine', 'research'], skills: ['notes', 'browser'], builtin_tools: ['search'],
  });
  assert.deepEqual(user, before);
  assert.deepEqual(mergeChatSelections(user, agentSelections()), before, 'removing the agent restores personal choices');
});

test('skill IDs and names resolve to one inherited picker and discovery selection', () => {
  const skills = [{ id: 'skill-123', name: 'browser' }];
  const config = { skills: ['skill-123', { id: 'skill-123' }, 'browser', { id: 'missing' }] };
  const before = structuredClone(config);
  const inherited = agentSelections(config, skills);
  assert.deepEqual(inherited.skills, ['browser', 'missing']);
  assert.deepEqual(mergeChatSelections({ mcp_sets: [], skills: ['browser'], builtin_tools: [] }, inherited).skills, ['browser', 'missing']);
  assert.deepEqual(config, before, 'resolving display names must not mutate agent configuration');
  assert.deepEqual(agentSelections(config).skills, ['skill-123', 'browser', 'missing'], 'references survive until the catalog loads');
});

test('dual-source tools run once and removing personal selection retains inheritance', () => {
  const agent = agentSelections({ skills: ['browser', { id: 'browser' }], builtin_tools: ['search', 'search'] });
  const user = { mcp_sets: [], skills: ['browser'], builtin_tools: ['search'] };
  assert.deepEqual(mergeChatSelections(user, agent), user);
  assert.deepEqual(mergeChatSelections({ mcp_sets: [], skills: [], builtin_tools: [] }, agent), user);
  assert.deepEqual(user.skills, ['browser'], 'personal ownership remains independently recorded');
});

test('changing agents does not carry the previous agent tools forward', () => {
  const user = { mcp_sets: [], skills: ['mine'], builtin_tools: [] };
  mergeChatSelections(user, agentSelections({ skills: ['old'] }));
  assert.deepEqual(mergeChatSelections(user, agentSelections({ skills: ['new'] })).skills, ['mine', 'new']);
});

test('adopting the union preserves the setup after unbinding and serialization', () => {
  const user = { mcp_sets: ['mine'], skills: [], builtin_tools: ['search'] };
  const agent = agentSelections({ mcp_sets: ['agent'], skills: ['browser'], builtin_tools: ['search'] });
  const adopted = JSON.parse(JSON.stringify(mergeChatSelections(user, agent)));
  assert.deepEqual(mergeChatSelections(adopted, agentSelections()), {
    mcp_sets: ['mine', 'agent'], skills: ['browser'], builtin_tools: ['search'],
  });
});
