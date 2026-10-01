import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';
const { createSessionTurnController, emptySessionTurn, sessionTurnBusy } = await import(await moduleURL(new URL('../src/lib/helper/session-turn.ts', import.meta.url)));
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };

function fixture(overrides = {}) {
  let state = emptySessionTurn(), activities = 0;
  const sends = [], completions = [], confirmations = [];
  const controller = createSessionTurnController({
    send(id, content, event, error, done, attachments) {
      const abort = new AbortController();
      sends.push({ id, content, event, error, done, attachments, abort });
      return abort;
    },
    confirm: async (...args) => { confirmations.push(args); },
    complete: async (...args) => { completions.push(args); return true; },
    ...overrides,
    changed: next => { state = next; },
    activity: () => { activities++; },
  });
  return { controller, sends, completions, confirmations, state: () => state, activities: () => activities };
}

test('one owner covers text, correlated tool progress/results, confirmation and history adoption', async () => {
  const h = fixture();
  const attachments = [{ name: 'file.txt', data: 'AAAA', media_type: 'text/plain' }];
  assert.equal(h.controller.start('one', 'question', attachments), true);
  assert.equal(h.controller.start('one', 'duplicate'), false);
  const turn = h.sends[0];
  assert.deepEqual(turn.attachments, attachments);
  turn.event({ type: 'content', content: 'First' });
  turn.event({ type: 'tool_call', tool_name: 'bash', tool_id: 'call-1' });
  turn.event({ type: 'tool_progress', tool_name: 'bash', tool_id: 'call-1', content: 'working' });
  assert.equal(h.state().tools[0].progress, 'bash: working');
  turn.event({ type: 'tool_confirm', tool_name: 'bash', tool_id: 'call-1', arguments: '{"command":"ls"}' });
  assert.equal(h.state().phase, 'awaiting_confirmation');
  assert.equal(sessionTurnBusy(h.state()), true);
  await h.controller.confirm(true);
  assert.deepEqual(h.confirmations, [['one', 'call-1', true]]);
  turn.event({ type: 'tool_result', tool_name: 'bash', tool_id: 'call-1', result: 'done' });
  assert.deepEqual(h.state().tools, [{ type: 'result', name: 'bash', id: 'call-1', result: 'done' }]);
  turn.event({ type: 'content', content: 'Final' });
  assert.equal(h.state().content, 'First\n\nFinal');
  await turn.done();
  assert.equal(h.state().phase, 'completed');
  assert.equal(h.state().content, '');
  assert.equal(h.completions[0][1], 'Final');
  await turn.done(); assert.equal(h.completions.length, 1);
});

test('Stop retains received output and invalidates callbacks before abort', async () => {
  const h = fixture(); h.controller.start('one', 'question'); const old = h.sends[0];
  old.event({ type: 'content', content: 'partial' });
  old.abort.signal.addEventListener('abort', () => old.error('late abort error'));
  h.controller.stop();
  assert.equal(old.abort.signal.aborted, true);
  assert.equal(h.state().phase, 'stopped');
  assert.equal(h.state().content, 'partial');
  assert.match(h.state().error, /Generation stopped/);
  old.event({ type: 'content', content: 'late' }); await old.done();
  assert.equal(h.state().content, 'partial'); assert.equal(h.completions.length, 0);
});

test('navigation and destruction cannot mutate the next turn or reopen a destroyed controller', async () => {
  const h = fixture(); h.controller.start('one', 'first'); const old = h.sends[0];
  h.controller.reset(); h.controller.start('two', 'second');
  old.error('offline'); old.event({ type: 'content', content: 'foreign' }); await old.done();
  assert.equal(h.state().phase, 'running'); assert.equal(h.state().content, '');
  h.controller.destroy();
  h.sends[1].event({ type: 'content', content: 'late' });
  assert.equal(h.controller.start('three', 'question'), false);
  assert.equal(h.state().content, '');
});

test('late history adoption does not clear the response of a new session', async () => {
  const gate = deferred(); const h = fixture({ complete: async (_, __, current) => { await gate.promise; return current(); } });
  h.controller.start('one', 'question'); const finished = h.sends[0].done();
  assert.equal(h.state().phase, 'finishing');
  assert.equal(h.controller.start('one', 'duplicate'), false);
  h.controller.reset(); h.controller.start('two', 'question');
  h.sends[1].event({ type: 'content', content: 'new answer' });
  gate.resolve(); await finished;
  assert.equal(h.state().phase, 'running'); assert.equal(h.state().content, 'new answer');
});

test('failed history reads and rejected streams preserve partial content', async () => {
  const h = fixture({ complete: async () => false }); h.controller.start('one', 'question');
  h.sends[0].event({ type: 'content', content: 'answer' }); await h.sends[0].done();
  assert.equal(h.state().phase, 'completed'); assert.equal(h.state().content, 'answer');
  h.controller.start('one', 'next'); h.sends[1].event({ type: 'content', content: 'partial' }); h.sends[1].error('network lost');
  assert.equal(h.state().phase, 'failed'); assert.equal(h.state().content, 'partial'); assert.equal(h.state().error, 'network lost');
});

test('confirmation is single-flight and remains available after a failed request', async () => {
  const gate = deferred(); let calls = 0;
  const h = fixture({ confirm: async () => { calls++; await gate.promise; } });
  h.controller.start('one', 'question'); h.sends[0].event({ type: 'tool_confirm', tool_name: 'bash', tool_id: 'call' });
  const pending = h.controller.confirm(true);
  await h.controller.confirm(false); assert.equal(calls, 1); assert.equal(h.state().confirming, true);
  const rejected = assert.rejects(pending, /offline/); gate.reject(new Error('offline')); await rejected;
  assert.equal(h.state().confirming, false); assert.equal(h.state().confirmation.toolId, 'call');
});

test('late confirmation response never clears a new session confirmation', async () => {
  const gate = deferred(); const h = fixture({ confirm: async () => gate.promise });
  h.controller.start('one', 'question'); h.sends[0].event({ type: 'tool_confirm', tool_id: 'old', tool_name: 'bash' });
  const pending = h.controller.confirm(true);
  h.controller.reset(); h.controller.start('two', 'question'); h.sends[1].event({ type: 'tool_confirm', tool_id: 'new', tool_name: 'bash' });
  gate.resolve(); await pending;
  assert.equal(h.state().confirmation.toolId, 'new'); assert.equal(h.state().phase, 'awaiting_confirmation');
});

test('a timed-out confirmation disappears when its tool result arrives', () => {
  const h = fixture(); h.controller.start('one', 'question');
  h.sends[0].event({ type: 'tool_confirm', tool_id: 'call', tool_name: 'bash' });
  h.sends[0].event({ type: 'tool_result', tool_id: 'call', tool_name: 'bash', result: 'not approved' });
  assert.equal(h.state().confirmation, null); assert.equal(h.state().phase, 'running');
});

test('synchronous transport failures cannot leave the composer locked', () => {
  const h = fixture({ send: () => { throw new Error('start failed'); } });
  h.controller.start('one', 'question');
  assert.equal(h.state().phase, 'failed'); assert.equal(sessionTurnBusy(h.state()), false);
});
