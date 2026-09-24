import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const providersPage = await readFile(new URL('../src/pages/Providers.svelte', import.meta.url), 'utf8');
const chatPage = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const agentsPage = await readFile(new URL('../src/pages/Agents.svelte', import.meta.url), 'utf8');
const agentLoader = await readFile(new URL('../src/lib/helper/agent-page.svelte.ts', import.meta.url), 'utf8');

test('provider creation defaults to personal scope and exposes explicit publication scopes', () => {
  assert.match(providersPage, /let formScope = \$state<ProviderScope>\('personal'\)/);
  assert.match(providersPage, /formScope = canManagePersonal \? 'personal' : 'workspace'/);
  assert.match(providersPage, /Personal — only you/);
  assert.match(providersPage, /Workspace — members can use it/);
  assert.match(providersPage, /Global — every workspace/);
  assert.match(providersPage, /await createPersonalProvider\(formKey, cfg, 'personal'\)/);
  assert.match(providersPage, /if \(editingPersonalId\) \{\s+await updatePersonalProvider\(editingPersonalId, formKey, buildConfig\(\)\)/);
});

test('model choices retain stable references and ownership grouping', () => {
  assert.match(chatPage, /const reference = p\.reference \|\| p\.key/);
  assert.match(chatPage, /groups\.push\(\{ label: `\$\{p\.key\}\$\{scope\}`, models: providerModels \}\)/);
  assert.match(agentLoader, /key: provider\.reference \|\| provider\.key/);
  assert.match(agentsPage, /\{ label: 'Personal'/);
  assert.match(agentsPage, /\{ label: 'Workspace'/);
  assert.match(agentsPage, /\{ label: 'Global'/);
});
