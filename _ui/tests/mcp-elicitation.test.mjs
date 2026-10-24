import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { elicitFields, elicitDefaults, elicitContent, watchElicitations } = await import(await moduleURL(new URL('../src/lib/helper/mcp-elicitation.ts', import.meta.url)));

const schema = {
  type: 'object',
  properties: {
    name: { type: 'string', title: 'Name', minLength: 2 },
    email: { type: 'string', format: 'email' },
    size: { type: 'string', enum: ['s', 'm'], enumNames: ['Small', 'Medium'], default: 'm' },
    count: { type: 'integer', minimum: 1 },
    ok: { type: 'boolean', default: true },
  },
  required: ['name'],
};

test('schema properties become form fields with defaults', () => {
  const fields = elicitFields(schema);
  assert.deepEqual(fields.map(f => [f.name, f.kind, f.required]), [['name', 'text', true], ['email', 'text', false], ['size', 'select', false], ['count', 'number', false], ['ok', 'boolean', false]]);
  assert.equal(fields[1].inputType, 'email');
  assert.deepEqual(fields[2].options, [{ value: 's', label: 'Small' }, { value: 'm', label: 'Medium' }]);
  assert.deepEqual(elicitDefaults(fields), { name: '', email: '', size: 'm', count: '', ok: true });
});

test('form values become typed content, empty optional fields are omitted', () => {
  const fields = elicitFields(schema);
  assert.deepEqual(elicitContent(fields, { name: ' Ada ', email: '', size: 's', count: '3', ok: false }), { content: { name: 'Ada', size: 's', count: 3, ok: false } });
  assert.match(elicitContent(fields, { name: '', ok: true }).error, /Name is required/);
  assert.match(elicitContent(fields, { name: 'A', ok: true }).error, /at least 2/);
  assert.match(elicitContent(fields, { name: 'Ada', count: '1.5', ok: true }).error, /whole number/);
  assert.match(elicitContent(fields, { name: 'Ada', count: '0', ok: true }).error, /at least 1/);
});

test('the watcher long-polls while started and clears on stop', async () => {
  const seen = [];
  let calls = 0;
  const watcher = watchElicitations(async (wait, signal) => {
    calls++;
    assert.equal(wait, 20);
    await new Promise((resolve, reject) => {
      const t = setTimeout(resolve, 5);
      signal.addEventListener('abort', () => { clearTimeout(t); reject(new Error('aborted')); });
    });
    return [{ id: 'q' }];
  }, items => seen.push(items.length));
  watcher.start();
  await new Promise(r => setTimeout(r, 30));
  watcher.stop();
  assert.ok(calls >= 1);
  assert.equal(seen[0], 1);
  assert.equal(seen.at(-1), 0);
});
