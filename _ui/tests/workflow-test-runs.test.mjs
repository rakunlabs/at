import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/workflow/test-runs.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { buildTestRunOptions, pinNodeOutput, pinUnavailableReason } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

const result = () => ({ status: 'completed', pin_signature: 'signature', result_kind: 'selection', data: { response: { message: 'sample' } }, selection: ['success', 'always'], invocations: 1 });

test('pins preserve routing and detach data from mutable run snapshots', () => {
  const snapshot = result();
  const pin = pinNodeOutput(snapshot);
  snapshot.data.response.message = 'changed';
  snapshot.selection.push('error');
  assert.equal(pin.data.response.message, 'sample');
  assert.deepEqual(pin.selection, ['success', 'always']);
});

test('partial requests use upstream pins but always execute the selected step', () => {
  const pins = { upstream: pinNodeOutput(result()), target: pinNodeOutput(result()) };
  const options = buildTestRunOptions('target', pins, true);
  assert.equal(options.target_node_id, 'target');
  assert.deepEqual(Object.keys(options.pins), ['upstream']);
  options.pins.upstream.data.response.message = 'request-local';
  assert.equal(pins.upstream.data.response.message, 'sample');
  assert.ok(pins.target, 'execution does not silently unpin the saved fixture');
});

test('ordinary runs and disabling pins never transmit fixtures', () => {
  const pins = { node: pinNodeOutput(result()) };
  assert.equal(buildTestRunOptions(null, pins, false), undefined);
  assert.equal(buildTestRunOptions(null, {}, true), undefined);
  assert.deepEqual(buildTestRunOptions('target', pins, false), { target_node_id: 'target' });
  assert.ok(buildTestRunOptions(null, pins, true).pins.node);
});

test('incomplete, omitted and multi-invocation results cannot masquerade as fixtures', () => {
  for (const snapshot of [undefined, { ...result(), status: 'error' }, { ...result(), data_omitted: true }, { ...result(), invocations: 2 }, { ...result(), pin_signature: undefined }, { ...result(), result_kind: 'fan_out' }]) {
    assert.notEqual(pinUnavailableReason(snapshot), '');
    if (snapshot) assert.throws(() => pinNodeOutput(snapshot));
  }
  assert.equal(pinUnavailableReason(result()), '');
});
