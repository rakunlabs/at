import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { createChatTurnLifecycle, runChatIterations, parseChatToolArguments } = await import(await moduleURL(new URL('../src/lib/helper/chat-turn.ts', import.meta.url)));
const { dispatchChatTool } = await import(await moduleURL(new URL('../src/lib/helper/chat-tools.ts', import.meta.url)));

test('turn claim blocks a second send during creation, persistence and tools', async () => {
  const owner = createChatTurnLifecycle();
  const turn = owner.begin();
  assert.ok(turn);
  assert.equal(owner.begin(), null);
  await Promise.resolve();
  assert.equal(owner.begin(), null);
  owner.finish(turn);
  assert.ok(owner.begin());
});

test('navigation fences old callbacks and old cleanup cannot release a newer turn', () => {
  const owner = createChatTurnLifecycle();
  const old = owner.begin();
  owner.invalidate();
  const next = owner.begin();
  assert.equal(old.controller.signal.aborted, true);
  assert.throws(() => owner.assert(old), { name: 'AbortError' });
  owner.finish(old);
  assert.equal(owner.begin(), null);
  assert.equal(owner.current(next), true);
});

test('model, tool and follow-up steps share one trace and cancellation scope', async () => {
  const owner = createChatTurnLifecycle();
  const turn = owner.begin();
  const observed = [];
  const complete = await runChatIterations(20, async iteration => {
    observed.push([turn.traceId, turn.controller.signal]);
    return iteration < 2;
  }, () => owner.assert(turn));
  assert.equal(complete, true);
  assert.equal(observed.length, 3);
  assert.equal(new Set(observed.map(([trace]) => trace)).size, 1);
  assert.equal(new Set(observed.map(([, signal]) => signal)).size, 1);
});

test('stop during a tool step prevents the next generation and respects iteration ceiling', async () => {
  const owner = createChatTurnLifecycle();
  const turn = owner.begin();
  let steps = 0;
  await assert.rejects(runChatIterations(20, async () => {
    steps++;
    turn.controller.abort();
    return true;
  }, () => owner.assert(turn)), { name: 'AbortError' });
  assert.equal(steps, 1);
  steps = 0;
  assert.equal(await runChatIterations(3, async () => { steps++; return true; }, () => {}), false);
  assert.equal(steps, 3);
});

test('tool dispatch refuses malformed and non-object JSON without executing anything', async () => {
  let executed = 0;
  const sources = { change: { type: 'builtin' } };
  const handlers = { builtin: async () => { executed++; return 'ok'; } };
  for (const args of ['{"path":', '', 'null', '[]', '42', 'true', '"text"']) {
    const result = await dispatchChatTool({ function: { name: 'change', arguments: args } }, sources, handlers);
    assert.match(result, /^Error:/, args);
  }
  assert.equal(executed, 0);
  assert.deepEqual(parseChatToolArguments('{"enabled":false,"count":0}'), { enabled: false, count: 0 });
  assert.equal(await dispatchChatTool({ function: { name: 'change', arguments: '{}' } }, sources, handlers), 'ok');
  assert.equal(executed, 1);
});

test('tool errors are visible and cancellation is not converted into model output', async () => {
  const call = { function: { name: 'change', arguments: '{}' } };
  const sources = { change: { type: 'builtin' } };
  assert.match(await dispatchChatTool(call, {}, {}), /no handler/);
  assert.equal(await dispatchChatTool(call, sources, { builtin: async () => { throw new Error('failed'); } }), 'Error: failed');
  await assert.rejects(dispatchChatTool(call, sources, { builtin: async () => { throw new DOMException('Stopped', 'AbortError'); } }), { name: 'AbortError' });
});
