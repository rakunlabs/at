import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/api/chat-sessions.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source
  .replace("import axios from 'axios';", 'const axios = { create: () => ({}) };')
  .replace("import { authFetch as fetch } from './transport';", ''), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { consumeChatEvents } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

function stream(text, chunkSize = 1) {
  const bytes = new TextEncoder().encode(text);
  return new Response(new ReadableStream({ start(controller) {
    for (let i = 0; i < bytes.length; i += chunkSize) controller.enqueue(bytes.slice(i, i + chunkSize));
    controller.close();
  } }));
}

test('split UTF-8, CRLF, multiline data and final frame preserve the complete response', async () => {
  const events = [];
  let completed = false;
  await consumeChatEvents(stream(': heartbeat\r\n\r\nevent: content\r\ndata:{"type":"content",\r\ndata: "content":"Merhaba, dünya 👋"}\r\n\r\ndata: {"type":"done"}'), e => events.push(e), async () => {
    await Promise.resolve();
    completed = true;
  });
  assert.deepEqual(events, [{ type: 'content', content: 'Merhaba, dünya 👋' }]);
  assert.equal(completed, true);
});

test('an interrupted stream reports failure instead of erasing received text through onDone', async () => {
  const events = [];
  let completed = false;
  await assert.rejects(consumeChatEvents(stream('data: {"type":"content","content":"Partial answer"}\n\n'), e => events.push(e), () => { completed = true; }), /before the response completed/);
  assert.equal(completed, false);
  assert.equal(events[0].content, 'Partial answer');
});

test('server errors and malformed frames do not silently finish the turn', async () => {
  for (const [frame, error] of [
    ['event: error\ndata: {"error":"Provider unavailable"}\n\n', /Provider unavailable/],
    ['data: {"type":"error","error":{"message":"Quota exceeded"}}\n\n', /Quota exceeded/],
    ['data: {invalid}\n\n', /JSON|property/i],
  ]) {
    await assert.rejects(consumeChatEvents(stream(frame), () => assert.fail('not content'), () => assert.fail('not completed')), error);
  }
});

test('UI callback failures propagate rather than being mistaken for malformed JSON', async () => {
  await assert.rejects(consumeChatEvents(stream('data: {"type":"content","content":"answer"}\n\n'), () => { throw new Error('render failed'); }, () => assert.fail('not completed')), /render failed/);
});

test('completion releases the stream and does not process trailing events', async () => {
  let cancelled = false;
  let done = 0;
  const response = new Response(new ReadableStream({
    start(controller) { controller.enqueue(new TextEncoder().encode('data: {"type":"done"}\n\ndata: {"type":"content","content":"late"}\n\n')); },
    cancel() { cancelled = true; },
  }));
  await consumeChatEvents(response, () => assert.fail('trailing event'), () => { done++; });
  assert.equal(done, 1);
  assert.equal(cancelled, true);
  assert.equal(response.body.locked, false);
});
