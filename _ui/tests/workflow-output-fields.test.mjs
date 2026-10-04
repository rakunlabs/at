import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const f = await import(await moduleURL(new URL('../src/lib/workflow/output-fields.ts', import.meta.url)));

test('output field names follow the server rules', () => {
  assert.deepEqual(f.outputFieldNames(['text', { name: 'file' }, 'text', 'Final answer', 'input', 'files', '', '9x']), ['text', 'file']);
  assert.deepEqual(f.outputFieldNames(undefined), []);
  assert.equal(f.outputFieldProblem('input'), '"input" is reserved');
  assert.match(f.outputFieldProblem('my field'), /letters/);
  assert.equal(f.outputFieldProblem('summary'), '');
});

test('a workflow exposes the first Output node with named fields', () => {
  const graph = { nodes: [
    { type: 'input', data: {} },
    { type: 'output', data: { fields: ['legacy tag'] } },
    { type: 'output', data: { fields: ['text', 'file'] } },
  ] };
  assert.deepEqual(f.workflowOutputFields(graph), ['text', 'file']);
  assert.deepEqual(f.workflowOutputFields(undefined), []);
});
