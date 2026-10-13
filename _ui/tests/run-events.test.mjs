import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

// The shared client of durable runs, used by Developer Spaces and Chats.
const { consumeRunEvents, followRunStream, RunEventError } = await import(await moduleURL(new URL('../src/lib/helper/run-events.ts', import.meta.url)));

const body = text => new Response(text);
const replayHeaders = { 'X-AT-Stream-Replay': '1', 'X-AT-Stream-Complete': 'false' };

test('frames are delivered in order, split across chunks and CRLF, until done', async () => {
  const chunks = ['data: {"type":"delta","content":"Mer', 'haba 👋"}\r\n\r\n: ping\n\ndata: {"type":"done"}\n\ndata: {"type":"delta","content":"after"}\n\n'];
  const events = [];
  const response = new Response(new ReadableStream({ start(c) { for (const chunk of chunks) c.enqueue(new TextEncoder().encode(chunk)); c.close(); } }));
  await consumeRunEvents(response, e => events.push(e));
  assert.deepEqual(events.map(e => [e.type, e.content]), [['delta', 'Merhaba 👋'], ['done', undefined]]);
});

test('errors, cancellations and early ends reject distinctly', async () => {
  await assert.rejects(consumeRunEvents(body('data: {"type":"error","error":"quota"}\n\n'), () => {}), e => e instanceof RunEventError && !e.cancelled && e.message === 'quota');
  await assert.rejects(consumeRunEvents(body('data: {"type":"error","error":"Stopped","cancelled":true}\n\n'), () => {}), e => e.cancelled);
  await assert.rejects(consumeRunEvents(body('data: {"type":"delta"}\n\n'), () => {}, 'custom early'), /custom early/);
  // A final frame without a trailing blank line still counts.
  await consumeRunEvents(body('data: {"type":"done"}'), () => {});
});

test('a start is one POST; resuming is GET only; aborting never cancels', async () => {
  const calls = [];
  const fetcher = async (url, init) => { calls.push([String(url), init?.method || 'GET']); return new Response('data: {"type":"done"}\n\n: at-stream-complete\n\n', { headers: replayHeaders }); };
  await followRunStream({ fetcher, start: '/s/run', replay: '/s/streams', body: { a: 1 }, streamId: 'stream-000000000001' }, () => {});
  await followRunStream({ fetcher, start: '/s/run', replay: '/s/streams', streamId: 'stream-000000000002', resume: true }, () => {});
  assert.deepEqual(calls, [['/s/run', 'POST'], ['/s/streams/stream-000000000002?offset=0', 'GET']]);
});

test('refusals carry the server message', async () => {
  const fetcher = async () => new Response(JSON.stringify({ message: 'busy' }), { status: 409 });
  await assert.rejects(followRunStream({ fetcher, start: '/s/run', replay: '/s/streams' }, () => {}), /busy/);
});

test('a proxy error page never becomes the error text', async () => {
  const fetcher = async () => new Response('<html><body>502 Bad Gateway</body></html>', { status: 502, headers: { 'Content-Type': 'text/html' } });
  await assert.rejects(followRunStream({ fetcher, start: '/s/run', replay: '/s/streams' }, () => {}), e => e.message === 'Request failed (502)');
  await assert.rejects(
    followRunStream({ fetcher, start: '', replay: '/s/streams', streamId: 'stream-000000000003', resume: true, failed: status => `Live run unavailable (${status}).` }, () => {}),
    /Live run unavailable \(502\)/,
  );
});
