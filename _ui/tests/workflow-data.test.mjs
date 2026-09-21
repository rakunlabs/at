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
const { listDataFields } = await loadModule('../src/lib/workflow/data-fields.ts');
const { applyWorkflowEvent } = await loadModule('../src/lib/workflow/run-events.ts');

test('field picker emits JSON Pointers for nested arrays, escaped names and falsy values', () => {
  const result = listDataFields({ prompt: { 'a/b~c': [{ '': false, count: 0, value: null }] } });
  assert.deepEqual(result.fields.map(field => field.path), [
    '/prompt', '/prompt/a~1b~0c', '/prompt/a~1b~0c/0', '/prompt/a~1b~0c/0/',
    '/prompt/a~1b~0c/0/count', '/prompt/a~1b~0c/0/value',
  ]);
  assert.equal(result.fields.at(-1).kind, 'null');
  assert.equal(result.fields.at(-2).preview, '0');
  assert.equal(result.limited, false);
});

test('large field trees are bounded without rewriting the captured snapshot', () => {
  const data = { records: Array.from({ length: 1000 }, (_, n) => ({ id: n })) };
  const result = listDataFields(data);
  assert.equal(result.fields.length, 200);
  assert.equal(result.limited, true);
  assert.equal(data.records.length, 1000);
});

test('fan-out completions never mix inputs and outputs from separate invocations', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'a', inputs: { prompt: 'a' } });
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'b', inputs: { prompt: 'b' }, resolved_inputs: { prompt: 'mapped b' } });
  applyWorkflowEvent(state, { event_type: 'completed', node_id: 'n', execution_id: 'a', data: { response: 'wrong' } });
  assert.equal(state.nodeRunStates.n.status, 'running');
  assert.equal(state.nodeRunStates.n.data, undefined);
  applyWorkflowEvent(state, { event_type: 'completed', node_id: 'n', execution_id: 'b', data: { response: 'correct' } });
  assert.deepEqual(state.nodeRunStates.n.inputs, { prompt: 'b' });
  assert.deepEqual(state.nodeRunStates.n.resolved_inputs, { prompt: 'mapped b' });
  assert.deepEqual(state.nodeRunStates.n.data, { response: 'correct' });
  assert.equal(state.nodeRunStates.n.invocations, 2);
});

test('mapping errors retain input snapshots; new runs do not retain previous outputs', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'a', inputs: { prompt: { name: 'Ada' } } });
  applyWorkflowEvent(state, { event_type: 'error', node_id: 'n', execution_id: 'a', error: 'Missing field' });
  assert.deepEqual(state.nodeRunStates.n.inputs, { prompt: { name: 'Ada' } });
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'b', inputs_omitted: true });
  assert.equal(state.nodeRunStates.n.inputs, undefined);
  assert.equal(state.nodeRunStates.n.error, undefined);
  assert.equal(state.nodeRunStates.n.inputs_omitted, true);
});

test('older events without invocation IDs still retain paired input on completion', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n' });
  applyWorkflowEvent(state, { event_type: 'completed', node_id: 'n', data: { ok: true } });
  assert.deepEqual(state.nodeRunStates.n.data, { ok: true });
  assert.equal(state.nodeRunStates.n.inputs, undefined);
});

test('pinned events do not invent captured inputs; empty complete output remains pinnable', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'a', pinned: true });
  applyWorkflowEvent(state, { event_type: 'completed', node_id: 'n', execution_id: 'a', pinned: true, pin_signature: 'sig', result_kind: 'result' });
  assert.equal(state.nodeRunStates.n.inputs, undefined);
  assert.deepEqual(state.nodeRunStates.n.data, {});
  assert.equal(state.nodeRunStates.n.pinned, true);
  assert.equal(state.nodeRunStates.n.pin_signature, 'sig');
});

test('retry attempts keep one invocation and preserve failures after eventual success', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  const base = { node_id: 'n', execution_id: 'one', max_attempts: 3 };
  applyWorkflowEvent(state, { ...base, event_type: 'started', inputs: { prompt: 'hello' } });
  applyWorkflowEvent(state, { ...base, event_type: 'attempt_started', attempt: 1 });
  applyWorkflowEvent(state, { ...base, event_type: 'attempt_failed', attempt: 1, error: '503', duration_ms: 20 });
  applyWorkflowEvent(state, { ...base, event_type: 'retrying', attempt: 1, retry_delay_ms: 1000 });
  assert.equal(state.nodeRunStates.n.status, 'running');
  assert.equal(state.nodeRunStates.n.retry_delay_ms, 1000);
  assert.equal(state.nodeRunStates.n.invocations, 1);
  applyWorkflowEvent(state, { ...base, event_type: 'attempt_started', attempt: 2 });
  assert.equal(state.nodeRunStates.n.retry_delay_ms, undefined);
  applyWorkflowEvent(state, { ...base, event_type: 'completed', attempt: 2, data: { response: 'ok' }, pin_signature: 'sig', result_kind: 'result' });
  assert.equal(state.nodeRunStates.n.status, 'completed');
  assert.equal(state.nodeRunStates.n.error, undefined);
  assert.equal(state.nodeRunStates.n.attempt_history[0].error, '503');
  assert.equal(state.nodeRunStates.n.attempt_history[1].status, 'completed');
  assert.deepEqual(state.nodeRunStates.n.inputs, { prompt: 'hello' });
});

test('handled failures remain visible without changing the workflow into a failed run', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'one', inputs: { data: 1 } });
  applyWorkflowEvent(state, { event_type: 'error_handled', node_id: 'n', execution_id: 'one', error_policy: 'error_output', error: 'failed', data: { __error: { message: 'failed' } }, attempt: 3 });
  applyWorkflowEvent(state, { event_type: 'done', outputs: { recovered: true } });
  assert.equal(state.status, 'completed');
  assert.equal(state.nodeRunStates.n.status, 'error');
  assert.equal(state.nodeRunStates.n.error_policy, 'error_output');
  assert.equal(state.nodeRunStates.n.pin_signature, undefined);
});

test('retry events from a different fan-out invocation do not overwrite the selected snapshot', () => {
  const state = { nodeRunStates: {}, status: 'running', error: '', outputs: null };
  applyWorkflowEvent(state, { event_type: 'started', node_id: 'n', execution_id: 'current', inputs: { value: 2 } });
  applyWorkflowEvent(state, { event_type: 'retrying', node_id: 'n', execution_id: 'older', attempt: 2, retry_delay_ms: 1000 });
  assert.equal(state.nodeRunStates.n.retry_delay_ms, undefined);
  assert.deepEqual(state.nodeRunStates.n.inputs, { value: 2 });
});
