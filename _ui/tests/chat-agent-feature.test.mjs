import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');

test('Chats removes agent binding controls when the Agents feature is disabled', () => {
  assert.match(source, /let agentsAvailable = \$derived\(isFeatureEnabled\(FEATURE_AGENTS\)\)/);
  assert.match(source, /async function loadAgents\(\) \{\s+if \(!agentsAvailable\) return;/);
  assert.match(source, /\{#if workbenchTab === 'agent'\}\s+\{#if agentsAvailable\}[\s\S]*?Choose an agent…[\s\S]*?\{\/if\}/);
  assert.match(source, /\{#if agentsAvailable && boundAgentId\}[\s\S]*?aria-label="Configure selected agent"/);
});

test('a saved agent binding does not block Chats while the feature is disabled', () => {
  assert.match(source, /if \(agentsAvailable && boundAgentId && !boundAgent\)/);
  assert.match(source, /\(agentsAvailable && !!boundAgentId && !boundAgent\)/);
});
