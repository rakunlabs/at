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
const { switchOutputPorts, newSwitchRule, literalError } = await loadModule('../src/lib/workflow/data-operations.ts');
const { workflowPaletteGroups, createDefaultWorkflowNodeData } = await loadModule('../src/lib/workflow/node-definitions.ts');

test('renaming and reordering cases preserves their connection identifiers', () => {
  const a = newSwitchRule();
  const b = newSwitchRule([a]);
  const ports = switchOutputPorts({ rules: [a, b] });
  a.label = 'Approved';
  const reordered = switchOutputPorts({ rules: [b, a] });
  assert.deepEqual(reordered.map(port => port.id), [ports[1].id, ports[0].id, 'fallback']);
  assert.equal(reordered[1].label, 'Approved');
  assert.notEqual(a.id, b.id);
  assert.deepEqual(switchOutputPorts({ rules: [b] }).map(port => port.id), [b.id, 'fallback']);
});

test('invalid saved case IDs cannot replace fallback or crash keyed handle rendering', () => {
  assert.deepEqual(switchOutputPorts({ rules: [null, { id: '__error' }, { id: 'fallback' }, { id: 'case_a' }, { id: 'case_a', label: 'duplicate' }] }), [
    { id: 'case_a', label: 'case_a' }, { id: 'fallback', label: 'Fallback' },
  ]);
});

test('new data node defaults are independent and each type is discoverable once', () => {
  for (const type of ['edit_fields', 'filter', 'switch', 'merge', 'aggregate']) {
    assert.equal(workflowPaletteGroups.flatMap(group => group.nodes).filter(node => node.type === type).length, 1);
    const a = createDefaultWorkflowNodeData(type);
    const b = createDefaultWorkflowNodeData(type);
    a.label = 'changed';
    assert.notEqual(a.label, b.label);
    if (a.rules) { a.rules[0].label = 'changed'; assert.notEqual(a.rules[0].label, b.rules[0].label); }
    if (a.conditions) { a.conditions[0].path = '/changed'; assert.notEqual(a.conditions[0].path, b.conditions[0].path); }
  }
});

test('literal validation keeps false, zero, null and numeric strings distinct', () => {
  for (const config of [{ value_type: 'number', value: '0' }, { value_type: 'boolean', value: 'false' }, { value_type: 'null', value: '' }, { value_type: 'string', value: '0' }, { value_type: 'json', value: '{"items":[]}' }]) {
    assert.equal(literalError(config), '');
  }
  for (const config of [{ value_type: 'number', value: '"3"' }, { value_type: 'number', value: '1e999' }, { value_type: 'boolean', value: '"false"' }, { value_type: 'json', value: '{' }, { value_type: 'json', value: '{"number":1e999}' }, { value_type: 'eval', value: '0' }]) {
    assert.notEqual(literalError(config), '');
  }
});
