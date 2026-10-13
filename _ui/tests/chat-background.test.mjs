import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const background = await import(await moduleURL(new URL('../src/lib/helper/chat-background.ts', import.meta.url)));

// chat-runs.ts imports axios; the harness compiles one module to a data URL,
// so it is replaced by a stub and the shared run-events helper is loaded separately.
const runEventsURL = await moduleURL(new URL('../src/lib/helper/run-events.ts', import.meta.url));
const runsSource = (await readFile(new URL('../src/lib/api/chat-runs.ts', import.meta.url), 'utf8'))
  .replace("import axios from 'axios';", 'const axios = { create: () => ({}) };')
  .replace("from '../helper/run-events'", `from '${runEventsURL}'`);
const runsCode = ts.transpileModule(runsSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const runs = await import(`data:text/javascript;base64,${Buffer.from(runsCode).toString('base64')}`);

test('background mode is per conversation and off unless stored as true', () => {
  assert.equal(background.isBackgroundConfig(undefined), false);
  assert.equal(background.isBackgroundConfig({}), false);
  assert.equal(background.isBackgroundConfig({ background: 'yes' }), false);
  assert.equal(background.isBackgroundConfig({ background: true }), true);
});

test('local providers cannot run in the background', () => {
  const isLocal = model => model.startsWith('local:');
  assert.match(background.backgroundBlocker({ model: '', frontend_tools: [] }, isLocal), /model/);
  assert.match(background.backgroundBlocker({ model: 'local:lm/qwen', frontend_tools: [] }, isLocal), /Local providers/);
  assert.equal(background.backgroundBlocker({ model: 'openai/gpt', frontend_tools: [] }, isLocal), '');
});

test('the reader is told which browser-only tools a run loses', () => {
  // The question prompt works in the background too: it waits in the transcript.
  assert.deepEqual(background.backgroundOmissions({ model: 'a/b', frontend_tools: ['todo_write', 'question'] }, { localServers: [], extensions: [] }), []);
  assert.deepEqual(
    background.backgroundOmissions({ model: 'a/b', frontend_tools: [] }, { localServers: ['laptop'], extensions: ['Pages'] }),
    ['local MCP servers: laptop', 'browser extensions: Pages'],
  );
});

const call = (id, name, args) => ({ id, type: 'function', function: { name, arguments: JSON.stringify(args) } });

test('a stored question is the unanswered question call of the last step', () => {
  const ask = call('q1', 'question', { question: 'Which?', header: 'Pick', options: [{ label: 'A', description: 'first' }, { label: 'B' }, { bad: 1 }], multiple: true });
  const messages = [
    { role: 'user', content: 'go' },
    { role: 'assistant', content: '', tool_calls: [call('t1', 'current_time', {}), ask] },
    { role: 'tool', tool_call_id: 't1', content: 'now' },
  ];
  assert.deepEqual(background.pendingStoredQuestion(messages), {
    toolCallId: 'q1', question: 'Which?', header: 'Pick',
    options: [{ label: 'A', description: 'first' }, { label: 'B' }], multiple: true, custom: true,
  });
  // Answered, superseded by a new message, or absent: nothing is waiting.
  assert.equal(background.pendingStoredQuestion([...messages, { role: 'tool', tool_call_id: 'q1', content: '{"answer":"A"}' }]), null);
  assert.equal(background.pendingStoredQuestion([...messages, { role: 'user', content: 'never mind' }]), null);
  assert.equal(background.pendingStoredQuestion([{ role: 'assistant', content: 'hi' }]), null);
  // Malformed arguments still produce an answerable prompt.
  const broken = [{ role: 'assistant', tool_calls: [{ id: 'q2', type: 'function', function: { name: 'question', arguments: '{' } }] }];
  assert.deepEqual(background.pendingStoredQuestion(broken), { toolCallId: 'q2', question: 'Please answer:', options: [], multiple: false, custom: true });
  assert.equal(background.questionAnswerResult('A, B'), '{"answer":"A, B"}');
});

test('a run waiting for an answer finishes its stream as done', async () => {
  const events = [];
  const body = 'data: {"type":"question","tool_call_id":"q1"}\n\ndata: {"type":"done","waiting":true}\n\n';
  await runs.consumeChatRunEvents(new Response(body), e => events.push(e));
  assert.deepEqual(events.map(e => e.type), ['question', 'done']);
  assert.equal(events[1].waiting, true);
});

const replayHeaders = { 'X-AT-Stream-Replay': '1', 'X-AT-Stream-Complete': 'false' };

test('a run is started once, its events are delivered in order, and done resolves', async () => {
  const body = 'data: {"type":"status","run_id":"r1","trace_id":"t"}\n\n'
    + 'data: {"type":"delta","content":"Hel"}\n\ndata: {"type":"delta","content":"lo"}\n\n'
    + ': ping\n\n'
    + 'data: {"type":"message","message":{"id":"m1","sequence":2,"role":"assistant","data":{"content":"Hello"}}}\n\n'
    + 'data: {"type":"done"}\n\n: at-stream-complete\n\n';
  const calls = [];
  const fetcher = async (url, init) => {
    calls.push([url, init.method, init.headers instanceof Headers ? init.headers.get('X-AT-Stream-ID') : '']);
    return new Response(body, { headers: replayHeaders });
  };
  const events = [];
  await runs.followChatRun('c1', { streamId: 'stream-0000000001', traceId: 't', signal: new AbortController().signal, fetcher }, e => events.push(e.type));
  assert.deepEqual(events, ['status', 'delta', 'delta', 'message', 'done']);
  assert.deepEqual(calls, [['api/v1/chats/conversations/c1/run', 'POST', 'stream-0000000001']]);
});

test('reattaching replays from the start without a second POST', async () => {
  const calls = [];
  const fetcher = async (url, init) => {
    calls.push([url, init.method || 'GET']);
    return new Response('data: {"type":"done"}\n\n: at-stream-complete\n\n', { headers: replayHeaders });
  };
  await runs.followChatRun('c1', { streamId: 'stream-0000000002', resume: true, signal: new AbortController().signal, fetcher }, () => {});
  assert.deepEqual(calls, [['api/v1/chats/conversations/c1/streams/stream-0000000002?offset=0', 'GET']]);
});

test('leaving the page detaches and never cancels the run', async () => {
  const calls = [];
  const leave = new AbortController();
  const fetcher = async (url, init) => {
    calls.push(init.method || 'GET');
    return new Response(new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode('data: {"type":"status","run_id":"r","trace_id":"t"}\n\n')); } }), { headers: replayHeaders });
  };
  const following = runs.followChatRun('c1', { streamId: 'stream-0000000003', signal: leave.signal, fetcher }, () => leave.abort());
  await assert.rejects(following);
  assert.ok(!calls.includes('DELETE'), `detaching sent ${calls}`);
});

test('a stopped run and a failed run are distinguishable', async () => {
  const stop = new Response('data: {"type":"error","error":"Stopped. Completed steps are saved.","cancelled":true}\n\n', { headers: replayHeaders });
  await assert.rejects(runs.consumeChatRunEvents(stop, () => {}), e => e instanceof runs.ChatRunError && e.cancelled);
  const fail = new Response('data: {"type":"error","error":"model call failed"}\n\n', { headers: replayHeaders });
  await assert.rejects(runs.consumeChatRunEvents(fail, () => {}), e => e instanceof runs.ChatRunError && !e.cancelled && /model call failed/.test(e.message));
  // A stream that ends without a terminal event is not a finished run.
  const cut = new Response('data: {"type":"delta","content":"x"}\n\n');
  await assert.rejects(runs.consumeChatRunEvents(cut, () => {}), /ended early/);
});

test('refusals are reported with the server message', async () => {
  const fetcher = async () => new Response(JSON.stringify({ message: 'a background run is answering this conversation' }), { status: 409 });
  await assert.rejects(
    runs.followChatRun('c1', { streamId: 'stream-0000000004', signal: new AbortController().signal, fetcher }, () => {}),
    /background run is answering/,
  );
});

test('the replay of a full live view ends with a truncation notice', async () => {
  const events = [];
  // The server writes no terminal event after this; the page follows the receipt instead.
  await assert.rejects(runs.consumeChatRunEvents(new Response('data: {"type":"delta","content":"x"}\n\ndata: {"type":"truncated"}\n\n'), e => events.push(e.type)), /ended early/);
  assert.deepEqual(events, ['delta', 'truncated']);
});
