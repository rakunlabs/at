import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const script = source.slice(source.indexOf('>') + 1, source.indexOf('</script>'));
const ast = ts.createSourceFile('Chat.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const names = ['sendMessage', 'beginTurn', 'finishTurn', 'runCompletion', 'runCompletionStep', 'executeToolCall', 'ensureConversation', 'persistPending', 'resetBuffer'];
const functions = ast.statements.filter(node => ts.isFunctionDeclaration(node) && names.includes(node.name?.text)).map(node => node.getText(ast));
assert.equal(functions.length, names.length);
const writer = ast.statements.find(node => ts.isVariableStatement(node) && node.declarationList.declarations.some(declaration => declaration.name.getText(ast) === 'transcriptWriter')).getText(ast);
const turnURL = await moduleURL(new URL('../src/lib/helper/chat-turn.ts', import.meta.url));
const toolsURL = await moduleURL(new URL('../src/lib/helper/chat-tools.ts', import.meta.url));
const persistenceURL = await moduleURL(new URL('../src/lib/helper/chat-persistence.ts', import.meta.url));
const harness = `
import { createChatTurnLifecycle, runChatIterations } from '${turnURL}';
import { dispatchChatTool } from '${toolsURL}';
import { createTranscriptWriter } from '${persistenceURL}';
export function fixture() {
  const turnLifecycle = createChatTurnLifecycle();
  let userInput = 'Hello', selectedModel = 'p/m', systemPrompt = 'Prompt', effectiveReasoningEffort = 'high';
  let streaming = false, saving = false, disposed = false, abortController = null;
  let conversationId = '', scratchSessionId = '', routedId = '', conversation = null, savedSettings = null;
  let historyTruncated = false;
  let messages = [], meta = [], conversations = [], rawMessages = {}, pendingImages = [], pendingRefusals = [];
  let turnSkillRuns = [], skillRunProgress = {}, turnArtifacts = [], skillSystemPrompts = [], turnTraceId = '';
  let discoveredTools = [{ type: 'function', function: { name: 'test', parameters: {} } }], toolSourceMap = { test: { type: 'frontend' } };
  let activeTool = null, contextTokens = 0, completionTokens = 0, totalTokens = 0, confirmClear = false;
  let chatRecording = false, chatTranscribing = false, voiceContext = 0, todos = [], pendingQuestion = null;
  let createImpl = async input => ({ id: 'created', ...input }), streamImpl, uploadImpl = async data => data;
  const creates = [], appends = [], streams = [], tools = [], toasts = [], routes = [];
  const MAX_TOOL_ITERATIONS = 20, PLAYGROUND_MESSAGE_BATCH_MAX = 200, INLINE_IMAGE_TYPES = [];
  const setupReady = () => true, scrollToBottom = () => {};
  const scheduleSettingsSave = () => {};
  const splitModel = value => ({ provider_key: value.split('/')[0], model: value.split('/').slice(1).join('/') });
  const currentConfig = () => ({ builtin_tools: [] }), settingsSnapshot = () => ({ model: selectedModel });
  const playgroundTitleFrom = title => title, playgroundRoute = id => '/chats/' + id;
  const playgroundErrorMessage = (_, fallback) => fallback;
  const mergeConversation = value => { conversations.push(value); };
  const push = route => routes.push(route), addToast = message => toasts.push(message);
  const attachmentPart = image => image, toMessageData = message => ({ content: message.content });
  const getTextContent = content => typeof content === 'string' ? content : content.map(part => part.text ?? '').join('');
  const mergeDeltaContent = (previous, delta) => previous + delta;
  const outgoingContent = async content => content;
  const persistPlaygroundImages = async data => await uploadImpl(data), uploadAttachment = () => {};
  const createPlaygroundConversation = async input => { creates.push(input); return await createImpl(input); };
  const appendPlaygroundMessages = async (id, inputs) => {
    appends.push([id, inputs]);
    return inputs.map((input, index) => ({ id: 'stored-' + appends.length + '-' + index, data: input.data, sequence: appends.length * 100 + index, created_at: 'stored' }));
  };
  const streamChatCompletion = async (url, body, callbacks, signal, headers) => {
    streams.push({ body, signal, headers });
    assertStrict(callbacks.requireComplete);
    if (streamImpl) return await streamImpl(callbacks, signal, streams.length);
    callbacks.onDelta('Answer');
  };
  const assertStrict = value => { if (!value) throw new Error('tool execution must require a complete stream'); };
  const executeFrontendTool = async (name, args) => { tools.push([name, args]); return 'Tool result'; };
  const executeSkillTool = async () => '', callMCPSetTool = async () => ({}), callBuiltinTool = async () => ({ result: '' });
  const executeLocalTool = async () => '', executeExtensionTool = async () => '', collectSkillRuns = async () => '';
  ${writer}
  ${functions.join('\n')}
  return {
    sendMessage, persistPending,
    setCreate(value) { createImpl = value; }, setStream(value) { streamImpl = value; }, setUpload(value) { uploadImpl = value; },
    type(text) { userInput = text; },
    navigate(id) { resetBuffer(); conversationId = id; messages = [{ role: 'user', content: 'Other chat' }]; meta = [{ sequence: 1, created_at: 'other', imageNames: [] }]; },
    snapshot() { return { streaming, messages, meta, conversationId, creates, appends, streams, tools, toasts, routes }; },
  };
}
`;
const code = ts.transpileModule(harness, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { fixture } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};

test('the actual send handler claims the turn before lazy conversation creation', async () => {
  const chat = fixture(), create = deferred();
  chat.setCreate(async () => await create.promise);
  const first = chat.sendMessage();
  chat.type('Second Enter');
  await chat.sendMessage();
  assert.equal(chat.snapshot().streaming, true);
  assert.equal(chat.snapshot().creates.length, 1);
  assert.equal(chat.snapshot().messages.length, 1);
  create.resolve({ id: 'saved' });
  await first;
  assert.equal(chat.snapshot().streams.length, 1);
  assert.equal(chat.snapshot().streaming, false);
  assert.equal(chat.snapshot().streams[0].body.at_conversation_id, 'saved');
  assert.deepEqual(chat.snapshot().streams[0].body.messages.at(-1), { at_message_id: 'stored-1-0' });
  assert.ok(chat.snapshot().appends[0][1][0].client_id);
});

test('the send lock also covers attachment persistence before the first generation', async () => {
  const chat = fixture(), upload = deferred();
  chat.navigate('saved');
  chat.setUpload(async data => { await upload.promise; return data; });
  const first = chat.sendMessage();
  await Promise.resolve();
  chat.type('Second Enter');
  await chat.sendMessage();
  assert.equal(chat.snapshot().streams.length, 0);
  upload.resolve();
  await first;
  assert.equal(chat.snapshot().streams.length, 1);
});

test('late lazy creation cannot select or route back to the abandoned chat', async () => {
  const chat = fixture(), create = deferred();
  chat.setCreate(async () => await create.promise);
  const send = chat.sendMessage();
  chat.navigate('other');
  create.resolve({ id: 'abandoned' });
  await send;
  assert.equal(chat.snapshot().conversationId, 'other');
  assert.deepEqual(chat.snapshot().routes, []);
  assert.deepEqual(chat.snapshot().streams, []);
  assert.deepEqual(chat.snapshot().messages, [{ role: 'user', content: 'Other chat' }]);
});

test('late stream callbacks cannot alter the new chat or unlock its running turn', async () => {
  const chat = fixture(), old = deferred(), next = deferred(), started = deferred();
  chat.navigate('saved');
  chat.setStream(async (callbacks, signal, count) => {
    if (count === 1) { started.resolve(); await old.promise; callbacks.onDelta('Old answer'); }
    else { await next.promise; callbacks.onDelta('New answer'); }
  });
  const first = chat.sendMessage();
  await started.promise;
  chat.navigate('other');
  chat.type('New question');
  const second = chat.sendMessage();
  old.resolve();
  await first;
  assert.equal(chat.snapshot().streaming, true);
  assert.equal(chat.snapshot().messages.some(message => message.content === 'Old answer'), false);
  next.resolve();
  await second;
  assert.equal(chat.snapshot().messages.at(-1).content, 'New answer');
  assert.equal(chat.snapshot().streaming, false);
});

test('tool follow-ups retain the turn trace and malformed arguments execute no tool', async () => {
  for (const args of ['{}', '{broken']) {
    const chat = fixture();
    chat.setStream(async (callbacks, _, count) => {
      if (count === 1) callbacks.onToolCalls([{ id: 'call-1', type: 'function', function: { name: 'test', arguments: args } }]);
      else callbacks.onDelta('Final answer');
    });
    await chat.sendMessage();
    const snapshot = chat.snapshot();
    assert.equal(snapshot.streams.length, 2);
    assert.equal(snapshot.streams[0].headers['x-at-trace-id'], snapshot.streams[1].headers['x-at-trace-id']);
    assert.equal(snapshot.streams[0].signal, snapshot.streams[1].signal);
    assert.equal(snapshot.tools.length, args === '{}' ? 1 : 0);
    assert.equal(snapshot.messages.at(-1).content, 'Final answer');
  }
});
