import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const api = await import(await moduleURL(new URL('../src/lib/helper/studio-toolkit.ts', import.meta.url)));

test('normalize keeps only known capabilities and well-typed names', () => {
  const toolkit = api.normalizeStudioToolkit({
    capabilities: {
      video: { mcp_sets: ['fal', ' fal ', '', 3, 'runway'], notes: 'prefer finals', extra: true },
      teleport: { mcp_sets: ['x'] },
      image: 'nope',
    },
    notes: 7,
    applied: { skills: ['a', 'a'] },
  });
  assert.deepEqual(toolkit.capabilities.video, { mcp_sets: ['fal', 'runway'], skills: [], builtin_tools: [], notes: 'prefer finals' });
  assert.equal('teleport' in toolkit.capabilities, false);
  assert.deepEqual(toolkit.capabilities.image, api.emptyCapability());
  assert.equal(toolkit.notes, '');
  assert.deepEqual(toolkit.applied.skills, ['a']);
  for (const raw of [null, [], 'x', 1]) assert.deepEqual(api.normalizeStudioToolkit(raw), api.createStudioToolkit());
});

test('toolkit sources are deduplicated across capabilities', () => {
  const toolkit = api.createStudioToolkit();
  toolkit.capabilities.image.mcp_sets = ['fal'];
  toolkit.capabilities.video.mcp_sets = ['fal', 'runway'];
  toolkit.capabilities.speech.builtin_tools = ['generate_image'];
  assert.deepEqual(api.toolkitSources(toolkit), { mcp_sets: ['fal', 'runway'], skills: [], builtin_tools: ['generate_image'] });
  assert.equal(api.capabilityConfigured(toolkit.capabilities.video), true);
  assert.equal(api.capabilityConfigured(toolkit.capabilities.lipsync), false);
});

test('applying attaches new sources, detaches previously applied ones and keeps the agent own', () => {
  const config = {
    skills: ['studio_library', { id: 'old_video', connections: { x: 'c' } }, 'own_skill'],
    mcp_sets: ['old-set', 'own-set'],
    builtin_tools: ['bash_execute'],
  };
  const previous = { skills: ['old_video', 'studio_library'], mcp_sets: ['old-set'], builtin_tools: [] };
  const next = { skills: ['new_skill'], mcp_sets: ['fal'], builtin_tools: ['generate_image'] };
  const result = api.applyToolkitToAgent(config, previous, next);
  assert.deepEqual(result.skills, ['studio_library', 'own_skill', 'new_skill']);
  assert.deepEqual(result.mcp_sets, ['own-set', 'fal']);
  assert.deepEqual(result.builtin_tools, ['bash_execute', 'generate_image']);
  assert.deepEqual(config.mcp_sets, ['old-set', 'own-set'], 'input is not mutated');
  assert.equal(api.applyToolkitToAgent(result, next, next), null, 'no change reports null');
});

test('only agents carrying studio_library receive the toolkit', () => {
  assert.equal(api.isStudioProductionAgent({ skills: ['studio_library'] }), true);
  assert.equal(api.isStudioProductionAgent({ skills: [{ id: 'studio_library' }] }), true);
  assert.equal(api.isStudioProductionAgent({ skills: [] }), false);
  assert.equal(api.isStudioProductionAgent({}), false);
});
