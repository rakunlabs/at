import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { createDebouncedSave } = await import(await moduleURL(new URL('../src/lib/helper/debounced-save.ts', import.meta.url)));

function fixture() {
  const callbacks = new Map();
  const writes = [];
  let sequence = 0;
  const save = createDebouncedSave(async value => { writes.push(value); }, 1200, {
    set(callback, delay) {
      assert.equal(delay, 1200);
      callbacks.set(++sequence, callback);
      return sequence;
    },
    clear(handle) { callbacks.delete(handle); },
  });
  return { save, callbacks, writes };
}

test('rapid changes debounce to one write carrying the latest snapshot', async () => {
  const { save, callbacks, writes } = fixture();
  save.schedule({ model: 'first' });
  save.schedule({ model: 'last', builtin_tools: [] });
  assert.equal(callbacks.size, 1);
  [...callbacks.values()][0]();
  await Promise.resolve();
  assert.deepEqual(writes, [{ model: 'last', builtin_tools: [] }]);
  assert.equal(callbacks.size, 0);
});

test('navigation or destruction flushes the captured setup once and clears its timer', async () => {
  const { save, callbacks, writes } = fixture();
  let current = { model: 'old chat', reasoning_effort: 'high' };
  save.schedule({ ...current });
  current = { model: 'new chat', reasoning_effort: '' };
  await save.flush();
  await save.flush();
  assert.deepEqual(writes, [{ model: 'old chat', reasoning_effort: 'high' }]);
  assert.equal(callbacks.size, 0);
});

test('cancel removes pending writes and flushing an idle saver does nothing', async () => {
  const { save, callbacks, writes } = fixture();
  await save.flush();
  save.schedule({ model: 'cancelled' });
  save.cancel();
  await save.flush();
  assert.deepEqual(writes, []);
  assert.equal(callbacks.size, 0);
});

test('a newer defaults write waits for an in-flight older write', async () => {
  let finishOld;
  const old = new Promise(resolve => { finishOld = resolve; });
  const writes = [];
  const save = createDebouncedSave(async value => {
    writes.push(`start:${value}`);
    if (value === 'old') await old;
    writes.push(`finish:${value}`);
  }, 1200);
  save.schedule('old');
  const first = save.flush();
  save.schedule('new');
  const next = save.flush();
  assert.deepEqual(writes, ['start:old']);
  finishOld();
  await Promise.all([first, next]);
  assert.deepEqual(writes, ['start:old', 'finish:old', 'start:new', 'finish:new']);
});
