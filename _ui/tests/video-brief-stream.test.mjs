import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('../src/lib/components/studio/VideoBriefBuilderPanel.svelte', import.meta.url), 'utf8');
const send = source.slice(source.indexOf('  async function sendMessage()'), source.indexOf('  function stopStreaming()'));
const code = ts.transpile(send, { target: ts.ScriptTarget.ES2022 });
const harness = new Function('fetch', `
  let userInput = 'Edit', destroyed = false, disabled = false, streaming = false;
  let selectedModel = 'test', controller = null, error = '', status = '', messages = [];
  let executed = 0;
  const systemPrompt = '', tools = [];
  const getTextContent = x => x;
  const mergeDeltaContent = (a, b) => a + b;
  const scrollToBottom = async () => {};
  const executeToolCall = () => { executed++; return '{}'; };
  ${code}
  return sendMessage().then(() => ({ executed, error, streaming }));
`);
const event = x => `data: ${JSON.stringify(x)}\n\n`;
const choice = (delta, finish_reason = null) => event({ choices: [{ delta, finish_reason }] });
const calls = choice({ tool_calls: [{ index: 0, id: 'call-1', function: { name: 'update_video_brief', arguments: '{"title":' } }] })
  + choice({ tool_calls: [{ index: 0, function: { arguments: '"Test"}' } }] });
const done = 'data: [DONE]\n\n';
const stop = choice({}, 'stop') + done;

for (const [name, stream, expected] of [
  ['complete tool call', calls + choice({}, 'tool_calls') + done, 1],
  ['EOF after valid JSON arguments', calls, 0],
  ['EOF after finish without DONE', calls + choice({}, 'tool_calls'), 0],
  ['DONE without finish', calls + done, 0],
  ['gateway error content and stop', calls + choice({ content: '\n\nError: upstream failed' }, 'stop') + done, 0],
  ['error envelope', calls + event({ error: { message: 'upstream failed' } }) + done, 0],
  ['error after finish', calls + choice({}, 'tool_calls') + event({ error: { message: 'failed' } }) + done, 0],
  ['length finish', calls + choice({}, 'length') + done, 0],
  ['malformed data', calls + 'data: {broken}\n\n' + choice({}, 'tool_calls') + done, 0],
  ['normal text', choice({ content: 'Hello' }) + stop, 0],
]) {
  test(name, async () => {
    let requests = 0;
    const result = await harness(async () => {
      const text = requests++ === 0 ? stream : stop;
      return new Response(new ReadableStream({ start(controller) {
        // Byte-sized chunks exercise fragmentation and line buffering.
        for (const byte of new TextEncoder().encode(text)) controller.enqueue(new Uint8Array([byte]));
        controller.close();
      } }));
    });
    assert.equal(result.executed, expected);
    assert.equal(result.streaming, false);
    assert.equal(Boolean(result.error), expected === 0 && name !== 'normal text');
  });
}
