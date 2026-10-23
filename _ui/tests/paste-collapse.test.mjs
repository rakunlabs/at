import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const load = async path => import(await moduleURL(new URL(path, import.meta.url)));
const { shouldCollapsePaste, insertPaste, deletePaste, expandPastes, prunePastes } = await load('../src/lib/helper/paste-collapse.ts');
const { codeModeSelection, mcpCodeModePrompt } = await load('../src/lib/helper/chat-tools.ts');

const block = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join('\n');

test('only multi-line or long pastes collapse', () => {
  assert.equal(shouldCollapsePaste('one line'), false);
  assert.equal(shouldCollapsePaste('a\nb\n'), false);
  assert.equal(shouldCollapsePaste('a\nb\nc'), true);
  assert.equal(shouldCollapsePaste('x'.repeat(400)), true);
});

test('a paste becomes a placeholder and expands back on send', () => {
  const entries = new Map();
  const edit = insertPaste('Review: ', 8, 8, block, entries);
  assert.equal(edit.value, 'Review: [Pasted ~30 lines]');
  assert.equal(edit.caret, edit.value.length);
  assert.equal(expandPastes(`${edit.value} thanks`, entries), `Review: ${block} thanks`);
});

test('identical sizes get distinct placeholders', () => {
  const entries = new Map();
  let edit = insertPaste('', 0, 0, block, entries);
  edit = insertPaste(edit.value, edit.caret, edit.caret, block.replace('line 1', 'other'), entries);
  assert.equal(edit.value, '[Pasted ~30 lines][Pasted ~30 lines #2]');
  assert.equal(expandPastes(edit.value, entries), block + block.replace('line 1', 'other'));
});

test('one Backspace removes the whole placeholder', () => {
  const entries = new Map();
  const { value } = insertPaste('a ', 2, 2, block, entries);
  const end = value.length;
  assert.deepEqual(deletePaste(value, end, end, 'Backspace', entries), { value: 'a ', caret: 2 });
  assert.equal(entries.size, 0);
});

test('Delete before a placeholder removes it; ordinary edits pass through', () => {
  const entries = new Map();
  const { value } = insertPaste('x', 0, 0, block, entries);
  assert.equal(deletePaste(value, 0, 0, 'Delete', entries).value, 'x');
  const again = insertPaste('ab', 2, 2, block, entries).value;
  assert.equal(deletePaste(again, 1, 1, 'Backspace', entries), null);
  assert.equal(deletePaste(again, 0, 3, 'Backspace', entries), null, 'a selection deletes as usual');
});

test('typed look-alikes are left alone and stale entries are pruned', () => {
  const entries = new Map();
  assert.equal(expandPastes('[Pasted ~3 lines]', entries), '[Pasted ~3 lines]');
  insertPaste('', 0, 0, block, entries);
  prunePastes('nothing here', entries);
  assert.equal(entries.size, 0);
});

test('code_execute in Chats reaches only server-side tools', () => {
  const selection = codeModeSelection({
    code_execute: { type: 'builtin' },
    current_time: { type: 'builtin' },
    search: { type: 'mcpset', mcpSetName: 'web' },
    fetch: { type: 'mcpset', mcpSetName: 'web' },
    laptop_tool: { type: 'local' },
    todo: { type: 'frontend' },
  });
  assert.deepEqual(selection, { builtin_tools: ['current_time'], mcp_sets: ['web'] });
});

test('MCP code mode lists tool names and first sentences, bounded', () => {
  assert.equal(mcpCodeModePrompt([]), '');
  const prompt = mcpCodeModePrompt([{ name: 'gitlab_list', description: 'List issues. With filters.' }, { name: 'bare' }]);
  assert.match(prompt, /code_execute/);
  assert.match(prompt, /- gitlab_list: List issues\.\n- bare$/);
  const many = Array.from({ length: 5 }, (_, i) => ({ name: `t${i}` }));
  assert.match(mcpCodeModePrompt(many, 3), /… 2 more; find them with searchTools$/);
});
