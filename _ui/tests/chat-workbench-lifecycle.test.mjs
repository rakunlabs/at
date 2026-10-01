import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const page = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const script = page.slice(page.indexOf('>') + 1, page.indexOf('</script>'));
const ast = ts.createSourceFile('Chat.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const methods = ['currentSetup', 'currentConfig', 'applySetup', 'applyConfig', 'openConversation', 'loadDefaults', 'saveDefaults', 'workbenchChanged'];
const functions = ast.statements.filter(node => ts.isFunctionDeclaration(node) && methods.includes(node.name?.text)).map(node => node.getText(ast));
assert.equal(functions.length, methods.length);
const destroyCall = ast.statements.find(node => ts.isExpressionStatement(node)
  && ts.isCallExpression(node.expression) && node.expression.expression.getText(ast) === 'onDestroy');
const destroy = destroyCall.expression.arguments[0].getText(ast);

// Execute the page's actual lifecycle methods with in-memory API/DOM seams.
// AST extraction leaves these tests independent of formatting and regex shapes.
const helpers = await moduleURL(new URL('../src/lib/helper/chat-tool-selections.ts', import.meta.url));
const debounce = await moduleURL(new URL('../src/lib/helper/debounced-save.ts', import.meta.url));
const harness = `
import { initialWorkbenchSetup, newWorkbenchSetup, normalizeWorkbenchSetup } from '${helpers}';
import { createDebouncedSave } from '${debounce}';
export function fixture() {
  const FRONTEND_TOOL_NAMES = ['question', 'todo_read'];
  let models = ['p/first', 'p/saved'];
  let selectedModel = 'p/first', systemPrompt = '', reasoningEffort = '';
  let selectedMCPSetNames = [], selectedSkillNames = [], enabledBuiltinTools = ['whoami'], enabledFrontendTools = [...FRONTEND_TOOL_NAMES];
  let showTodoPanel = false, legacyMcpUrls = [], legacyMcpHeaders = {};
  let accountDefaults = initialWorkbenchSetup(FRONTEND_TOOL_NAMES), setupRevision = 0, defaultsLoaded = false, disposed = false;
  let conversationId = '', params = {}, conversation = null, scratchSessionId = '', parentTitle = '';
  let historyTruncated = false, savedSettings = null, historyLoading = false, appliedPresetId = '';
  let messages = [], rawMessages = {}, meta = [], toolDiscoveryVersion = 0, settingsTimer = null, confirmClearTimer = null;
  let extensionUnsubscribe = null, extensionBridge = null;
  const turnLifecycle = { invalidate() {}, generation: () => 0 };
  let abortController = null;
  let settingsSaves = 0, discoveries = 0, resets = 0;
  let storedDefaults = { builtin_tools: ['whoami'] }, storedConversation;
  const writes = [], timers = new Map();
  let timerID = 0;
  const defaultsSave = createDebouncedSave(async setup => { writes.push(setup); }, 1200, {
    set(callback) { timers.set(++timerID, callback); return timerID; },
    clear(id) { timers.delete(id); },
  });
  const getPlaygroundDefaults = async () => await storedDefaults;
  const getPlaygroundConversation = async () => storedConversation;
  const listPlaygroundMessages = async () => ({ data: [], meta: {} });
  const joinModel = (provider, model) => model ? provider + '/' + model : provider;
  const settingsSnapshot = () => currentSetup();
  const mergeConversation = () => {}, scrollToBottom = () => {}, loadParentTitle = () => {}, toChatMessage = message => message;
  const HISTORY_MAX_PAGES = 10, HISTORY_PAGE_SIZE = 200;
  const saveSettings = async () => {};
  const scheduleSettingsSave = () => { settingsSaves++; };
  const discoverTools = async () => { discoveries++; };
  const resetBuffer = () => { resets++; systemPrompt = ''; messages = []; };
  const addToast = message => { throw new Error(message); };
  const playgroundErrorMessage = (_, fallback) => fallback;
  ${functions.join('\n')}
  const destroy = ${destroy};
  return {
    openConversation, loadDefaults, workbenchChanged, currentSetup, currentConfig, destroy,
    setDefaults(value) { storedDefaults = value; },
    setConversation(value) { storedConversation = value; },
    change(value) { applySetup(normalizeWorkbenchSetup(value)); workbenchChanged(); },
    snapshot() { return { setup: currentSetup(), accountDefaults, conversationId, historyLoading, discoveries, resets, settingsSaves, timers: timers.size, writes, disposed }; },
  };
}
`;
const compiled = ts.transpileModule(harness, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { fixture } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`);

test('opening saved history does not replace defaults; New restores the entire account setup', async () => {
  const chat = fixture();
  chat.setDefaults({ model: 'p/saved', system_prompt: 'Default prompt', reasoning_effort: 'high', skills: ['docs'], builtin_tools: [], frontend_tools: [] });
  await chat.loadDefaults(0);
  const defaults = chat.currentSetup();
  chat.setConversation({ id: 'old', provider_key: 'p', model: 'first', system_prompt: 'Old prompt', config: { builtin_tools: ['current_time'], frontend_tools: ['question'], reasoning_effort: 'low' } });
  await chat.openConversation('old');
  assert.equal(chat.currentSetup().system_prompt, 'Old prompt');
  assert.equal(chat.snapshot().timers, 0);
  assert.deepEqual(chat.snapshot().writes, []);
  await chat.openConversation('');
  assert.deepEqual(chat.currentSetup(), defaults);
  assert.equal(chat.snapshot().historyLoading, false);
  // New on the already-scratch route follows exactly the same path.
  await chat.openConversation('');
  assert.deepEqual(chat.currentSetup(), defaults);
});

test('navigation flushes edits before resetting and destruction clears the next pending timer', async () => {
  const chat = fixture();
  await chat.loadDefaults(0);
  chat.change({ model: 'p/saved', system_prompt: 'Edited', reasoning_effort: 'high', builtin_tools: [], frontend_tools: [] });
  const edited = chat.currentSetup();
  assert.equal(chat.snapshot().timers, 1);
  await chat.openConversation('');
  assert.deepEqual(chat.snapshot().writes, [edited]);
  assert.deepEqual(chat.currentSetup(), edited);
  assert.equal(chat.snapshot().timers, 0);
  chat.change({ ...edited, system_prompt: 'Last edit' });
  chat.destroy();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(chat.snapshot().timers, 0);
  assert.deepEqual(chat.snapshot().writes.map(setup => setup.system_prompt), ['Edited', 'Last edit']);
});

test('conversation serialization preserves legacy MCP records only in that conversation', async () => {
  const chat = fixture();
  await chat.loadDefaults(0);
  chat.setConversation({ id: 'legacy', provider_key: 'p', model: 'first', system_prompt: 'Legacy prompt', config: { mcp_urls: ['https://old.example/mcp'], mcp_headers: { Authorization: 'saved' }, builtin_tools: [], frontend_tools: [], reasoning_effort: 'high' } });
  await chat.openConversation('legacy');
  assert.deepEqual(chat.currentConfig(), { mcp_urls: ['https://old.example/mcp'], mcp_headers: { Authorization: 'saved' }, mcp_sets: [], skills: [], builtin_tools: [], frontend_tools: [], reasoning_effort: 'high' });
  await chat.openConversation('');
  assert.deepEqual(chat.currentConfig().mcp_urls, []);
  assert.deepEqual(chat.currentConfig().mcp_headers, {});
  assert.deepEqual(chat.currentConfig().builtin_tools, ['whoami']);
});

test('late defaults cannot undo a user edit', async () => {
  const chat = fixture();
  let resolve;
  chat.setDefaults(new Promise(done => { resolve = done; }));
  const loading = chat.loadDefaults(0);
  chat.change({ model: 'p/saved', system_prompt: 'Typed while loading', builtin_tools: [] });
  resolve({ system_prompt: 'Stale stored prompt', builtin_tools: ['whoami'] });
  await loading;
  assert.equal(chat.currentSetup().system_prompt, 'Typed while loading');
  assert.deepEqual(chat.currentSetup().builtin_tools, []);
  chat.destroy();
  assert.equal(chat.snapshot().writes[0].system_prompt, 'Typed while loading');
});

test('defaults completing after destruction schedule no writes or discovery', async () => {
  const chat = fixture();
  let resolve;
  chat.setDefaults(new Promise(done => { resolve = done; }));
  const loading = chat.loadDefaults(0);
  chat.destroy();
  resolve({ system_prompt: 'Late' });
  await loading;
  assert.equal(chat.currentSetup().system_prompt, '');
  assert.equal(chat.snapshot().discoveries, 0);
  assert.equal(chat.snapshot().timers, 0);
});
