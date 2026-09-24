import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const chat = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const dialog = await readFile(new URL('../src/lib/components/playground/ShareDialog.svelte', import.meta.url), 'utf8');
const shared = await readFile(new URL('../src/pages/SharedChat.svelte', import.meta.url), 'utf8');
const routes = await readFile(new URL('../src/routes.ts', import.meta.url), 'utf8');

test('Chats exposes only persisted completed assistant boundaries', () => {
  assert.match(chat, /message\.role !== 'assistant'/);
  assert.match(chat, /message\.tool_calls\?\.length/);
  assert.match(chat, /meta\[index\]\?\.sequence/);
  assert.match(chat, /FEATURE_CHAT_SHARING/);
});

test('share dialog contains the complete snapshot lifecycle', () => {
  for (const label of ['Workspace', 'Share through', 'System prompt', 'Tool calls and outputs', 'Image attachments', 'Preview', 'Copy link', 'Revoke', 'Update snapshot', 'Publish snapshot']) {
    assert.ok(dialog.includes(label), `missing ${label}`);
  }
  assert.match(dialog, /searchParams\.set\('workspace_id'/);
});

test('read-only share page imports an independent recipient copy', () => {
  assert.match(routes, /'\/chats\/shared\/:id'/);
  assert.match(shared, /Copy to my chats/);
  assert.match(shared, /No credential or provider access is transferred/);
  assert.match(shared, /Historical result only/);
  assert.match(shared, /workspaceTransport\.selected/);
});
