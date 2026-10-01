import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';
const { resumableStream } = await import(await moduleURL(new URL('../src/lib/helper/resumable-stream.ts', import.meta.url)));
const headers = { 'X-AT-Stream-Replay': '1', 'X-AT-Stream-Complete': 'false' };

test('disconnect replays from exact byte cursor without a second POST, including split UTF-8', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const bytes = new TextEncoder().encode('data: {"text":"dünya"}\n\ndata: [DONE]\n\n: at-stream-complete\n\n');
  const split = new TextEncoder().encode('data: {"text":"d').length + 1;
  const calls = [];
  const fetcher = async (url, init) => {
    calls.push([url, init.method || 'GET']);
    if (init.method === 'POST') {
      let first = true;
      return new Response(new ReadableStream({ pull(c) {
        if (first) { first = false; c.enqueue(bytes.slice(0, split)); } else c.error(new Error('connection lost'));
      } }), { headers });
    }
    assert.equal(new URL(url, 'https://example.test').searchParams.get('offset'), String(split));
    return new Response(bytes.slice(split), { headers });
  };
  const response = await resumableStream(fetcher, '/start', { method: 'POST', signal: new AbortController().signal }, '/streams');
  const result = response.text();
  for (let i = 0; i < 20; i++) await Promise.resolve();
  t.mock.timers.tick(500);
  assert.equal(await result, new TextDecoder().decode(bytes));
  assert.deepEqual(calls.map(c => c[1]), ['POST', 'GET']);
});

test('lost POST response uses GET only; unknown replay fails without rerunning work', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  const pending = resumableStream(async (url, init) => {
    calls.push(init.method || 'GET');
    if (init.method === 'POST') throw new Error('lost response');
    return new Response('{"message":"replay unavailable"}', { status: 404 });
  }, '/start', { method: 'POST' }, '/streams');
  const rejected = assert.rejects(pending, /replay unavailable/);
  for (let i = 0; i < 10; i++) await Promise.resolve();
  t.mock.timers.tick(500); await rejected;
  assert.deepEqual(calls, ['POST', 'GET']);
});

test('explicit abort sends cancellation while a network failure does not', async () => {
  const calls = [], controller = new AbortController();
  const response = await resumableStream(async (_, init) => {
    calls.push(init.method);
    return new Response(new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode(': ping\n\n')); } }), { headers });
  }, '/start', { method: 'POST', signal: controller.signal }, '/streams');
  controller.abort();
  await response.body.cancel();
  assert.deepEqual(calls, ['POST', 'DELETE']);
});

test('missing heartbeats reattach without extending a dead connection indefinitely', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  const response = await resumableStream(async (url, init) => {
    calls.push(init.method || 'GET');
    if (init.method === 'POST') return new Response(new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode(': ping\n\n')); } }), { headers });
    assert.equal(new URL(url, 'https://example.test').searchParams.get('offset'), '8');
    return new Response('data: [DONE]\n\n: at-stream-complete\n\n', { headers });
  }, '/start', { method: 'POST' }, '/streams');
  const read = response.text();
  for (let i = 0; i < 30; i++) await Promise.resolve();
  t.mock.timers.tick(45000);
  for (let i = 0; i < 30; i++) await Promise.resolve();
  t.mock.timers.tick(500);
  assert.equal(await read, ': ping\n\ndata: [DONE]\n\n: at-stream-complete\n\n');
  assert.deepEqual(calls, ['POST', 'GET']);
});

test('a gracefully truncated completed replay is not mistaken for the full body', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  const full = 'data: [DONE]\n\n: at-stream-complete\n\n';
  const response = await resumableStream(async (_, init) => {
    calls.push(init.method || 'GET');
    return new Response(init.method === 'POST' ? full.slice(0, 4) : full.slice(4), { headers: { ...headers, 'X-AT-Stream-Complete': 'true', 'X-AT-Stream-Length': String(full.length) } });
  }, '/start', { method: 'POST' }, '/streams');
  const read = response.text();
  for (let i = 0; i < 30; i++) await Promise.resolve();
  t.mock.timers.tick(500);
  assert.equal(await read, full);
  assert.deepEqual(calls, ['POST', 'GET']);
});
