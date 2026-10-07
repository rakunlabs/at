import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const script = source.slice(source.indexOf('>') + 1, source.indexOf('</script>'));
const ast = ts.createSourceFile('Chat.ts', script, ts.ScriptTarget.Latest, true);
const functions = ast.statements.filter(n => ts.isFunctionDeclaration(n) && ['openConversation', 'loadOlderHistory', 'storedUsage'].includes(n.name?.text)).map(n => n.getText(ast));
const code = ts.transpileModule(`export function fixture(pages) {
  let settingsTimer = null, toolDiscoveryVersion = 0, generation = 0, disposed = false;
  let conversationId = '', scratchSessionId = '', conversation = null, parentTitle = '', appliedPresetId = '', customSetup = null, selectedModel = '', systemPrompt = '';
  let historyTruncated = false, historyCursor = '', loadingOlderHistory = false, historyLoading = false, savedSettings = null;
  let streaming = false, saving = false, messages = [], meta = [], rawMessages = {};
  const calls = [], errors = [], HISTORY_PAGE_SIZE = 50, COMPACTION_FLAG = 'compaction';
  const turnLifecycle = { generation: () => generation };
  const resetBuffer = () => { generation++; messages = []; meta = []; };
  const defaultsSave = { flush: async () => {} }, saveSettings = () => {};
  const getPlaygroundConversation = async id => ({ id, config: {}, provider_key: 'p', model: 'm' });
  const listPlaygroundMessages = async (id, opts) => { calls.push([id, opts]); const page = pages.shift(); if (page instanceof Error) throw page; return await page; };
  const joinModel = (p,m) => p+'/'+m, applyConfig = () => {}, settingsSnapshot = () => ({}), mergeConversation = () => {}, discoverTools = () => {}, scrollToBottom = () => {};
  const toChatMessage = m => ({ role: m.role, content: m.data.content });
  const playgroundErrorMessage = e => e.message, addToast = e => errors.push(e);
  const chatContainer = { scrollTop: 20, get scrollHeight() { return messages.length * 100; } };
  const tick = async () => {};
  let todos = [];
  const latestTodos = () => null, loadParentTitle = async () => {};
  ${functions.join('\n')}
  return { open: openConversation, older: loadOlderHistory,
    snapshot: () => ({ calls, errors, messages, meta, historyCursor, historyTruncated, loadingOlderHistory, scrollTop: chatContainer.scrollTop }),
    raw: () => { rawMessages = { 0: true }; }, navigate: () => { generation++; conversationId = 'other'; messages = []; meta = []; },
  };
}`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { fixture } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const message = id => ({ id, sequence: Number(id), role: 'user', data: { content: id } });

test('opening downloads one page; older history preserves chronology and viewport', async () => {
  const chat = fixture([{ data: [message('3')], meta: { next_before: '3' } }, { data: [message('1'), message('2')], meta: {} }]);
  await chat.open('conversation');
  assert.deepEqual(chat.snapshot().calls, [['conversation', { limit: 50 }]]);
  assert.equal(chat.snapshot().historyTruncated, true);
  chat.raw(); await chat.older();
  assert.deepEqual(chat.snapshot().meta.map(m => m.id), ['1', '2', '3']);
  assert.equal(chat.snapshot().scrollTop, 220);
  assert.equal(chat.snapshot().historyTruncated, false);
});

test('older-page failure keeps the visible transcript and cursor for retry', async () => {
  const chat = fixture([{ data: [message('3')], meta: { next_before: '3' } }, new Error('offline'), { data: [message('2')], meta: {} }]);
  await chat.open('conversation'); await chat.older();
  assert.equal(chat.snapshot().historyCursor, '3');
  assert.deepEqual(chat.snapshot().meta.map(m => m.id), ['3']);
  assert.deepEqual(chat.snapshot().errors, ['offline']);
  await chat.older(); assert.deepEqual(chat.snapshot().meta.map(m => m.id), ['2', '3']);
});

test('navigation discards a late older-history response', async () => {
  let resolve;
  const pending = new Promise(done => { resolve = done; });
  const chat = fixture([{ data: [message('3')], meta: { next_before: '3' } }, pending]);
  await chat.open('conversation');
  const request = chat.older(); chat.navigate(); resolve({ data: [message('2')], meta: {} }); await request;
  assert.deepEqual(chat.snapshot().messages, []);
});
