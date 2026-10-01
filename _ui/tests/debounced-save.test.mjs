import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/debounced-save.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { createDebouncedSave } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

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
