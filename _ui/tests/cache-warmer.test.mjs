import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { createCacheWarmer } = await import(await moduleURL(new URL('../src/lib/helper/cache-warmer.ts', import.meta.url)));

function fakeTimers() {
  let now = 0, next = 1;
  const pending = new Map();
  return {
    timers: {
      set: (cb, ms) => { const id = next++; pending.set(id, { at: now + ms, cb }); return id; },
      clear: id => pending.delete(id),
      now: () => now,
    },
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        const due = [...pending.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        pending.delete(due[0]);
        now = due[1].at;
        await due[1].cb();
      }
      now = end;
    },
    pending: () => pending.size,
  };
}

test('warms every interval while idle, until the duration after the last activity', async () => {
  const clock = fakeTimers();
  let warms = 0;
  const warmer = createCacheWarmer({ warm: async () => { warms++; }, allowed: () => true, interval: 4, duration: 30, timers: clock.timers });
  await clock.advance(100);
  assert.equal(warms, 0, 'nothing before the first real request');
  warmer.activity();
  await clock.advance(100);
  assert.equal(warms, 7, 'about seven warms per window (4..28)');
  assert.equal(clock.pending(), 0, 'stops after the window');
});

test('real activity restarts the window; reset and disallowed periods send nothing', async () => {
  const clock = fakeTimers();
  let warms = 0, allowed = true;
  const warmer = createCacheWarmer({ warm: async () => { warms++; }, allowed: () => allowed, interval: 4, duration: 30, timers: clock.timers });
  warmer.activity();
  await clock.advance(10);
  assert.equal(warms, 2);
  warmer.activity();
  await clock.advance(3);
  assert.equal(warms, 2, 'the interval restarts after activity');
  allowed = false;
  await clock.advance(10);
  assert.equal(warms, 2, 'not sent while disallowed');
  allowed = true;
  warmer.reset();
  await clock.advance(50);
  assert.equal(warms, 2, 'reset stops warming until the next activity');
});

test('a failing warm request is reported and warming continues', async () => {
  const clock = fakeTimers();
  const errors = [];
  let calls = 0;
  const warmer = createCacheWarmer({ warm: async () => { calls++; throw new Error('429'); }, allowed: () => true, onError: e => errors.push(e.message), interval: 4, duration: 12, timers: clock.timers });
  warmer.activity();
  await clock.advance(20);
  assert.equal(calls, 2);
  assert.deepEqual(errors, ['429', '429']);
  warmer.destroy();
});
