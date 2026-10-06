import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const script = source.slice(source.indexOf('>') + 1, source.indexOf('</script>'));
const ast = ts.createSourceFile('Chat.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const names = ['sendMessage', 'beginTurn', 'finishTurn', 'runCompletion', 'runCompletionStep', 'executeToolCall', 'ensureConversation', 'persistPending', 'resetBuffer', 'appendUserMessages', 'drainQueue', 'continueWithQueue', 'runUserTurn', 'sendQueuedNow', 'interruptAndSend', 'repairInterruptedTail'];
const functions = ast.statements.filter(node => ts.isFunctionDeclaration(node) && names.includes(node.name?.text)).map(node => node.getText(ast));
assert.equal(functions.length, names.length);
const writer = ast.statements.find(node => ts.isVariableStatement(node) && node.declarationList.declarations.some(declaration => declaration.name.getText(ast) === 'transcriptWriter')).getText(ast);
const turnURL = await moduleURL(new URL('../src/lib/helper/chat-turn.ts', import.meta.url));
const toolsURL = await moduleURL(new URL('../src/lib/helper/chat-tools.ts', import.meta.url));
const persistenceURL = await moduleURL(new URL('../src/lib/helper/chat-persistence.ts', import.meta.url));
const queueURL = await moduleURL(new URL('../src/lib/helper/chat-queue.ts', import.meta.url));
const harness = `
import { createChatTurnLifecycle, runChatIterations } from '${turnURL}';
import { dispatchChatTool } from '${toolsURL}';
import { createTranscriptWriter } from '${persistenceURL}';
import { INTERRUPTED_TOOL_RESULT, isEmptyAssistant, queuedMessage, unansweredToolCalls, userMessageContent } from '${queueURL}';
export function fixture() {
  const turnLifecycle = createChatTurnLifecycle();
  let userInput = 'Hello', selectedModel = 'p/m', systemPrompt = 'Prompt', effectiveReasoningEffort = 'high';
  let streaming = false, saving = false, disposed = false, abortController = null, loadingTools = false;
  let queuedMessages = [], activeTurn = null;
  let conversationId = '', scratchSessionId = '', routedId = '', conversation = null, savedSettings = null;
  let historyTruncated = false;
  let messages = [], meta = [], conversations = [], rawMessages = {}, pendingImages = [], pendingRefusals = [];
  let turnSkillRuns = [], skillRunProgress = {}, turnArtifacts = [], skillSystemPrompts = [], turnTraceId = '';
  let discoveredTools = [{ type: 'function', function: { name: 'test', parameters: {} } }], toolSourceMap = { test: { type: 'frontend' } };
  let activeTool = null, contextTokens = 0, completionTokens = 0, totalTokens = 0, confirmClear = false;
  let chatRecording = false, chatTranscribing = false, voiceContext = 0, todos = [], pendingQuestion = null;
  let createImpl = async input => ({ id: 'created', ...input }), streamImpl, toolImpl, uploadImpl = async data => data;
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
  const executeFrontendTool = async (name, args) => { tools.push([name, args]); return toolImpl ? await toolImpl(name, args) : 'Tool result'; };
  const executeSkillTool = async () => '', callMCPSetTool = async () => ({}), callBuiltinTool = async () => ({ result: '' });
  const executeLocalTool = async () => '', executeExtensionTool = async () => '', collectSkillRuns = async () => '';
  const isLocalModelRef = ref => ref.startsWith('local:');
  const localProviderFor = ref => ({ provider: { id: 'lp', name: ref.slice(6).split('/')[0], base_url: 'http://127.0.0.1:11434/v1' }, model: ref.slice(6).split('/').slice(1).join('/') });
  const runLocalProviderCompletion = async (turn, target, reqMessages, callbacks) => {
    streams.push({ local: target, body: { messages: reqMessages }, signal: turn.controller.signal });
    assertStrict(callbacks.requireComplete && callbacks.mintMissingToolCallIds);
    callbacks.onDelta('Local answer');
  };
  ${writer}
  ${functions.join('\n')}
  return {
    sendMessage, persistPending, sendQueuedNow, interruptAndSend,
    stop() { abortController?.abort(); },
    setCreate(value) { createImpl = value; }, setStream(value) { streamImpl = value; }, setTool(value) { toolImpl = value; }, setUpload(value) { uploadImpl = value; },
    type(text) { userInput = text; },
    select(model) { selectedModel = model; },
    truncateHistory() { historyTruncated = true; },
    navigate(id) { resetBuffer(); conversationId = id; messages = [{ role: 'user', content: 'Other chat' }]; meta = [{ sequence: 1, created_at: 'other', imageNames: [] }]; },
    snapshot() { return { streaming, messages, meta, conversationId, creates, appends, streams, tools, toasts, routes, queuedMessages, userInput }; },
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

test('a message sent during lazy creation is queued and joins the same turn', async () => {
  const chat = fixture(), create = deferred();
  chat.setCreate(async () => await create.promise);
  const first = chat.sendMessage();
  chat.type('Second Enter');
  await chat.sendMessage();
  assert.equal(chat.snapshot().streaming, true);
  assert.equal(chat.snapshot().creates.length, 1);
  assert.equal(chat.snapshot().messages.length, 1);
  assert.equal(chat.snapshot().queuedMessages.length, 1);
  assert.equal(chat.snapshot().userInput, '');
  create.resolve({ id: 'saved' });
  await first;
  const s = chat.snapshot();
  assert.equal(s.streaming, false);
  assert.equal(s.queuedMessages.length, 0);
  assert.equal(s.streams.length, 2);
  assert.equal(s.streams[0].body.at_conversation_id, 'saved');
  assert.deepEqual(s.streams[0].body.messages.at(-1), { at_message_id: 'stored-1-0' });
  assert.ok(s.appends[0][1][0].client_id);
  // The queued message follows the first answer, and the follow-up call sees it.
  assert.deepEqual(s.messages.map(m => [m.role, m.content]), [['user', 'Hello'], ['assistant', 'Answer'], ['user', 'Second Enter'], ['assistant', 'Answer']]);
  assert.equal(s.streams[1].body.messages.length, 4);
  // One trace: the queued message continued the turn rather than starting another.
  assert.equal(s.streams[0].headers['x-at-trace-id'], s.streams[1].headers['x-at-trace-id']);
});

test('the send lock still covers attachment persistence before the first generation', async () => {
  const chat = fixture(), upload = deferred();
  chat.navigate('saved');
  chat.setUpload(async data => { await upload.promise; return data; });
  const first = chat.sendMessage();
  await Promise.resolve();
  chat.type('Second Enter');
  await chat.sendMessage();
  assert.equal(chat.snapshot().streams.length, 0);
  assert.equal(chat.snapshot().queuedMessages.length, 1);
  upload.resolve();
  await first;
  assert.equal(chat.snapshot().streams.length, 2);
});

test('queued messages are delivered after tool results, before the next model call', async () => {
  const chat = fixture(), gate = deferred();
  chat.setStream(async (callbacks, _signal, count) => {
    if (count === 1) {
      callbacks.onToolCalls([{ id: 'c1', type: 'function', function: { name: 'test', arguments: '{}' } }]);
      await gate.promise;
      return;
    }
    callbacks.onDelta('Done');
  });
  const send = chat.sendMessage();
  await Promise.resolve();
  chat.type('Also check X');
  await chat.sendMessage();
  gate.resolve();
  await send;
  const s = chat.snapshot();
  assert.deepEqual(s.messages.map(m => m.role), ['user', 'assistant', 'tool', 'user', 'assistant']);
  assert.equal(s.messages[3].content, 'Also check X');
  assert.equal(s.streams.length, 2);
});

test('Stop keeps queued messages waiting until the user sends them', async () => {
  const chat = fixture(), gate = deferred();
  chat.setStream(async (_callbacks, signal) => {
    await new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))));
  });
  const send = chat.sendMessage();
  await Promise.resolve();
  chat.type('Queued');
  await chat.sendMessage();
  chat.stop();
  await send;
  assert.equal(chat.snapshot().streaming, false);
  assert.equal(chat.snapshot().queuedMessages.length, 1);
  chat.setStream(async callbacks => { callbacks.onDelta('Later'); });
  chat.sendQueuedNow();
  await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(chat.snapshot().queuedMessages.length, 0);
  assert.equal(chat.snapshot().messages.at(-2).content, 'Queued');
  assert.equal(chat.snapshot().messages.at(-1).content, 'Later');
  gate.resolve();
});

test('interrupt & send cuts a generation short and continues with the queue', async () => {
  const chat = fixture();
  chat.setStream(async (callbacks, signal, count) => {
    if (count === 1) {
      callbacks.onDelta('Partial');
      await new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))));
    }
    callbacks.onDelta('Redirected');
  });
  const send = chat.sendMessage();
  await new Promise(resolve => setTimeout(resolve, 0));
  chat.type('Change of plan');
  chat.interruptAndSend();
  await send;
  for (let i = 0; i < 5 && chat.snapshot().streams.length < 2; i++) await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
  const s = chat.snapshot();
  assert.equal(s.queuedMessages.length, 0);
  assert.equal(s.userInput, '');
  assert.deepEqual(s.messages.map(m => [m.role, m.content]), [['user', 'Hello'], ['assistant', 'Partial'], ['user', 'Change of plan'], ['assistant', 'Redirected']]);
  assert.equal(s.streams.length, 2);
  assert.notEqual(s.streams[0].headers['x-at-trace-id'], s.streams[1].headers['x-at-trace-id']);
});

test('interrupting a tool call closes it with an explicit result before continuing', async () => {
  const chat = fixture(), tool = deferred(), started = deferred();
  chat.setTool(async () => { started.resolve(); await tool.promise; return 'late result'; });
  chat.setStream(async (callbacks, _signal, count) => {
    if (count === 1) {
      callbacks.onToolCalls([{ id: 'c1', type: 'function', function: { name: 'test', arguments: '{}' } }]);
      return;
    }
    callbacks.onDelta('After interrupt');
  });
  const send = chat.sendMessage();
  await started.promise;
  chat.type('Stop that');
  chat.interruptAndSend();
  tool.resolve();
  await send;
  for (let i = 0; i < 10 && chat.snapshot().streams.length < 2; i++) await new Promise(resolve => setTimeout(resolve, 0));
  await new Promise(resolve => setTimeout(resolve, 0));
  const s = chat.snapshot();
  assert.deepEqual(s.messages.map(m => m.role), ['user', 'assistant', 'tool', 'user', 'assistant']);
  assert.equal(s.messages[2].tool_call_id, 'c1');
  assert.match(s.messages[2].content, /Interrupted by the user/);
  assert.equal(s.messages[3].content, 'Stop that');
  assert.equal(s.messages[4].content, 'After interrupt');
  // The late tool output never entered the transcript.
  assert.ok(!s.messages.some(m => m.content === 'late result'));
});

test('interrupt with nothing queued does nothing, and Stop alone still waits', async () => {
  const chat = fixture(), opened = deferred();
  chat.setStream(async (_callbacks, signal) => {
    opened.resolve();
    await new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))));
  });
  const send = chat.sendMessage();
  await opened.promise;
  chat.type('');
  chat.interruptAndSend();
  assert.equal(chat.snapshot().streaming, true);
  chat.stop();
  await send;
  assert.equal(chat.snapshot().streams.length, 1);
});

test('navigation discards the queue of the abandoned chat', async () => {
  const chat = fixture(), create = deferred();
  chat.setCreate(async () => await create.promise);
  const send = chat.sendMessage();
  chat.type('Queued');
  await chat.sendMessage();
  chat.navigate('other');
  create.resolve({ id: 'abandoned' });
  await send;
  assert.equal(chat.snapshot().queuedMessages.length, 0);
  assert.equal(chat.snapshot().streams.length, 0);
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

test('a local model goes to the browser-called provider with history inline, never by reference', async () => {
  const chat = fixture();
  chat.navigate('saved');
  chat.select('local:ollama/llama3:8b');
  chat.type('Local question');
  await chat.sendMessage();
  const snapshot = chat.snapshot();
  assert.equal(snapshot.streams.length, 1);
  assert.deepEqual(snapshot.streams[0].local.model, 'llama3:8b');
  assert.equal(snapshot.streams[0].local.provider.name, 'ollama');
  // Saved rows are sent as content: the provider cannot resolve at_message_id.
  assert.equal(snapshot.streams[0].body.messages.some(m => 'at_message_id' in m), false);
  assert.deepEqual(snapshot.streams[0].body.messages.map(m => m.content), ['Other chat', 'Local question']);
  assert.equal(snapshot.messages.at(-1).content, 'Local answer');
});

test('a local model refuses to run on a partially loaded conversation', async () => {
  const chat = fixture();
  chat.navigate('saved');
  chat.truncateHistory();
  chat.select('local:ollama/llama3');
  await chat.sendMessage();
  const snapshot = chat.snapshot();
  assert.equal(snapshot.streams.length, 0);
  assert.match(snapshot.toasts.at(-1), /Load older messages/);
});
