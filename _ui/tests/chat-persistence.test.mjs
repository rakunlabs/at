import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { createTranscriptWriter } = await import(await moduleURL(new URL('../src/lib/helper/chat-persistence.ts', import.meta.url)));
const { createSerialQueue } = await import(await moduleURL(new URL('../src/lib/helper/serial-queue.ts', import.meta.url)));
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

function fixture(overrides = {}) {
  let generation = 0;
  const rows = [{ id: 'one', text: 'original', sequence: null }];
  const appends = [], adopted = [], busy = [];
  const writer = createTranscriptWriter({
    current: scope => scope === generation,
    snapshot: () => rows.filter(row => row.sequence === null).map(row => ({ ...row })),
    prepare: async entry => ({ text: entry.text }),
    append: async (scope, inputs) => { appends.push([scope, inputs]); return inputs.map((_, index) => ({ sequence: index + 1 })); },
    adopt: (entry, stored) => { rows.find(row => row.id === entry.id).sequence = stored.sequence; adopted.push(entry); },
    busy: value => busy.push(value), batchSize: 200,
    ...overrides,
  });
  return { writer, rows, appends, adopted, busy, navigate: () => generation++ };
}

test('queued writes resnapshot after adoption and do not append the same message twice', async () => {
  const chat = fixture();
  await Promise.all([chat.writer.write(0), chat.writer.write(0)]);
  assert.equal(chat.appends.length, 1);
  assert.equal(chat.adopted.length, 1);
  assert.deepEqual(chat.busy, [true, false]);
});

test('attachment preparation uses a complete detached snapshot taken before its first await', async () => {
  const upload = deferred();
  const chat = fixture({ prepare: async entry => { await upload.promise; return { text: entry.text }; } });
  const write = chat.writer.write(0);
  chat.rows[0].text = 'changed while uploading';
  upload.resolve();
  await write;
  assert.deepEqual(chat.appends, [[0, [{ text: 'original' }]]]);
});

test('navigation during upload prevents an append and skips queued work for the old chat', async () => {
  const upload = deferred();
  const chat = fixture({ prepare: async entry => { await upload.promise; return entry; } });
  const first = chat.writer.write(0), queued = chat.writer.write(0);
  chat.navigate();
  upload.resolve();
  await Promise.all([first, queued]);
  assert.deepEqual(chat.appends, []);
  assert.deepEqual(chat.adopted, []);
});

test('a late append response cannot update the newly opened transcript', async () => {
  const append = deferred(), started = deferred();
  const chat = fixture({ append: async () => { started.resolve(); return await append.promise; } });
  const write = chat.writer.write(0);
  await started.promise;
  chat.navigate();
  chat.rows.splice(0, 1, { id: 'new', text: 'new chat', sequence: null });
  append.resolve([{ sequence: 99 }]);
  await write;
  assert.deepEqual(chat.adopted, []);
  assert.equal(chat.rows[0].sequence, null);
});

test('successful batches are adopted before a later batch fails, so retry saves only the remainder', async () => {
  let attempts = 0;
  const chat = fixture({ batchSize: 1, append: async (_, inputs) => {
    if (++attempts === 2) throw new Error('temporary failure');
    return inputs.map(() => ({ sequence: attempts }));
  } });
  chat.rows.push({ id: 'two', text: 'second', sequence: null });
  await assert.rejects(chat.writer.write(0), /temporary failure/);
  assert.equal(chat.rows[0].sequence, 1);
  assert.equal(chat.rows[1].sequence, null);
  await chat.writer.write(0);
  assert.equal(chat.rows[1].sequence, 3);
});

test('serial writes cannot finish out of order; failures do not poison the queue', async () => {
  const queue = createSerialQueue(), old = deferred(), writes = [];
  const first = queue.run(async () => { writes.push('old-start'); await old.promise; writes.push('old-end'); });
  const next = queue.run(async () => { writes.push('new'); });
  assert.deepEqual(writes, ['old-start']);
  old.resolve();
  await Promise.all([first, next]);
  assert.deepEqual(writes, ['old-start', 'old-end', 'new']);
  await assert.rejects(queue.run(async () => { throw new Error('failed'); }), /failed/);
  assert.equal(await queue.run(async () => 'recovered'), 'recovered');
});
