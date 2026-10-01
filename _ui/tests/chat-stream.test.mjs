import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
import { moduleURL } from './typescript-module.mjs';

const source = await readFile(new URL('../src/lib/helper/chat.ts', import.meta.url), 'utf8');
const resumeURL = await moduleURL(new URL('../src/lib/helper/resumable-stream.ts', import.meta.url));
const code = ts.transpileModule(source.replace("from './resumable-stream'", `from '${resumeURL}'`).replace("import { authFetch as fetch } from '../api/transport';", 'const fetch = (...args) => globalThis.chatStreamFetch(...args);'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { streamChatCompletion } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const event = value => `data: ${JSON.stringify(value)}\r\n\r\n`;
const tool = event({ choices: [{ delta: { tool_calls: [{ index: 0, id: 'call-1', function: { name: 'change', arguments: '{}' } }] } }] });
const finish = reason => event({ choices: [{ delta: {}, finish_reason: reason }] });

async function run(text, overrides = {}) {
  const calls = [], content = [];
  const response = new Response(new ReadableStream({ start(controller) {
    for (const byte of new TextEncoder().encode(text)) controller.enqueue(new Uint8Array([byte]));
    controller.close();
  } }));
  globalThis.chatStreamFetch = async () => response;
  const request = streamChatCompletion('api/v1/chats/completions', { model: 'p/m', messages: [], stream: true }, {
    requireComplete: true, onDelta: delta => content.push(delta), onToolCalls: value => calls.push(...value), onError() {}, ...overrides,
  }, new AbortController().signal);
  return { request, calls, content, response };
}

for (const [name, ending] of [
  ['early EOF', ''], ['DONE without finish', 'data: [DONE]\n\n'],
  ['truncated', finish('length')], ['filtered', finish('content_filter')],
  ['malformed event', 'data: {broken}\n\n' + finish('tool_calls')],
  ['provider error after finish', finish('tool_calls') + event({ error: { message: 'upstream failed' } })],
]) {
  test(`${name} never delivers pending calls to the Chats executor`, async () => {
    const result = await run(tool + ending);
    await assert.rejects(result.request);
    assert.deepEqual(result.calls, []);
    assert.equal(result.response.body.locked, false);
  });
}

test('complete calls and split UTF-8 are preserved, including a final line without newline', async () => {
  const result = await run(event({ choices: [{ delta: { content: 'Merhaba 🌍' } }] }) + tool + finish('tool_calls').trimEnd());
  await result.request;
  assert.deepEqual(result.content, ['Merhaba 🌍']);
  assert.equal(result.calls[0].function.arguments, '{}');
  assert.equal(result.response.body.locked, false);
});

test('sparse, out-of-order tool indices correlate their own argument fragments', async () => {
  const result = await run(event({ choices: [{ delta: { tool_calls: [
    { index: 4, id: 'four', function: { name: 'change', arguments: '{"value":' } },
    { index: 1, id: 'one', function: { name: 'change', arguments: '{}' } },
  ] } }] }) + event({ choices: [{ delta: { tool_calls: [{ index: 4, function: { arguments: '4}' } }] } }] }) + finish('tool_calls'));
  await result.request;
  assert.deepEqual(result.calls.map(call => [call.id, call.function.arguments]), [['one', '{}'], ['four', '{"value":4}']]);
});

test('callback errors propagate instead of being swallowed as malformed provider JSON', async () => {
  const result = await run(event({ choices: [{ delta: { content: 'Text' } }] }) + tool + finish('tool_calls'), {
    onDelta() { throw new Error('UI callback failed'); },
  });
  await assert.rejects(result.request, /UI callback failed/);
  assert.deepEqual(result.calls, []);
  assert.equal(result.response.body.locked, false);
});
