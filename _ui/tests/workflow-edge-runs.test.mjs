import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function loadModule(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const { edgeRunLabel } = await loadModule('../src/lib/workflow/edge-runs.ts');
const edge = (source_handle) => ({ source: 'a', source_handle, target: 'b' });

test('a port value is counted as one item, an array by its length', () => {
  const state = { status: 'completed', result_kind: 'result', data: { response: 'hi', items: [1, 2, 3], none: [] } };
  assert.equal(edgeRunLabel(edge('response'), state), '1 item');
  assert.equal(edgeRunLabel(edge('items'), state), '3 items');
  assert.equal(edgeRunLabel(edge('none'), state), '0 items');
  assert.equal(edgeRunLabel(edge('missing'), state), '');
});

test('only the selected branch of a routing step is labelled', () => {
  const state = { status: 'completed', result_kind: 'selection', selection: ['true'], data: { result: true } };
  assert.equal(edgeRunLabel(edge('true'), state), '1 item');
  assert.equal(edgeRunLabel(edge('false'), state), '');
});

test('steps that did not complete leave their edges unlabelled', () => {
  assert.equal(edgeRunLabel(edge('data'), undefined), '');
  assert.equal(edgeRunLabel(edge('data'), { status: 'running' }), '');
  assert.equal(edgeRunLabel(edge('data'), { status: 'error', error: 'boom' }), '');
  assert.equal(edgeRunLabel(edge('data'), { status: 'idle', skipped: true }), '');
});

test('fan-out counts the downstream invocations', () => {
  const state = { status: 'completed', result_kind: 'fan_out', data: {} };
  assert.equal(edgeRunLabel(edge('item'), state, { status: 'completed', invocations: 4 }), '4 items');
  assert.equal(edgeRunLabel(edge('item'), state, undefined), '');
});

test('failure output is labelled only when the failure was routed to it', () => {
  assert.equal(edgeRunLabel(edge('__error'), { status: 'error', error_policy: 'error_output' }), 'failure');
  assert.equal(edgeRunLabel(edge('__error'), { status: 'error', error_policy: 'skip' }), '');
  assert.equal(edgeRunLabel(edge('__error'), { status: 'completed', result_kind: 'result', data: {} }), '');
});

test('omitted data still marks the edge as taken', () => {
  assert.equal(edgeRunLabel(edge('data'), { status: 'completed', result_kind: 'result', data_omitted: true }), '✓');
});

const { loadCollapsedNodes, saveCollapsedNodes } = await loadModule('../src/lib/workflow/collapsed-nodes.ts');

test('collapsed steps persist per workflow and tolerate bad storage', () => {
  const store = new Map();
  const storage = { getItem: k => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: k => store.delete(k) };
  saveCollapsedNodes('wf1', ['a', 'b'], storage);
  assert.deepEqual([...loadCollapsedNodes('wf1', storage)], ['a', 'b']);
  assert.equal(loadCollapsedNodes('wf2', storage).size, 0);
  saveCollapsedNodes('wf1', [], storage);
  assert.equal(store.size, 0);
  store.set('at.workflow.collapsed.wf3', '{not json');
  assert.equal(loadCollapsedNodes('wf3', storage).size, 0);
  store.set('at.workflow.collapsed.wf4', JSON.stringify(['ok', 7, null]));
  assert.deepEqual([...loadCollapsedNodes('wf4', storage)], ['ok']);
});
